package eco

import (
	"context"
	"fmt"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
)

// Adapter exposes ECO through the unified platform.Adapter.
type Adapter struct {
	c       *Client
	steamID string
}

func NewAdapter(c *Client, steamID string) *Adapter { return &Adapter{c: c, steamID: steamID} }

func (a *Adapter) Channel() domain.Channel { return domain.ChannelECO }

func (a *Adapter) Caps() platform.Capabilities {
	return platform.Capabilities{
		DepositDirect:          false, // deposit is derived by the platform formula
		LongLeaseThresholdDays: 21,
		MaxBatchPublish:        100,
		MaxBatchReprice:        100,
		RentMaxDayMin:          8,
		RentMaxDayMax:          90,
	}
}

// Healthy performs a cheap authenticated call.
func (a *Adapter) Healthy(ctx context.Context) error {
	_, err := a.c.GetWalletBalance(ctx)
	return err
}

// Inventory is served from ECO's Steam-stock view; full sync lands in M3.
// Inventory reads ECO's Steam-stock view (official OpenAPI api-220956670).
// Response Status is the SteamStockStatus enum: 1 待上架, 2 出售上架,
// 3 出售交易中, 4 出租上架, 5 出租交易中, 6 租售上架, 7 租售交易中,
// 8 预售上架, 9 预售交易中, 10 打包上架, 11 打包交易中.
// Mark price uses Price (平台市场价), falling back to SteamPrice (Steam市场价).
// Single page of ≤100 — mirrors the UU adapter; page through if ever needed.
func (a *Adapter) Inventory(ctx context.Context) ([]domain.InventoryItem, error) {
	var raw struct {
		PageResult []struct {
			StockID    string  `json:"StockId"`
			AssetID    string  `json:"AssetId"`
			HashName   string  `json:"HashName"`
			GoodsName  string  `json:"GoodsName"`
			SteamPrice float64 `json:"SteamPrice"`
			Price      float64 `json:"Price"`
			Tradable   bool    `json:"Tradable"`
			Status     int     `json:"Status"`
		} `json:"PageResult"`
		TotalRecord int `json:"TotalRecord"`
	}
	biz := map[string]any{"GameId": "730", "PageIndex": 1, "PageSize": 100}
	if err := a.c.post(ctx, "/Api/Selling/QueryStock", biz, &raw); err != nil {
		return nil, err
	}
	out := make([]domain.InventoryItem, 0, len(raw.PageResult))
	for _, it := range raw.PageResult {
		ref := it.AssetID
		if ref == "" {
			ref = it.StockID
		}
		mark := it.Price
		if mark <= 0 {
			mark = it.SteamPrice
		}
		status := "locked"
		switch {
		case !it.Tradable:
		case it.Status == 1: // 待上架
			status = "in_stock"
		case it.Status == 4 || it.Status == 6 || it.Status == 8 || it.Status == 10: // 出租/租售/预售/打包上架
			status = "listed"
		}
		out = append(out, domain.InventoryItem{
			Channel: domain.ChannelECO, AssetID: ref,
			HashName: it.HashName, DisplayName: it.GoodsName,
			MarkPrice: mark, Tradable: it.Tradable, Status: status,
		})
	}
	return out, nil
}

func (a *Adapter) LeaseShelf(ctx context.Context) ([]domain.ShelfListing, error) {
	goods, err := a.c.QuerySelfRentGoods(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ShelfListing, 0, len(goods))
	for _, g := range goods {
		listedAt, _ := time.Parse(time.DateTime, g.CreateTime)
		out = append(out, domain.ShelfListing{
			Channel:       domain.ChannelECO,
			GoodsRef:      g.GoodsNum,
			AssetID:       g.AssetID,
			HashName:      g.HashName,
			DisplayName:   g.GoodsName,
			RentPrice:     g.Price,
			LongRentPrice: g.LongRentPrice,
			MaxDays:       g.RentMaxDay,
			Deposit:       g.Deposits,
			MarkPrice:     g.MarkPrice,
			Leased:        g.Status == 2,
			ListedAt:      listedAt,
		})
	}
	return out, nil
}

func (a *Adapter) PublishLease(ctx context.Context, items []platform.PublishLeaseRequest) ([]platform.PublishLeaseResult, error) {
	payload := make([]RentPublishItem, 0, len(items))
	for _, it := range items {
		p := RentPublishItem{
			AssetID:      it.AssetRef,
			SteamGameID:  "730",
			TradeTypes:   []int{TradeTypeRent},
			RentPrice:    it.RentPrice,
			RentMaxDay:   it.MaxDays,
			RentDeposits: derivedDeposit(it),
		}
		if it.LongRentPrice > 0 {
			v := it.LongRentPrice
			p.LongRentPrice = &v
		}
		payload = append(payload, p)
	}
	results, err := a.c.PublishRentAndSale(ctx, a.steamID, PublishTypeAdd, payload)
	out := make([]platform.PublishLeaseResult, len(items))
	for i := range items {
		out[i] = platform.PublishLeaseResult{AssetRef: items[i].AssetRef}
	}
	for i, r := range results {
		if i >= len(out) {
			break
		}
		out[i].Success = r.IsSuccess
		out[i].Error = r.ErrorMsg
		out[i].GoodsRef = r.GoodNum
	}
	if err != nil {
		return out, err
	}
	return out, platform.PartialIfAnyFailed(out)
}

