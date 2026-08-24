//go:build integration

package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/3219378872/rent-auto/backend/internal/auth"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

func openAPIDB(t *testing.T) (*store.Store, func()) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := store.Open(context.Background(), url)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	st := store.New(pool)
	if _, err := store.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `TRUNCATE app_settings`)
		pool.Close()
	}
	return st, cleanup
}

// Logout must revoke every outstanding token via the session epoch:
// old token → 401 unauthorized (client auto-discards), fresh login works.
func TestLogoutRevokesTokens(t *testing.T) {
	st, cleanup := openAPIDB(t)
	defer cleanup()

	s := NewServer(st, auth.NewJWT([]byte(testSecret)), "admin", "test",
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	h, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	s.PasswordHash = func(context.Context) (string, error) { return h, nil }
	routes := s.Routes()

	rec := do(t, routes, "POST", "/api/v1/auth/login", "", `{"username":"admin","password":"hunter2"}`)
	if rec.Code != 200 {
		t.Fatalf("login: %d", rec.Code)
	}
	var lr struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lr); err != nil {
		t.Fatal(err)
	}

	if rec := do(t, routes, "GET", "/api/v1/auth/me", lr.Token, ""); rec.Code != 200 {
		t.Fatalf("pre-logout me: %d", rec.Code)
	}
	if rec := do(t, routes, "POST", "/api/v1/auth/logout", lr.Token, ""); rec.Code != 200 {
		t.Fatalf("logout: %d", rec.Code)
	}
	rec = do(t, routes, "GET", "/api/v1/auth/me", lr.Token, "")
	if rec.Code != 401 {
		t.Fatalf("post-logout me must be revoked, got %d", rec.Code)
	}
	var eb struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &eb); err != nil || eb.Code != "unauthorized" {
		t.Fatalf("revocation error body: %s (%v)", rec.Body.String(), err)
	}

	// Fresh login after the epoch bump issues a valid token.
	rec = do(t, routes, "POST", "/api/v1/auth/login", "", `{"username":"admin","password":"hunter2"}`)
	if rec.Code != 200 {
		t.Fatalf("re-login: %d", rec.Code)
	}
	var lr2 struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &lr2)
	if rec := do(t, routes, "GET", "/api/v1/auth/me", lr2.Token, ""); rec.Code != 200 {
		t.Fatalf("fresh token must work: %d", rec.Code)
	}
}
