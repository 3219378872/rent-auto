//go:build integration

package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/testutil"
)

func openReviewStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	pool, err := Open(ctx, testutil.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	clean := func() {
		if _, err := pool.Exec(ctx, `TRUNCATE app_settings,audit_log,templates,inventory_items,listings,lease_orders,market_snapshots,price_actions,strategies,fund_flows,daily_stats CASCADE`); err != nil {
			t.Fatal(err)
		}
	}
	clean()
	t.Cleanup(clean)
	return New(pool)
}

func TestPhysicalAssetAccountingAndFirstCost(t *testing.T) {
	st := openReviewStore(t)
	ctx := context.Background()
	value, cost := 1000.0, 800.0
	if err := st.UpsertTemplate(ctx, Template{HashName: "H", Category: "category", UUMarkPrice: &value}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecomputeAnchors(ctx); err != nil {
		t.Fatal(err)
	}
	it := domain.InventoryItem{Channel: domain.ChannelUU, AssetID: "same", HashName: "H", Status: "in_stock"}
	if err := st.UpsertInventoryItem(ctx, it, nil); err != nil {
		t.Fatal(err)
	}
	if first, err := st.FirstCostDate(ctx); err != nil || first != nil {
		t.Fatalf("no cost yet: %v %v", first, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE inventory_items SET last_synced_at=now()-interval '90 days'`); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Add(-time.Second)
	if err := st.SetCostBasis(ctx, domain.ChannelUU, "same", cost); err != nil {
		t.Fatal(err)
	}
	first, err := st.FirstCostDate(ctx)
	if err != nil || first == nil || first.Before(before) {
		t.Fatalf("first actual cost: %v %v", first, err)
	}
	it.Channel = domain.ChannelECO
	if err := st.UpsertInventoryItem(ctx, it, &cost); err != nil {
		t.Fatal(err)
	}
	check := func(wantValue, wantCost float64) {
		t.Helper()
		v, err := st.AssetValuation(ctx)
		if err != nil || v != wantValue {
			t.Fatalf("value=%v err=%v", v, err)
		}
		c, err := st.TotalCostEverBasis(ctx)
		if err != nil || c != wantCost {
			t.Fatalf("cost=%v err=%v", c, err)
		}
	}
	check(1000, 800)
	if err := st.SetCostBasis(ctx, domain.ChannelECO, "same", 850); err != nil {
		t.Fatal(err)
	}
	check(1000, 850)
	later, err := st.FirstCostDate(ctx)
	if err != nil || !later.Equal(*first) {
		t.Fatalf("cost clock moved: %v => %v, %v", first, later, err)
	}
	var mirrors int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM inventory_items WHERE cost_basis=850`).Scan(&mirrors); err != nil || mirrors != 2 {
		t.Fatalf("mirrors=%d %v", mirrors, err)
	}
	it.AssetID = "other"
	if err := st.UpsertInventoryItem(ctx, it, &cost); err != nil {
		t.Fatal(err)
	}
	check(2000, 1650) // Same template, different physical copy.
	if err := st.ApplyInventorySnapshot(ctx, domain.ChannelUU, nil); err != nil {
		t.Fatal(err)
	}
	check(2000, 1650) // ECO still observes both assets.
	if err := st.ApplyInventorySnapshot(ctx, domain.ChannelECO, nil); err != nil {
		t.Fatal(err)
	}
	check(0, 1650) // Missing is excluded from valuation but not historical capital.
}

func TestIncomeProjectionCorrectionsConcurrencyAndReversal(t *testing.T) {
	st := openReviewStore(t)
	ctx := context.Background()
	if err := st.UpsertTemplate(ctx, Template{HashName: "H", Category: "new-category"}); err != nil {
		t.Fatal(err)
	}
	order := domain.LeaseOrder{Channel: domain.ChannelECO, OrderRef: "corrected", Status: "delivering"}
	if err := st.UpsertLeaseOrder(ctx, order); err != nil {
		t.Fatal(err)
	}
	order.AssetID, order.HashName, order.Status, order.Amount = "asset", "H", "done", 10
	order.DueAt = time.Now().Add(30 * 24 * time.Hour)
	if err := st.UpsertLeaseOrder(ctx, order); err != nil {
		t.Fatal(err)
	}
	rows, err := st.UnrecordedTerminalOrders(ctx, 200)
	if err != nil || len(rows) != 1 {
		t.Fatalf("pending=%v %v", rows, err)
	}
	firstFinish := rows[0].Finished
	if firstFinish.After(time.Now().Add(time.Second)) {
		t.Fatal("due date used as completion")
	}
	// A queued batch must use the newer amount even when it carries stale 10.
	order.Amount = 20
	if err := st.UpsertLeaseOrder(ctx, order); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- st.RecordIncomeBatch(ctx, rows) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	check := func(want float64, wantCount int) {
		t.Helper()
		var income float64
		var count int
		if err := st.Pool.QueryRow(ctx, `SELECT COALESCE(SUM(income),0),COALESCE(SUM(order_count),0) FROM daily_stats`).Scan(&income, &count); err != nil {
			t.Fatal(err)
		}
		if income != want || count != wantCount {
			t.Fatalf("income=%v count=%d want=%v/%d", income, count, want, wantCount)
		}
	}
	check(20, 1)
	order.AssetID, order.HashName = "", ""
	if err := st.UpsertLeaseOrder(ctx, order); err != nil {
		t.Fatal(err)
	}
	stored, _, err := st.ListOrders(ctx, OrderFilter{})
	if err != nil || len(stored) != 1 || stored[0].HashName != "H" || !stored[0].FinishedAt.Equal(firstFinish) {
		t.Fatalf("identity/finish not stable: %+v %v", stored, err)
	}
	var asset string
	if err := st.Pool.QueryRow(ctx, `SELECT asset_id FROM lease_orders`).Scan(&asset); err != nil || asset != "asset" {
		t.Fatalf("asset=%s %v", asset, err)
	}
	correctedDate := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	order.FinishedAt = &correctedDate
	order.Amount = 30
	if err := st.UpsertLeaseOrder(ctx, order); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertTemplate(ctx, Template{HashName: "H", Category: "corrected-category"}); err != nil {
		t.Fatal(err)
	}
	rows, err = st.UnrecordedTerminalOrders(ctx, 200)
	if err != nil || len(rows) != 1 {
		t.Fatalf("correction=%v %v", rows, err)
	}
	if err := st.RecordIncomeBatch(ctx, rows); err != nil {
		t.Fatal(err)
	}
	check(30, 1)
	var date, category string
	if err := st.Pool.QueryRow(ctx, `SELECT stat_date::text,category FROM order_income_ledger`).Scan(&date, &category); err != nil || date != "2026-08-01" || category != "corrected-category" {
		t.Fatalf("ledger date/category=%s/%s %v", date, category, err)
	}
	order.Status = "cancelled"
	if err := st.UpsertLeaseOrder(ctx, order); err != nil {
		t.Fatal(err)
	}
	rows, err = st.UnrecordedTerminalOrders(ctx, 200)
	if err != nil || len(rows) != 1 {
		t.Fatalf("reversal=%v %v", rows, err)
	}
	if err := st.RecordIncomeBatch(ctx, rows); err != nil {
		t.Fatal(err)
	}
	check(0, 0)
	rows, err = st.UnrecordedTerminalOrders(ctx, 200)
	if err != nil || len(rows) != 0 {
		t.Fatalf("must settle: %v %v", rows, err)
	}
}

func TestFinancialMigrationRepairsLegacyProjection(t *testing.T) {
	st := openReviewStore(t)
	ctx := context.Background()
	if _, err := MigrateDown(ctx, st.Pool, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := MigrateUp(ctx, st.Pool); err != nil {
			t.Error(err)
		}
	})
	_, err := st.Pool.Exec(ctx, `INSERT INTO templates(hash_name,category,value_anchor) VALUES('legacy','category',1000);
	 INSERT INTO inventory_items(channel,asset_id,hash_name,status,cost_basis,cost_updated_at)
	 VALUES('uu','same','legacy','in_stock',800,now()-interval '20 days'),
	       ('eco','same','legacy','listed',800,now()-interval '10 days'),
	       ('uu','uncosted','legacy','missing',NULL,now()-interval '90 days');
	 INSERT INTO lease_orders(channel,order_ref,hash_name,status,order_amount,income_recorded,finished_at)
	 VALUES('uu','corrected','legacy','done',20,true,'2026-08-01T00:00:00Z');
	 INSERT INTO daily_stats(stat_date,channel,category,income,order_count,asset_snapshot)
	 VALUES('2026-08-01','uu','category',10,1,'{"kept":true}');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateUp(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	var income float64
	var count int
	var snapshot string
	if err := st.Pool.QueryRow(ctx, `SELECT income,order_count,asset_snapshot->>'kept' FROM daily_stats WHERE stat_date='2026-08-01'`).Scan(&income, &count, &snapshot); err != nil || income != 20 || count != 1 || snapshot != "true" {
		t.Fatalf("migrated income=%v orders=%v snapshot=%s err=%v", income, count, snapshot, err)
	}
	if v, err := st.AssetValuation(ctx); err != nil || v != 1000 {
		t.Fatalf("migrated valuation=%v %v", v, err)
	}
	if c, err := st.TotalCostEverBasis(ctx); err != nil || c != 800 {
		t.Fatalf("migrated cost=%v %v", c, err)
	}
	var first *time.Time
	if err := st.Pool.QueryRow(ctx, `SELECT cost_updated_at FROM inventory_items WHERE asset_id='uncosted'`).Scan(&first); err != nil || first != nil {
		t.Fatalf("uncosted timestamp=%v %v", first, err)
	}
}

func TestAnonymousInventoryCostsRemainIndependent(t *testing.T) {
	st := openReviewStore(t)
	ctx := context.Background()
	if err := st.UpsertTemplate(ctx, Template{HashName: "anonymous"}); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []domain.Channel{domain.ChannelUU, domain.ChannelECO} {
		if err := st.UpsertInventoryItem(ctx, domain.InventoryItem{Channel: ch, HashName: "anonymous", Status: "in_stock"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetCostBasis(ctx, domain.ChannelUU, "", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE inventory_items SET cost_updated_at=now()-interval '30 days' WHERE channel='uu'`); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second)
	if err := st.SetCostBasis(ctx, domain.ChannelECO, "", 9); err != nil {
		t.Fatal(err)
	}
	var first time.Time
	if err := st.Pool.QueryRow(ctx, `SELECT cost_updated_at FROM inventory_items WHERE channel='eco'`).Scan(&first); err != nil || first.Before(before) {
		t.Fatalf("unrelated asset cost clock: %v %v", first, err)
	}
	if cost, err := st.TotalCostEverBasis(ctx); err != nil || cost != 14 {
		t.Fatalf("anonymous assets collapsed: %v %v", cost, err)
	}
}
