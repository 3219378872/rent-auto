//go:build integration

package recon

import (
	"context"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/store"
	"github.com/3219378872/rent-auto/backend/internal/testutil"
)

func reconDB(t *testing.T) *store.Store {
	t.Helper()
	url := testutil.DatabaseURL(t)
	pool, err := store.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `TRUNCATE inventory_items,listings,lease_orders,market_snapshots,price_actions,strategies,templates CASCADE`)
		pool.Close()
	})
	return store.New(pool)
}

func TestPlannerClockAndPersistentObservation(t *testing.T) {
	st := reconDB(t)
	ctx := context.Background()
	if _, _, err := st.EnsureGlobalStrategy(ctx, `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE strategies SET channel_route='eco_only'`); err != nil {
		t.Fatal(err)
	}
	item := domain.InventoryItem{Channel: domain.ChannelECO, AssetID: "owned", HashName: "H", Status: "listed", Tradable: true, MarkPrice: 100}
	if err := st.ApplyInventorySnapshot(ctx, domain.ChannelECO, []domain.InventoryItem{item}); err != nil {
		t.Fatal(err)
	}
	l := domain.ShelfListing{Channel: domain.ChannelECO, AssetID: item.AssetID, GoodsRef: "goods", HashName: item.HashName, RentPrice: 1, Deposit: 140, MaxDays: 30, ListedAt: time.Now().Add(-48 * time.Hour)}
	if err := st.UpsertListingFromShelf(ctx, l); err != nil {
		t.Fatal(err)
	}
	p := Planner{Store: st}
	if plan, err := p.Plan(ctx); err != nil || len(plan) != 0 {
		t.Fatalf("listed inventory must stay desired: %+v %v", plan, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE templates SET blacklisted=true`); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second)
	if plan, err := p.Plan(ctx); err != nil || len(plan) != 0 {
		t.Fatalf("first orphan observation: %+v %v", plan, err)
	}
	listings, err := st.AllActiveListings(ctx)
	if err != nil || len(listings) != 1 || listings[0].MismatchSince == nil || listings[0].MismatchSince.Before(before) {
		t.Fatalf("clock not initialized at call: %+v %v", listings, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE listings SET recon_mismatch_since=now()-interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertListingFromShelf(ctx, l); err != nil {
		t.Fatal(err)
	}
	if plan, err := p.Plan(ctx); err != nil || len(plan) != 1 || plan[0].Kind != "delist" {
		t.Fatalf("fresh heartbeat must not reset old mismatch: %+v %v", plan, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE templates SET blacklisted=false`); err != nil {
		t.Fatal(err)
	}
	if plan, err := p.Plan(ctx); err != nil || len(plan) != 0 {
		t.Fatalf("recovered desired state: %+v %v", plan, err)
	}
	listings, err = st.AllActiveListings(ctx)
	if err != nil || listings[0].MismatchSince != nil {
		t.Fatalf("recovery must clear observation: %+v %v", listings, err)
	}
}

func TestPlannerRejectsExpiredQuotesWithoutInjectedClock(t *testing.T) {
	st := reconDB(t)
	ctx := context.Background()
	if _, _, err := st.EnsureGlobalStrategy(ctx, `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE strategies SET channel_route='uu_only'`); err != nil {
		t.Fatal(err)
	}
	item := domain.InventoryItem{Channel: domain.ChannelUU, AssetID: "stock", HashName: "H", Status: "in_stock", Tradable: true, MarkPrice: 100}
	if err := st.ApplyInventorySnapshot(ctx, domain.ChannelUU, []domain.InventoryItem{item}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecomputeAnchors(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := store.Snapshot{HashName: "H", Source: "uu_market", Kind: "lease_short", Rank: 1, Price: 1, CapturedAt: time.Now().Add(-2 * time.Hour)}
	if err := st.InsertSnapshots(ctx, []store.Snapshot{snapshot}); err != nil {
		t.Fatal(err)
	}
	p := Planner{Store: st}
	if plan, err := p.Plan(ctx); err != nil || len(plan) != 0 {
		t.Fatalf("expired market must not publish: %+v %v", plan, err)
	}
	snapshot.CapturedAt = time.Now()
	if err := st.InsertSnapshots(ctx, []store.Snapshot{snapshot}); err != nil {
		t.Fatal(err)
	}
	if plan, err := p.Plan(ctx); err != nil || len(plan) != 1 {
		t.Fatalf("fresh quotes should publish: %+v %v", plan, err)
	}
}

func TestInventorySnapshotAtomicMissingAndRoutableStates(t *testing.T) {
	st := reconDB(t)
	ctx := context.Background()
	item := domain.InventoryItem{Channel: domain.ChannelECO, AssetID: "a", HashName: "H", Status: "listed", Tradable: true, MarkPrice: 100}
	other := item
	other.AssetID = "b"
	if err := st.ApplyInventorySnapshot(ctx, domain.ChannelECO, []domain.InventoryItem{item, other}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCostBasis(ctx, domain.ChannelECO, "a", 75); err != nil {
		t.Fatal(err)
	}
	bad := other
	bad.Status = "invalid"
	item.MarkPrice = 200
	if err := st.ApplyInventorySnapshot(ctx, domain.ChannelECO, []domain.InventoryItem{item, bad}); err == nil {
		t.Fatal("invalid later row must rollback complete snapshot")
	}
	var price, cost float64
	if err := st.Pool.QueryRow(ctx, `SELECT mark_price, cost_basis FROM inventory_items WHERE asset_id='a'`).Scan(&price, &cost); err != nil || price != 100 || cost != 75 {
		t.Fatalf("partial update escaped transaction: %v %v %v", price, cost, err)
	}
	if err := st.ApplyInventorySnapshot(ctx, domain.ChannelECO, []domain.InventoryItem{other}); err != nil {
		t.Fatal(err)
	}
	var state string
	var tradable bool
	if err := st.Pool.QueryRow(ctx, `SELECT status,tradable FROM inventory_items WHERE asset_id='a'`).Scan(&state, &tradable); err != nil || state != "missing" || tradable {
		t.Fatalf("missing is not sold: %s %v %v", state, tradable, err)
	}
	if err := st.ApplyInventorySnapshot(ctx, domain.ChannelECO, []domain.InventoryItem{item, other}); err != nil {
		t.Fatal(err)
	}
	items, err := st.RoutableInventory(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("listed assets must remain desired: %+v %v", items, err)
	}
	item.Channel, item.Status = domain.ChannelUU, "locked"
	if err := st.ApplyInventorySnapshot(ctx, domain.ChannelUU, []domain.InventoryItem{item}); err != nil {
		t.Fatal(err)
	}
	items, err = st.RoutableInventory(ctx)
	if err != nil || len(items) != 1 || items[0].AssetID != "b" {
		t.Fatalf("locked mirror cannot publish: %+v %v", items, err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT cost_basis FROM inventory_items WHERE channel='eco' AND asset_id='a'`).Scan(&cost); err != nil || cost != 75 {
		t.Fatalf("restoration overwrote manual cost: %v %v", cost, err)
	}
}
