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
	ErrorCode               int    `json:"ErrorCode"` // ErrorCodes enum; OK=1, 0 = field absent (legacy shape)
}

// ecoErrorOK is the platform ErrorCodes.OK value shared by delivery results.
const ecoErrorOK = 1

// Failed judges one send item by ErrorCode: an explicit non-OK code is
// authoritative; payloads without the field (code 0, legacy shape) fall back
// to the Error text. Confirmation flags are NOT failure — a sent offer
// awaiting mobile confirm is in-flight, handled by the Steam confirmlist flow.
func (r SendOfferResult) Failed() bool {
	if r.ErrorCode != 0 {
		return r.ErrorCode != ecoErrorOK
	}
	return r.Error != ""
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

// Failed judges one accept item: the accept endpoint always reports
// ErrorCode, so any non-OK code (including absent/0 = unknown) is failure,
// as is any Error text.
func (r AcceptOfferResult) Failed() bool {
	return r.ErrorCode != ecoErrorOK || r.Error != ""
}

// ResolveOfferResult is the aggregate of OneClickResolveOffer.
type ResolveOfferResult struct {
	SendOffers   []SendOfferResult   `json:"sendOfferResults"`
	AcceptOffers []AcceptOfferResult `json:"acceptOfferResults"`
}

// ErrorCodes enum (official): OK=1; 常见失败如 TooManyPending=108、
// NotSettled=100、TwoFactorCodeMismatch=88——逐单结果以 ErrorCode+Error 判定。

// FailedSends lists order nums whose send item failed per-item judgment;
// FailedAccepts likewise for accepts. Batch callers branch on these instead
// of reimplementing the ErrorCode rule.
func (r *ResolveOfferResult) FailedSends() []string {
	var out []string
	for _, s := range r.SendOffers {
		if s.Failed() {
			out = append(out, s.OrderNum)
		}
	}
	return out
}

// FailedAccepts lists order nums whose accept item failed per-item judgment.
func (r *ResolveOfferResult) FailedAccepts() []string {
	var out []string
	for _, a := range r.AcceptOffers {
		if a.Failed() {
			out = append(out, a.OrderNum)
		}
	}
	return out
}

// OneClickResolveOffer asks the platform to send all pending seller offers
// and accept all pending incoming offers in one batch call. Per-item
// outcomes stay in the result body — judge them with FailedSends/
// FailedAccepts (a transport-level nil error does NOT mean every item won).
func (c *Client) OneClickResolveOffer(ctx context.Context) (*ResolveOfferResult, error) {
	var out ResolveOfferResult
	if err := c.post(ctx, "/Api/open/order/OneClickResolveOffer", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SellerSendOffer sends the trade offer for one specific order
// (targeted retry path when a batch item fails). A per-item platform
// rejection (ErrorCode/Error on the single item) is returned as an error —
// callers that ignore the result body must not misread it as "offer sent".
func (c *Client) SellerSendOffer(ctx context.Context, orderNum string) (*SendOfferResult, error) {
	if orderNum == "" {
		return nil, fmt.Errorf("eco: order num required")
	}
	var out SendOfferResult
	biz := map[string]any{"OrderNum": orderNum, "GameId": "730"}
	if err := c.post(ctx, "/Api/open/order/SellerSendOffer", biz, &out); err != nil {
		return nil, err
	}
	if out.Failed() {
		return &out, fmt.Errorf("eco: send offer %s failed (code=%d): %s", orderNum, out.ErrorCode, out.Error)
	}
	return &out, nil
}
