package eco

import (
	"context"
	"fmt"
)

// ---- delivery offers (order fulfilment) — platform-eco-api-notes §发货域 ----

// SendOfferResult is one order's outcome from the delivery endpoints.
type SendOfferResult struct {
	OrderNum                string `json:"OrderNum"`
	OfferID                 string `json:"OfferId"`
	NeedsMobileConfirmation bool   `json:"NeedsMobileConfirmation"`
	NeedsEmailConfirmation  bool   `json:"NeedsEmailConfirmation"`
	Error                   string `json:"Error"`
}

// AcceptOfferResult is one incoming-offer acceptance outcome.
type AcceptOfferResult struct {
	OrderNum               string `json:"OrderNum"`
	OfferID                string `json:"OfferId"`
	NeedMobileConfirmation bool   `json:"NeedMobileConfirmation"`
	NeedEmailConfirmation  bool   `json:"NeedEmailConfirmation"`
	Error                  string `json:"Error"`
	ErrorCode              int    `json:"ErrorCode"` // ErrorCodes enum; OK=1
}

// ResolveOfferResult is the aggregate of OneClickResolveOffer.
type ResolveOfferResult struct {
	SendOffers   []SendOfferResult   `json:"sendOfferResults"`
	AcceptOffers []AcceptOfferResult `json:"acceptOfferResults"`
}

// ErrorCodes enum (official): OK=1; 常见失败如 TooManyPending=108、
// NotSettled=100、TwoFactorCodeMismatch=88——逐单结果以 ErrorCode+Error 判定。

// OneClickResolveOffer asks the platform to send all pending seller offers
// and accept all pending incoming offers in one batch call.
func (c *Client) OneClickResolveOffer(ctx context.Context) (*ResolveOfferResult, error) {
	var out ResolveOfferResult
	if err := c.post(ctx, "/Api/open/order/OneClickResolveOffer", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SellerSendOffer sends the trade offer for one specific order
// (targeted retry path when a batch item fails).
func (c *Client) SellerSendOffer(ctx context.Context, orderNum string) (*SendOfferResult, error) {
	if orderNum == "" {
		return nil, fmt.Errorf("eco: order num required")
	}
	var out SendOfferResult
	biz := map[string]any{"OrderNum": orderNum, "GameId": "730"}
	if err := c.post(ctx, "/Api/open/order/SellerSendOffer", biz, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
