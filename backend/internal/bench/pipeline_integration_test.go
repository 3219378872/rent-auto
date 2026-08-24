//go:build integration

package bench_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/bench"
	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/logging"
	"github.com/3219378872/rent-auto/backend/internal/platform"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

// fakeAdapter feeds deterministic data into the collector pipeline.
type fakeAdapter struct {
	ch     domain.Channel
	inven  []domain.InventoryItem
	shelf  []domain.ShelfListing
	orders []domain.LeaseOrder
}

func (f *fakeAdapter) Channel() domain.Channel { return f.ch }
func (f *fakeAdapter) Caps() platform.Capabilities {
	return platform.Capabilities{DepositDirect: true, MaxBatchPublish: 10}
}
func (f *fakeAdapter) Healthy(context.Context) error { return nil }
func (f *fakeAdapter) Inventory(context.Context) ([]domain.InventoryItem, error) {
	return f.inven, nil
}
func (f *fakeAdapter) LeaseShelf(context.Context) ([]domain.ShelfListing, error) {
	return f.shelf, nil
}
func (f *fakeAdapter) PublishLease(context.Context, []platform.PublishLeaseRequest) ([]platform.PublishLeaseResult, error) {
	return nil, platform.ErrUnsupported
}
func (f *fakeAdapter) RepriceLease(context.Context, []platform.RepriceLeaseRequest) ([]platform.RepriceLeaseResult, error) {
	return nil, platform.ErrUnsupported
}
func (f *fakeAdapter) Delist(context.Context, []string) error { return platform.ErrUnsupported }
func (f *fakeAdapter) LeaseOrders(context.Context, time.Time) ([]domain.LeaseOrder, error) {
	return f.orders, nil
}
func (f *fakeAdapter) Wallet(context.Context) (float64, error) { return 0, platform.ErrUnsupported }

func openBizDB(t *testing.T) (*store.Store, func()) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := store.Open(context.Background(), url)
	if err != nil {
		t.Skipf("db unavailable: %v", err)
	}
	st := store.New(pool)
	if _, err := store.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cleanup := func() {
		_, _ = pool.Exec(context.Background(),
			`TRUNCATE inventory_items, listings, lease_orders, market_snapshots,
			        price_actions, strategies, fund_flows, daily_stats, templates CASCADE`)
		pool.Close()
	}
	return st, cleanup
}

