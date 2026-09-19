//go:build integration

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/3219378872/rent-auto/backend/internal/analytics"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

func panelGet[T any](t *testing.T, routes http.Handler, token, path string) T {
	t.Helper()
	r := do(t, routes, "GET", "/api/v1"+path, token, "")
	var value T
	if err := json.Unmarshal(r.Body.Bytes(), &value); err != nil || r.Code != 200 {
		t.Fatalf("%s: %d %s (%v)", path, r.Code, r.Body.String(), err)
	}
	return value
}

func TestPanelExecutionPermissions(t *testing.T) {
	st, closeDB := openAPIDB(t)
	defer closeDB()
	defer truncateTables(t, st, "strategies, templates CASCADE")
	ctx := context.Background()
	id, _, err := st.EnsureGlobalStrategy(ctx, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertTemplate(ctx, store.Template{HashName: "panel-template"}); err != nil {
		t.Fatal(err)
	}
	s := newAuthedServer(t, st)
	env := false
	s.EnvironmentDryRun = &env
	routes := s.Routes()
	token := tokenFor(t, routes)
	for _, tc := range []struct{ env, global, template, expected bool }{{true, true, true, true}, {false, false, true, true}, {false, true, false, true}, {false, true, true, false}} {
		env = tc.env
		if err := st.UpdateGlobalStrategy(ctx, id, store.StrategyGlobalPatch{RealEnabled: &tc.global}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpsertTemplateStrategy(ctx, store.TemplateStrategy{HashName: "panel-template", Route: "eco_only", Params: []byte(`{}`), RealEnabled: &tc.template}); err != nil {
			t.Fatal(err)
		}
		result := panelGet[struct {
			DryRun  bool     `json:"dry_run"`
			Reasons []string `json:"reasons"`
			Route   string   `json:"channel_route"`
		}](t, routes, token, "/execution/status?hash_name=panel-template")
		if result.DryRun != tc.expected || result.Route != "eco_only" || tc.expected && len(result.Reasons) == 0 {
			t.Fatalf("mode %+v => %+v", tc, result)
		}
	}
	s.EnvironmentDryRun = nil
	if r := do(t, routes, "GET", "/api/v1/execution/status", token, ""); r.Code != 503 {
		t.Fatalf("unwired environment: %d", r.Code)
	}
}

type panelPage[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}

func TestPanelPriceActionHistoryAndAuditDetails(t *testing.T) {
	st, closeDB := openAPIDB(t)
	defer closeDB()
	defer truncateTables(t, st, "price_actions, audit_log, templates CASCADE")
	ctx := context.Background()
	_, err := st.Pool.Exec(ctx, `INSERT INTO price_actions(ts,channel,hash_name,asset_id,action,old_rent,new_rent,new_days,decision,dry_run,success,error)
	 SELECT timestamptz '2026-09-19 00:00:00Z' + i * interval '1 second','eco','panel-template','asset-'||i,
	 CASE WHEN i%3=0 THEN 'skip' ELSE 'reprice' END,NULL,i/100.0,30,
	 '{"factor":1.02,"skip":"cooldown","nested":{"token":"historical-secret"},"key_fp":"abcd1234abcd"}'::jsonb,
	 i%2=0,i%3<>1,CASE WHEN i%3=1 THEN 'upstream-private-error' ELSE NULL END FROM generate_series(1,125) i`)
	if err != nil {
		t.Fatal(err)
	}
	routes := newAuthedServer(t, st).Routes()
	token := tokenFor(t, routes)
	page := panelGet[panelPage[store.PriceActionRow]](t, routes, token, "/price-actions?channel=eco&page=2&page_size=50")
	if page.Total != 125 || len(page.Items) != 50 || page.Items[0].AssetID != "asset-75" || page.Items[0].OldRent != nil || page.Items[0].NewDays == nil {
		t.Fatalf("history paging/null fields: %+v", page)
	}
	for _, row := range page.Items {
		if strings.Contains(string(row.Decision), "historical-secret") || strings.Contains(row.Error, "upstream-private-error") {
			t.Fatal("history leaked secrets")
		}
	}
	for _, query := range []string{"mode=dry_run&result=success", "mode=real&result=failed", "result=skip", "hash_name=panel-template&asset_id=asset-4", "since=2026-09-19T00:01:00Z&until=2026-09-19T00:01:03Z"} {
		p := panelGet[panelPage[store.PriceActionRow]](t, routes, token, "/price-actions?"+query)
		if p.Total == 0 {
			t.Fatalf("filter empty: %s", query)
		}
		for _, r := range p.Items {
			if strings.Contains(query, "mode=dry_run") && !r.DryRun || strings.Contains(query, "mode=real") && r.DryRun || strings.Contains(query, "result=failed") && r.Success || query == "result=skip" && r.Action != "skip" {
				t.Fatalf("filter %s: %+v", query, r)
			}
		}
	}
	byID := panelGet[panelPage[store.PriceActionRow]](t, routes, token, fmt.Sprintf("/price-actions?id=%d", page.Items[0].ID))
	if byID.Total != 1 {
		t.Fatal("id filter")
	}
	_, err = st.Pool.Exec(ctx, `INSERT INTO templates(hash_name) VALUES('panel-template');
	 INSERT INTO listings(channel,asset_id,hash_name,goods_ref,desired_state,actual_state,rent_price) VALUES('eco','asset-4','panel-template','goods-4','active','none',1.20);
	 UPDATE price_actions SET listing_id=(SELECT id FROM listings WHERE goods_ref='goods-4') WHERE asset_id='asset-4'`)
	if err != nil {
		t.Fatal(err)
	}
	listings := panelGet[panelPage[store.ListingRow]](t, routes, token, "/listings?search=asset-4&mismatch=true")
	if listings.Total != 1 || listings.Items[0].Last == nil || !listings.Items[0].Last.DryRun || listings.Items[0].Last.Success || listings.Items[0].Last.ID == 0 {
		t.Fatalf("listing decision metadata: %+v", listings)
	}
	for _, query := range []string{"mode=bogus", "result=bogus", "id=-1", "since=invalid", "since=2026-09-20T00:00:00Z&until=2026-09-19T00:00:00Z"} {
		if r := do(t, routes, "GET", "/api/v1/price-actions?"+query, token, ""); r.Code != 400 {
			t.Fatalf("invalid %s: %d", query, r.Code)
		}
	}
	_, err = st.Pool.Exec(ctx, `INSERT INTO audit_log(actor,action,detail) VALUES('admin','panel.long-detail',jsonb_build_object('notes',$1::text,'password','historical-secret'))`, strings.Repeat("detail", 800))
	if err != nil {
		t.Fatal(err)
	}
	r := do(t, routes, "GET", "/api/v1/audit?action=panel.long-detail", token, "")
	if r.Code != 200 || strings.Contains(r.Body.String(), "historical-secret") || !strings.Contains(r.Body.String(), strings.Repeat("detail", 800)) {
		t.Fatal("audit detail lost or leaked")
	}
}

func TestPanelInventoryCatalogOrdersAndFinancialMetadata(t *testing.T) {
	st, closeDB := openAPIDB(t)
	defer closeDB()
	defer truncateTables(t, st, "templates, lease_orders, daily_stats CASCADE")
	ctx := context.Background()
	exec := func(sql string) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO templates(hash_name,display_name,category,blacklisted) SELECT 'panel-'||lpad(i::text,3,'0'),'同名商品','rifle',i>120 FROM generate_series(1,125) i`)
	exec(`INSERT INTO inventory_items(channel,asset_id,hash_name,mark_price,cost_basis,cost_updated_at,cost_modified_at)
	 SELECT 'eco','asset-'||i,'panel-001',i,CASE WHEN i%2=0 THEN 50 ELSE NULL END,now()-interval '10 days',now() FROM generate_series(1,125) i`)
	exec(`INSERT INTO lease_orders(channel,order_ref,hash_name,asset_id,order_type,status,order_amount,started_at,due_at)
	 SELECT 'eco','order-'||i,'panel-001','asset-'||i,CASE WHEN i%2=0 THEN 'buyout' ELSE 'short' END,
	 CASE WHEN i%3=0 THEN 'done' WHEN i%3=1 THEN 'delivering' ELSE 'leasing' END,i,
	 timestamptz '2026-09-19 00:00:00Z'+i*interval '1 second',now()+i*interval '1 day' FROM generate_series(1,125) i`)
	routes := newAuthedServer(t, st).Routes()
	token := tokenFor(t, routes)
	catalog := panelGet[panelPage[store.Template]](t, routes, token, "/templates/catalog?search=panel-&blacklisted=false&page=3&page_size=50")
	if catalog.Total != 120 || len(catalog.Items) != 20 || catalog.Items[0].HashName != "panel-101" {
		t.Fatalf("catalog paging: %+v", catalog)
	}
	categories := panelGet[[]string](t, routes, token, "/inventory/categories")
	if len(categories) != 1 || categories[0] != "rifle" {
		t.Fatalf("categories %v", categories)
	}
	inventory := panelGet[panelPage[store.InventoryRow]](t, routes, token, "/inventory?category=rifle&cost_missing=true&sort=price_desc&page=2&page_size=50")
	if inventory.Total != 63 || len(inventory.Items) != 13 || inventory.Items[0].MarkPrice != 25 {
		t.Fatalf("inventory filtering: %+v", inventory)
	}
	asset := panelGet[panelPage[store.InventoryRow]](t, routes, token, "/inventory?asset_id=asset-4&hash_name=panel-001")
	if asset.Total != 1 || asset.Items[0].CostModifiedAt == nil {
		t.Fatal("asset/cost metadata")
	}
	orders := panelGet[panelPage[store.OrderRow]](t, routes, token, "/orders?view=attention&order_type=buyout&sort=amount_desc")
	if orders.Total != 21 || orders.Items[0].Amount != 124 || orders.Items[0].AssetID != "asset-124" {
		t.Fatalf("order view/type/sort: %+v", orders)
	}
	window := panelGet[panelPage[store.OrderRow]](t, routes, token, "/orders?since=2026-09-19T00:01:00Z&until=2026-09-19T00:01:03Z&sort=started_desc")
	if window.Total != 3 || window.Items[0].OrderRef != "order-62" {
		t.Fatalf("half-open time window: %+v", window)
	}
	for _, path := range []string{"/inventory?sort=", "/orders?sort="} {
		p := panelGet[panelPage[map[string]any]](t, routes, token, path+url.QueryEscape("id; DROP TABLE templates"))
		if p.Total != 125 {
			t.Fatal("sort whitelist changed results")
		}
	}
	exec(`INSERT INTO daily_stats(stat_date,channel,category,income,order_count) VALUES((now() AT TIME ZONE 'utc')::date,'eco','rifle',123.45,3)`)
	d, err := analytics.BuildDashboard(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !d.ROIAvailable || d.ObservationDays < 9 || d.CostedItems != 62 || d.InventoryItems != 125 || len(d.Series30d) != 1 || d.Series30d[0].Orders != 3 {
		t.Fatalf("financial metadata: %+v", d)
	}
}
