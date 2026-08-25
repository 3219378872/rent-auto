package uu

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
)

// Adapter exposes the UU client through the unified platform.Adapter.
type Adapter struct{ c *Client }

func NewAdapter(c *Client) *Adapter { return &Adapter{c: c} }

func (a *Adapter) Channel() domain.Channel { return domain.ChannelUU }

func (a *Adapter) Caps() platform.Capabilities {
	return platform.Capabilities{
		DepositDirect:          true,
		LongLeaseThresholdDays: 8,
		MaxBatchPublish:        50,
		MaxBatchReprice:        50,
		RentMaxDayMin:          1,
		RentMaxDayMax:          90,
	}
}

func (a *Adapter) Healthy(ctx context.Context) error { return a.c.fetchUserInfo(ctx) }

func (a *Adapter) Inventory(ctx context.Context) ([]domain.InventoryItem, error) {
	raw, err := a.c.GetInventory(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]domain.InventoryItem, 0, len(raw))
	for _, it := range raw {
		status := "in_stock"
		if it.AssetStatus != 0 || !it.Tradable {
			status = "locked"
		}
		out = append(out, domain.InventoryItem{
			Channel:     domain.ChannelUU,
			AssetID:     it.SteamAssetID,
			HashName:    it.MarketHashName,
			DisplayName: it.ShotName,
			TemplateID:  it.TemplateInfo.ID,
			MarkPrice:   it.TemplateInfo.MarkPrice,
			Tradable:    it.Tradable && it.AssetStatus == 0,
			Status:      status,
		})
	}
	return out, nil
}

func (a *Adapter) LeaseShelf(ctx context.Context) ([]domain.ShelfListing, error) {
	raw, err := a.c.ListLeaseShelf(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ShelfListing, 0, len(raw))
	for _, it := range raw {
		out = append(out, domain.ShelfListing{
			Channel:       domain.ChannelUU,
			GoodsRef:      strconv.FormatInt(it.ID, 10),
			AssetID:       it.SteamAssetID,
			DisplayName:   it.Name,
			TemplateID:    it.TemplateID,
			RentPrice:     it.ShortRent(),
			LongRentPrice: it.LongRent(),
			MaxDays:       it.LeaseMaxDays,
			Deposit:       it.Deposit(),
			Leased:        false,
		})
	}
	return out, nil
}

func (a *Adapter) PublishLease(ctx context.Context, items []platform.PublishLeaseRequest) ([]platform.PublishLeaseResult, error) {
	payload := make([]OnLeaseItem, 0, len(items))
	for _, it := range items {
		var long *float64
		if it.LongRentPrice > 0 {
			v := it.LongRentPrice
			long = &v
		}
		assetID, err := strconv.ParseInt(it.AssetRef, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("uu: asset ref %q: %w", it.AssetRef, err)
		}
		payload = append(payload, OnLeaseItem{
			AssetID: assetID, IsCanLease: true, IsCanSold: false,
			LeaseDeposit: strconv.FormatFloat(it.Deposit, 'f', -1, 64),
			LeaseMaxDays: it.MaxDays, LeaseUnitPrice: it.RentPrice,
			LongLeaseUnitPrice: long,
		})
	}
	raws, err := a.c.PutItemsOnLeaseShelf(ctx, payload, defaultCompensationType)
	results := make([]platform.PublishLeaseResult, len(items))
	for i := range items {
		results[i] = platform.PublishLeaseResult{AssetRef: items[i].AssetRef}
	}
	// map results positionally (API preserves ItemInfos order)
	for i, r := range raws {
		if i >= len(results) {
			break
		}
		results[i].Success = r.Status == 1
		results[i].Error = r.Remark
		results[i].GoodsRef = strconv.FormatInt(r.CommodityID, 10)
	}
	if err != nil {
		return results, err
	}
	return results, platform.PartialIfAnyFailed(results)
}

const defaultCompensationType = 7

func (a *Adapter) RepriceLease(ctx context.Context, items []platform.RepriceLeaseRequest) ([]platform.RepriceLeaseResult, error) {
	payload := make([]ChangeLeaseItem, 0, len(items))
	for _, it := range items {
		goodsID, err := strconv.ParseInt(it.GoodsRef, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("uu: goods ref %q: %w", it.GoodsRef, err)
		}
		ci := ChangeLeaseItem{
			CommodityID:  goodsID,
			LeaseDeposit: strconv.FormatFloat(it.Deposit, 'f', -1, 64),
			LeaseMaxDays: it.MaxDays, LeaseUnitPrice: it.RentPrice,
		}
		if it.LongRentPrice > 0 {
			v := it.LongRentPrice
			ci.LongLeaseUnitPrice = &v
		}
		payload = append(payload, ci)
	}
	success, fails, err := a.c.ChangeLeasePrices(ctx, payload, defaultCompensationType)
	results := make([]platform.RepriceLeaseResult, len(items))
	failMap := map[int64]string{}
	for _, f := range fails {
		if f.IsSuccess != 1 {
			failMap[f.CommodityID] = f.Message
		}
	}
	okCount := success
	for i := range items {
		results[i].GoodsRef = items[i].GoodsRef
		goodsID, _ := strconv.ParseInt(items[i].GoodsRef, 10, 64)
		if msg, bad := failMap[goodsID]; bad {
			results[i].Success = false
			results[i].Error = msg
		} else {
			results[i].Success = true
		}
	}
	if okCount < len(items) && err == nil {
		err = fmt.Errorf("%w: %d/%d succeeded", platform.ErrPartialFailure, okCount, len(items))
	}
	return results, err
}

func (a *Adapter) Delist(ctx context.Context, goodsRefs []string) error {
	return a.c.OffShelf(ctx, goodsRefs)
}

func (a *Adapter) LeaseOrders(ctx context.Context, since time.Time) ([]domain.LeaseOrder, error) {
	raws, err := a.c.GetLeasedOutOrders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.LeaseOrder, 0, len(raws))
	for _, o := range raws {
		started, _ := time.Parse(time.DateTime, o.StartTime)
		due, _ := time.Parse(time.DateTime, o.EndTime)
		lo := domain.LeaseOrder{
			Channel:   domain.ChannelUU,
			OrderRef:  o.OrderID,
			AssetID:   o.AssetRef(),
			HashName:  o.CommodityInfo.Name,
			Status:    mapUUOrderStatus(o.OrderStatus),
			RentDays:  o.LeaseDays,
			RentPrice: strF(o.RentPrice),
			Deposits:  strF(o.Deposit),
			StartedAt: started,
			DueAt:     due,
			UpdatedAt: time.Now().UTC(),
		}
		if lo.StartedAt.Before(since.AddDate(0, 0, -30)) && !since.IsZero() {
			continue
		}
		out = append(out, lo)
	}
	return out, nil
}

// mapUUOrderStatus translates observed UU order status ints into the unified
// state machine. Unknown values map to "" and are surfaced via Raw payloads in M3.
func mapUUOrderStatus(code int) string {
	switch code {
	case 0:
		return "leasing"
	case 2:
		return "done"
	case 3:
		return "bought_out"
	default:
		// 未知码显式落 'unknown'（0006 CHECK 允许），不再以空串隐身
		return "unknown"
	}
}

func (a *Adapter) Wallet(context.Context) (float64, error) { return 0, platform.ErrUnsupported }
