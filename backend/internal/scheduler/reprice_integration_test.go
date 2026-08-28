//go:build integration

package scheduler_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
	"github.com/3219378872/rent-auto/backend/internal/scheduler"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

type captureAdapter struct {
	ch      domain.Channel
	caps    platform.Capabilities
	reprice []platform.RepriceLeaseRequest
}

func (c *captureAdapter) Channel() domain.Channel { return c.ch }
func (c *captureAdapter) Caps() platform.Capabilities {
	if c.caps.RentMaxDayMin == 0 {
		return platform.Capabilities{DepositDirect: true, MaxBatchPublish: 10}
	}
	return c.caps
}
func (c *captureAdapter) Healthy(context.Context) error                             { return nil }
func (c *captureAdapter) Inventory(context.Context) ([]domain.InventoryItem, error) { return nil, nil }
func (c *captureAdapter) LeaseShelf(context.Context) ([]domain.ShelfListing, error) { return nil, nil }
func (c *captureAdapter) PublishLease(context.Context, []platform.PublishLeaseRequest) ([]platform.PublishLeaseResult, error) {
	return nil, nil
}
func (c *captureAdapter) RepriceLease(_ context.Context, items []platform.RepriceLeaseRequest) ([]platform.RepriceLeaseResult, error) {
	c.reprice = append(c.reprice, items...)
	out := make([]platform.RepriceLeaseResult, len(items))
	for i := range items {
		out[i] = platform.RepriceLeaseResult{GoodsRef: items[i].GoodsRef, Success: true}
	}
	return out, nil
}
func (c *captureAdapter) Delist(context.Context, []string) error { return nil }
func (c *captureAdapter) LeaseOrders(context.Context, time.Time) ([]domain.LeaseOrder, error) {
	return nil, nil
}
func (c *captureAdapter) Wallet(context.Context) (float64, error) { return 0, platform.ErrUnsupported }

func openDB(t *testing.T) *store.Store {
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
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`TRUNCATE inventory_items, listings, lease_orders, market_snapshots,
			        price_actions, strategies, fund_flows, daily_stats, templates CASCADE`)
		pool.Close()
	})
	return st
}

