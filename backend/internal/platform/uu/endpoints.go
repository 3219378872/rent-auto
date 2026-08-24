package uu

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// ---- inventory ----

type InventoryItem struct {
	SteamAssetID string `json:"SteamAssetId"`
	AssetStatus  int    `json:"AssetStatus"`
	Tradable     bool   `json:"Tradable"`
	ShotName     string `json:"ShotName"`
	TemplateInfo struct {
		ID        int64   `json:"Id"`
		MarkPrice float64 `json:"MarkPrice"`
	} `json:"TemplateInfo"`
	MarketHashName string `json:"MarketHashName"`
}

// GetInventory pages through the full Steam inventory (pageSize=1000 per call,
// looped until a short page — never silently truncated).
func (c *Client) GetInventory(ctx context.Context, refresh bool) ([]InventoryItem, error) {
	const pageSize = 1000
	var out []InventoryItem
	for pageIndex := 1; ; pageIndex++ {
		payload := map[string]any{
			"pageIndex": pageIndex, "pageSize": pageSize, "AppType": 4,
			"IsMerge": 0, "Sessionid": c.device,
		}
		if refresh && pageIndex == 1 {
			payload["IsRefresh"] = true
			payload["RefreshType"] = 2
		}
		data, err := c.do(ctx, "POST", "/api/commodity/Inventory/GetUserInventoryDataListV3", payload)
		if err != nil {
			return nil, err
		}
		env, err := decodeEnvelope(data)
		if err != nil {
			return nil, err
		}
		if err := checkEnv(env, "inventory"); err != nil {
			return nil, err
		}
		var d struct {
			ItemsInfos []InventoryItem `json:"ItemsInfos"`
		}
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return nil, fmt.Errorf("uu: inventory payload: %w", err)
		}
		out = append(out, d.ItemsInfos...)
		if len(d.ItemsInfos) < pageSize {
			return out, nil
		}
	}
}

// ---- lease shelf ----

type LeaseShelfItem struct {
	ID                int64  `json:"id"` // commodityId used for reprice/offshelf
	SteamAssetID      string `json:"steamAssetId"`
	TemplateID        int64  `json:"templateId"`
	Name              string `json:"name"`
	DepositAmount     string `json:"depositAmount"`
	ShortLeaseAmount  string `json:"shortLeaseAmount"`
	LongLeaseAmount   string `json:"longLeaseAmount"`
	LeaseMaxDays      int    `json:"leaseMaxDays"`
	CommodityCanSell  bool   `json:"commodityCanSell"`
	CommodityCanLease bool   `json:"commodityCanLease"`
}

