//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

func shelfTime(t *testing.T, st *Store) time.Time {
	t.Helper()
	at, err := st.ShelfObservationTime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestShelfSnapshotOrderingAndFreshRecovery(t *testing.T) {
	st, cleanup := openStoreDB(t)
	defer cleanup()
	ctx := context.Background()
	old := shelfTime(t, st)
	newer := shelfTime(t, st)
	row := domain.ShelfListing{Channel: domain.ChannelUU, GoodsRef: "new", HashName: "H", RentPrice: 2}
	if _, _, err := st.ApplyShelfSnapshot(ctx, domain.ChannelUU, []domain.ShelfListing{row}, newer); err != nil {
		t.Fatal(err)
	}
	// An older response must not create a row absent from the newer snapshot.
	stale := row
	stale.GoodsRef = "stale"
	if _, _, err := st.ApplyShelfSnapshot(ctx, domain.ChannelUU, []domain.ShelfListing{stale}, old); err != nil {
		t.Fatal(err)
	}
	rows, total, err := st.ListListings(ctx, ListingFilter{})
	if err != nil || total != 1 || rows[0].GoodsRef != "new" || rows[0].ActualState != "active" {
		t.Fatalf("out-of-order snapshot changed shelf: %+v %v", rows, err)
	}
	row.RentPrice = 2.4
	if _, _, err := st.ApplyShelfSnapshot(ctx, domain.ChannelUU, []domain.ShelfListing{row}, shelfTime(t, st)); err != nil {
		t.Fatal(err)
	}
	var updated float64
	if err := st.Pool.QueryRow(ctx, `SELECT rent_price FROM listings WHERE goods_ref='new'`).Scan(&updated); err != nil || updated != 2.4 {
		t.Fatalf("fresh update rejected: %v %v", updated, err)
	}
	// A genuinely later observation must still update terms and retire absences.
	row.GoodsRef, row.RentPrice = "fresh", 3
	if n, _, err := st.ApplyShelfSnapshot(ctx, domain.ChannelUU, []domain.ShelfListing{row}, shelfTime(t, st)); err != nil || n != 1 {
		t.Fatalf("fresh snapshot: missing=%d err=%v", n, err)
	}
	var rent float64
	if err := st.Pool.QueryRow(ctx, `SELECT rent_price FROM listings WHERE goods_ref='fresh'`).Scan(&rent); err != nil || rent != 3 {
		t.Fatalf("fresh rent=%v err=%v", rent, err)
	}
}

func TestShelfSnapshotRollsBackRowsAndObservation(t *testing.T) {
	st, cleanup := openStoreDB(t)
	defer cleanup()
	ctx := context.Background()
	before := shelfTime(t, st)
	valid := domain.ShelfListing{Channel: domain.ChannelECO, GoodsRef: "valid", HashName: "rollback", RentPrice: 1}
	invalid := valid
	invalid.GoodsRef = "invalid"
	invalid.RentPrice = 1e12 // numeric(12,2) overflows after the first row was written
	if _, _, err := st.ApplyShelfSnapshot(ctx, domain.ChannelECO, []domain.ShelfListing{valid, invalid}, before); err == nil {
		t.Fatal("expected rejected row")
	}
	var n int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM listings WHERE hash_name='rollback'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial snapshot persisted: rows=%d err=%v", n, err)
	}
	// The failed attempt must not consume the observation, so this can retry.
	if _, _, err := st.ApplyShelfSnapshot(ctx, domain.ChannelECO, []domain.ShelfListing{valid}, before); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM listings WHERE goods_ref='valid'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("retry lost: rows=%d err=%v", n, err)
	}
}

func TestShelfSnapshotPreservesLeasedAndEmptyBreaker(t *testing.T) {
	st, cleanup := openStoreDB(t)
	defer cleanup()
	ctx := context.Background()
	leased := domain.ShelfListing{Channel: domain.ChannelUU, GoodsRef: "leased", HashName: "leased-H", Leased: true}
	if err := st.UpsertListingFromShelf(ctx, leased); err != nil {
		t.Fatal(err)
	}
	if n, active, err := st.ApplyShelfSnapshot(ctx, domain.ChannelUU, nil, shelfTime(t, st)); err != nil || n != 0 || active != 1 {
		t.Fatalf("empty breaker: %d %d %v", n, active, err)
	}
	other := leased
	other.GoodsRef, other.Leased = "other", false
	if _, _, err := st.ApplyShelfSnapshot(ctx, domain.ChannelUU, []domain.ShelfListing{other}, shelfTime(t, st)); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := st.Pool.QueryRow(ctx, `SELECT actual_state FROM listings WHERE goods_ref='leased'`).Scan(&state); err != nil || state != "leased" {
		t.Fatalf("leased state=%s err=%v", state, err)
	}
}
