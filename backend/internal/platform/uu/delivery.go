package uu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ---- rental delivery: send trade offers via the platform ----

type TodoOrder struct {
	OrderNo       string `json:"orderNo"`
	CommodityName string `json:"commodityName"`
	Message       string `json:"message"`
}

// GetWaitDeliverList pages through the to-do list (pending deliveries etc.).
func (c *Client) GetWaitDeliverList(ctx context.Context) ([]TodoOrder, error) {
	var out []TodoOrder
	pageIndex := 1
	for {
		if pageIndex > maxListPages {
			break
		}
		data, err := c.do(ctx, "POST", "/api/youpin/bff/trade/todo/v1/orderTodo/list", map[string]any{
			"userId": c.userID, "pageIndex": pageIndex, "pageSize": 20,
			"Sessionid": c.device,
		})
		if err != nil {
			return nil, err
		}
		env, err := decodeEnvelope(data)
		if err != nil {
			return nil, err
		}
		if err := checkEnv(env, "orderTodo/list"); err != nil {
			return nil, err
		}
		var list []TodoOrder
		if len(env.Data) > 0 && string(env.Data) != "null" {
			if err := json.Unmarshal(env.Data, &list); err != nil {
				return nil, fmt.Errorf("uu: orderTodo payload: %w", err)
			}
		}
		out = append(out, list...)
		if len(list) < 20 {
			break
		}
		pageIndex++
	}
	return out, nil
}

const (
	// msgWaitSendOffer is the exact to-do message meaning "buyer paid,
	// seller must send the Steam offer" (upstream behavior).
	msgWaitSendOffer = "有买家下单，待您发送报价"
	// msgGift marks gift orders which we deliberately do not touch.
	msgGift = "赠送"
)

// SendDeliveryOffer asks the platform to emit the Steam trade offer for an order.
func (c *Client) SendDeliveryOffer(ctx context.Context, orderNo string) error {
	data, err := c.do(ctx, "PUT", "/api/youpin/bff/trade/v1/order/sell/delivery/send-offer", map[string]any{
		"orderNo": orderNo, "Sessionid": c.device,
	})
	if err != nil {
		return err
	}
	env, err := decodeEnvelope(data)
	if err != nil {
		return err
	}
	return checkEnv(env, "send-offer")
}

// GetDeliveryOfferStatus returns the raw delivery-offer status int
// (3 = sent successfully per upstream polling logic).
func (c *Client) GetDeliveryOfferStatus(ctx context.Context, orderNo string) (int, error) {
	data, err := c.do(ctx, "POST", "/api/youpin/bff/trade/v1/order/sell/delivery/get-offer-status", map[string]any{
		"orderNo": orderNo, "Sessionid": c.device,
	})
	if err != nil {
		return -1, err
	}
	env, err := decodeEnvelope(data)
	if err != nil {
		return -1, err
	}
	if err := checkEnv(env, "get-offer-status"); err != nil {
		return -1, err
	}
	var d struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return -1, fmt.Errorf("uu: offer status payload: %w", err)
	}
	return d.Status, nil
}

// DeliverPendingRentals walks the to-do list and sends offers for every
// payable rental order. Gift orders are counted and skipped.
// Returns (sentOrderNos, skippedGifts).
func (c *Client) DeliverPendingRentals(ctx context.Context, pollAttempts int, pollInterval func()) ([]string, int, error) {
	todos, err := c.GetWaitDeliverList(ctx)
	if err != nil {
		return nil, 0, err
	}
	var sent []string
	gifts := 0
	for _, t := range todos {
		if msgGift != "" && containsStr(t.Message, msgGift) {
			gifts++
			continue
		}
		if t.Message != msgWaitSendOffer {
			continue
		}
		if err := c.SendDeliveryOffer(ctx, t.OrderNo); err != nil {
			return sent, gifts, fmt.Errorf("uu: send offer %s: %w", t.OrderNo, err)
		}
		for i := 0; i < pollAttempts; i++ {
			if pollInterval != nil {
				pollInterval()
			}
			st, err := c.GetDeliveryOfferStatus(ctx, t.OrderNo)
			if err == nil && st == 3 {
				sent = append(sent, t.OrderNo)
				break
			}
			if i == pollAttempts-1 {
				return sent, gifts, fmt.Errorf("uu: offer %s not confirmed after %d polls (last=%d)", t.OrderNo, pollAttempts, st)
			}
		}
	}
	return sent, gifts, nil
}

func containsStr(s, sub string) bool { return strings.Contains(s, sub) }
