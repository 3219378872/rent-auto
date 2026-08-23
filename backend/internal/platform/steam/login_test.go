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
			var wtr pbWriter
			wtr.u64(1, 42)
			wtr.bytes(2, []byte("reqid"))
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
