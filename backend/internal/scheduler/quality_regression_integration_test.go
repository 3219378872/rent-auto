//go:build integration

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/bench"
	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/scheduler"
)

// Regressions for CQ-01/CQ-02; failed against review baseline 1158513.
type qualityShelfAdapter struct {
	captureAdapter
	fetch func(context.Context) ([]domain.ShelfListing, error)
}

func (a *qualityShelfAdapter) LeaseShelf(ctx context.Context) ([]domain.ShelfListing, error) {
	return a.fetch(ctx)
}

func TestQualityShelfSnapshotMustNotEraseLaterPublish(t *testing.T) {
	st := openDB(t)
	ctx := context.Background()
	hash := "quality-snapshot"
	seedFactorFixture(t, st, hash)
	ad := &qualityShelfAdapter{captureAdapter: captureAdapter{ch: domain.ChannelUU}}
	ad.fetch = func(ctx context.Context) ([]domain.ShelfListing, error) {
		// The remote snapshot is already captured; a reconcile write completes
		// before the shelf response reaches SyncShelf's database writes.
		snapshot := []domain.ShelfListing{{Channel: domain.ChannelUU,
			GoodsRef: "G-" + hash, AssetID: "A-" + hash, HashName: hash,
			RentPrice: 2, Deposit: 100, MaxDays: 30}}
		err := st.RecordPublishedListing(ctx, "uu", "new-asset", hash, "new-ref", 2, 1.9, 100, 30)
		return snapshot, err
	}
	if _, err := bench.SyncShelf(ctx, ad, st, testLog()); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := st.Pool.QueryRow(ctx, `SELECT actual_state FROM listings WHERE goods_ref='new-ref'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "active" {
		t.Fatalf("listing published after snapshot: actual_state=%s; want active", state)
	}
}

func TestQualityShelfSnapshotMustNotOverwriteLaterReprice(t *testing.T) {
	st := openDB(t)
	ctx := context.Background()
	hash := "quality-reprice"
	id := seedFactorFixture(t, st, hash)
	ad := &qualityShelfAdapter{captureAdapter: captureAdapter{ch: domain.ChannelUU}}
	ad.fetch = func(ctx context.Context) ([]domain.ShelfListing, error) {
		snapshot := []domain.ShelfListing{{Channel: domain.ChannelUU,
			GoodsRef: "G-" + hash, AssetID: "A-" + hash, HashName: hash,
			RentPrice: 2, Deposit: 100, MaxDays: 30}}
		err := st.UpdateListingDecision(ctx, id, struct {
			Rent, Long, Deposit float64
			Days                int
		}{Rent: 2.3, Long: 2.2, Deposit: 100, Days: 30})
		return snapshot, err
	}
	if _, err := bench.SyncShelf(ctx, ad, st, testLog()); err != nil {
		t.Fatal(err)
	}
	var rent float64
	if err := st.Pool.QueryRow(ctx, `SELECT rent_price FROM listings WHERE id=$1`, id).Scan(&rent); err != nil {
		t.Fatal(err)
	}
	if rent != 2.3 {
		t.Fatalf("reprice completed after snapshot: rent=%.2f; want 2.30", rent)
	}
}

func TestQualityOldOrderMustNotFoldIntoReplacementListing(t *testing.T) {
	st := openDB(t)
	ctx := context.Background()
	hash := "quality-factor"
	oldID := seedFactorFixture(t, st, hash)
	mkOrder(t, st, "old-order", "A-"+hash, hash, "done", 3)
	if err := st.MarkListingDelisted(ctx, "uu", "G-"+hash); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordPublishedListing(ctx, "uu", "A-"+hash, hash, "replacement-ref", 2, 1.9, 100, 30); err != nil {
		t.Fatal(err)
	}
	var newID int64
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM listings WHERE goods_ref='replacement-ref'`).Scan(&newID); err != nil {
		t.Fatal(err)
	}
	orders, err := st.UnhandledFactorOrders(ctx, time.Now().Add(-24*time.Hour), 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].ListingID != oldID {
		t.Fatalf("old order identity: %+v", orders)
	}
	d := &scheduler.Deps{Store: st, Log: testLog()}
	if err := d.RunFactorEvents(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("old factor=%.2f, replacement factor=%.2f", factorOf(t, st, oldID), factorOf(t, st, newID))
	if got := factorOf(t, st, newID); got != 1 {
		t.Fatalf("replacement listing inherited a pre-publish order event: factor=%.2f; want 1.00", got)
	}
	if err := d.RunFactorEvents(ctx); err != nil {
		t.Fatal(err)
	}
	if factorOf(t, st, oldID) != 1.03 || factorOf(t, st, newID) != 1 {
		t.Fatal("repeated cycle changed the bound order's factors")
	}
}

func TestQualityShelfSnapshotMustNotUndoLaterDelist(t *testing.T) {
	st := openDB(t)
	ctx := context.Background()
	hash := "quality-delist"
	id := seedFactorFixture(t, st, hash)
	ad := &qualityShelfAdapter{captureAdapter: captureAdapter{ch: domain.ChannelUU}}
	ad.fetch = func(ctx context.Context) ([]domain.ShelfListing, error) {
		snapshot := []domain.ShelfListing{{Channel: domain.ChannelUU,
			GoodsRef: "G-" + hash, AssetID: "A-" + hash, HashName: hash,
			RentPrice: 2, Deposit: 100, MaxDays: 30}}
		return snapshot, st.MarkListingDelisted(ctx, "uu", "G-"+hash)
	}
	if _, err := bench.SyncShelf(ctx, ad, st, testLog()); err != nil {
		t.Fatal(err)
	}
	var state string
	var retired *time.Time
	if err := st.Pool.QueryRow(ctx, `SELECT actual_state,retired_at FROM listings WHERE id=$1`, id).Scan(&state, &retired); err != nil {
		t.Fatal(err)
	}
	if state != "none" || retired == nil {
		t.Fatalf("old snapshot undid delist: state=%s retired=%v", state, retired)
	}
}
