//go:build integration

package analytics_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/analytics"
	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

func openAnalyticsDB(t *testing.T) *store.Store {
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

func TestRollupAndDashboard(t *testing.T) {
	st := openAnalyticsDB(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	now := time.Now().UTC()

	pf := func(v float64) *float64 { return &v }
	hash := "Stat Item (Minimal Wear)"
	if err := st.UpsertTemplate(ctx, store.Template{HashName: hash, Category: "步枪", UUMarkPrice: pf(100)}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecomputeAnchors(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertInventoryItem(ctx, domain.InventoryItem{
		Channel: domain.ChannelUU, AssetID: "a1", HashName: hash,
		MarkPrice: 100, Tradable: true, Status: "leased",
	}, pf(80)); err != nil {
		t.Fatal(err)
	}

	// one done order (income) + one leasing order (held deposit)
	mk := func(ref, status string, amount, deposits float64) error {
		return st.UpsertLeaseOrder(ctx, domain.LeaseOrder{
			Channel: domain.ChannelUU, OrderRef: ref, AssetID: "a1", HashName: hash,
			Status: status, RentDays: 7, RentPrice: 2, Amount: amount, Deposits: deposits,
			DueAt: now,
		})
	}

	if err := mk("o-done", "done", 14, 100); err != nil {
		t.Fatal(err)
	}
	if err := mk("o-live", "leasing", 0, 120); err != nil {
		t.Fatal(err)
	}

	n, err := analytics.RollupTerminalOrders(ctx, st, log)
	if err != nil || n != 1 {
		t.Fatalf("rollup: %v %d", err, n)
	}
	// idempotent second run
	if n, _ := analytics.RollupTerminalOrders(ctx, st, log); n != 0 {
		t.Fatalf("second rollup must be no-op, got %d", n)
	}

	wallets := map[domain.Channel]float64{domain.ChannelECO: 555.5}
	dash, err := analytics.BuildDashboard(ctx, st, wallets)
	if err != nil {
		t.Fatal(err)
	}
	if dash.Assets.Inventory != 100 {
		t.Fatalf("inventory=%v want 100 (anchor)", dash.Assets.Inventory)
	}
	if dash.Assets.Deposits["uu"] != 120 {
		t.Fatalf("deposits=%v", dash.Assets.Deposits)
	}
	if dash.Assets.Wallets["eco"] != 555.5 {
		t.Fatalf("wallets=%v", dash.Assets.Wallets)
	}
	wantTotal := 100 + 120 + 555.5
	if diff := dash.Assets.Total - wantTotal; diff > 0.01 || diff < -0.01 {
		t.Fatalf("total=%v want %v", dash.Assets.Total, wantTotal)
	}
	if dash.Income.Total != 14 || dash.Income.ByChannel[0].Channel != domain.ChannelUU {
		t.Fatalf("income: %+v", dash.Income)
	}
	if dash.LeasedOut != 1 {
		t.Fatalf("leased_out=%d", dash.LeasedOut)
	}
	// categories per 口径 B: cost=all-status basis=80, net income=14-0(sold)=14
	if len(dash.Categories) != 1 || dash.Categories[0].Category != "步枪" || dash.Categories[0].Cost != 80 {
		t.Fatalf("categories: %+v", dash.Categories)
	}
	if dash.Categories[0].Yield < 0.1749 || dash.Categories[0].Yield > 0.1751 {
		t.Fatalf("yield=%v", dash.Categories[0].Yield)
	}
	// ROI per 口径 B: net=income−sold(0)=14 over all-time basis 80 → positive
	if dash.AnnualizedROI <= 0 {
		t.Fatalf("roi should be positive realized return: %v", dash.AnnualizedROI)
	}
	// series has at least today's point
	if len(dash.Series30d) == 0 || dash.Series30d[0].Income != 14 {
		t.Fatalf("series: %+v", dash.Series30d)
	}
}

// Sold inventory cost must be deducted from income (口径 B) in both the
// annualized ROI numerator and category yields.
func TestDashboardSoldCostDeduction(t *testing.T) {
	st := openAnalyticsDB(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	now := time.Now().UTC()

	pf := func(v float64) *float64 { return &v }
	hash := "Sold Item (Field-Tested)"
	if err := st.UpsertTemplate(ctx, store.Template{HashName: hash, Category: "手枪", UUMarkPrice: pf(40)}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecomputeAnchors(ctx); err != nil {
		t.Fatal(err)
	}
	// item bought for 50, later sold out
	if err := st.UpsertInventoryItem(ctx, domain.InventoryItem{
		Channel: domain.ChannelUU, AssetID: "s1", HashName: hash,
		MarkPrice: 40, Tradable: true, Status: "sold",
	}, pf(50)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertLeaseOrder(ctx, domain.LeaseOrder{
		Channel: domain.ChannelUU, OrderRef: "o-sold", AssetID: "s1", HashName: hash,
		Status: "bought_out", RentDays: 7, RentPrice: 2, Amount: 10, Deposits: 60,
		DueAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := analytics.RollupTerminalOrders(ctx, st, log); err != nil || n != 1 {
		t.Fatalf("rollup: %v %d", err, n)
	}

	dash, err := analytics.BuildDashboard(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	// category: cost=50 (all-time), net income = 10 − 50(sold) = −40, yield −0.8
	if len(dash.Categories) != 1 {
		t.Fatalf("categories: %+v", dash.Categories)
	}
	c := dash.Categories[0]
	if c.Cost != 50 || c.Income != -40 || c.Yield < -0.8001 || c.Yield > -0.7999 {
		t.Fatalf("category yield wrong: %+v", c)
	}
	// ROI numerator = 10 − 50 = −40 over basis 50 → negative
	if dash.AnnualizedROI >= 0 {
		t.Fatalf("roi must reflect consumed capital loss: %v", dash.AnnualizedROI)
	}
}
