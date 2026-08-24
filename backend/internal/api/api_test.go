package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/auth"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	h, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(nil, auth.NewJWT([]byte(testSecret)), "admin", "test", discardLogger())
	s.PasswordHash = func(_ context.Context) (string, error) { return h, nil }
	return s
}

func do(t *testing.T, h http.Handler, method, target, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthPublic(t *testing.T) {
	s := newTestServer(t)
	rec := do(t, s.Routes(), "GET", "/api/v1/health", "", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("health: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLoginAndMe(t *testing.T) {
	s := newTestServer(t)
	h := s.Routes()

	rec := do(t, h, "POST", "/api/v1/auth/login", "", `{"username":"admin","password":"wrong"}`)
	if rec.Code != 401 {
		t.Fatalf("bad login should 401, got %d", rec.Code)
	}

	rec = do(t, h, "POST", "/api/v1/auth/login", "", `{"username":"admin","password":"hunter2"}`)
	if rec.Code != 200 {
		t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	}
	var lr struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lr); err != nil || lr.Token == "" {
		t.Fatalf("no token: %v %s", err, rec.Body.String())
	}

	rec = do(t, h, "GET", "/api/v1/auth/me", "", "")
	if rec.Code != 401 {
		t.Fatalf("me without token: %d", rec.Code)
	}
	rec = do(t, h, "GET", "/api/v1/auth/me", lr.Token, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "admin") {
		t.Fatalf("me with token: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUnknownProtectedEndpoint(t *testing.T) {
	s := newTestServer(t)
	rec := do(t, s.Routes(), "GET", "/api/v1/dashboard", "", "")
	if rec.Code != 401 {
		t.Fatalf("unknown protected endpoint must 401 without token, got %d", rec.Code)
	}
}

// Repeated failures from the same IP+username must lock out before the window
// elapses; a successful login resets the counter.
func TestLoginBruteForceLockout(t *testing.T) {
	s := newTestServer(t)
	h := s.Routes()
	body := `{"username":"admin","password":"wrong"}`

	for i := 0; i < loginMaxFails; i++ {
		if rec := do(t, h, "POST", "/api/v1/auth/login", "", body); rec.Code != 401 {
			t.Fatalf("attempt %d: want 401, got %d", i+1, rec.Code)
		}
	}
	rec := do(t, h, "POST", "/api/v1/auth/login", "", body)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("locked out attempt: want 429, got %d", rec.Code)
	}

	// even correct credentials are rejected while locked out
	rec = do(t, h, "POST", "/api/v1/auth/login", "", `{"username":"admin","password":"hunter2"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("correct password during lockout: want 429, got %d", rec.Code)
	}

	// a different username (different key) is unaffected
	rec = do(t, h, "POST", "/api/v1/auth/login", "", `{"username":"nobody","password":"x"}`)
	if rec.Code != 401 {
		t.Fatalf("other username should not be locked: got %d", rec.Code)
	}
}

// Oversized bodies must be rejected by the transport guard, not decoded —
// the public login endpoint previously accepted unbounded JSON (DoS).
func TestBodyLimitRejectsOversized(t *testing.T) {
	s := newTestServer(t)
	big := `{"username":"admin","password":"` + strings.Repeat("x", 2<<20) + `"}`
	rec := do(t, s.Routes(), "POST", "/api/v1/auth/login", "", big)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized body: status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Normal-sized requests keep working.
	ok := do(t, s.Routes(), "POST", "/api/v1/auth/login", "", `{"username":"admin","password":"hunter2"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("normal login broken: %d", ok.Code)
	}
}

// Expired limiter slots must be evicted once the map grows; usernames are
// user-controlled keys and would otherwise accumulate forever.
func TestLoginLimiterSweepEvictsExpired(t *testing.T) {
	l := newLoginLimiter()
	for i := 0; i < 2048; i++ {
		l.fail(fmt.Sprintf("user-%d", i))
	}
	if len(l.fails) < 1024 {
		t.Fatalf("setup: %d entries", len(l.fails))
	}
	// Age every slot past the window, then trigger eviction via allow().
	for _, s := range l.fails {
		s.start = time.Now().Add(-2 * loginFailWindow)
	}
	if !l.allow("fresh-user") {
		t.Fatal("fresh key must be allowed")
	}
	for k := range l.fails {
		if strings.HasPrefix(k, "user-") {
			t.Fatalf("expired slot %q survived sweep", k)
		}
	}
}
