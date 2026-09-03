package steam

// Mock regression tests for the review-fix round: X-eresult checks on
// RefreshAccessToken/GetReceivedActiveOffers, auth/rate sentinel mapping,
// and the doRawFull redirect cap.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/platform"
)

func TestCheckEresultSentinelMapping(t *testing.T) {
	for _, code := range []int{5, 25, 84, 85, 87, 88} {
		if err := checkEresult("M", code); !errors.Is(err, platform.ErrAuthExpired) {
			t.Fatalf("eresult=%d must map ErrAuthExpired, got %v", code, err)
		}
	}
	for _, code := range []int{10, 95, 96, 97, 108, 110, 116} {
		if err := checkEresult("M", code); !errors.Is(err, platform.ErrRateLimited) {
			t.Fatalf("eresult=%d must map ErrRateLimited, got %v", code, err)
		}
	}
	for _, code := range []int{2, 999} {
		err := checkEresult("M", code)
		if err == nil {
			t.Fatalf("eresult=%d must error", code)
		}
		for _, s := range []error{platform.ErrAuthExpired, platform.ErrRateLimited} {
			if errors.Is(err, s) {
				t.Fatalf("eresult=%d must stay generic, got %v", code, err)
			}
		}
	}
}

func TestRefreshAccessTokenChecksEresult(t *testing.T) {
	exp := time.Now().Add(2 * time.Hour).Unix()
	s, _ := newMockSteam(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "GenerateAccessTokenForApp") {
			w.WriteHeader(404)
			return
		}
		if r.FormValue("refresh_token") == "dead" {
			w.Header().Set("X-eresult", "5") // InvalidPassword: dead refresh token
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if r.FormValue("refresh_token") == "hot" {
			w.Header().Set("X-eresult", "10") // Busy
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.Header().Set("X-eresult", "1")
		_, _ = w.Write([]byte(`{"response":{"access_token":"` + makeJWT(exp) + `"}}`))
	})

	s.Tokens = SessionTokens{SteamID: "123", RefreshToken: "ok"}
	if err := s.RefreshAccessToken(context.Background()); err != nil {
		t.Fatalf("eresult=1 must succeed: %v", err)
	}

	s.Tokens = SessionTokens{SteamID: "123", RefreshToken: "dead"}
	if err := s.RefreshAccessToken(context.Background()); !errors.Is(err, platform.ErrAuthExpired) {
		t.Fatalf("dead refresh must map ErrAuthExpired, got %v", err)
	}

	s.Tokens = SessionTokens{SteamID: "123", RefreshToken: "hot"}
	if err := s.RefreshAccessToken(context.Background()); !errors.Is(err, platform.ErrRateLimited) {
		t.Fatalf("busy refresh must map ErrRateLimited, got %v", err)
	}
}

func TestGetReceivedActiveOffersChecksEresult(t *testing.T) {
	s, _ := newMockSteam(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "GetTradeOffers") {
			w.WriteHeader(404)
			return
		}
		if r.URL.Query().Get("access_token") == "dead" {
			w.Header().Set("X-eresult", "88") // TwoFactorCodeMismatch family → auth
			_, _ = w.Write([]byte(`{"response":{}}`))
			return
		}
		w.Header().Set("X-eresult", "1")
		_, _ = w.Write([]byte(`{"response":{"trade_offers_received":[{"tradeofferid":"7","trade_offer_state":2}]}}`))
	})
	s.Tokens = SessionTokens{SteamID: "123", AccessToken: "live"}

	offers, err := s.GetReceivedActiveOffers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 1 || offers[0].TradeOfferID != "7" {
		t.Fatalf("offers=%+v", offers)
	}

	s.Tokens.AccessToken = "dead"
	if _, err := s.GetReceivedActiveOffers(context.Background()); !errors.Is(err, platform.ErrAuthExpired) {
		t.Fatalf("dead token must map ErrAuthExpired, got %v", err)
	}
}

func TestDoRawFullRedirectCap(t *testing.T) {
	s, _ := newMockSteam(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/loop")
		w.WriteHeader(http.StatusFound)
	})
	// Disable the transport's own following so the manual chain is exercised.
	s.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://steamcommunity.com/loop", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = s.doRawFull(req)
	if err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Fatalf("redirect loop must hit the cap, got %v", err)
	}
}
