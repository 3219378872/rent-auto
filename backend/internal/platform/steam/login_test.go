package steam

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// roundTripToLocalhost rewrites every request onto the mock server.
type rewriteTransport struct{ target *httptest.Server }

func (t rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = "http"
	r.URL.Host = strings.TrimPrefix(t.target.URL, "http://")
	return http.DefaultTransport.RoundTrip(r)
}

func makeJWT(exp int64) string {
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	p := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp)))
	return h + "." + p + ".sig"
}

func creds() Credentials {
	return Credentials{
		Username: "tester", Password: "hunter2",
		SharedSecret:   "AAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		IdentitySecret: "AQEBAQEBAQEBAQEBAQEBAQEBAQE=",
	}
}

func newMockSteam(t *testing.T, handler http.HandlerFunc) (*Session, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	s := NewSession(creds())
	s.http.Transport = rewriteTransport{srv}
	return s, srv
}

func TestLoginFullFlow(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour).Unix()
	gotUpdateCode := false

	var srvURL string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "sid123"})
			w.WriteHeader(200)
		case strings.Contains(r.URL.Path, "GetPasswordRSAPublicKey"):
			modHex := fmt.Sprintf("%x", key.N)
			expHex := fmt.Sprintf("%x", key.E)
			var wtr pbWriter
			wtr.str(1, modHex)
			wtr.str(2, expHex)
			wtr.u64(3, uint64(time.Now().Unix()))
			_, _ = w.Write(wtr.buf)
		case strings.Contains(r.URL.Path, "BeginAuthSessionViaCredentials"):
			// real Steam responses also carry interval as a float (wire 5);
			// leaving it out once hid the wire-type regression from tests.
			var wtr pbWriter
			wtr.u64(1, 42)
			wtr.bytes(2, []byte("reqid"))
			wtr.f32(3, 5.0)
			var inner pbWriter
			inner.u64(1, 3)
			wtr.bytes(4, inner.buf)
			wtr.u64(5, 76561199000000000)
			_, _ = w.Write(wtr.buf)
		case strings.Contains(r.URL.Path, "UpdateAuthSessionWithSteamGuardCode"):
			gotUpdateCode = true
			w.WriteHeader(200)
		case strings.Contains(r.URL.Path, "PollAuthSessionStatus"):
			var wtr pbWriter
			wtr.str(3, "refresh-token-xyz")
			wtr.str(4, makeJWT(exp))
			_, _ = w.Write(wtr.buf)
		case strings.HasSuffix(r.URL.Path, "/jwt/finalizelogin"):
			_, _ = w.Write([]byte(`{"steamID":76561199000000000,"transfer_info":[{"url":"` +
				srvURL + `/transfer","params":{"nonce":"n","auth":"a"}}]}`))
		case strings.HasSuffix(r.URL.Path, "/transfer"):
			w.WriteHeader(200)
		case r.URL.Path == "/my":
			http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "sid123"})
			w.WriteHeader(200)
		case r.URL.Path == "/trade/new/acknowledge":
			_, _ = w.Write([]byte("ok"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()
	srvURL = ts.URL

	jar, _ := cookiejar.New(nil)
	s := NewSession(creds())
	s.http.Jar = jar
	s.http.Transport = rewriteTransport{ts}

	if err := s.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Tokens.RefreshToken != "refresh-token-xyz" ||
		s.Tokens.SteamID != "76561199000000000" ||
		s.Tokens.AccessExp != exp {
		t.Fatalf("tokens: %+v", s.Tokens)
	}
	if !gotUpdateCode {
		t.Fatal("guard update never called")
	}
	if s.sessionid == "" {
		t.Fatal("sessionid cookie missing")
	}
}

// fullFlowMock is the TestLoginFullFlow mock with per-endpoint X-eresult
// header overrides, so failure-injection tests mirror the real WebAPI
// behavior: HTTP 200 + application error in the header.
func fullFlowMock(t *testing.T, eresult map[string]string) (*Session, *httptest.Server) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour).Unix()

	var srvURL string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v, ok := eresult["*"]; ok {
			w.Header().Set("X-eresult", v)
		}
		switch {
		case r.URL.Path == "/":
			http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "sid123"})
			w.WriteHeader(200)
		case strings.Contains(r.URL.Path, "GetPasswordRSAPublicKey"):
			if v, ok := eresult["GetPasswordRSAPublicKey"]; ok {
				w.Header().Set("X-eresult", v)
			}
			modHex := fmt.Sprintf("%x", key.N)
			var wtr pbWriter
			wtr.str(1, modHex)
			wtr.str(2, fmt.Sprintf("%x", key.E))
			wtr.u64(3, uint64(time.Now().Unix()))
			_, _ = w.Write(wtr.buf)
		case strings.Contains(r.URL.Path, "BeginAuthSessionViaCredentials"):
			if v, ok := eresult["BeginAuthSessionViaCredentials"]; ok {
				w.Header().Set("X-eresult", v)
			}
			var wtr pbWriter
			wtr.u64(1, 42)
			wtr.bytes(2, []byte("reqid"))
			wtr.f32(3, 5.0)
			var inner pbWriter
			inner.u64(1, 3)
			wtr.bytes(4, inner.buf)
			wtr.u64(5, 76561199000000000)
			_, _ = w.Write(wtr.buf)
		case strings.Contains(r.URL.Path, "UpdateAuthSessionWithSteamGuardCode"):
			if v, ok := eresult["UpdateAuthSessionWithSteamGuardCode"]; ok {
				w.Header().Set("X-eresult", v)
			}
			w.WriteHeader(200)
		case strings.Contains(r.URL.Path, "PollAuthSessionStatus"):
			if v, ok := eresult["PollAuthSessionStatus"]; ok {
				w.Header().Set("X-eresult", v)
			}
			var wtr pbWriter
			wtr.str(3, "refresh-token-xyz")
			wtr.str(4, makeJWT(exp))
			_, _ = w.Write(wtr.buf)
		case strings.HasSuffix(r.URL.Path, "/jwt/finalizelogin"):
			_, _ = w.Write([]byte(`{"steamID":76561199000000000,"transfer_info":[{"url":"` +
				srvURL + `/transfer","params":{"nonce":"n","auth":"a"}}]}`))
		case strings.HasSuffix(r.URL.Path, "/transfer"):
			w.WriteHeader(200)
		case r.URL.Path == "/my":
			http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "sid123"})
			w.WriteHeader(200)
		case r.URL.Path == "/trade/new/acknowledge":
			_, _ = w.Write([]byte("ok"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	srvURL = ts.URL

	jar, _ := cookiejar.New(nil)
	s := NewSession(creds())
	s.http.Jar = jar
	s.http.Transport = rewriteTransport{ts}
	return s, ts
}

// A rejected TOTP (X-eresult=85 on guard update) must surface the real
// failure — previously the header was ignored and login misreported as
// "empty refresh token after poll" (2026-08-27 real-machine incident).
func TestGuardUpdateEresultSurfaces(t *testing.T) {
	s, _ := fullFlowMock(t, map[string]string{
		"UpdateAuthSessionWithSteamGuardCode": "85",
	})
	err := s.Login(context.Background())
	if err == nil || !strings.Contains(err.Error(), "UpdateAuthSessionWithSteamGuardCode") ||
		!strings.Contains(err.Error(), "eresult=85") ||
		strings.Contains(err.Error(), "empty refresh token") {
		t.Fatalf("guard rejection must surface with eresult: %v", err)
	}
}

// 29 DuplicateRequest on the guard update is tolerated (upstream
// ignore_error_num=[29]) — login must proceed to poll and succeed.
func TestGuardUpdateDuplicateRequest29Tolerated(t *testing.T) {
	s, _ := fullFlowMock(t, map[string]string{
		"UpdateAuthSessionWithSteamGuardCode": "29",
	})
	if err := s.Login(context.Background()); err != nil {
		t.Fatalf("eresult=29 must be tolerated: %v", err)
	}
	if s.Tokens.RefreshToken != "refresh-token-xyz" {
		t.Fatalf("tokens: %+v", s.Tokens)
	}
}

// BeginAuthSession rejection (e.g. wrong password → 5) must fail at that step.
func TestBeginAuthSessionEresultSurfaces(t *testing.T) {
	s, _ := fullFlowMock(t, map[string]string{
		"BeginAuthSessionViaCredentials": "5",
	})
	err := s.Login(context.Background())
	if err == nil || !strings.Contains(err.Error(), "BeginAuthSessionViaCredentials") ||
		!strings.Contains(err.Error(), "InvalidPassword") {
		t.Fatalf("begin rejection must surface with name: %v", err)
	}
}

// Poll-side rate limiting (84) must be reported as-is, not as empty tokens.
func TestPollEresultSurfaces(t *testing.T) {
	s, _ := fullFlowMock(t, map[string]string{
		"PollAuthSessionStatus": "84",
	})
	err := s.Login(context.Background())
	if err == nil || !strings.Contains(err.Error(), "PollAuthSessionStatus") ||
		!strings.Contains(err.Error(), "RateLimitExceeded") {
		t.Fatalf("poll rejection must surface: %v", err)
	}
}

func TestRefreshAccessToken(t *testing.T) {
	exp := time.Now().Add(2 * time.Hour).Unix()
	s, _ := newMockSteam(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "GenerateAccessTokenForApp") {
			_ = r.ParseForm()
			if r.FormValue("refresh_token") != "rt" || r.FormValue("steamid") != "123" {
				t.Errorf("form: %v", r.Form)
			}
			w.Write([]byte(`{"response":{"access_token":"` + makeJWT(exp) + `"}}`))
			return
		}
		w.WriteHeader(404)
	})
	s.Tokens = SessionTokens{SteamID: "123", RefreshToken: "rt"}
	if err := s.RefreshAccessToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Tokens.AccessExp != exp {
		t.Fatalf("exp=%d want %d", s.Tokens.AccessExp, exp)
	}
}

