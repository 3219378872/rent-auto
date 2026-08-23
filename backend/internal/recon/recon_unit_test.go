package recon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
	"github.com/3219378872/rent-auto/backend/internal/pricing"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

func okDecision(rent float64) *pricing.Decision {
	return &pricing.Decision{OK: true, Rent: rent, Long: rent * 0.98, MaxDays: 60, Deposit: 140}
}

func TestPlanFromMatrix(t *testing.T) {
	v := 100.0
	snap := Snapshot{
		Items: []store.RoutableItem{
			{AssetID: "a-both", HashName: "Both", V: &v, Route: "both"},
			{AssetID: "a-fb", HashName: "Fallback", V: &v, Route: "uu_primary_eco_fallback"},
			{AssetID: "a-eco", HashName: "EcoOnly", V: &v, Route: "eco_only"},
			{AssetID: "a-delisted", HashName: "WrongShelf", V: &v, Route: "eco_only"},
		},
		Listings: []store.ActiveListing{
			{Channel: domain.ChannelUU, HashName: "WrongShelf", GoodsRef: "G1"}, // on wrong channel
			{Channel: domain.ChannelECO, HashName: "EcoOnly", GoodsRef: "G2"},   // already correct
		},
		Health: map[string]string{"uu": "error: expired", "eco": "ok"},
	}
	var decided []string
	decide := func(_ context.Context, ch domain.Channel, it store.RoutableItem) *pricing.Decision {
		decided = append(decided, string(ch)+"/"+it.HashName)
		return okDecision(1.5)
	}
	plan := PlanFrom(snap, time.Now(), decide)

	var pubs, dels []Action
	for _, a := range plan {
		if a.Kind == "publish" {
			pubs = append(pubs, a)
		} else {
			dels = append(dels, a)
		}
	}
	// publishes: Both×2(uu+eco), Fallback→eco(uu不健康), WrongShelf→eco(其路由本就是eco_only)
	wantPubs := map[string]bool{"uu/Both": true, "eco/Both": true, "eco/Fallback": true, "eco/WrongShelf": true}
	gotPubs := map[string]bool{}
	for _, a := range pubs {
		gotPubs[string(a.Channel)+"/"+a.HashName] = true
	}
	for k := range wantPubs {
		if !gotPubs[k] {
			t.Fatalf("missing publish %s (got %v decided %v)", k, gotPubs, decided)
		}
	}
	if len(pubs) != len(wantPubs) {
		t.Fatalf("extra publishes: %+v", pubs)
	}
	// delist: WrongShelf on uu (failover reason since route=fallback... it's eco_only → not_routed)
	if len(dels) != 1 || dels[0].GoodsRef != "G1" || !strings.Contains(dels[0].Reason, "not_routed") {
		t.Fatalf("delists: %+v", dels)
	}
}

func TestPlanFromFailoverDelistReason(t *testing.T) {
	v := 100.0
	snap := Snapshot{
		Items:    []store.RoutableItem{{AssetID: "a1", HashName: "H", V: &v, Route: "uu_primary_eco_fallback"}},
		Listings: []store.ActiveListing{{Channel: domain.ChannelUU, HashName: "H", GoodsRef: "G9"}},
		Health:   map[string]string{"uu": "error", "eco": "ok"},
	}
	decide := func(context.Context, domain.Channel, store.RoutableItem) *pricing.Decision { return okDecision(2) }
	plan := PlanFrom(snap, time.Now(), decide)
	delistFound := false
	for _, a := range plan {
		if a.Kind == "delist" && a.Reason == "uu_unhealthy_failover" {
			delistFound = true
		}
	}
	if !delistFound {
		t.Fatalf("failover delist missing: %+v", plan)
	}
}

func TestPlanFromSkipsWhenDecideFails(t *testing.T) {
	v := 100.0
	snap := Snapshot{
		Items:  []store.RoutableItem{{AssetID: "a1", HashName: "H", V: &v, Route: "uu_only"}},
		Health: map[string]string{"uu": "ok", "eco": "ok"},
	}
	decide := func(context.Context, domain.Channel, store.RoutableItem) *pricing.Decision {
		return &pricing.Decision{OK: false, SkipReason: "no_baseline"}
	}
	if plan := PlanFrom(snap, time.Now(), decide); len(plan) != 0 {
		t.Fatalf("failing decision must gate publish: %+v", plan)
	}
}

// ---- executor ----

type stubAdapter struct {
	ch       domain.Channel
	pubCalls []platform.PublishLeaseRequest
	delCalls int
	pubErr   error
	delErr   error
	failPub  bool
}

