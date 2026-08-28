package eco

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ---- rent shelf publish / reprice (one endpoint, PublishType 1|2) ----

const (
	TradeTypeSell  = 1
	TradeTypeRent  = 2
	PublishTypeAdd = 1
	PublishTypeMod = 2
)

// Sublet (转租) policy per official PublishRentAndSaleItemModel schema
// (api-220956685): SupportSublet 0=关闭 1=开启 99=禁用;
// SubletPricingMethod 1=自定义价格 2=动态定价 (platform-managed).
// Channel policy: every ECO rent listing allows sublet priced dynamically
// by the platform — custom sublet price fields (SubletPrice et al.) stay
// unset for that reason.
const (
	SubletOff      = 0
	SubletOn       = 1
	SubletDisabled = 99

	SubletPricingCustom  = 1
	SubletPricingDynamic = 2
)

type RentPublishItem struct {
	AssetID             string   `json:"AssetId,omitempty"`
	StockID             string   `json:"StockId,omitempty"`
	SteamGameID         string   `json:"SteamGameId"`
	TradeTypes          []int    `json:"TradeTypes"`
	RentPrice           float64  `json:"RentPrice"`
	LongRentPrice       *float64 `json:"LongRentPrice,omitempty"`
	RentMaxDay          int      `json:"RentMaxDay"`
	RentDeposits        float64  `json:"RentDeposits"`
	RentDescription     string   `json:"RentDescription,omitempty"`
	SupportSublet       int      `json:"SupportSublet"`
	SubletPricingMethod int      `json:"SubletPricingMethod"`
}

// applySubletPolicy stamps the channel-wide sublet policy onto a rent item
// (publish and reprice alike — reprice must carry the full item body).
func applySubletPolicy(p *RentPublishItem) {
	p.SupportSublet = SubletOn
	p.SubletPricingMethod = SubletPricingDynamic
}

type publishItemResult struct {
	AssetID   string `json:"AssetId"`
	StockID   string `json:"StockId"`
	IsSuccess bool   `json:"IsSuccess"`
	ErrorMsg  string `json:"ErrorMsg"`
	GoodNum   string `json:"GoodNum"`
}

// PublishRentAndSale publishes or reprices rent(-and-sale) listings; ≤100 items.
func (c *Client) PublishRentAndSale(ctx context.Context, steamID string, publishType int, items []RentPublishItem) ([]publishItemResult, error) {
	var results []publishItemResult
	biz := map[string]any{
		"SteamId":     steamID,
		"PublishType": publishType,
		"Assets":      items,
	}
	if err := c.post(ctx, "/Api/Rent/PublishRentAndSaleGoods", biz, &results); err != nil {
		return nil, err
	}
	return results, nil
}

// ---- self rent shelf ----

type RentGoods struct {
	GoodsNum      string  `json:"GoodsNum"`
	GoodsID       string  `json:"GoodsId"`
	StockID       string  `json:"StockId"`
	MarkPrice     float64 `json:"MarkPrice"`
	Status        int     `json:"Status"` // 1 listed, 2 leased
	RentMaxDay    int     `json:"RentMaxDay"`
	Price         float64 `json:"Price"`
	Deposits      float64 `json:"Deposits"`
	LongRentDay   int     `json:"LongRentDay"`
	LongRentPrice float64 `json:"LongRentPrice"`
	AssetID       string  `json:"AssetId"`
	HashName      string  `json:"HashName"`
	GoodsName     string  `json:"GoodsName"`
	CreateTime    string  `json:"CreateTime"`
	RentExpire    string  `json:"RentExpire"`
}

type page struct {
	PageIndex   int             `json:"PageIndex"`
	PageSize    int             `json:"PageSize"`
	TotalRecord int             `json:"TotalRecord"`
	PageResult  json.RawMessage `json:"PageResult"`
}

// maxListPages bounds every paginated fetch: a misbehaving server that keeps
// returning full pages must degrade to a truncated snapshot instead of an
// infinite rate-limited loop. 500 pages ≫ any realistic volume.
const maxListPages = 500

