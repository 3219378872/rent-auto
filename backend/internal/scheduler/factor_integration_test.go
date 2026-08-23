//go:build integration

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/scheduler"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

// seedFactorFixture creates one template + active listing (factor=1.0) and
// returns the listing id.
func seedFactorFixture(t *testing.T, st *store.Store, hash string) int64 {
	t.Helper()
	ctx := context.Background()
	if err := st.UpsertTemplate(ctx, store.Template{
		HashName: hash, DisplayName: hash, UUMarkPrice: ptrF(100), EcoRefPrice: ptrF(100),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecomputeAnchors(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.EnsureGlobalStrategy(ctx, `{}`); err != nil {
		t.Fatal(err)
	}
	l := domain.ShelfListing{
		Channel: domain.ChannelUU, GoodsRef: "G-" + hash, AssetID: "A-" + hash,
		HashName: hash, DisplayName: hash, RentPrice: 2.0, Deposit: 100, MaxDays: 30,
		ListedAt: time.Now().Add(-48 * time.Hour),
	}
	if err := st.UpsertListingFromShelf(ctx, l); err != nil {
		t.Fatal(err)
	}
	cands, err := st.ListRepriceCandidates(ctx, domain.ChannelUU)
	if err != nil || len(cands) == 0 {
		t.Fatalf("candidates: %v", err)
	}
	for _, c := range cands {
		if c.HashName == hash {
			return c.ListingID
		}
	}
	t.Fatal("seeded listing not found")
	return 0
}

func factorOf(t *testing.T, st *store.Store, listingID int64) float64 {
	t.Helper()
	var f float64
	if err := st.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(factor,1.0) FROM listings WHERE id=$1`, listingID).Scan(&f); err != nil {
		t.Fatal(err)
	}
	return f
}

func mkOrder(t *testing.T, st *store.Store, ref, assetID, hash, status string, rentDays int) {
	t.Helper()
	if err := st.UpsertLeaseOrder(context.Background(), domain.LeaseOrder{
		Channel: domain.ChannelUU, OrderRef: ref, AssetID: assetID, HashName: hash,
		Status: status, RentDays: rentDays, RentPrice: 2, Amount: 14, Deposits: 100,
		DueAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

// done order with partial-term rental → factor steps up once; rerun folds nothing.
func TestFactorEventsRentSuccessIdempotent(t *testing.T) {
	st := openDB(t)
	hash := "Ctrl Item A (Minimal Wear)"
	listingID := seedFactorFixture(t, st, hash)
	mkOrder(t, st, "O-A1", "A-"+hash, hash, "done", 3)

	deps := &scheduler.Deps{Store: st, Log: testLog()}
	if err := deps.RunFactorEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := factorOf(t, st, listingID); got < 1.029 || got > 1.031 {
		t.Fatalf("factor after rent_success = %v, want ~1.03", got)
	}

	// second run must be a no-op (order folded)
	if err := deps.RunFactorEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := factorOf(t, st, listingID); got < 1.029 || got > 1.031 {
		t.Fatalf("rerun moved factor to %v — folding not idempotent", got)
	}
}

// bought_out weighs double; a full-term done order folds without changing factor.
func TestFactorEventsBoughtOutAndNeutralFullTerm(t *testing.T) {
	st := openDB(t)
	hashB := "Ctrl Item B (Field-Tested)"
	hashC := "Ctrl Item C (Battle-Scarred)"
	idB := seedFactorFixture(t, st, hashB)
	idC := seedFactorFixture(t, st, hashC)
	mkOrder(t, st, "O-B1", "A-"+hashB, hashB, "bought_out", 5)
	mkOrder(t, st, "O-C1", "A-"+hashC, hashC, "done", 30) // full term → neutral

	deps := &scheduler.Deps{Store: st, Log: testLog()}
	if err := deps.RunFactorEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := factorOf(t, st, idB); got < 1.059 || got > 1.061 {
		t.Fatalf("bought_out factor = %v, want ~1.06", got)
	}
	if got := factorOf(t, st, idC); got != 1.0 {
		t.Fatalf("full-term done must be neutral, got %v", got)
	}
}

// stale scan steps the factor down after stale_days of inactivity, and at f_min
// resets to 1.00 with an audit alert.
func TestStaleScanStepDownThenReset(t *testing.T) {
	st := openDB(t)
	ctx := context.Background()
	hashD := "Ctrl Item D (Factory New)"
	idD := seedFactorFixture(t, st, hashD)

	// anchor the last controller event 8 days ago (stale_days default = 7)
	if _, err := st.Pool.Exec(ctx,
		`UPDATE listings SET last_reprice_at = now() - interval '8 days' WHERE id=$1`, idD); err != nil {
		t.Fatal(err)
	}

	var audits []string
	deps := &scheduler.Deps{Store: st, Log: testLog(),
		Audit: func(_ context.Context, e domain.AuditEntry) { audits = append(audits, e.Action) }}

	if err := deps.RunFactorEvents(ctx); err != nil {
		t.Fatal(err)
	}
	if got := factorOf(t, st, idD); got > 0.999 {
		t.Fatalf("stale step-down missing: factor=%v", got)
	}

	// drive to f_min and let the next stale window pass
	if _, err := st.Pool.Exec(ctx,
		`UPDATE listings SET factor=0.85, last_factor_event_at = now() - interval '8 days' WHERE id=$1`, idD); err != nil {
		t.Fatal(err)
	}
	if err := deps.RunFactorEvents(ctx); err != nil {
		t.Fatal(err)
	}
	if got := factorOf(t, st, idD); got < 0.999 || got > 1.001 {
		t.Fatalf("f_min listing must reset to 1.00, got %v", got)
	}
	found := false
	for _, a := range audits {
		if a == "pricing.factor_reset" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reset alert not audited: %v", audits)
	}

	// freshly anchored listing must NOT step again immediately
	before := factorOf(t, st, idD)
	if err := deps.RunFactorEvents(ctx); err != nil {
		t.Fatal(err)
	}
	if after := factorOf(t, st, idD); after != before {
		t.Fatalf("anchor not respected: %v → %v", before, after)
	}
}
