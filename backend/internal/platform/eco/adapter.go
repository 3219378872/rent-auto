package eco

import (
	"context"
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
func (a *Adapter) Inventory(ctx context.Context) ([]domain.InventoryItem, error) {
	var raw struct {
		PageResult []struct {
			StockID   string  `json:"StockId"`
			AssetID   string  `json:"AssetId"`
			HashName  string  `json:"HashName"`
			GoodsName string  `json:"GoodsName"`
			MarkPrice float64 `json:"MarkPrice"`
			Status    int     `json:"Status"`
		} `json:"PageResult"`
		TotalRecord int `json:"TotalRecord"`
	}
	biz := map[string]any{"SteamGameId": "730", "PageIndex": 1, "PageSize": 100}
	if err := a.c.post(ctx, "/Api/Selling/QuerySteamStock", biz, &raw); err != nil {
		return nil, err
	}
	out := make([]domain.InventoryItem, 0, len(raw.PageResult))
	for _, it := range raw.PageResult {
		ref := it.AssetID
		if ref == "" {
			ref = it.StockID
		}
		status := "in_stock"
		if it.Status != 0 {
			status = "locked"
		}
		out = append(out, domain.InventoryItem{
			Channel: domain.ChannelECO, AssetID: ref,
			HashName: it.HashName, DisplayName: it.GoodsName,
			MarkPrice: it.MarkPrice, Tradable: true, Status: status,
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
	return out, err
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
		out[i].GoodsRef = items[i].GoodsRef
		out[i].Success = true
	}
	for i, r := range results {
		if i >= len(out) {
			break
		}
		out[i].Success = r.IsSuccess
		out[i].Error = r.ErrorMsg
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
		return ""
	}
}

func (a *Adapter) Wallet(ctx context.Context) (float64, error) { return a.c.GetWalletBalance(ctx) }