// QuerySelfRentGoods pages through our lease shelf. state: nil=all,1=listed,2=leased.
func (c *Client) QuerySelfRentGoods(ctx context.Context, state *int) ([]RentGoods, error) {
	var out []RentGoods
	pageIndex := 1
	for pageIndex <= maxListPages {
		biz := map[string]any{
			"SteamGameId": "730",
			"PageIndex":   pageIndex,
			"PageSize":    100,
		}
		if state != nil {
			biz["State"] = *state
		}
		var p page
		if err := c.post(ctx, "/Api/Rent/QuerySelfRentGoods", biz, &p); err != nil {
			return nil, err
		}
		var items []RentGoods
		if len(p.PageResult) > 0 && string(p.PageResult) != "null" {
			if err := json.Unmarshal(p.PageResult, &items); err != nil {
				return nil, err
			}
		}
		out = append(out, items...)
		if pageIndex*100 >= p.TotalRecord || len(items) == 0 {
			break
		}
		pageIndex++
	}
	return out, nil
}

// ---- offshelf ----

type delistItem struct {
	GoodsNum    string `json:"GoodsNum,omitempty"`
	AssetID     string `json:"AssetId,omitempty"`
	SteamGameID string `json:"SteamGameId,omitempty"`
}

type delistResult struct {
	GoodsNum  string `json:"GoodsNum"`
	AssetID   string `json:"AssetId"`
	IsSuccess bool   `json:"IsSuccess"`
	ErrorMsg  string `json:"ErrorMsg"`
}

// OffshelfRentGoods removes up to 100 lease listings.
func (c *Client) OffshelfRentGoods(ctx context.Context, goodsNums []string) ([]delistResult, error) {
	items := make([]delistItem, 0, len(goodsNums))
	for _, g := range goodsNums {
		items = append(items, delistItem{GoodsNum: g, SteamGameID: "730"})
	}
	var results []delistResult
	biz := map[string]any{"goodsNumList": items}
	if err := c.post(ctx, "/Api/Rent/OffshelfRentGoods", biz, &results); err != nil {
		return nil, err
	}
	return results, nil
}

// ---- seller rent orders ----

// ecoCST is the platform's wall-clock zone (Beijing, UTC+8). Real-machine
// finding 2026-08-28: time-window request params are compared against CST
// CreateTime strings, and every timestamp string in responses is CST too —
// formatting UTC bounds verbatim made fresh rent orders invisible to
// orders_sync for ~8h (evidence 2026-08-28-eco-orders-tz-8h-lag.md).
var ecoCST = time.FixedZone("UTC+8", 8*3600)

// formatEcoTime renders t in the platform's wall-clock zone.
func formatEcoTime(t time.Time, layout string) string {
	return t.In(ecoCST).Format(layout)
}

// parseEcoTime parses a platform timestamp string in the platform's zone;
// malformed input yields the zero time (callers store NULL via nullTime).
func parseEcoTime(s, layout string) time.Time {
	t, err := time.ParseInLocation(layout, s, ecoCST)
	if err != nil {
		return time.Time{}
	}
	return t
}

type SellerRentOrder struct {
	OrderNum     string  `json:"OrderNum"`
	RentType     int     `json:"RentType"` // 1 short, 2 long
	Status       int     `json:"Status"`
	CreateTime   string  `json:"CreateTime"`
	RentExpire   string  `json:"RentExpire"`
	RevertExpire string  `json:"RevertExpire"`
	Price        float64 `json:"Price"`
	OrderAmount  float64 `json:"OrderAmount"`
	RentDay      int     `json:"RentDay"`
	MaxRentDay   int     `json:"MaxRentDay"`
	Deposits     float64 `json:"Deposits"`
	CancelReason string  `json:"CancelReason"`
	HashName     string  `json:"HashName"`
	AssetID      string  `json:"AssetId"`
}

// SellerRentOrderList queries orders in [start,end]. 平台单次查询窗口上限
// 31 天（code=7002「最大支持查询31天内数据」，2026-08-27 真机确认）——
// orders_sync 的回看窗最长 100 天（ADR-0003 长租锚点），因此按 30 天分段
// 聚合，段内再翻页。
func (c *Client) SellerRentOrderList(ctx context.Context, start, end time.Time, status []int) ([]SellerRentOrder, error) {
	var out []SellerRentOrder
	const layout = "2006-01-02 15:04:05"
	const chunk = 30 * 24 * time.Hour
	// Hard bound: never look back further than one year regardless of the
	// caller's anchor (zero-time callers must not explode into thousands of
	// segments; the scheduler's own cap is 100d).
	if minStart := end.AddDate(0, 0, -365); start.Before(minStart) {
		start = minStart
	}
	for cursor := start; !cursor.After(end); cursor = cursor.Add(chunk).Add(time.Second) {
		segEnd := end
		if segEnd.After(cursor.Add(chunk)) {
			segEnd = cursor.Add(chunk)
		}
		orders, err := c.sellerRentOrderPage(ctx, cursor, segEnd, status, layout)
		if err != nil {
			return nil, err
		}
		out = append(out, orders...)
	}
	return out, nil
}

