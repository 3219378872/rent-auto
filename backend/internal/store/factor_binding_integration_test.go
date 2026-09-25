//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

func bindingListing(t *testing.T, st *Store, ref, asset string, at time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	if err := st.UpsertListingFromShelf(ctx, domain.ShelfListing{
		Channel: domain.ChannelUU, GoodsRef: ref, AssetID: asset, HashName: "binding-H", ListedAt: at, MaxDays: 30,
	}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM listings WHERE channel='uu' AND goods_ref=$1`, ref).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func bindingOrder(t *testing.T, st *Store, ref, asset string, at time.Time) {
	t.Helper()
	if err := st.UpsertLeaseOrder(context.Background(), domain.LeaseOrder{
		Channel: domain.ChannelUU, OrderRef: ref, AssetID: asset, HashName: "binding-H",
		Status: "done", StartedAt: at, RentDays: 3,
	}); err != nil {
		t.Fatal(err)
	}
}

func orderBinding(t *testing.T, st *Store, ref string) *int64 {
	t.Helper()
	var id *int64
	if err := st.Pool.QueryRow(context.Background(), `SELECT factor_listing_id FROM lease_orders WHERE order_ref=$1`, ref).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFactorBindingDelayedOrdersAndAmbiguousBatch(t *testing.T) {
	st, cleanup := openStoreDB(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	oldID := bindingListing(t, st, "old", "same", now.Add(-4*time.Hour))
	newID := bindingListing(t, st, "new", "same", now.Add(-time.Hour))
	if _, err := st.Pool.Exec(ctx, `UPDATE listings SET actual_state='none',retired_at=$2 WHERE id=$1`, oldID, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Insert the ambiguous event first: it must not occupy the limit=1 batch.
	bindingOrder(t, st, "ambiguous", "same", time.Time{})
	bindingOrder(t, st, "late-old", "same", now.Add(-3*time.Hour))
	bindingOrder(t, st, "new-order", "same", now.Add(-30*time.Minute))
	if got := orderBinding(t, st, "ambiguous"); got != nil {
		t.Fatalf("unknown start guessed identity %d", *got)
	}
	for ref, want := range map[string]int64{"late-old": oldID, "new-order": newID} {
		if got := orderBinding(t, st, ref); got == nil || *got != want {
			t.Fatalf("%s binding=%v want=%d", ref, got, want)
		}
	}
	since := now.Add(-24 * time.Hour)
	for _, want := range []int64{oldID, newID} {
		orders, err := st.UnhandledFactorOrders(ctx, since, 1)
		if err != nil || len(orders) != 1 || orders[0].ListingID != want {
			t.Fatalf("batch starved or misattributed: %+v %v", orders, err)
		}
		if err := st.ApplyFactorFolds(ctx, []FactorFold{{ListingID: want, Factor: 1.03}}, orders); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := st.UnresolvedFactorOrders(ctx, since); err != nil || n != 1 {
		t.Fatalf("unresolved=%d err=%v", n, err)
	}
	// Later timestamp enrichment resolves the event without replaying others.
	bindingOrder(t, st, "ambiguous", "same", now.Add(-3*time.Hour))
	orders, err := st.UnhandledFactorOrders(ctx, since, 10)
	if err != nil || len(orders) != 1 || orders[0].ListingID != oldID {
		t.Fatalf("enriched binding: %+v %v", orders, err)
	}
}

func TestFactorBindingWaitsForShelfAndPreservesFirstObservation(t *testing.T) {
	st, cleanup := openStoreDB(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	bindingOrder(t, st, "first", "a", time.Time{})
	var first time.Time
	if err := st.Pool.QueryRow(ctx, `SELECT factor_observed_at FROM lease_orders WHERE order_ref='first'`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	// A newly published replacement alone is not evidence for an older order.
	bindingListing(t, st, "future", "a", first.Add(time.Hour))
	bindingOrder(t, st, "first", "a", time.Time{})
	if orders, err := st.UnhandledFactorOrders(ctx, now.Add(-24*time.Hour), 10); err != nil || len(orders) != 0 {
		t.Fatalf("future listing consumed old order: %+v %v", orders, err)
	}
	var again time.Time
	if err := st.Pool.QueryRow(ctx, `SELECT factor_observed_at FROM lease_orders WHERE order_ref='first'`).Scan(&again); err != nil || !again.Equal(first) {
		t.Fatalf("first observation refreshed: %s / %s err=%v", first, again, err)
	}
	oldID := bindingListing(t, st, "discovered", "a", now.Add(-time.Hour))
	orders, err := st.UnhandledFactorOrders(ctx, now.Add(-24*time.Hour), 10)
	if err != nil || len(orders) != 1 || orders[0].ListingID != oldID {
		t.Fatalf("late shelf did not resolve identity: %+v %v", orders, err)
	}
	// Empty asset identities must never create a many-to-many association.
	bindingListing(t, st, "no-asset", "", now.Add(-time.Hour))
	bindingOrder(t, st, "no-asset-order", "", now)
	if got := orderBinding(t, st, "no-asset-order"); got != nil {
		t.Fatal("bound an empty asset identity")
	}
}

func TestFactorBindingRejectsConcurrentOrderCorrection(t *testing.T) {
	st, cleanup := openStoreDB(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	oldID := bindingListing(t, st, "old-correction", "old-asset", now.Add(-time.Hour))
	newID := bindingListing(t, st, "new-correction", "new-asset", now.Add(-time.Hour))
	bindingOrder(t, st, "corrected", "old-asset", now)
	orders, err := st.UnhandledFactorOrders(ctx, now.Add(-24*time.Hour), 10)
	if err != nil || len(orders) != 1 {
		t.Fatalf("planned orders: %+v %v", orders, err)
	}
	// Sync repairs the asset after the feedback job has read its input.
	bindingOrder(t, st, "corrected", "new-asset", now)
	err = st.ApplyFactorFolds(ctx, []FactorFold{{ListingID: oldID, Factor: 1.03}}, orders)
	if !errors.Is(err, ErrFactorOrdersChanged) {
		t.Fatalf("stale plan accepted: %v", err)
	}
	var factor float64
	var applied bool
	if err := st.Pool.QueryRow(ctx, `SELECT factor FROM listings WHERE id=$1`, oldID).Scan(&factor); err != nil || factor != 1 {
		t.Fatalf("stale factor persisted: %v %v", factor, err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT factor_applied FROM lease_orders WHERE order_ref='corrected'`).Scan(&applied); err != nil || applied {
		t.Fatalf("corrected event consumed: %v %v", applied, err)
	}
	orders, err = st.UnhandledFactorOrders(ctx, now.Add(-24*time.Hour), 10)
	if err != nil || len(orders) != 1 || orders[0].ListingID != newID {
		t.Fatalf("corrected event not replayable: %+v %v", orders, err)
	}
	if err := st.ApplyFactorFolds(ctx, []FactorFold{{ListingID: newID, Factor: 1.03}}, orders); err != nil {
		t.Fatal(err)
	}
}