// derivedDeposit mirrors the platform rule max(ref×140%, rent×D, long×D);
// we send our best estimate — the platform recomputes server-side.
func derivedDeposit(it platform.PublishLeaseRequest) float64 {
	dep := it.Deposit // caller passes V*1.4 as Deposit hint for eco
	if v := it.RentPrice * float64(it.MaxDays); v > dep {
		dep = v
	}
	if it.LongRentPrice > 0 {
		if v := it.LongRentPrice * float64(it.MaxDays); v > dep {
			dep = v
		}
	}
	return dep
}

func (a *Adapter) RepriceLease(ctx context.Context, items []platform.RepriceLeaseRequest) ([]platform.RepriceLeaseResult, error) {
	payload := make([]RentPublishItem, 0, len(items))
	for _, it := range items {
		p := RentPublishItem{
			AssetID:      it.AssetRef,
			SteamGameID:  "730",
			TradeTypes:   []int{TradeTypeRent},
			RentPrice:    it.RentPrice,
			RentMaxDay:   it.MaxDays,
			RentDeposits: it.Deposit,
		}
		if it.LongRentPrice > 0 {
			v := it.LongRentPrice
			p.LongRentPrice = &v
		}
		payload = append(payload, p)
	}
	results, err := a.c.PublishRentAndSale(ctx, a.steamID, PublishTypeMod, payload)
	out := make([]platform.RepriceLeaseResult, len(items))
	for i := range items {
		// default-deny: an item with no explicit per-item result is a FAILURE,
		// never optimistic success (mirrors PublishLease above).
		out[i] = platform.RepriceLeaseResult{GoodsRef: items[i].GoodsRef}
	}
	for i, r := range results {
		if i >= len(out) {
			break
		}
		out[i].Success = r.IsSuccess
		out[i].Error = r.ErrorMsg
	}
	if len(results) != len(items) {
		for i := len(results); i < len(out); i++ {
			out[i].Error = "eco: missing per-item reprice result"
		}
		return out, fmt.Errorf("%w: %d/%d reprice results returned",
			platform.ErrPartialFailure, len(results), len(items))
	}
	return out, err
}

func (a *Adapter) Delist(ctx context.Context, goodsRefs []string) error {
	results, err := a.c.OffshelfRentGoods(ctx, goodsRefs)
	if err != nil {
		return err
	}
	for _, r := range results {
		if !r.IsSuccess {
			return &platform.PartialError{Ref: firstNonEmpty(r.GoodsNum, r.AssetID), Msg: r.ErrorMsg}
		}
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (a *Adapter) LeaseOrders(ctx context.Context, since time.Time) ([]domain.LeaseOrder, error) {
	end := time.Now()
	orders, err := a.c.SellerRentOrderList(ctx, since.AddDate(0, 0, -1), end, nil)
	if err != nil {
		return nil, err
	}
	const layout = "2006-01-02 15:04:05"
	out := make([]domain.LeaseOrder, 0, len(orders))
	for _, o := range orders {
		started, _ := time.Parse(layout, o.CreateTime)
		due, _ := time.Parse(layout, o.RentExpire)
		ot := "short"
		if o.RentType == 2 {
			ot = "long"
		}
		out = append(out, domain.LeaseOrder{
			Channel:   domain.ChannelECO,
			OrderRef:  o.OrderNum,
			AssetID:   o.AssetID,
			HashName:  o.HashName,
			OrderType: ot,
			Status:    mapECOOrderStatus(o.Status),
			RentDays:  o.RentDay,
			RentPrice: o.Price,
			Amount:    o.OrderAmount,
			Deposits:  o.Deposits,
			StartedAt: started,
			DueAt:     due,
			UpdatedAt: time.Now().UTC(),
		})
	}
	return out, nil
}

// mapECOOrderStatus per platform-eco-api-notes.md state table.
func mapECOOrderStatus(code int) string {
	switch code {
	case 1:
		return "pending_payment"
	case 2:
		return "delivering"
	case 3:
		return "leasing"
	case 4:
		return "returning"
	case 5:
		return "breach"
	case 6:
		return "arbitrating"
	case 7, 12:
		return "done"
	case 8:
		return "bought_out"
	case 9:
		return "cancelled"
	case 10, 11:
		return "breach"
	default:
		// 未知码显式落 'unknown'（0006 CHECK 允许），不再以空串隐身
		return "unknown"
	}
}

func (a *Adapter) Wallet(ctx context.Context) (float64, error) { return a.c.GetWalletBalance(ctx) }
