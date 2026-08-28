//go:build integration

package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

// A re-synced order must heal stale started_at/due_at (tz-skew regression
// 2026-08-28: rows stored before the ECO CST fix would keep wrong instants
// forever) and a payload without timestamps must never NULL them out.
func TestUpsertLeaseOrderHealsTimestamps(t *testing.T) {
	ctx := context.Background()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := Open(ctx, url)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	defer pool.Close()
	st := New(pool)
	if _, err := MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `TRUNCATE lease_orders`)
	})

	skew := time.Date(2026, 8, 23, 12, 32, 29, 0, time.UTC) // UTC-parsed CST string (8h early)
	healed := skew.Add(-8 * time.Hour)
	due := time.Date(2026, 8, 31, 12, 34, 16, 0, time.UTC)

	o := domain.LeaseOrder{
		Channel: domain.ChannelECO, OrderRef: "TZ-1", HashName: "H-TZ",
		Status: "delivering", StartedAt: skew, DueAt: due,
	}
	if err := st.UpsertLeaseOrder(ctx, o); err != nil {
		t.Fatal(err)
	}

	// Re-sync without timestamps: existing values must survive.
	o.StartedAt, o.DueAt = time.Time{}, time.Time{}
	if err := st.UpsertLeaseOrder(ctx, o); err != nil {
		t.Fatal(err)
	}
	var started, storedDue *time.Time
	if err := st.Pool.QueryRow(ctx,
		`SELECT started_at, due_at FROM lease_orders WHERE channel='eco' AND order_ref='TZ-1'`,
	).Scan(&started, &storedDue); err != nil {
		t.Fatal(err)
	}
	if started == nil || !started.Equal(skew) || storedDue == nil || !storedDue.Equal(due) {
		t.Fatalf("timestamps not preserved: %v %v", started, storedDue)
	}

	// Re-sync with healed values: they must overwrite the stale ones.
	o.StartedAt, o.DueAt = healed, due
	if err := st.UpsertLeaseOrder(ctx, o); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx,
		`SELECT started_at, due_at FROM lease_orders WHERE channel='eco' AND order_ref='TZ-1'`,
	).Scan(&started, &storedDue); err != nil {
		t.Fatal(err)
	}
	if started == nil || !started.Equal(healed) {
		t.Fatalf("started_at not healed: %v, want %v", started, healed)
	}
}
