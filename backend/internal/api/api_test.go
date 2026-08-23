package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