func TestCollectorPipeline(t *testing.T) {
	st, done := openBizDB(t)
	defer done()
	ctx := context.Background()
	log := discardLog()

	ua := &fakeAdapter{ch: domain.ChannelUU, inven: []domain.InventoryItem{
		{Channel: domain.ChannelUU, AssetID: "u1", HashName: "AK-47 | Redline (FT)", DisplayName: "AK红线", TemplateID: 11, MarkPrice: 100, Tradable: true, Status: "in_stock"},
	}}
	ea := &fakeAdapter{ch: domain.ChannelECO, inven: []domain.InventoryItem{
		{Channel: domain.ChannelECO, AssetID: "e1", HashName: "AK-47 | Redline (FT)", DisplayName: "AK红线", MarkPrice: 110, Tradable: true, Status: "in_stock"},
	}}

	if n, err := bench.SyncInventory(ctx, ua, st, log); err != nil || n != 1 {
		t.Fatalf("uu sync: %v %d", err, n)
	}
	if n, err := bench.SyncInventory(ctx, ea, st, log); err != nil || n != 1 {
		t.Fatalf("eco sync: %v %d", err, n)
	}

	tpl, err := st.GetTemplate(ctx, "AK-47 | Redline (FT)")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.UUTemplateID == nil || *tpl.UUTemplateID != 11 {
		t.Fatalf("uu template id: %v", tpl.UUTemplateID)
	}
	if tpl.UUMarkPrice == nil || *tpl.UUMarkPrice != 100 || tpl.EcoRefPrice == nil || *tpl.EcoRefPrice != 110 {
		t.Fatalf("prices: %+v", tpl)
	}

	if _, err := bench.RecomputeAnchors(ctx, st); err != nil {
		t.Fatal(err)
	}
	tpl, _ = st.GetTemplate(ctx, "AK-47 | Redline (FT)")
	if tpl.ValueAnchor == nil || *tpl.ValueAnchor != 105.0 {
		t.Fatalf("anchor = %v want 105", tpl.ValueAnchor)
	}

	// shelf sync creates listings; second sync without the row marks it stale->none
	ua.shelf = []domain.ShelfListing{
		{Channel: domain.ChannelUU, GoodsRef: "77", AssetID: "u1", HashName: "AK-47 | Redline (FT)", RentPrice: 1.5, Deposit: 50, MaxDays: 60},
	}
	if n, err := bench.SyncShelf(ctx, ua, st, log); err != nil || n != 1 {
		t.Fatalf("shelf sync: %v %d", err, n)
	}
	listings, total, err := st.ListListings(ctx, store.ListingFilter{Channel: domain.ChannelUU})
	if err != nil || total != 1 || listings[0].ActualState != "active" {
		t.Fatalf("listings: %v %d %+v", err, total, listings)
	}
	// Empty payload while an active listing exists → breaker skips marking.
	if _, err := bench.SyncShelf(ctx, ua, st, log); err != nil {
		t.Fatal(err)
	}
	listings, _, _ = st.ListListings(ctx, store.ListingFilter{Channel: domain.ChannelUU})
	if listings[0].ActualState != "active" {
		t.Fatalf("empty-shelf breaker must keep state, got %s", listings[0].ActualState)
	}

	// A NON-empty shelf without the row prunes it (normal disappearance).
	ua.shelf = []domain.ShelfListing{
		{Channel: domain.ChannelUU, GoodsRef: "88", AssetID: "u2", HashName: "Other Item", RentPrice: 2.0, Deposit: 60, MaxDays: 60},
	}
	if _, err := bench.SyncShelf(ctx, ua, st, log); err != nil {
		t.Fatal(err)
	}
	listings, _, _ = st.ListListings(ctx, store.ListingFilter{Channel: domain.ChannelUU})
	states := map[string]string{}
	for _, l := range listings {
		states[l.GoodsRef] = l.ActualState
	}
	if states["77"] != "none" || states["88"] != "active" {
		t.Fatalf("expected 77→none 88→active, got %v", states)
	}

	// orders upsert + terminal income flag
	ua.orders = []domain.LeaseOrder{
		{Channel: domain.ChannelUU, OrderRef: "o1", HashName: "AK-47 | Redline (FT)", Status: "leasing", RentDays: 7, RentPrice: 1.5, Amount: 10.5, Deposits: 50},
	}
	if n, err := bench.SyncOrders(ctx, ua, st, time.Time{}, log); err != nil || n != 1 {
		t.Fatalf("orders sync: %v %d", err, n)
	}
	ua.orders = []domain.LeaseOrder{
		{Channel: domain.ChannelUU, OrderRef: "o1", HashName: "AK-47 | Redline (FT)", Status: "done", RentDays: 7, RentPrice: 1.5, Amount: 10.5, Deposits: 50, DueAt: time.Now().UTC()},
	}
	if _, err := bench.SyncOrders(ctx, ua, st, time.Time{}, log); err != nil {
		t.Fatal(err)
	}
	orders, total, err := st.ListOrders(ctx, store.OrderFilter{})
	if err != nil || total != 1 {
		t.Fatalf("orders list: %v %d", err, total)
	}
	if orders[0].Status != "done" || orders[0].FinishedAt == nil {
		t.Fatalf("terminal order: %+v", orders[0])
	}

	// snapshots batch insert
	err = st.InsertSnapshots(ctx, []store.Snapshot{
		{HashName: "AK-47 | Redline (FT)", Source: "uu_market", Kind: "lease_short", Rank: 1, Price: 1.4},
		{HashName: "AK-47 | Redline (FT)", Source: "uu_market", Kind: "deposit", Rank: 1, Price: 98},
	})
	if err != nil {
		t.Fatal(err)
	}

	// cost basis
	if err := st.SetCostBasis(ctx, domain.ChannelUU, "u1", 80); err != nil {
		t.Fatal(err)
	}
	items, _, _ := st.ListInventory(ctx, store.InventoryFilter{})
	if items[0].CostBasis != 80 {
		t.Fatalf("cost basis: %v", items[0].CostBasis)
	}
}

func discardLog() *slog.Logger { return logging.New("error") }