func strF(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func (i LeaseShelfItem) Deposit() float64   { return strF(i.DepositAmount) }
func (i LeaseShelfItem) ShortRent() float64 { return strF(i.ShortLeaseAmount) }
func (i LeaseShelfItem) LongRent() float64 {
	if i.LongLeaseAmount == "" {
		return 0
	}
	return strF(i.LongLeaseAmount)
}

// ListLeaseShelf returns shelf items; zeroCD channel included when requested.
func (c *Client) ListLeaseShelf(ctx context.Context, includeZeroCD bool) ([]LeaseShelfItem, error) {
	var out []LeaseShelfItem
	paths := []string{"/api/youpin/bff/new/commodity/v1/commodity/list/lease"}
	if includeZeroCD {
		paths = append(paths, "/api/youpin/bff/new/commodity/v1/commodity/list/zeroCDLease")
	}
	for _, p := range paths {
		pageIndex := 1
		for {
			if pageIndex > maxListPages {
				break
			}
			data, err := c.do(ctx, "POST", p, map[string]any{
				"pageIndex": pageIndex, "pageSize": 100,
				"whetherMerge": 0, "Sessionid": c.device,
			})
			if err != nil {
				return nil, err
			}
			env, err := decodeEnvelope(data)
			if err != nil {
				return nil, err
			}
			if env.Code == 9004001 { // empty shelf on this channel
				break
			}
			if err := checkEnv(env, p); err != nil {
				return nil, err
			}
			var d struct {
				CommodityInfoList []LeaseShelfItem `json:"commodityInfoList"`
			}
			if err := json.Unmarshal(env.Data, &d); err != nil {
				return nil, fmt.Errorf("uu: %s payload: %w", p, err)
			}
			out = append(out, d.CommodityInfoList...)
			if len(d.CommodityInfoList) < 100 {
				break
			}
			pageIndex++
		}
	}
	return out, nil
}

// ---- publish / reprice / offshelf ----

type OnLeaseItem struct {
	AssetID            int64    `json:"AssetId"`
	IsCanLease         bool     `json:"IsCanLease"`
	IsCanSold          bool     `json:"IsCanSold"`
	LeaseDeposit       string   `json:"LeaseDeposit"` // NOTE: string on the wire
	LeaseMaxDays       int      `json:"LeaseMaxDays"`
	LeaseUnitPrice     float64  `json:"LeaseUnitPrice"`
	LongLeaseUnitPrice *float64 `json:"LongLeaseUnitPrice,omitempty"`
	CompensationType   *int     `json:"CompensationType,omitempty"`
}

type PublishResultRaw struct {
	AssetID     int64  `json:"AssetId"`
	Status      int    `json:"Status"`
	Remark      string `json:"Remark"`
	CommodityID int64  `json:"CommodityId"`
}

func (c *Client) PutItemsOnLeaseShelf(ctx context.Context, items []OnLeaseItem, compensationType int) ([]PublishResultRaw, error) {
	for i := range items {
		ct := compensationType
		items[i].CompensationType = &ct
	}
	data, err := c.do(ctx, "POST", "/api/commodity/Inventory/SellInventoryWithLeaseV2", map[string]any{
		"GameId": 730, "ItemInfos": items, "Sessionid": c.device,
	})
	if err != nil {
		return nil, err
	}
	env, err := decodeEnvelope(data)
	if err != nil {
		return nil, err
	}
	if err := checkEnv(env, "publish"); err != nil {
		return nil, err
	}
	var results []PublishResultRaw
	if err := json.Unmarshal(env.Data, &results); err != nil {
		return nil, fmt.Errorf("uu: publish payload: %w", err)
	}
	return results, nil
}

type ChangeLeaseItem struct {
	CommodityID        int64    `json:"CommodityId"`
	IsCanLease         bool     `json:"IsCanLease"`
	IsCanSold          bool     `json:"IsCanSold"`
	LeaseDeposit       string   `json:"LeaseDeposit"`
	LeaseMaxDays       int      `json:"LeaseMaxDays"`
	LeaseUnitPrice     float64  `json:"LeaseUnitPrice"`
	LongLeaseUnitPrice *float64 `json:"LongLeaseUnitPrice,omitempty"`
	CompensationType   int      `json:"CompensationType"`
}

type changeFailInfo struct {
	CommodityID int64  `json:"CommodityId"`
	IsSuccess   int    `json:"IsSuccess"`
	Message     string `json:"Message"`
}

// ChangeLeasePrices runs the pre-change init call then the batch price update.
func (c *Client) ChangeLeasePrices(ctx context.Context, items []ChangeLeaseItem, compensationType int) (int, []changeFailInfo, error) {
	if len(items) == 0 {
		return 0, nil, nil
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, strconv.FormatInt(it.CommodityID, 10))
	}
	initPayload := map[string]any{
		"changePriceChannel": 0, "commodityIdList": ids,
		"gameId": "730", "Sessionid": c.device,
	}
	if _, err := c.do(ctx, "POST", "/api/youpin/bff/new/commodity/commodity/change/price/v3/init/info", initPayload); err != nil {
		return 0, nil, fmt.Errorf("uu: prechange init: %w", err)
	}
	for i := range items {
		items[i].CompensationType = compensationType
		items[i].IsCanLease = true
		items[i].IsCanSold = false
	}
	data, err := c.do(ctx, "PUT", "/api/commodity/Commodity/PriceChangeWithLeaseV2", map[string]any{
		"Commoditys": items, "Sessionid": c.device,
	})
	if err != nil {
		return 0, nil, err
	}
	env, err := decodeEnvelope(data)
	if err != nil {
		return 0, nil, err
	}
	if err := checkEnv(env, "reprice"); err != nil {
		return 0, nil, err
	}
	var d struct {
		SuccessCount int              `json:"SuccessCount"`
		FailCount    int              `json:"FailCount"`
		Commoditys   []changeFailInfo `json:"Commoditys"`
	}
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return 0, nil, fmt.Errorf("uu: reprice payload: %w", err)
	}
	return d.SuccessCount, d.Commoditys, nil
}

