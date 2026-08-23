// Package channels owns platform credentials and adapter lifecycle.
package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/bench"
	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
	"github.com/3219378872/rent-auto/backend/internal/platform/eco"
	"github.com/3219378872/rent-auto/backend/internal/platform/uu"
	"github.com/3219378872/rent-auto/backend/internal/secrets"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

const (
	keyUUToken  = "uu_token"     // value_enc: {"token":"..."} (legacy rows may still hold value_plain)
	keyECOCreds = "eco_creds"    // value_enc: {"partner_id":"...","private_key_pem":"..."}
	keyECOSteam = "eco_steam_id" // value_plain: {"steam_id":"..."}
)

// Registry builds and refreshes channel adapters from stored credentials.
type Registry struct {
	mu         sync.RWMutex
	st         *store.Store
	box        *secrets.Box
	log        *slog.Logger
	ad         map[domain.Channel]platform.Adapter
	lim        map[domain.Channel]platform.Limiter
	uuClient   *uu.Client
	ecoClient  *eco.Client
	ecoSteamID string
	uuHTTP     *http.Client // test seam: overrides UU API transport
}

func NewRegistry(st *store.Store, box *secrets.Box, log *slog.Logger) *Registry {
	return &Registry{st: st, box: box, log: log,
		ad:  map[domain.Channel]platform.Adapter{},
		lim: map[domain.Channel]platform.Limiter{}}
}

func (r *Registry) SetLimiter(ch domain.Channel, l platform.Limiter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lim[ch] = l
}

// Refresh (re)builds adapters from whatever credentials are stored.
func (r *Registry) Refresh(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// UU — token is stored encrypted; legacy plaintext rows are migrated lazily.
	setting, err := r.st.GetSetting(ctx, keyUUToken)
	if err != nil && err != store.ErrNotFound {
		return err
	}
	if err == nil {
		var payload struct {
			Token string `json:"token"`
		}
		switch {
		case setting.ValueEnc != nil && r.box != nil:
			plain, err := r.box.Open(string(setting.ValueEnc))
			if err != nil {
				r.log.Warn("uu token decrypt failed", "err", err)
			} else if json.Unmarshal(plain, &payload) == nil && payload.Token != "" {
				r.buildUUAdapter(ctx, payload.Token)
			}
		case setting.ValuePlain != nil:
			if json.Unmarshal([]byte(*setting.ValuePlain), &payload) == nil && payload.Token != "" {
				if r.buildUUAdapter(ctx, payload.Token) && r.box != nil {
					// Lazy migration: re-seal legacy plaintext into value_enc.
					b, _ := json.Marshal(map[string]string{"token": payload.Token})
					if enc, err := r.box.Seal(b); err == nil &&
						r.st.UpsertSettingEnc(ctx, keyUUToken, []byte(enc)) == nil { // clears value_plain
						r.log.Info("uu token migrated to encrypted storage")
					}
				}
			}
		}
	}

	// ECO
	ecoSetting, ecoErr := r.st.GetSetting(ctx, keyECOCreds)
	switch {
	case ecoErr == nil && ecoSetting.ValueEnc != nil && r.box != nil:
		plain, err := r.box.Open(string(ecoSetting.ValueEnc))
		if err != nil {
			r.log.Warn("eco creds decrypt failed", "err", err)
			break
		}
		var creds struct {
			PartnerID     string `json:"partner_id"`
			PrivateKeyPEM string `json:"private_key_pem"`
		}
		if json.Unmarshal(plain, &creds) == nil && creds.PartnerID != "" {
			steamID := ""
			if s, err := r.st.GetSetting(ctx, keyECOSteam); err == nil && s.ValuePlain != nil {
				var p struct {
					SteamID string `json:"steam_id"`
				}
				_ = json.Unmarshal([]byte(*s.ValuePlain), &p)
				steamID = p.SteamID
			}
			opts := []eco.Option{}
			if l, ok := r.lim[domain.ChannelECO]; ok {
				opts = append(opts, eco.WithLimiter(l))
			}
			if c, err := eco.NewClient(creds.PartnerID, []byte(creds.PrivateKeyPEM), opts...); err == nil {
				r.ad[domain.ChannelECO] = eco.NewAdapter(c, steamID)
				r.ecoClient = c
				r.ecoSteamID = steamID
			} else {
				r.log.Warn("eco adapter build failed", "err", err)
				delete(r.ad, domain.ChannelECO)
			}
		}
	case ecoErr != nil && ecoErr != store.ErrNotFound:
		return ecoErr
	}
	return nil
}

func (r *Registry) Get(ch domain.Channel) (platform.Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.ad[ch]
	return a, ok
}

func (r *Registry) All() []platform.Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]platform.Adapter, 0, len(r.ad))
	for _, ch := range []domain.Channel{domain.ChannelUU, domain.ChannelECO} {
		if a, ok := r.ad[ch]; ok {
			out = append(out, a)
		}
	}
	return out
}