func (s *stubAdapter) Channel() domain.Channel                                   { return s.ch }
func (s *stubAdapter) Caps() platform.Capabilities                               { return platform.Capabilities{} }
func (s *stubAdapter) Healthy(context.Context) error                             { return nil }
func (s *stubAdapter) Inventory(context.Context) ([]domain.InventoryItem, error) { return nil, nil }
func (s *stubAdapter) LeaseShelf(context.Context) ([]domain.ShelfListing, error) { return nil, nil }
func (s *stubAdapter) PublishLease(_ context.Context, items []platform.PublishLeaseRequest) ([]platform.PublishLeaseResult, error) {
	s.pubCalls = append(s.pubCalls, items...)
	if s.pubErr != nil {
		return nil, s.pubErr
	}
	out := make([]platform.PublishLeaseResult, len(items))
	for i := range items {
		out[i] = platform.PublishLeaseResult{AssetRef: items[i].AssetRef, Success: !s.failPub, Error: pubErrMsg(s.failPub)}
	}
	return out, nil
}

func pubErrMsg(fail bool) string {
	if fail {
		return "rejected"
	}
	return ""
}

func (s *stubAdapter) RepriceLease(context.Context, []platform.RepriceLeaseRequest) ([]platform.RepriceLeaseResult, error) {
	return nil, nil
}
func (s *stubAdapter) Delist(context.Context, []string) error {
	s.delCalls++
	return s.delErr
}
func (s *stubAdapter) LeaseOrders(context.Context, time.Time) ([]domain.LeaseOrder, error) {
	return nil, nil
}
func (s *stubAdapter) Wallet(context.Context) (float64, error) { return 0, nil }

func TestExecutorPaths(t *testing.T) {
	uu := &stubAdapter{ch: domain.ChannelUU}
	audits := 0
	e := &Executor{
		DryRun: false, Log: discardLogger(),
		Audit:    func(context.Context, domain.AuditEntry) { audits++ },
		Adapters: map[domain.Channel]platform.Adapter{domain.ChannelUU: uu},
	}
	plan := []Action{
		{Kind: "publish", Channel: domain.ChannelUU, AssetID: "a1", HashName: "H", Decision: okDecision(1.5)},
		{Kind: "delist", Channel: domain.ChannelUU, GoodsRef: "G1", HashName: "H"},
		{Kind: "publish", Channel: domain.ChannelECO, AssetID: "a2", HashName: "H2", Decision: okDecision(2)}, // no adapter
	}
	applied, failed := e.Execute(context.Background(), plan)
	if applied != 2 || failed != 1 {
		t.Fatalf("applied=%d failed=%d", applied, failed)
	}
	if len(uu.pubCalls) != 1 || uu.delCalls != 1 {
		t.Fatalf("adapter calls: pub=%d del=%d", len(uu.pubCalls), uu.delCalls)
	}
	if audits != 2 { // missing-adapter action short-circuits before audit
		t.Fatalf("audits=%d", audits)
	}
}

func TestExecutorDryRunAndFailures(t *testing.T) {
	uu := &stubAdapter{ch: domain.ChannelUU, failPub: true, delErr: errors.New("busy")}
	e := &Executor{
		DryRun: false, Log: discardLogger(),
		Adapters: map[domain.Channel]platform.Adapter{domain.ChannelUU: uu},
	}
	plan := []Action{
		{Kind: "publish", Channel: domain.ChannelUU, AssetID: "a1", HashName: "H", Decision: okDecision(1)},
		{Kind: "delist", Channel: domain.ChannelUU, GoodsRef: "G1", HashName: "H"},
	}
	applied, failed := e.Execute(context.Background(), plan)
	if applied != 0 || failed != 2 {
		t.Fatalf("applied=%d failed=%d", applied, failed)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestConvertQuotes(t *testing.T) {
	rows := []store.QuoteRow{
		{Kind: "lease_short", Price: 2.0},
		{Kind: "lease_long", Price: 1.9},
		{Kind: "deposit", Price: 200},
		{Kind: "unknown", Price: 5},
	}
	qs := ConvertQuotes(rows)
	if len(qs) != 4 || qs[0].Short != 2.0 || qs[1].Long != 1.9 || qs[2].Deposit != 200 {
		t.Fatalf("quotes: %+v", qs)
	}
}

func TestRentDayBoundsAndErrHelpers(t *testing.T) {
	if rentDayMin(domain.ChannelECO) != 8 || rentDayMin(domain.ChannelUU) != 1 {
		t.Fatal("min bounds")
	}
	if rentDayMax(domain.ChannelECO) != 90 {
		t.Fatal("max bound")
	}
	if errText(nil) != "" || errText(errors.New("x")) != "x" {
		t.Fatal("errText")
	}
	res := []platform.PublishLeaseResult{{Success: false, Error: "bad"}}
	if errString(nil, res) != "bad" || errString(errors.New("net"), nil) != "net" || errString(nil, nil) != "" {
		t.Fatal("errString")
	}
}
