package eco

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ---- seller order list / detail (delivery loop, Steamauto-aligned) ----

// SellerOrder is one row of /Api/open/order/SellerOrderList.
type SellerOrder struct {
	OrderNum       string  `json:"OrderNum"`
	OrderStateCode int     `json:"OrderStateCode"` // 1 = offer not sent yet
	GoodsName      string  `json:"GoodsName"`
	OrderAmount    float64 `json:"OrderAmount"`
}

// SellerOrdersPage is one page of the seller order list.
type SellerOrdersPage struct {
	PageResult  []SellerOrder `json:"PageResult"`
	TotalRecord int           `json:"TotalRecord"`
}

// SellerOrderList pages through seller orders; detailsState nil = all.
func (c *Client) SellerOrderList(ctx context.Context, start, end time.Time, detailsState *int, steamID string) ([]SellerOrder, error) {
	const layout = "2006-01-02"
	var out []SellerOrder
	pageIndex := 1
	for {
		biz := map[string]any{
			"StartTime": start.Format(layout),
			"EndTime":   end.Format(layout),
			"PageIndex": pageIndex,
			"PageSize":  100,
			"SteamId":   steamID,
		}
		if detailsState != nil {
			biz["DetailsState"] = *detailsState
		}
		var p SellerOrdersPage
		if err := c.post(ctx, "/Api/open/order/SellerOrderList", biz, &p); err != nil {
			return nil, err
		}
		out = append(out, p.PageResult...)
		if len(p.PageResult) < 100 {
			break
		}
		pageIndex++
	}
	return out, nil
}

// SellerOrderDetail carries the trade-offer id once the platform created it.
type SellerOrderDetail struct {
	TradeOfferID  string  `json:"TradeOfferId"`
	GoodsName     string  `json:"GoodsName"`
	TotalMoney    float64 `json:"TotalMoney"`
	BuyerNickname string  `json:"BuyerNickname"`
}

// GetSellerOrderDetail fetches fulfilment detail for one order.
func (c *Client) GetSellerOrderDetail(ctx context.Context, orderNum string) (*SellerOrderDetail, error) {
	var raw json.RawMessage
	if err := c.post(ctx, "/Api/open/order/SellerOrderDetail",
		map[string]any{"OrderNum": orderNum}, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("eco: empty detail for %s", orderNum)
	}
	var d SellerOrderDetail
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("eco: detail decode: %w", err)
	}
	return &d, nil
}

// SendOffer is the canonical name wrapper for SellerSendOffer.
func (c *Client) SendOffer(ctx context.Context, orderNum string) (*SendOfferResult, error) {
	return c.SellerSendOffer(ctx, orderNum)
}

// Detail is the canonical name wrapper for GetSellerOrderDetail.
func (c *Client) Detail(ctx context.Context, orderNum string) (*SellerOrderDetail, error) {
	return c.GetSellerOrderDetail(ctx, orderNum)
}