// Health probes each configured adapter; missing channels report ErrNotFound-like state.
func (r *Registry) Health(ctx context.Context) map[string]string {
	out := map[string]string{"uu": "not_configured", "eco": "not_configured"}
	for _, a := range r.All() {
		if err := a.Healthy(ctx); err != nil {
			out[string(a.Channel())] = fmt.Sprintf("error: %v", err)
		} else {
			out[string(a.Channel())] = "ok"
		}
	}
	return out
}

// SetUUToken validates then persists the token (encrypted at rest) and rebuilds the adapter.
func (r *Registry) SetUUToken(ctx context.Context, token string) error {
	if r.box == nil {
		return fmt.Errorf("APP_MASTER_KEY not configured: cannot store credentials safely")
	}
	c, err := uu.NewClient(ctx, token, r.uuOptions()...)
	if err != nil {
		return fmt.Errorf("token invalid: %w", err)
	}
	b, _ := json.Marshal(map[string]string{"token": token})
	enc, err := r.box.Seal(b)
	if err != nil {
		return err
	}
	if err := r.st.UpsertSettingEnc(ctx, keyUUToken, []byte(enc)); err != nil {
		return err
	}
	r.mu.Lock()
	r.ad[domain.ChannelUU] = uu.NewAdapter(c)
	r.uuClient = c
	r.mu.Unlock()
	r.log.Info("uu credential updated", "nickname", c.Nickname())
	return nil
}

func (r *Registry) uuOptions() []uu.Option {
	opts := []uu.Option{}
	if l, ok := r.lim[domain.ChannelUU]; ok {
		opts = append(opts, uu.WithLimiter(l))
	}
	opts = append(opts, uu.WithLogger(r.log))
	if r.uuHTTP != nil {
		opts = append(opts, uu.WithHTTPClient(r.uuHTTP))
	}
	return opts
}

// SetUUHTTPClient pins the HTTP client used to build UU clients. Production
// code never calls this; tests use it to point the UU API at a local mock.
func (r *Registry) SetUUHTTPClient(h *http.Client) { r.uuHTTP = h }

// buildUUAdapter constructs the UU adapter from a raw token; reports success.
func (r *Registry) buildUUAdapter(ctx context.Context, token string) bool {
	c, err := uu.NewClient(ctx, token, r.uuOptions()...)
	if err != nil {
		r.log.Warn("uu adapter build failed", "err", err)
		delete(r.ad, domain.ChannelUU)
		return false
	}
	r.ad[domain.ChannelUU] = uu.NewAdapter(c)
	r.uuClient = c
	return true
}

// SetECOCreds validates then persists ECO credentials (encrypted at rest).
func (r *Registry) SetECOCreds(ctx context.Context, partnerID, privateKeyPEM, steamID string) error {
	if _, err := eco.NewClient(partnerID, []byte(privateKeyPEM)); err != nil {
		return fmt.Errorf("eco creds invalid: %w", err)
	}
	if steamID != "" {
		b, _ := json.Marshal(map[string]string{"steam_id": steamID})
		if err := r.st.UpsertSettingPlain(ctx, keyECOSteam, json.RawMessage(b)); err != nil {
			return err
		}
	}
	creds, _ := json.Marshal(map[string]string{
		"partner_id": partnerID, "private_key_pem": privateKeyPEM,
	})
	if r.box == nil {
		return fmt.Errorf("APP_MASTER_KEY not configured: cannot store credentials safely")
	}
	enc, err := r.box.Seal(creds)
	if err != nil {
		return err
	}
	if err := r.st.UpsertSettingEnc(ctx, keyECOCreds, []byte(enc)); err != nil {
		return err
	}
	return r.Refresh(ctx)
}

// ---- convenience passthroughs used by scheduler jobs ----

// UUMarketQuotes fetches ranked lease quotes for one template.
func (r *Registry) UUMarketQuotes(ctx context.Context, tplID int64, minP, maxP float64) ([]uu.MarketLeaseItem, error) {
	r.mu.RLock()
	c := r.uuClient
	r.mu.RUnlock()
	if c == nil {
		return nil, platform.ErrUnsupported
	}
	return c.GetMarketLeasePrice(ctx, tplID, minP, maxP, 15)
}

// EcoRefPrices converts the market dump into hash→price map.
func (r *Registry) EcoRefPrices(ctx context.Context) (map[string]float64, error) {
	r.mu.RLock()
	c := r.ecoClient
	r.mu.RUnlock()
	if c == nil {
		return nil, platform.ErrUnsupported
	}
	rows, err := c.GetMarketPriceDump(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(rows))
	for _, row := range rows {
		if row.MarketHashName != "" && row.SteamPriceCNY > 0 {
			out[row.MarketHashName] = bench.Round2(row.SteamPriceCNY)
		}
	}
	return out, nil
}

// ClearZeroCD enables 0CD sublet for all eligible UU orders.
func (r *Registry) ClearZeroCD(ctx context.Context) error {
	r.mu.RLock()
	c := r.uuClient
	r.mu.RUnlock()
	if c == nil {
		return platform.ErrUnsupported
	}
	orders, err := c.GetZeroCDList(ctx)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(orders))
	for _, o := range orders {
		ids = append(ids, o.OrderID)
	}
	if len(ids) == 0 {
		return nil
	}
	if err := c.EnableZeroCD(ctx, ids); err != nil {
		return err
	}
	audit(ctx, domain.AuditEntry{Time: time.Now().UTC(), Actor: "system",
		Action: "shelf.zero_cd_enabled", Channel: string(domain.ChannelUU),
		Detail: map[string]any{"orders": len(ids)}})
	return nil
}