func TestFactorMigrationPreservesAppliedHistory(t *testing.T) {
	st, cleanup := openStoreDB(t)
	defer cleanup()
	ctx := context.Background()
	for {
		rolled, err := MigrateDown(ctx, st.Pool, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(rolled) == 0 {
			t.Fatal("factor migration not found")
		}
		if rolled[0] == "0011_shelf_factor_consistency" {
			break
		}
	}
	defer func() {
		if _, err := MigrateUp(ctx, st.Pool); err != nil {
			t.Error(err)
		}
	}()
	if _, err := st.Pool.Exec(ctx, `INSERT INTO templates(hash_name) VALUES('legacy-factor');
	 INSERT INTO listings(channel,goods_ref,asset_id,hash_name,factor) VALUES('uu','legacy','asset','legacy-factor',1.12);
	 INSERT INTO lease_orders(channel,order_ref,asset_id,hash_name,status,factor_applied,updated_at)
	 VALUES('uu','legacy','asset','legacy-factor','done',true,'2026-09-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateUp(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	var applied bool
	var observed time.Time
	if err := st.Pool.QueryRow(ctx, `SELECT factor_applied,factor_observed_at FROM lease_orders WHERE order_ref='legacy'`).Scan(&applied, &observed); err != nil || !applied || observed.Format(time.RFC3339) != "2026-09-01T00:00:00Z" {
		t.Fatalf("legacy markers changed: %v %s %v", applied, observed, err)
	}
	var factor float64
	if err := st.Pool.QueryRow(ctx, `SELECT factor FROM listings WHERE goods_ref='legacy'`).Scan(&factor); err != nil || factor != 1.12 {
		t.Fatalf("legacy factor rewritten: %v %v", factor, err)
	}
	if orders, err := st.UnhandledFactorOrders(ctx, time.Time{}, 10); err != nil || len(orders) != 0 {
		t.Fatalf("legacy order replayed: %+v %v", orders, err)
	}
}