// OffShelf removes lease/sale listings by commodity ids.
func (c *Client) OffShelf(ctx context.Context, commodityIDs []string) error {
	joined := ""
	for i, id := range commodityIDs {
		if i > 0 {
			joined += ","
		}
		joined += id
	}
	data, err := c.do(ctx, "PUT", "/api/commodity/Commodity/OffShelf", map[string]any{
		"Ids": joined, "IsDeleteCommodityCache": 1, "IsForceOffline": true,
	})
	if err != nil {
		return err
	}
	env, err := decodeEnvelope(data)
	if err != nil {
		return err
	}
	// A 200 envelope carrying a business error code is a FAILED delist; treating
	// it as success strands ghost shelves that the reprice loop keeps touching.
	return checkEnv(env, "offshelf")
}

// ---- market lease quotes ----

type MarketLeaseItem struct {
	CommodityName      string   `json:"CommodityName"`
	LeaseUnitPrice     *float64 `json:"LeaseUnitPrice"`
	LongLeaseUnitPrice *float64 `json:"LongLeaseUnitPrice"`
	LeaseDeposit       *string  `json:"LeaseDeposit"`
}

func (m MarketLeaseItem) Deposit() float64 {
	if m.LeaseDeposit == nil {
		return 0
	}
	return strF(*m.LeaseDeposit)
}

// GetMarketLeasePrice fetches up to cnt lease offers for a template whose
// deposit lies in the (minPrice,maxPrice) window — mirrors upstream filtering.
func (c *Client) GetMarketLeasePrice(ctx context.Context, templateID int64, minPrice, maxPrice float64, cnt int) ([]MarketLeaseItem, error) {
	payload := map[string]any{
		"hasLease": "true", "haveBuZhangType": 0, "listSortType": "2",
		"listType": 30, "mergeFlag": 0, "pageIndex": 1, "pageSize": 50,
		"sortType": "1", "sortTypeKey": "LEASE_DEFAULT", "status": "20",
		"stickerAbrade": 0, "stickersIsSort": false,
		"templateId":              strconv.FormatInt(templateID, 10),
		"ultraLongLeaseMoreZones": 0, "userId": c.userID,
		"Sessionid": c.device,
	}
	data, err := c.do(ctx, "POST", "/api/homepage/v3/detail/commodity/list/lease", payload)
	if err != nil {
		return nil, err
	}
	env, err := decodeEnvelope(data)
	if err != nil {
		return nil, err
	}
	if err := checkEnv(env, "market-lease"); err != nil {
		return nil, err
	}
	var d struct {
		CommodityList []MarketLeaseItem `json:"CommodityList"`
	}
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return nil, fmt.Errorf("uu: market-lease payload: %w", err)
	}
	out := make([]MarketLeaseItem, 0, cnt)
	for _, it := range d.CommodityList {
		if len(out) >= cnt {
			break
		}
		if it.LeaseDeposit != nil && minPrice < it.Deposit() && it.Deposit() < maxPrice {
			out = append(out, it)
		}
	}
	return out, nil
}

// maxListPages bounds every paginated fetch: a misbehaving server that
// keeps returning full pages must degrade to a truncated snapshot instead of
// an infinite rate-limited loop. 500 pages ≫ any realistic volume.
const maxListPages = 500

