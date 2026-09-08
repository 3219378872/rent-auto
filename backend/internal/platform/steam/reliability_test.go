package steam

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type reliabilityTransport func(*http.Request) (*http.Response, error)

func (f reliabilityTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNetworkErrorRedactsAccessToken(t *testing.T) {
	s := NewSession(Credentials{})
	s.tokens.AccessToken = "FAKE_ACCESS_TOKEN_MUST_NOT_LEAK"
	s.http.Transport = reliabilityTransport(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})
	_, err := s.GetReceivedActiveOffers(context.Background())
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("transport error category lost: %v", err)
	}
	if strings.Contains(err.Error(), s.tokens.AccessToken) {
		t.Fatal("secret token present in propagated error")
	}
}

func TestIsAliveRejectsLoginRedirect(t *testing.T) {
	s := NewSession(Credentials{})
	s.http.Transport = reliabilityTransport(func(r *http.Request) (*http.Response, error) {
		h := http.Header{}
		status := http.StatusOK
		if r.URL.Path == "/my" {
			status = http.StatusFound
			h.Set("Location", "/login/home/?goto=")
		}
		return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader("Steam sign in")), Request: r}, nil
	})
	if s.IsAlive(context.Background()) {
		t.Fatal("logged-out session reported alive")
	}
}

func TestHealthConcurrentWithAccept(t *testing.T) {
	s := NewSession(Credentials{})
	s.http.Transport = reliabilityTransport(func(r *http.Request) (*http.Response, error) {
		h := http.Header{}
		if r.URL.Path == "/my" {
			h.Set("Set-Cookie", "sessionid=renewed; Path=/")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(strings.NewReader(`{"trade_offer_state":"accepted"}`)), Request: r}, nil
	})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			s.IsAlive(context.Background())
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_, _ = s.AcceptOfferWithPartner(context.Background(), "123", "456")
		}
	}()
	wg.Wait()
}

func TestRefreshConcurrentWithOfferQuery(t *testing.T) {
	s := NewSession(Credentials{})
	s.tokens = SessionTokens{SteamID: "fake", RefreshToken: "fake", AccessToken: "old"}
	s.http.Transport = reliabilityTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"response":{"trade_offers_received":[]}}`
		if strings.Contains(r.URL.Path, "GenerateAccessTokenForApp") {
			body = `{"response":{"access_token":"fresh.fake.token"}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = s.RefreshAccessToken(context.Background())
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_, _ = s.GetReceivedActiveOffers(context.Background())
		}
	}()
	wg.Wait()
}

func TestAcceptTradeOfferStateRecovery(t *testing.T) {
	for _, tc := range []struct {
		name        string
		state       int
		wantOK      bool
		wantConfirm int
	}{
		{"needs_confirmation", 9, true, 1},
		{"already_accepted", 3, true, 0},
		{"cancelled", 6, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			confirmCalls, acceptCalls := 0, 0
			s, _ := newMockSteam(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/IEconService/GetTradeOffer/v1/":
					_, _ = fmt.Fprintf(w, `{"response":{"offer":{"tradeofferid":"999","trade_offer_state":%d,"items_to_give":[]}}}`, tc.state)
				case "/mobileconf/getlist":
					_, _ = w.Write([]byte(`{"success":true,"conf":[{"id":"c1","nonce":"n1","creator_id":"999"}]}`))
				case "/mobileconf/ajaxop":
					confirmCalls++
					_, _ = w.Write([]byte(`{"success":true}`))
				case "/tradeoffer/999/accept":
					acceptCalls++
					http.Error(w, "cannot accept pending/already accepted offer", http.StatusBadRequest)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
				}
			})
			ok, err := s.AcceptTradeOffer(context.Background(), "999")
			if ok != tc.wantOK || (err == nil) != tc.wantOK || confirmCalls != tc.wantConfirm || acceptCalls != 0 {
				t.Fatalf("ok=%v err=%v confirms=%d accepts=%d", ok, err, confirmCalls, acceptCalls)
			}
		})
	}
}

func TestZeroCostAcceptanceRechecksOffer(t *testing.T) {
	s, _ := newMockSteam(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/IEconService/GetTradeOffer/v1/" {
			t.Errorf("costly offer caused write: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"response":{"offer":{"tradeofferid":"999","trade_offer_state":9,"items_to_give":[{"assetid":"1"}]}}}`))
	})
	if ok, err := s.AcceptZeroCostTradeOffer(context.Background(), "999"); ok || err == nil {
		t.Fatalf("costly pending offer accepted: %v %v", ok, err)
	}
}

func TestAcceptDirectReceiptIsSuccess(t *testing.T) {
	s, _ := newMockSteam(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tradeid":"123456","needs_mobile_confirmation":false}`))
	})
	if ok, err := s.AcceptOfferWithPartner(context.Background(), "999", "123"); !ok || err != nil {
		t.Fatalf("completed trade receipt rejected: %v %v", ok, err)
	}
}
