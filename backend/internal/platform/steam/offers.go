package steam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type rawOffer struct {
	TradeOfferID    string        `json:"tradeofferid"`
	TradeOfferState int           `json:"trade_offer_state"`
	ItemsToGive     []interface{} `json:"items_to_give"`
	ItemsToReceive  []interface{} `json:"items_to_receive"`
}

// GetReceivedActiveOffers returns received, active offers (raw counts only).
// The WebAPI result rides the X-eresult header: a throttled or logged-out
// token answers HTTP 200 with an application error, which must map to the
// unified sentinels (auth → session rebuild, rate → cooldown) instead of
// decoding into a silently empty offer list.
func (s *Session) GetReceivedActiveOffers(ctx context.Context) ([]rawOffer, error) {
	q := url.Values{
		"access_token":        {s.Tokens.AccessToken},
		"get_sent_offers":     {"1"},
		"get_received_offers": {"1"},
		"get_descriptions":    {"0"},
		"language":            {"english"},
		"active_only":         {"1"},
		"historical_only":     {"0"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		storeURL+"/IEconService/GetTradeOffers/v1/?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	body, status, er, err := s.doRawFull(req)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("steam: trade offers: http %d", status)
	}
	if err := checkEresult("GetTradeOffers", er); err != nil {
		return nil, err
	}
	var parsed struct {
		Response struct {
			TradeOffersReceived []rawOffer `json:"trade_offers_received"`
		} `json:"response"`
	}
	if err := jsonUnmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("steam: trade offers decode: %w", err)
	}
	return parsed.Response.TradeOffersReceived, nil
}

// IsZeroCost reports whether accepting the offer costs us nothing.
// Covers gift offers AND rental-return offers (renter sends item back).
func (o rawOffer) IsZeroCost() bool { return len(o.ItemsToGive) == 0 }

var (
	rePartnerID    = regexp.MustCompile(`g_ulTradePartnerSteamID\s*=\s*'(\d+)'`)
	reSevenDayHold = regexp.MustCompile(`You have logged in from a new device`)
)

var ErrSevenDayHold = errors.New("steam: 7-day trade hold (new device)")

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func timeNow() int64 { return time.Now().Unix() }

var sleepShort = func() { time.Sleep(3 * time.Second) }

func (s *Session) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Requested-With", "com.valvesoftware.android.steam.community")
	return s.doRaw(req)
}

// ResolvePartnerID extracts g_ulTradePartnerSteamID from an offer page.
func (s *Session) ResolvePartnerID(ctx context.Context, offerID string) (string, error) {
	pageBytes, err := s.get(ctx, communityURL+"/tradeoffer/"+offerID+"/")
	if err != nil {
		return "", err
	}
	page := string(pageBytes)
	if reSevenDayHold.MatchString(page) {
		return "", ErrSevenDayHold
	}
	m := rePartnerID.FindSubmatch(pageBytes)
	if m == nil {
		return "", fmt.Errorf("steam: partner id not found on offer page %s", offerID)
	}
	return string(m[1]), nil
}

// AcceptOfferWithPartner is the full accept path: resolve partner → accept → confirm.
// Ambiguous responses (non-2xx, non-JSON, unknown state) are reported as errors —
// never silently treated as success — so callers can retry safely.
func (s *Session) AcceptOfferWithPartner(ctx context.Context, offerID, partnerID string) (bool, error) {
	form := url.Values{
		"sessionid":    {s.sessionid},
		"tradeofferid": {offerID},
		"serverid":     {"1"},
		"partner":      {partnerID},
		"captcha":      {""},
	}
	accReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		communityURL+"/tradeoffer/"+offerID+"/accept",
		strings.NewReader(form.Encode()))
	if err != nil {
		return false, err
	}
	accReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	accReq.Header.Set("Referer", communityURL+"/tradeoffer/"+offerID+"/")
	body, status, err := s.doRawStatus(accReq)
	if err != nil {
		return false, err
	}
	if status < 200 || status > 299 {
		return false, fmt.Errorf("steam: accept %s: http %d", offerID, status)
	}
	var resp struct {
		NeedsMobileConfirmation bool   `json:"needs_mobile_confirmation"`
		TradeOfferState         string `json:"trade_offer_state"`
	}
	if err := jsonUnmarshal(body, &resp); err != nil {
		return false, fmt.Errorf("steam: accept %s: non-json response (http %d)", offerID, status)
	}
	if resp.NeedsMobileConfirmation {
		if err := s.confirmTradeOffer(ctx, offerID); err != nil {
			return false, err
		}
		return true, nil
	}
	if strings.EqualFold(resp.TradeOfferState, "accepted") {
		return true, nil
	}
	return false, fmt.Errorf("steam: accept %s not accepted (state=%q)", offerID, resp.TradeOfferState)
}

type confListResp struct {
	Success bool `json:"success"`
	Conf    []struct {
		ID    string `json:"id"`
		Nonce string `json:"nonce"`
		// 真机校订 (2026-08-27): Steam returns creator_id as a JSON string,
		// not an int64 — a typed field failed the whole confirmlist decode.
		CreatorID string `json:"creator_id"`
	} `json:"conf"`
}

func (s *Session) confParams(tag string) (url.Values, error) {
	ts := timeNow()
	key, err := GenerateConfirmationKey(s.Creds.IdentitySecret, tag, ts)
	if err != nil {
		return nil, err
	}
	v := url.Values{
		"p":   {GenerateDeviceID(s.Tokens.SteamID)},
		"a":   {s.Tokens.SteamID},
		"k":   {key},
		"t":   {strconv.FormatInt(ts, 10)},
		"m":   {"android"},
		"tag": {tag},
	}
	return v, nil
}

// confirmTradeOffer finds the pending confirmation matching offerID and allows it.
func (s *Session) confirmTradeOffer(ctx context.Context, offerID string) error {
	for attempt := 0; attempt < 3; attempt++ {
		params, err := s.confParams("conf")
		if err != nil {
			return err
		}
		listBytes, err := s.get(ctx, communityURL+"/mobileconf/getlist?"+params.Encode())
		if err != nil {
			return err
		}
		text := string(listBytes)
		if strings.Contains(text, "Incorrect Steam Guard codes") {
			return errors.New("steam: identity_secret invalid")
		}
		var list confListResp
		if err := jsonUnmarshal(listBytes, &list); err != nil {
			return fmt.Errorf("steam: confirmlist decode: %w", err)
		}
		for _, c := range list.Conf {
			// Exact creator_id == offer id only (upstream steampy default,
			// match_end=False): a loose suffix match once allowed confirming an
			// unrelated trade whose creator_id happened to end with our digits.
			if c.CreatorID == offerID {
				return s.allowConfirmation(ctx, c.ID, c.Nonce)
			}
		}
		sleepShort()
	}
	return errors.New("steam: confirmation not found after retries")
}

func (s *Session) allowConfirmation(ctx context.Context, confID, nonce string) error {
	params, err := s.confParams("allow")
	if err != nil {
		return err
	}
	params.Set("op", "allow")
	params.Set("cid", confID)
	params.Set("ck", nonce)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		communityURL+"/mobileconf/ajaxop?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	body, err := s.doRaw(req)
	if err != nil {
		return err
	}
	var out struct {
		Success bool `json:"success"`
	}
	if err := jsonUnmarshal(body, &out); err != nil {
		return fmt.Errorf("steam: ajaxop decode: %w", err)
	}
	if !out.Success {
		return errors.New("steam: ajaxop allow failed")
	}
	return nil
}