// ---- leased-out orders ----

// LeasedOutOrder is one entry of the leased-out order list.
//
// AssetRef: the response's asset-id field name is pending real-machine
// calibration (platform-uu-api-notes §待办); we probe the observed candidate
// spellings so the factor controller's listing join works as soon as any of
// them carries a value.
type LeasedOutOrder struct {
	OrderID       string `json:"orderId"`
	AssetID       string `json:"assetId"`
	CommodityInfo struct {
		Name         string `json:"name"`
		SteamAssetID string `json:"steamAssetId"`
		GoodsID      int64  `json:"goodsId"`
	} `json:"commodityInfo"`
	OrderStatus int    `json:"orderStatus"`
	LeaseDays   int    `json:"leaseDays"`
	RentPrice   string `json:"rentPrice"`
	Deposit     string `json:"deposit"`
	StartTime   string `json:"startTime"`
	EndTime     string `json:"endTime"`
}

// AssetRef returns the first non-empty asset identifier among the candidate
// fields; "" when the payload carries none (pre-calibration payloads).
func (o LeasedOutOrder) AssetRef() string {
	if o.AssetID != "" {
		return o.AssetID
	}
	return o.CommodityInfo.SteamAssetID
}

// GetLeasedOutOrders pages through finished/active rental orders.
func (c *Client) GetLeasedOutOrders(ctx context.Context) ([]LeasedOutOrder, error) {
	var out []LeasedOutOrder
	pageIndex := 0
	for {
		pageIndex++
		if pageIndex > maxListPages {
			break
		}
		data, err := c.do(ctx, "POST", "/api/youpin/bff/trade/v1/order/lease/out/list", map[string]any{
			"gameId": 730, "pageIndex": pageIndex, "pageSize": 50,
			"sortType": 0, "keywords": "",
		})
		if err != nil {
			return nil, err
		}
		env, err := decodeEnvelope(data)
		if err != nil {
			return nil, err
		}
		if err := checkEnv(env, "leased-out"); err != nil {
			return nil, err
		}
		var d struct {
			OrderDataList []LeasedOutOrder `json:"orderDataList"`
		}
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return nil, fmt.Errorf("uu: leased-out payload: %w", err)
		}
		out = append(out, d.OrderDataList...)
		if len(d.OrderDataList) < 50 {
			break
		}
	}
	return out, nil
}

// ---- zero CD sublet ----

type ZeroCDOrder struct {
	OrderID       int64 `json:"orderId"`
	CommodityInfo struct {
		Name string `json:"name"`
	} `json:"commodityInfo"`
}

func (c *Client) GetZeroCDList(ctx context.Context) ([]ZeroCDOrder, error) {
	data, err := c.do(ctx, "POST", "/api/youpin/bff/trade/v1/order/lease/sublet/canEnable/list", map[string]any{
		"pageIndex": 1, "pageSize": 20,
	})
	if err != nil {
		return nil, err
	}
	env, err := decodeEnvelope(data)
	if err != nil {
		return nil, err
	}
	if err := checkEnv(env, "zerocd-list"); err != nil {
		return nil, err
	}
	var orders []ZeroCDOrder
	if err := json.Unmarshal(env.Data, &orders); err != nil {
		return nil, fmt.Errorf("uu: zerocd-list payload: %w", err)
	}
	return orders, nil
}

func (c *Client) EnableZeroCD(ctx context.Context, orderIDs []int64) error {
	data, err := c.do(ctx, "POST", "/api/youpin/bff/order/sublet/open", map[string]any{"orderIds": orderIDs})
	if err != nil {
		return err
	}
	env, err := decodeEnvelope(data)
	if err != nil {
		return err
	}
	// same failure-determination rule as OffShelf: business code ≠ 0 must not
	// read as success, or the audit log records a zeroCD enable that never happened.
	return checkEnv(env, "zerocd-open")
}