// audit dispatches a write-operation audit entry when the hook is wired.
func audit(ctx context.Context, e domain.AuditEntry) {
	if AuditFn != nil {
		AuditFn(ctx, e)
	}
}

// SendLoginSmsCode starts the UU SMS login flow and reports the delivery mode.
// captcha is non-nil when retrying after a SmsModeCaptcha result.
func (r *Registry) SendLoginSmsCode(ctx context.Context, phone, sessionID string, captcha *uu.CaptchaResult) (uu.SmsCodeResult, error) {
	return uu.SendLoginSmsCode(ctx, nil, phone, sessionID, captcha)
}

// GetSmsUpSignInConfig exposes the manual-SMS instructions for the uplink flow.
func (r *Registry) GetSmsUpSignInConfig(ctx context.Context) (uu.SmsUpConfig, error) {
	return uu.GetSmsUpSignInConfig(ctx, nil)
}

// VerifyUUSms completes the SMS login and stores the token.
func (r *Registry) VerifyUUSms(ctx context.Context, phone, code, sessionID, loginReqTicket string) error {
	token, err := uu.SmsSignIn(ctx, nil, phone, code, sessionID, loginReqTicket)
	if err != nil {
		return err
	}
	return r.SetUUToken(ctx, token)
}

// DeliverPendingRentals walks the UU to-do list and sends pending offers,
// auditing every confirmed send (order.offer_sent).
func (r *Registry) DeliverPendingRentals(ctx context.Context) (sent []string, gifts int, err error) {
	r.mu.RLock()
	c := r.uuClient
	r.mu.RUnlock()
	if c == nil {
		return nil, 0, platform.ErrUnsupported
	}
	sent, gifts, err = c.DeliverPendingRentals(ctx, 5, func() { time.Sleep(1500 * time.Millisecond) })
	for _, orderNo := range sent {
		audit(ctx, domain.AuditEntry{Time: time.Now().UTC(), Actor: "system",
			Action: "order.offer_sent", Channel: string(domain.ChannelUU), Target: orderNo})
	}
	return sent, gifts, err
}

// AuditFn receives write-operation audit entries (wired to store by main).
var AuditFn func(ctx context.Context, e domain.AuditEntry)

// EcoOneClickResolve runs the platform batch send/accept for order fulfilment.
func (r *Registry) EcoOneClickResolve(ctx context.Context) error {
	r.mu.RLock()
	c := r.ecoClient
	r.mu.RUnlock()
	if c == nil {
		return platform.ErrUnsupported
	}
	out, err := c.OneClickResolveOffer(ctx)
	if err != nil {
		return err
	}
	for _, so := range out.SendOffers {
		if so.Error != "" || so.NeedsMobileConfirmation || so.NeedsEmailConfirmation {
			logWarnDelivery(r.log, "eco send offer failed", so.OrderNum, so.Error)
			audit(ctx, domain.AuditEntry{Time: time.Now().UTC(), Actor: "system",
				Action: "order.send_offer_failed", Channel: "eco", Target: so.OrderNum,
				Detail: map[string]any{"error": so.Error}})
			continue
		}
		audit(ctx, domain.AuditEntry{Time: time.Now().UTC(), Actor: "system",
			Action: "order.offer_sent", Channel: "eco", Target: so.OrderNum})
	}
	for _, ao := range out.AcceptOffers {
		if ao.ErrorCode != 1 || ao.Error != "" { // ErrorCode OK=1
			logWarnDelivery(r.log, "eco accept offer failed", ao.OrderNum, ao.Error)
			audit(ctx, domain.AuditEntry{Time: time.Now().UTC(), Actor: "system",
				Action: "order.accept_offer_failed", Channel: "eco", Target: ao.OrderNum,
				Detail: map[string]any{"error": ao.Error, "code": ao.ErrorCode}})
			continue
		}
		audit(ctx, domain.AuditEntry{Time: time.Now().UTC(), Actor: "system",
			Action: "order.accepted", Channel: "eco", Target: ao.OrderNum})
	}
	return nil
}

func logWarnDelivery(log interface{ Warn(string, ...any) }, msg, orderNum, errMsg string) {
	if log != nil {
		log.Warn(msg, "order", orderNum, "err", errMsg)
	}
}

// EcoOrderClient exposes the eco client for the delivery loop.
func (r *Registry) EcoOrderClient() interface {
	SellerOrderList(ctx context.Context, start, end time.Time, detailsState *int, steamID string) ([]eco.SellerOrder, error)
	SendOffer(ctx context.Context, orderNum string) (*eco.SendOfferResult, error)
	Detail(ctx context.Context, orderNum string) (*eco.SellerOrderDetail, error)
} {
	r.mu.RLock()
	c := r.ecoClient
	r.mu.RUnlock()
	if c == nil {
		return nil
	}
	return c
}