func (c *Client) sellerRentOrderPage(ctx context.Context, start, end time.Time, status []int, layout string) ([]SellerRentOrder, error) {
	var out []SellerRentOrder
	pageIndex := 1
	for pageIndex <= maxListPages {
		biz := map[string]any{
			"StartTime": formatEcoTime(start, layout),
			"EndTime":   formatEcoTime(end, layout),
			"PageIndex": pageIndex,
			"PageSize":  100,
		}
		if len(status) > 0 {
			biz["Status"] = status
		}
		var p page
		if err := c.post(ctx, "/Api/Rent/SellerRentOrderList", biz, &p); err != nil {
			return nil, err
		}
		var items []SellerRentOrder
		if len(p.PageResult) > 0 && string(p.PageResult) != "null" {
			if err := json.Unmarshal(p.PageResult, &items); err != nil {
				return nil, err
			}
		}
		out = append(out, items...)
		if pageIndex*100 >= p.TotalRecord || len(items) == 0 {
			break
		}
		pageIndex++
	}
	return out, nil
}

// ---- wallet & market anchor ----

type WalletMoney struct {
	Money float64 `json:"Money"`
}

func (c *Client) GetWalletBalance(ctx context.Context) (float64, error) {
	var w WalletMoney
	if err := c.post(ctx, "/Api/Merchant/GetTotalMoney", map[string]any{}, &w); err != nil {
		return 0, err
	}
	return w.Money, nil
}

// MarketPriceEntry is one row of the full market dump.
type MarketPriceEntry struct {
	MarketName     string  `json:"market_name"`
	MarketHashName string  `json:"market_hash_name"`
	GoodsID        int64   `json:"goods_id"`
	SteamPriceCNY  float64 `json:"steam_price_cny"`
}

// GetMarketPriceDump pulls the full platform sell-price list.
// Platform requires ≥60s between calls — schedule-level responsibility.
// Response payload appears as either {"List":[…]} or a bare array depending on
// deployment version; both shapes are accepted.
func (c *Client) GetMarketPriceDump(ctx context.Context) ([]MarketPriceEntry, error) {
	raw, err := c.postRaw(ctx, "/Api/Market/GetHashNameAndPriceList", map[string]any{})
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	var wrapped struct {
		List []MarketPriceEntry `json:"List"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && len(wrapped.List) > 0 {
		return wrapped.List, nil
	}
	var list []MarketPriceEntry
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	return nil, nil
}

// SellerRentOrderDetailResult is /Api/Rent/SellerRentOrderDetail (api-220956684).
// OfferId semantics: while awaiting delivery it is the RENT trade-offer id the
// platform created; during return it is the return offer id. SendOfferRole:
// 未设置=0 买家=1 卖家=2 (who must send the offer).
type SellerRentOrderDetailResult struct {
	OrderNum       string  `json:"OrderNum"`
	RentType       int     `json:"RentType"`
	Status         int     `json:"Status"`         // RentOrderDetailStatus
	ProgressStatus int     `json:"ProgressStatus"` // RentOrderProgressStatus: 等待发货=2 发货中=3 …租赁中=5
	CreateTime     string  `json:"CreateTime"`
	RentExpire     string  `json:"RentExpire"`
	RevertExpire   string  `json:"RevertExpire"`
	Price          float64 `json:"Price"`
	OrderAmount    float64 `json:"OrderAmount"`
	RentDay        int     `json:"RentDay"`
	MaxRentDay     int     `json:"MaxRentDay"`
	Deposits       float64 `json:"Deposits"`
	HashName       string  `json:"HashName"`
	OfferID        string  `json:"OfferId"`
	SendOfferRole  int     `json:"SendOfferRole"`
}

// SellerRentOrderDetail fetches one rent order's detail (OfferId drives the
// Steam accept step of rent delivery — rent orders never appear in the
// sale-order SellerOrderList view, real-machine finding 2026-08-27).
func (c *Client) SellerRentOrderDetail(ctx context.Context, orderNum string) (*SellerRentOrderDetailResult, error) {
	if orderNum == "" {
		return nil, fmt.Errorf("eco: order num required")
	}
	var out SellerRentOrderDetailResult
	biz := map[string]any{"OrderNum": orderNum}
	if err := c.post(ctx, "/Api/Rent/SellerRentOrderDetail", biz, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
