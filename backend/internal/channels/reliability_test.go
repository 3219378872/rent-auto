package channels

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform/eco"
	"github.com/3219378872/rent-auto/backend/internal/platform/steam"
)

type reliabilityTransport func(*http.Request) (*http.Response, error)

func (f reliabilityTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOneClickSendErrorCodeControlsAudit(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	for _, tc := range []struct{ name, key, item, wanted, forbidden string }{
		{"explicit_failure", "sendOfferResults", `{"OrderNum":"order-1","ErrorCode":88,"Error":""}`, "order.send_offer_failed", "order.offer_sent"},
		{"pending_confirmation", "sendOfferResults", `{"OrderNum":"order-1","ErrorCode":1,"NeedsMobileConfirmation":true}`, "order.offer_sent", "order.send_offer_failed"},
		{"accept_failure_with_flag", "acceptOfferResults", `{"OrderNum":"order-1","ErrorCode":88,"NeedMobileConfirmation":true}`, "order.accept_offer_failed", "order.accepted"},
		{"accept_legacy_pending", "acceptOfferResults", `{"OrderNum":"order-1","ErrorCode":0,"NeedMobileConfirmation":true}`, "", "order.accept_offer_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &http.Client{Transport: reliabilityTransport(func(r *http.Request) (*http.Response, error) {
				body := `{"ResultCode":0,"ResultData":{"` + tc.key + `":[` + tc.item + `]}}`
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			c, err := eco.NewClient("mock-partner", keyPEM, eco.WithHTTP(h))
			if err != nil {
				t.Fatal(err)
			}
			r := NewRegistry(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			r.ecoClient = c
			var entries []domain.AuditEntry
			r.SetAuditFn(func(_ context.Context, e domain.AuditEntry) { entries = append(entries, e) })
			if err := r.EcoOneClickResolve(context.Background()); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, e := range entries {
				if e.Action == tc.wanted {
					found = true
				}
				if e.Action == tc.forbidden {
					t.Errorf("wrong audit %s for %s", e.Action, tc.item)
				}
			}
			if tc.wanted != "" && !found {
				t.Errorf("missing audit %s", tc.wanted)
			}
		})
	}
}

func TestSteamSessionHealthAndConfirmationConcurrent(t *testing.T) {
	h := &http.Client{Transport: reliabilityTransport(func(r *http.Request) (*http.Response, error) {
		header := http.Header{}
		var body string
		switch r.URL.Path {
		case "/my":
			header.Set("Set-Cookie", "sessionid=current; Path=/")
			body = "profile"
		case "/IEconService/GetTradeOffers/v1/":
			body = `{"response":{"trade_offers_received":[{"tradeofferid":"999","trade_offer_state":9,"items_to_give":[]}]}}`
		case "/IEconService/GetTradeOffer/v1/":
			body = `{"response":{"offer":{"tradeofferid":"999","trade_offer_state":9,"items_to_give":[]}}}`
		case "/mobileconf/getlist":
			body = `{"success":true,"conf":[{"id":"c","nonce":"n","creator_id":"999"}]}`
		case "/mobileconf/ajaxop":
			body = `{"success":true}`
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	ctx := context.Background()
	sess := steam.NewSession(steam.Credentials{IdentitySecret: "AAAAAAAAAAAAAAAAAAAAAAAAAAA="}, steam.WithHTTPClient(h))
	sess.AttachTokens(ctx, steam.SessionTokens{SteamID: "123", AccessToken: "fake", AccessExp: time.Now().Add(24 * time.Hour).Unix()})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewSteamSession(nil, nil, log)
	s.session = sess
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 25; i++ {
			if state := s.Health(ctx); state != "ok:123" {
				t.Errorf("health=%s", state)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 25; i++ {
			accepted, _, err := s.AcceptZeroCostOffers(ctx, log)
			if err != nil || accepted != 1 {
				t.Errorf("accepted=%d err=%v", accepted, err)
			}
		}
	}()
	wg.Wait()
}
