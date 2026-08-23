//go:build integration

package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

func openTestDB(t *testing.T) (*Store, func()) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := Open(context.Background(), url)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	st := New(pool)
	if _, err := MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	cleanup := func() {
		_, _ = pool.Exec(context.Background(),
			`TRUNCATE app_settings, audit_log`)
		pool.Close()
	}
	return st, cleanup
}

func TestSettingsRoundTrip(t *testing.T) {
	st, done := openTestDB(t)
	defer done()
	ctx := context.Background()

	if err := st.UpsertSettingPlain(ctx, "cfg", map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	s, err := st.GetSetting(ctx, "cfg")
	if err != nil {
		t.Fatal(err)
	}
	if s.ValuePlain == nil {
		t.Fatal("plain value is nil")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(*s.ValuePlain), &got); err != nil {
		t.Fatal(err)
	}
	if got["a"] != float64(1) {
		t.Fatalf("plain = %v", *s.ValuePlain)
	}

	if err := st.UpsertSettingEnc(ctx, "secret", []byte("\x01\x02cipher")); err != nil {
		t.Fatal(err)
	}
	s2, err := st.GetSetting(ctx, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if string(s2.ValueEnc) != "\x01\x02cipher" {
		t.Fatalf("enc = %x", s2.ValueEnc)
	}

	if _, err := st.GetSetting(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// Rewriting a key with encrypted storage must clear any stale plaintext twin.
func TestUpsertSettingEncClearsPlainTwin(t *testing.T) {
	st, done := openTestDB(t)
	defer done()
	ctx := context.Background()

	if err := st.UpsertSettingPlain(ctx, "dual", map[string]any{"secret": "legacy"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSettingEnc(ctx, "dual", []byte("\x01cipher")); err != nil {
		t.Fatal(err)
	}
	s, err := st.GetSetting(ctx, "dual")
	if err != nil {
		t.Fatal(err)
	}
	if s.ValuePlain != nil {
		t.Fatalf("stale plaintext survived enc rewrite: %s", *s.ValuePlain)
	}
	if string(s.ValueEnc) != "\x01cipher" {
		t.Fatalf("enc = %x", s.ValueEnc)
	}
}

func TestAuditInsertAndList(t *testing.T) {
	st, done := openTestDB(t)
	defer done()
	ctx := context.Background()

	e := domain.AuditEntry{
		Time:    time.Now().UTC(),
		Actor:   "system",
		Action:  "reprice",
		Channel: "uu",
		Target:  "AK-47 | Redline",
		Detail:  map[string]any{"old": 1.5, "new": 1.4},
	}
	if err := st.InsertAudit(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertAudit(ctx, domain.AuditEntry{
		Time: time.Now().UTC(), Actor: "user:admin", Action: "login.success",
	}); err != nil {
		t.Fatal(err)
	}

	items, total, err := st.ListAudit(ctx, AuditFilter{Action: "reprice"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("filtered list = %d/%d", len(items), total)
	}
	if items[0].Channel != "uu" || items[0].Detail["new"] != 1.4 {
		t.Fatalf("entry mismatch: %+v", items[0])
	}

	all, totalAll, err := st.ListAudit(ctx, AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if totalAll < 2 || len(all) < 2 {
		t.Fatalf("expected >=2 entries, got %d/%d", len(all), totalAll)
	}
}
