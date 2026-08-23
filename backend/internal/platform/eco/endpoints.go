package eco

import (
	"context"
	"encoding/json"
	"time"
)

// ---- rent shelf publish / reprice (one endpoint, PublishType 1|2) ----

const (
	TradeTypeSell  = 1
	TradeTypeRent  = 2
	PublishTypeAdd = 1
	PublishTypeMod = 2
)

type RentPublishItem struct {
	AssetID         string   `json:"AssetId,omitempty"`
	StockID         string   `json:"StockId,omitempty"`
	SteamGameID     string   `json:"SteamGameId"`
	TradeTypes      []int    `json:"TradeTypes"`
	RentPrice       float64  `json:"RentPrice"`
	LongRentPrice   *float64 `json:"LongRentPrice,omitempty"`
	RentMaxDay      int      `json:"RentMaxDay"`
	RentDeposits    float64  `json:"RentDeposits"`
	RentDescription string   `json:"RentDescription,omitempty"`
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

// QuerySelfRentGoods pages through our lease shelf. state: nil=all,1=listed,2=leased.
func (c *Client) QuerySelfRentGoods(ctx context.Context, state *int) ([]RentGoods, error) {
	var out []RentGoods
	pageIndex := 1
	for {
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

// SellerRentOrderList queries orders in [start,end] window (max span per docs).
func (c *Client) SellerRentOrderList(ctx context.Context, start, end time.Time, status []int) ([]SellerRentOrder, error) {
	var out []SellerRentOrder
	const layout = "2006-01-02 15:04:05"
	pageIndex := 1
	for {
		biz := map[string]any{
			"StartTime": start.Format(layout),
			"EndTime":   end.Format(layout),
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
	if err := c.post(ctx, "/Api/Merchant/GetMerchantMoney", map[string]any{}, &w); err != nil {
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
