//go:build integration

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/auth"
	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/pricing"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

// ---- shared helpers for DB-backed handler tests ----

// newAuthedServer wires a Server with a real store and a known password.
func newAuthedServer(t *testing.T, st *store.Store) *Server {
	t.Helper()
	s := NewServer(st, auth.NewJWT([]byte(testSecret)), "admin", "test",
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	h, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	s.PasswordHash = func(context.Context) (string, error) { return h, nil }
	return s
}

// tokenFor logs in against the live routes and returns a fresh bearer token.
func tokenFor(t *testing.T, routes http.Handler) string {
	t.Helper()
	rec := do(t, routes, "POST", "/api/v1/auth/login", "", `{"username":"admin","password":"hunter2"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	var lr struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lr); err != nil || lr.Token == "" {
		t.Fatalf("login body: %s", rec.Body.String())
	}
	return lr.Token
}

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

// truncateTables empties the tables a test touched. It must be deferred AFTER
// cleanup (LIFO: runs first) — t.Cleanup callbacks execute once the test
// function's defers have already closed the pool, where the TRUNCATE fails
// silently and rows leak across runs (observed: TestAuditSinceUntilPaging
// accumulated probe rows, total=12 instead of 3).
func truncateTables(t *testing.T, st *store.Store, tables string) {
	t.Helper()
	if _, err := st.Pool.Exec(context.Background(), `TRUNCATE `+tables); err != nil {
		t.Errorf("truncate %s: %v", tables, err)
	}
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

// Blacklist toggle: flips the flag, audits, 404 on unknown hash.
func TestTemplateBlacklistRoundTrip(t *testing.T) {
	st, cleanup := openAPIDB(t)
	defer cleanup()
	defer truncateTables(t, st, "templates, audit_log CASCADE")
	if _, err := store.MigrateUp(context.Background(), st.Pool); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertTemplate(context.Background(), store.Template{HashName: "H-BL"}); err != nil {
		t.Fatal(err)
	}

	s := newAuthedServer(t, st)
	routes := s.Routes()

	rec := do(t, routes, "PUT", "/api/v1/templates/blacklist", tokenFor(t, routes),
		`{"hash_name":"H-BL","blacklisted":true}`)
	if rec.Code != 200 {
		t.Fatalf("blacklist: %d %s", rec.Code, rec.Body.String())
	}
	tpl, err := st.GetTemplate(context.Background(), "H-BL")
	if err != nil || !tpl.Blacklisted {
		t.Fatalf("template not blacklisted: %+v %v", tpl, err)
	}

	rec = do(t, routes, "PUT", "/api/v1/templates/blacklist", tokenFor(t, routes),
		`{"hash_name":"no-such","blacklisted":true}`)
	if rec.Code != 404 {
		t.Fatalf("unknown template: %d", rec.Code)
	}
}

// Audit listing honors since/until (RFC3339) and page/page_size offsets.
func TestAuditSinceUntilPaging(t *testing.T) {
	st, cleanup := openAPIDB(t)
	defer cleanup()
	defer truncateTables(t, st, "audit_log")

	for i := 0; i < 3; i++ {
		if err := st.InsertAudit(context.Background(), domain.AuditEntry{
			Time:  time.Now().UTC().Add(time.Duration(i) * time.Minute),
			Actor: "system", Action: "probe.audit",
			Detail: map[string]any{"i": i},
		}); err != nil {
			t.Fatal(err)
		}
	}

	s := newAuthedServer(t, st)
	routes := s.Routes()
	tok := tokenFor(t, routes)

	rec := do(t, routes, "GET", "/api/v1/audit?action=probe.audit&page_size=2&page=1", tok, "")
	var p1 struct {
		Items []domain.AuditEntry `json:"items"`
		Total int                 `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p1); err != nil || p1.Total != 3 || len(p1.Items) != 2 {
		t.Fatalf("page1: %s (%v)", rec.Body.String(), err)
	}
	rec = do(t, routes, "GET", "/api/v1/audit?action=probe.audit&page_size=2&page=2", tok, "")
	var p2 struct {
		Items []domain.AuditEntry `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &p2)
	if len(p2.Items) != 1 {
		t.Fatalf("page2: %s", rec.Body.String())
	}

	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	rec = do(t, routes, "GET",
		"/api/v1/audit?action=probe.audit&since="+future, tok, "")
	var pf struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &pf)
	if pf.Total != 0 {
		t.Fatalf("since filter: %s", rec.Body.String())
	}
}

// Template-scope strategy lifecycle: upsert → effective merge → replace same
// row → delete → global fallback (US-STRAT-02 backend contract).
func TestTemplateStrategyLifecycle(t *testing.T) {
	st, cleanup := openAPIDB(t)
	defer cleanup()
	defer truncateTables(t, st, "strategies, templates CASCADE")
	ctx := context.Background()
	if _, err := store.MigrateUp(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertTemplate(ctx, store.Template{HashName: "H-TS"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.EnsureGlobalStrategy(ctx, "{}"); err != nil {
		t.Fatal(err)
	}

	s := newAuthedServer(t, st)
	routes := s.Routes()
	tok := tokenFor(t, routes)

	rec := do(t, routes, "POST", "/api/v1/strategies/template", tok,
		`{"hash_name":"H-TS","channel_route":"eco_only","params":{"baseline":{"k1":0.9}},"real_execution_enabled":true}`)
	if rec.Code != 200 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == 0 {
		t.Fatalf("create body: %s", rec.Body.String())
	}

	// Effective strategy must merge the template layer over global.
	es, err := st.GetEffectiveStrategy(ctx, "H-TS")
	if err != nil {
		t.Fatal(err)
	}
	if es.Route != "eco_only" || !es.RealEnabled {
		t.Fatalf("effective: route=%s real=%v", es.Route, es.RealEnabled)
	}
	pp, err := pricing.ParseParams(es.GlobalParams, es.Params)
	if err != nil {
		t.Fatal(err)
	}
	if pp.Baseline.K1 != 0.9 {
		t.Fatalf("merged k1 = %v, want 0.9", pp.Baseline.K1)
	}

	// Re-upsert replaces the SAME row (unique per hash).
	rec = do(t, routes, "POST", "/api/v1/strategies/template", tok,
		`{"hash_name":"H-TS","channel_route":"uu_only","params":{"baseline":{"k1":0.95}}}`)
	if rec.Code != 200 {
		t.Fatalf("replace: %d %s", rec.Code, rec.Body.String())
	}
	var rows int
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM strategies WHERE scope='template' AND hash_name='H-TS'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("rows=%d err=%v", rows, err)
	}
	// real_execution_enabled omitted on update → keeps previous value.
	es, _ = st.GetEffectiveStrategy(ctx, "H-TS")
	if es.Route != "uu_only" || !es.RealEnabled {
		t.Fatalf("after replace: route=%s real=%v", es.Route, es.RealEnabled)
	}

	// Delete → falls back to global.
	rec = do(t, routes, "DELETE", fmt.Sprintf("/api/v1/strategies/template/%d", created.ID), tok, "")
	if rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	es, err = st.GetEffectiveStrategy(ctx, "H-TS")
	if err != nil {
		t.Fatal(err)
	}
	if es.Route != "both" {
		t.Fatalf("fallback route = %s", es.Route)
	}

	// Deleting again is a clean 404.
	rec = do(t, routes, "DELETE", fmt.Sprintf("/api/v1/strategies/template/%d", created.ID), tok, "")
	if rec.Code != 404 {
		t.Fatalf("re-delete: %d", rec.Code)
	}
}

// Validation matrix: bad route / invalid json / wrong-typed param value /
// unknown template hash must all be rejected with 400-class errors.
func TestTemplateStrategyValidation(t *testing.T) {
	st, cleanup := openAPIDB(t)
	defer cleanup()
	defer truncateTables(t, st, "strategies, templates CASCADE")
	ctx := context.Background()
	if _, err := store.MigrateUp(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}

	s := newAuthedServer(t, st)
	routes := s.Routes()
	tok := tokenFor(t, routes)

	cases := []struct {
		name string
		body string
	}{
		{"bad route", `{"hash_name":"X","channel_route":"sideways"}`},
		{"invalid json params", `{"hash_name":"X","channel_route":"both","params":{"k1":}}`},
		{"wrong-typed param", `{"hash_name":"X","channel_route":"both","params":{"baseline":{"topn":"many"}}}`},
		{"unknown hash violates FK", `{"hash_name":"no-such-tpl","channel_route":"both"}`},
		{"missing hash", `{"channel_route":"both"}`},
	}
	for _, tc := range cases {
		rec := do(t, routes, "POST", "/api/v1/strategies/template", tok, tc.body)
		if rec.Code < 400 || rec.Code >= 500 {
			t.Fatalf("%s: want 4xx got %d (%s)", tc.name, rec.Code, rec.Body.String())
		}
	}
}