func TestAcceptOfferWithConfirmation(t *testing.T) {
	var acceptForm, ajaxopQuery string
	s, _ := newMockSteam(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/tradeoffer/999/"):
			if !strings.HasSuffix(r.URL.Path, "/accept") {
				w.Write([]byte(`<script>var g_ulTradePartnerSteamID = '76561198000000001';</script>`))
				return
			}
			_ = r.ParseForm()
			acceptForm = r.Form.Encode()
			w.Write([]byte(`{"needs_mobile_confirmation":true}`))
		case r.URL.Path == "/mobileconf/getlist":
			w.Write([]byte(`{"success":true,"conf":[{"id":"c1","nonce":"n1","creator_id":999}]}`))
		case r.URL.Path == "/mobileconf/ajaxop":
			ajaxopQuery = r.URL.RawQuery
			w.Write([]byte(`{"success":true}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	s.sessionid = "sid-abc"
	ok, err := s.AcceptOfferWithPartner(context.Background(), "999", "76561198000000001")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !strings.Contains(acceptForm, "partner=76561198000000001") ||
		!strings.Contains(acceptForm, "sessionid=sid-abc") {
		t.Fatalf("accept form: %s", acceptForm)
	}
	for _, want := range []string{"op=allow", "cid=c1", "ck=n1", "tag=allow"} {
		if !strings.Contains(ajaxopQuery, want) {
			t.Fatalf("ajaxop missing %q: %s", want, ajaxopQuery)
		}
	}
}

// creator_id that is merely a numeric suffix of the offer id must never be
// confirmed — matching is exact only (upstream steampy match_end=False).
func TestAcceptOfferConfirmationExactMatchOnly(t *testing.T) {
	var ajaxopCalled bool
	s, _ := newMockSteam(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/accept"):
			_, _ = w.Write([]byte(`{"needs_mobile_confirmation":true}`))
		case r.URL.Path == "/mobileconf/getlist":
			// 456 is a suffix of 123456 — an unrelated confirmation that must
			// NOT be allowed just because its digits line up.
			_, _ = w.Write([]byte(`{"success":true,"conf":[{"id":"cx","nonce":"nx","creator_id":456}]}`))
		case r.URL.Path == "/mobileconf/ajaxop":
			ajaxopCalled = true
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			w.WriteHeader(404)
		}
	})
	old := sleepShort
	sleepShort = func() {}
	defer func() { sleepShort = old }()
	ok, err := s.AcceptOfferWithPartner(context.Background(), "123456", "76561198000000001")
	if ok || err == nil || !strings.Contains(err.Error(), "confirmation not found") {
		t.Fatalf("suffix-only creator must not be confirmed: ok=%v err=%v", ok, err)
	}
	if ajaxopCalled {
		t.Fatal("unrelated confirmation must never be allowed")
	}
}

func TestAcceptRejectsCostlyOffer(t *testing.T) {
	offers := []rawOffer{
		{TradeOfferID: "1", ItemsToGive: []interface{}{map[string]any{"id": "x"}}},
		{TradeOfferID: "2"},
	}
	costly := 0
	for _, o := range offers {
		if !o.IsZeroCost() {
			costly++
		}
	}
	if costly != 1 {
		t.Fatalf("zero-cost detection wrong: %d", costly)
	}
}

// ---- accept failure semantics: ambiguous responses must never read as success ----

func TestAcceptOfferRejectsHTTPError(t *testing.T) {
	s, _ := newMockSteam(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/tradeoffer/777/") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("<html>server error</html>"))
			return
		}
		w.WriteHeader(404)
	})
	ok, err := s.AcceptOfferWithPartner(context.Background(), "777", "76561198000000001")
	if ok || err == nil || !strings.Contains(err.Error(), "http 500") {
		t.Fatalf("http error must fail loudly: ok=%v err=%v", ok, err)
	}
}

func TestAcceptOfferRejectsNonJSONBody(t *testing.T) {
	// logged-out / login-page HTML with HTTP 200 must not count as accepted.
	s, _ := newMockSteam(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/tradeoffer/778/") && strings.HasSuffix(r.URL.Path, "/accept") {
			_, _ = w.Write([]byte(`<html><body>Login</body></html>`))
			return
		}
		w.WriteHeader(404)
	})
	ok, err := s.AcceptOfferWithPartner(context.Background(), "778", "76561198000000001")
	if ok || err == nil || !strings.Contains(err.Error(), "non-json") {
		t.Fatalf("html body must fail loudly: ok=%v err=%v", ok, err)
	}
}

func TestAcceptOfferRejectsUnknownState(t *testing.T) {
	s, _ := newMockSteam(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/accept") {
			_, _ = w.Write([]byte(`{"tradeofferid":"779","strError":"session mismatch"}`))
			return
		}
		w.WriteHeader(404)
	})
	ok, err := s.AcceptOfferWithPartner(context.Background(), "779", "76561198000000001")
	if ok || err == nil || !strings.Contains(err.Error(), "not accepted") {
		t.Fatalf("unknown state must fail: ok=%v err=%v", ok, err)
	}
}

func TestAcceptOfferDirectAcceptedState(t *testing.T) {
	// no mobile confirmation flag, state accepted → immediate success,
	// confirmer endpoints must never be contacted.
	contactedConf := false
	s, _ := newMockSteam(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/accept"):
			_, _ = w.Write([]byte(`{"tradeofferid":"880","trade_offer_state":"Accepted"}`))
		case strings.HasPrefix(r.URL.Path, "/mobileconf/"):
			contactedConf = true
			w.WriteHeader(404)
		default:
			w.WriteHeader(404)
		}
	})
	ok, err := s.AcceptOfferWithPartner(context.Background(), "880", "76561198000000001")
	if !ok || err != nil {
		t.Fatalf("accepted state: ok=%v err=%v", ok, err)
	}
	if contactedConf {
		t.Fatal("confirmer must not run for directly accepted offers")
	}
}

func TestAcceptOfferConfirmAllowFailurePropagates(t *testing.T) {
	s, _ := newMockSteam(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/accept"):
			_, _ = w.Write([]byte(`{"needs_mobile_confirmation":true}`))
		case r.URL.Path == "/mobileconf/getlist":
			_, _ = w.Write([]byte(`{"success":true,"conf":[{"id":"c9","nonce":"n9","creator_id":881}]}`))
		case r.URL.Path == "/mobileconf/ajaxop":
			_, _ = w.Write([]byte(`{"success":false}`))
		default:
			w.WriteHeader(404)
		}
	})
	ok, err := s.AcceptOfferWithPartner(context.Background(), "881", "76561198000000001")
	if ok || err == nil || !strings.Contains(err.Error(), "ajaxop allow failed") {
		t.Fatalf("allow failure must propagate: ok=%v err=%v", ok, err)
	}
}

func TestAcceptOfferAjaxopNonJSONFails(t *testing.T) {
	s, _ := newMockSteam(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/accept"):
			_, _ = w.Write([]byte(`{"needs_mobile_confirmation":true}`))
		case r.URL.Path == "/mobileconf/getlist":
			_, _ = w.Write([]byte(`{"success":true,"conf":[{"id":"c9","nonce":"n9","creator_id":882}]}`))
		case r.URL.Path == "/mobileconf/ajaxop":
			_, _ = w.Write([]byte(`<html>gateway</html>`))
		default:
			w.WriteHeader(404)
		}
	})
	if _, err := s.AcceptOfferWithPartner(context.Background(), "882", "76561198000000001"); err == nil ||
		!strings.Contains(err.Error(), "ajaxop decode") {
		t.Fatalf("ajaxop non-json must fail: %v", err)
	}
}