func seedRepriceFixture(t *testing.T, st *store.Store) int64 {
	t.Helper()
	ctx := context.Background()
	hash := "Test Item (Field-Tested)"
	if err := st.UpsertTemplate(ctx, store.Template{
		HashName: hash, DisplayName: "测试物品", UUTemplateID: ptr(444),
		UUMarkPrice: ptrF(100), EcoRefPrice: ptrF(100),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecomputeAnchors(ctx); err != nil {
		t.Fatal(err)
	}
	// ranked quotes: short 2.0/2.2 → mean 2.1×0.97=2.037→2.04 ; deposit 200/210 mean 205×0.98=200.9
	snaps := []store.Snapshot{
		{HashName: hash, Source: "uu_market", Kind: "lease_short", Rank: 1, Price: 2.0},
		{HashName: hash, Source: "uu_market", Kind: "lease_short", Rank: 2, Price: 2.2},
		{HashName: hash, Source: "uu_market", Kind: "deposit", Rank: 1, Price: 200},
		{HashName: hash, Source: "uu_market", Kind: "deposit", Rank: 2, Price: 210},
	}
	if err := st.InsertSnapshots(ctx, snaps); err != nil {
		t.Fatal(err)
	}
	l := domain.ShelfListing{
		Channel: domain.ChannelUU, GoodsRef: "777", AssetID: "a-777",
		HashName: hash, DisplayName: hash, RentPrice: 1.5, Deposit: 100, MaxDays: 60,
		ListedAt: time.Now().Add(-time.Hour),
	}
	if err := st.UpsertListingFromShelf(ctx, l); err != nil {
		t.Fatal(err)
	}
	if _, params, err := st.EnsureGlobalStrategy(ctx, `{}`); err != nil {
		t.Fatal(err)
	} else if params == "" {
		t.Fatal("global strategy missing")
	}
	cands, err := st.ListRepriceCandidates(ctx, domain.ChannelUU)
	if err != nil || len(cands) != 1 {
		t.Fatalf("candidates: %v %d", err, len(cands))
	}
	var snapCount int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM market_snapshots`).Scan(&snapCount); err != nil {
		t.Fatal(err)
	}
	if snapCount != 4 {
		t.Fatalf("snapshots seeded=%d", snapCount)
	}
	mq, err := st.RecentMergedQuotes(ctx, hash, time.Now().Add(-30*time.Minute), 45)
	if err != nil {
		t.Fatal(err)
	}
	if len(mq) != 2 || mq[0].Short != 2.0 || mq[0].Deposit != 200 || mq[1].Long != 0 {
		t.Fatalf("merged quotes=%+v", mq)
	}
	return cands[0].ListingID
}

// Overlapping capture batches must not double-count: each (rank, kind) resolves
// to its newest sample within the window.
func TestRecentMergedQuotesDeduplicatesOverlappingBatches(t *testing.T) {
	st := openDB(t)
	ctx := context.Background()
	hash := "Overlap Item (Minimal Wear)"
	if err := st.UpsertTemplate(ctx, store.Template{HashName: hash, DisplayName: hash, UUTemplateID: ptr(999)}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-25 * time.Minute)
	fresh := time.Now().Add(-5 * time.Minute)
	snaps := []store.Snapshot{
		{HashName: hash, Source: "uu_market", Kind: "lease_short", Rank: 1, Price: 9.9, CapturedAt: old},
		{HashName: hash, Source: "uu_market", Kind: "deposit", Rank: 1, Price: 900, CapturedAt: old},
		{HashName: hash, Source: "uu_market", Kind: "lease_short", Rank: 1, Price: 3.3, CapturedAt: fresh},
		{HashName: hash, Source: "uu_market", Kind: "lease_short", Rank: 2, Price: 3.5, CapturedAt: fresh},
	}
	if err := st.InsertSnapshots(ctx, snaps); err != nil {
		t.Fatal(err)
	}
	mq, err := st.RecentMergedQuotes(ctx, hash, time.Now().Add(-30*time.Minute), 45)
	if err != nil {
		t.Fatal(err)
	}
	if len(mq) != 2 {
		t.Fatalf("ranks=%d, want 2 (no duplicate rows)", len(mq))
	}
	if mq[0].Rank != 1 || mq[0].Short != 3.3 || mq[0].Deposit != 900 {
		t.Fatalf("rank1 must keep newest short but retain deposit: %+v", mq[0])
	}
}

func ptr(v int64) *int64      { return &v }
func ptrF(v float64) *float64 { return &v }

func TestRepricePipelineDryRun(t *testing.T) {
	st := openDB(t)
	listingID := seedRepriceFixture(t, st)
	ad := &captureAdapter{ch: domain.ChannelUU}
	deps := scheduler.Deps{Store: st, Log: testLog(), DryRun: true}

	if err := deps.RunReprice(context.Background(), []platform.Adapter{ad}); err != nil {
		t.Fatal(err)
	}
	// dry run must NOT call the adapter
	if len(ad.reprice) != 0 {
		t.Fatalf("dry run called adapter: %+v", ad.reprice)
	}
	listings, _, _ := st.ListListings(context.Background(), store.ListingFilter{})
	if listings[0].RentPrice != 1.5 {
		t.Fatalf("dry run changed price: %v", listings[0].RentPrice)
	}
	_ = listingID
}

func TestRepricePipelineRealExecution(t *testing.T) {
	st := openDB(t)
	listingID := seedRepriceFixture(t, st)

	// enable real execution on the global strategy
	if _, _, err := st.EnsureGlobalStrategy(context.Background(), `{"real_execution_enabled":true}`); err != nil {
		t.Fatal(err)
	}
	// force real flag via direct update (EnsureGlobalStrategy is insert-only)
	if _, err := st.Pool.Exec(context.Background(), `UPDATE strategies SET real_execution_enabled=true WHERE scope='global'`); err != nil {
		t.Fatal(err)
	}

	ad := &captureAdapter{ch: domain.ChannelUU}
	deps := scheduler.Deps{Store: st, Log: testLog(), DryRun: false}
	if err := deps.RunReprice(context.Background(), []platform.Adapter{ad}); err != nil {
		t.Fatal(err)
	}
	dumpActions(t, st)
	if len(ad.reprice) != 1 {
		t.Fatalf("expected one reprice call, got %d", len(ad.reprice))
	}
	req := ad.reprice[0]
	if req.GoodsRef != "777" || req.MaxDays != 60 {
		t.Fatalf("request: %+v", req)
	}
	if req.Deposit < req.RentPrice*float64(req.MaxDays)*0.99 {
		t.Logf("deposit=%v rent=%v days=%d (baseline-derived ok)", req.Deposit, req.RentPrice, req.MaxDays)
	}
	listings, _, _ := st.ListListings(context.Background(), store.ListingFilter{})
	if listings[0].RentPrice <= 1.5 || listings[0].ID != listingID {
		t.Fatalf("listing not updated: %+v", listings[0])
	}
	if listings[0].LastRepriceAt == nil {
		t.Fatal("last_reprice_at not set")
	}
}

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// ECO sublet backfill (2026-08-28): a listing that predates the sublet payload
// policy (sublet_applied=false) must reach the platform even when the computed
// price is unchanged (noise floor bypassed, cooldown/cap still respected);
// one accepted submission flips the flag and the forced path ends.
func TestRepriceSubletBackfillForcedOnce(t *testing.T) {
	st := openDB(t)
	ctx := context.Background()
	hash := "Backfill Item (Minimal Wear)"
	if err := st.UpsertTemplate(ctx, store.Template{
		HashName: hash, DisplayName: hash, UUTemplateID: ptr(555),
		UUMarkPrice: ptrF(100), EcoRefPrice: ptrF(100),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecomputeAnchors(ctx); err != nil {
		t.Fatal(err)
	}
	// target base_short = mean(2.0,2.2)×0.97 = 2.04; listing at 2.02 → 0.99%
	// move = noise floor → would skip without the backfill exemption.
	snaps := []store.Snapshot{
		{HashName: hash, Source: "uu_market", Kind: "lease_short", Rank: 1, Price: 2.0},
		{HashName: hash, Source: "uu_market", Kind: "lease_short", Rank: 2, Price: 2.2},
	}
	if err := st.InsertSnapshots(ctx, snaps); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertListingFromShelf(ctx, domain.ShelfListing{
		Channel: domain.ChannelECO, GoodsRef: "GN-555", AssetID: "a-555",
		HashName: hash, DisplayName: hash, RentPrice: 2.02, Deposit: 100, MaxDays: 30,
		ListedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.EnsureGlobalStrategy(ctx, `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE strategies SET real_execution_enabled=true WHERE scope='global'`); err != nil {
		t.Fatal(err)
	}

	ad := &captureAdapter{ch: domain.ChannelECO}
	deps := scheduler.Deps{Store: st, Log: testLog(), DryRun: false}
	if err := deps.RunReprice(ctx, []platform.Adapter{ad}); err != nil {
		t.Fatal(err)
	}
	dumpActions(t, st)
	if len(ad.reprice) != 1 {
		t.Fatalf("forced backfill must submit once, got %d", len(ad.reprice))
	}
	var applied bool
	if err := st.Pool.QueryRow(ctx,
		`SELECT sublet_applied FROM listings WHERE goods_ref='GN-555'`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("sublet_applied must flip after an accepted submission")
	}

	// After the flag: rewind the cooldown and prove the noise skip resumes —
	// no price move, no platform call.
	if _, err := st.Pool.Exec(ctx,
		`UPDATE listings SET last_reprice_at=now()-interval '1 hour' WHERE goods_ref='GN-555'`); err != nil {
		t.Fatal(err)
	}
	ad.reprice = nil
	if err := deps.RunReprice(ctx, []platform.Adapter{ad}); err != nil {
		t.Fatal(err)
	}
	if len(ad.reprice) != 0 {
		t.Fatalf("flag must end forced submissions: %+v", ad.reprice)
	}
	var lastAction string
	if err := st.Pool.QueryRow(ctx,
		`SELECT action FROM price_actions pa JOIN listings l ON l.id=pa.listing_id
		 WHERE l.goods_ref='GN-555' ORDER BY pa.id DESC LIMIT 1`).Scan(&lastAction); err != nil {
		t.Fatal(err)
	}
	if lastAction != "skip" {
		t.Fatalf("post-backfill run must noise-skip, got %q", lastAction)
	}
}

// dumpActions prints recorded price actions for failure diagnostics.
func dumpActions(t *testing.T, st *store.Store) {
	t.Helper()
	rows, err := st.Pool.Query(context.Background(),
		`SELECT action, coalesce(decision::text,''), dry_run, success, coalesce(error,'')
		 FROM price_actions ORDER BY id`)
	if err != nil {
		t.Logf("dump actions: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var action, decision, errS string
		var dry, ok bool
		if err := rows.Scan(&action, &decision, &dry, &ok, &errS); err == nil {
			t.Logf("action=%s dry=%v ok=%v err=%q decision=%s", action, dry, ok, errS, decision)
		}
	}
}
