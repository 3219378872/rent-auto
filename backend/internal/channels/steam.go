package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/platform/steam"
	"github.com/3219378872/rent-auto/backend/internal/secrets"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

const keySteamCreds = "steam_creds" // value_enc: full Credentials JSON

// SteamSession manages the Steam web session lifecycle on top of Registry.
type SteamSession struct {
	mu      sync.Mutex
	st      *store.Store
	box     *secrets.Box
	log     interface{ Info(string, ...any) }
	session *steam.Session
}

func NewSteamSession(st *store.Store, box *secrets.Box) *SteamSession {
	return &SteamSession{st: st, box: box}
}

// Restore rebuilds a session from stored tokens (fast path, no password login).
func (s *SteamSession) Restore(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.box == nil {
		return fmt.Errorf("APP_MASTER_KEY not configured")
	}
	setting, err := s.st.GetSetting(ctx, "steam_tokens")
	if err != nil {
		if err == store.ErrNotFound {
			return nil // not configured yet; silent
		}
		return err
	}
	if setting.ValueEnc == nil {
		return nil
	}
	plain, err := s.box.Open(string(setting.ValueEnc))
	if err != nil {
		return err
	}
	var tokens steam.SessionTokens
	if err := json.Unmarshal(plain, &tokens); err != nil {
		return err
	}
	creds, err := s.loadCreds(ctx)
	if err != nil || creds == nil {
		return err // creds missing → cannot build session
	}
	sess := steam.NewSession(*creds)
	sess.AttachTokens(tokens)
	s.session = sess
	return nil
}

func (s *SteamSession) loadCreds(ctx context.Context) (*steam.Credentials, error) {
	setting, err := s.st.GetSetting(ctx, keySteamCreds)
	if err == store.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if setting.ValueEnc == nil || s.box == nil {
		return nil, fmt.Errorf("steam credentials not stored")
	}
	plain, err := s.box.Open(string(setting.ValueEnc))
	if err != nil {
		return nil, err
	}
	var c steam.Credentials
	if err := json.Unmarshal(plain, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// SetCredentials validates and persists Steam credentials (encrypted), then logs in.
func (s *SteamSession) SetCredentials(ctx context.Context,
	username, password, sharedSecret, identitySecret string) error {
	creds := steam.Credentials{
		Username: username, Password: password,
		SharedSecret: sharedSecret, IdentitySecret: identitySecret,
	}
	sess := steam.NewSession(creds)
	if err := sess.Login(ctx); err != nil {
		return fmt.Errorf("steam login failed: %w", err)
	}
	b, _ := json.Marshal(creds)
	if s.box == nil {
		return fmt.Errorf("APP_MASTER_KEY not configured")
	}
	enc, err := s.box.Seal(b)
	if err != nil {
		return err
	}
	tok, _ := json.Marshal(sess.Tokens)
	tokEnc, err := s.box.Seal(tok)
	if err != nil {
		return err
	}
	if err := s.st.UpsertSettingEnc(ctx, keySteamCreds, []byte(enc)); err != nil {
		return err
	}
	if err := s.st.UpsertSettingEnc(ctx, "steam_tokens", []byte(tokEnc)); err != nil {
		return err
	}
	s.mu.Lock()
	s.session = sess
	s.mu.Unlock()
	s.log.Info("steam credential updated", "steamid", sess.Tokens.SteamID)
	return nil
}

// EnsureSession returns a live session, refreshing or relogging-in as needed.
func (s *SteamSession) EnsureSession(ctx context.Context) (*steam.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return nil, fmt.Errorf("steam not configured")
	}
	sess := s.session
	now := time.Now().Unix()
	if sess.Tokens.AccessExp != 0 && sess.Tokens.AccessExp-now < 3600 {
		if err := sess.RefreshAccessToken(ctx); err != nil {
			s.log.Info("access token refresh failed, relogin", "err", err)
			if err := sess.Login(ctx); err != nil {
				return nil, err
			}
			s.persistTokensLocked(ctx, sess)
		} else {
			s.persistTokensLocked(ctx, sess)
		}
	}
	return sess, nil
}

func (s *SteamSession) persistTokensLocked(ctx context.Context, sess *steam.Session) {
	if s.box == nil {
		return
	}
	tok, _ := json.Marshal(sess.Tokens)
	enc, err := s.box.Seal(tok)
	if err != nil {
		return
	}
	_ = s.st.UpsertSettingEnc(ctx, "steam_tokens", []byte(enc))
}

// Health reports steam status for the panel.
func (s *SteamSession) Health(ctx context.Context) string {
	s.mu.Lock()
	cfg := s.session != nil
	s.mu.Unlock()
	if !cfg {
		return "not_configured"
	}
	sess, err := s.EnsureSession(ctx)
	if err != nil || !sess.IsAlive(ctx) {
		return "error: session dead"
	}
	return "ok:" + sess.Tokens.SteamID
}

// AcceptZeroCostOffers polls incoming offers and accepts those costing us nothing.
func (s *SteamSession) AcceptZeroCostOffers(ctx context.Context, log interface{ Info(string, ...any) }) (accepted int, skipped int, err error) {
	sess, err := s.EnsureSession(ctx)
	if err != nil {
		return 0, 0, err
	}
	offers, err := sess.GetReceivedActiveOffers(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, o := range offers {
		if o.TradeOfferState != 2 && o.TradeOfferState != 3 { // not Active/ConfirmationNeed
			continue
		}
		if !o.IsZeroCost() {
			skipped++
			continue
		}
		partner, err := sess.ResolvePartnerID(ctx, o.TradeOfferID)
		if err != nil {
			return accepted, skipped, err
		}
		ok, err := sess.AcceptOfferWithPartner(ctx, o.TradeOfferID, partner)
		if err != nil {
			return accepted, skipped, err
		}
		if ok {
			accepted++
			log.Info("steam offer accepted", "offer", o.TradeOfferID)
		}
	}
	return accepted, skipped, nil
}

// AcceptTradeOffer accepts one offer with full mobile confirmation.
func (s *SteamSession) AcceptTradeOffer(ctx context.Context, offerID string) (bool, error) {
	sess, err := s.EnsureSession(ctx)
	if err != nil {
		return false, err
	}
	partner, err := sess.ResolvePartnerID(ctx, offerID)
	if err != nil {
		return false, err
	}
	return sess.AcceptOfferWithPartner(ctx, offerID, partner)
}
