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
			{Channel: domain.ChannelUU, HashName: "WrongShelf", GoodsRef: "G1", State: "active"}, // on wrong channel
			{Channel: domain.ChannelECO, HashName: "EcoOnly", GoodsRef: "G2", State: "active"},   // already correct
		},
		Health: map[string]string{"uu": "error: expired", "eco": "ok"},
	}
	var decided []string
	decide := func(_ context.Context, ch domain.Channel, it store.RoutableItem) *pricing.Decision {
		decided = append(decided, string(ch)+"/"+it.HashName)
		return okDecision(1.5)
	}
	plan := PlanFrom(context.Background(), snap, time.Now(), DefaultOrphanGrace, decide)

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
	// delist: WrongShelf on uu (route=eco_only → not_routed)
	if len(dels) != 1 || dels[0].GoodsRef != "G1" || !strings.Contains(dels[0].Reason, "not_routed") {
		t.Fatalf("delists: %+v", dels)
	}
}

// Multi-copy inventory must produce exactly the deficit of publishes per
// channel, keyed by distinct assets; assets already carrying a listing on the
// channel are never re-planned (cross-cycle idempotency).
func TestPlanFromMultiCopyDeficit(t *testing.T) {
	v := 100.0
	items := []store.RoutableItem{
		{AssetID: "a1", HashName: "H", V: &v, Route: "both"},
		{AssetID: "a2", HashName: "H", V: &v, Route: "both"},
		{AssetID: "a3", HashName: "H", V: &v, Route: "both"},
	}
	snap := Snapshot{
		Items:    items,
		Listings: []store.ActiveListing{{Channel: domain.ChannelUU, HashName: "H", GoodsRef: "G1", AssetID: "a1", State: "active"}},
		Health:   map[string]string{"uu": "ok", "eco": "ok"},
	}
	plan := PlanFrom(context.Background(), snap, time.Now(), DefaultOrphanGrace,
		func(context.Context, domain.Channel, store.RoutableItem) *pricing.Decision { return okDecision(1) })

	type pc struct {
		ch  domain.Channel
		ass string
		n   int
	}
	counts := map[pc]int{}
	for _, a := range plan {
		if a.Kind != "publish" {
			t.Fatalf("unexpected delist: %+v", a)
		}
		counts[pc{a.Channel, a.AssetID, 0}]++
	}
	// uu: a1 already listed → deficit 2 (a2,a3); eco: deficit 3 (a1..a3)
	for _, want := range []pc{{domain.ChannelUU, "a2", 0}, {domain.ChannelUU, "a3", 0},
		{domain.ChannelECO, "a1", 0}, {domain.ChannelECO, "a2", 0}, {domain.ChannelECO, "a3", 0}} {
		if counts[want] != 1 {
			t.Fatalf("expected exactly one publish %s/%s, got %d (%+v)", want.ch, want.ass, counts[want], counts)
		}
	}
	if len(plan) != 5 {
		t.Fatalf("plan size = %d, want 5", len(plan))
	}

	// Idempotency: after write-back records every listing, the same inventory replans empty.
	listings := make([]store.ActiveListing, 0, 6)
	for _, a := range plan {
		listings = append(listings, store.ActiveListing{
			ID: int64(len(listings) + 1), Channel: a.Channel, HashName: a.HashName,
			GoodsRef: "G" + a.AssetID + string(a.Channel), AssetID: a.AssetID, State: "active",
			SyncedAt: time.Now(),
		})
	}
	snap.Listings = append(snap.Listings, listings...)
	replan := PlanFrom(context.Background(), snap, time.Now().Add(time.Minute), DefaultOrphanGrace,
		func(context.Context, domain.Channel, store.RoutableItem) *pricing.Decision { return okDecision(1) })
	if len(replan) != 0 {
		t.Fatalf("recorded state must replan to zero actions, got %+v", replan)
	}
}

// Failover/route-change delists must never touch leased listings.
func TestPlanFromDelistSkipsLeased(t *testing.T) {
	v := 100.0
	old := time.Now().Add(-48 * time.Hour)
	snap := Snapshot{
		Items: []store.RoutableItem{{AssetID: "a1", HashName: "H", V: &v, Route: "uu_primary_eco_fallback"}},
		Listings: []store.ActiveListing{
			{Channel: domain.ChannelUU, HashName: "H", GoodsRef: "G-leased", State: "leased", SyncedAt: old},
			{Channel: domain.ChannelUU, HashName: "H", GoodsRef: "G-active", State: "active", SyncedAt: old},
		},
		Health: map[string]string{"uu": "error", "eco": "ok"},
	}
	plan := PlanFrom(context.Background(), snap, time.Now(), DefaultOrphanGrace,
		func(context.Context, domain.Channel, store.RoutableItem) *pricing.Decision { return okDecision(1) })
	var dels []Action
	for _, a := range plan {
		if a.Kind == "delist" {
			dels = append(dels, a)
		}
	}
	if len(dels) != 1 || dels[0].GoodsRef != "G-active" || dels[0].Reason != "uu_unhealthy_failover" {
		t.Fatalf("leased must be spared, active delisted: %+v", dels)
	}
}

// Orphaned listings (hash no longer routable) delist only after the grace
// window persisted; unknown sync age fails safe.
func TestPlanFromOrphanGrace(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	fresh := time.Now().Add(-time.Hour)
	base := func() Snapshot {
		return Snapshot{
			Items: []store.RoutableItem{}, // ghost hash has no routable anchor
			Listings: []store.ActiveListing{
				{Channel: domain.ChannelECO, HashName: "Ghost", GoodsRef: "G1", State: "active"},
			},
			Health: map[string]string{"uu": "ok", "eco": "ok"},
		}
	}
	decide := func(context.Context, domain.Channel, store.RoutableItem) *pricing.Decision { return nil }

	snap := base()
	snap.Listings[0].SyncedAt = old
	if plan := PlanFrom(context.Background(), snap, time.Now(), DefaultOrphanGrace, decide); len(plan) != 1 ||
		plan[0].Reason != "orphan_not_routable" {
		t.Fatalf("aged orphan must delist: %+v", plan)
	}
	snap = base()
	snap.Listings[0].SyncedAt = fresh
	if plan := PlanFrom(context.Background(), snap, time.Now(), DefaultOrphanGrace, decide); len(plan) != 0 {
		t.Fatalf("fresh orphan must be spared within grace: %+v", plan)
	}
	snap = base()
	if plan := PlanFrom(context.Background(), snap, time.Now(), DefaultOrphanGrace, decide); len(plan) != 0 {
		t.Fatalf("unknown sync age must fail safe: %+v", plan)
	}
}

// Surplus live listings beyond the wanted copy count are pruned (after grace).
func TestPlanFromSurplusCopies(t *testing.T) {
	v := 100.0
	old := time.Now().Add(-48 * time.Hour)
	snap := Snapshot{
		Items: []store.RoutableItem{{AssetID: "a1", HashName: "H", V: &v, Route: "uu_only"}},
		Listings: []store.ActiveListing{
			{Channel: domain.ChannelUU, HashName: "H", GoodsRef: "keep", AssetID: "a1", State: "active", SyncedAt: old},
			{Channel: domain.ChannelUU, HashName: "H", GoodsRef: "extra", State: "active", SyncedAt: old},
		},
		Health: map[string]string{"uu": "ok", "eco": "ok"},
	}
	plan := PlanFrom(context.Background(), snap, time.Now(), DefaultOrphanGrace,
		func(context.Context, domain.Channel, store.RoutableItem) *pricing.Decision { return nil })
	if len(plan) != 1 || plan[0].Kind != "delist" || plan[0].GoodsRef != "extra" ||
		plan[0].Reason != "surplus_copies" {
		t.Fatalf("surplus prune mismatch: %+v", plan)
	}
}

func TestPlanFromFailoverDelistReason(t *testing.T) {
	v := 100.0
	snap := Snapshot{
		Items:    []store.RoutableItem{{AssetID: "a1", HashName: "H", V: &v, Route: "uu_primary_eco_fallback"}},
		Listings: []store.ActiveListing{{Channel: domain.ChannelUU, HashName: "H", GoodsRef: "G9", State: "active"}},
		Health:   map[string]string{"uu": "error", "eco": "ok"},
	}
	decide := func(context.Context, domain.Channel, store.RoutableItem) *pricing.Decision { return okDecision(2) }
	plan := PlanFrom(context.Background(), snap, time.Now(), DefaultOrphanGrace, decide)
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
	if plan := PlanFrom(context.Background(), snap, time.Now(), DefaultOrphanGrace, decide); len(plan) != 0 {
		t.Fatalf("failing decision must gate publish: %+v", plan)
	}
}

// ---- executor ----

type stubAdapter struct {
	ch          domain.Channel
	pubCalls    []platform.PublishLeaseRequest
	delCalls    int
	pubErr      error
	delErr      error
	failPub     bool
	pubGoodsRef string // echoed goods ref on success
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
		out[i] = platform.PublishLeaseResult{AssetRef: items[i].AssetRef, GoodsRef: s.pubGoodsRef, Success: !s.failPub, Error: pubErrMsg(s.failPub)}
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

type fakeWriteBack struct {
	published []string // channel|goodsRef
	delisted  []string
}

func (f *fakeWriteBack) RecordPublishedListing(_ context.Context, channel, _, _, goodsRef string, _ float64, _ float64, _ float64, _ int) error {
	f.published = append(f.published, channel+"|"+goodsRef)
	return nil
}
func (f *fakeWriteBack) MarkListingDelisted(_ context.Context, channel, goodsRef string) error {
	f.delisted = append(f.delisted, channel+"|"+goodsRef)
	return nil
}

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
	if audits != 3 { // missing-adapter anomaly is audited too
		t.Fatalf("audits=%d", audits)
	}
}

func TestExecutorFailurePaths(t *testing.T) {
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

// Successful executions must write back so the next cycle plans a no-op;
// failures and dry-runs must not persist anything. Transport errors feed the
// risk-control hook.
func TestExecutorWriteBackAndPenalize(t *testing.T) {
	uu := &stubAdapter{ch: domain.ChannelUU, pubGoodsRef: "GR-1"}
	wb := &fakeWriteBack{}
	var penalties []domain.Channel
	e := &Executor{
		DryRun: false, Log: discardLogger(),
		Adapters: map[domain.Channel]platform.Adapter{domain.ChannelUU: uu},
		Store:    wb,
		Penalize: func(_ context.Context, ch domain.Channel, _ error) { penalties = append(penalties, ch) },
	}
	plan := []Action{
		{Kind: "publish", Channel: domain.ChannelUU, AssetID: "a1", HashName: "H", Decision: okDecision(1.5)},
		{Kind: "delist", Channel: domain.ChannelUU, GoodsRef: "G9", ListingID: 7, HashName: "H"},
	}
	applied, failed := e.Execute(context.Background(), plan)
	if applied != 2 || failed != 0 {
		t.Fatalf("applied=%d failed=%d", applied, failed)
	}
	if len(wb.published) != 1 || wb.published[0] != "uu|GR-1" {
		t.Fatalf("publish write-back missing: %+v", wb.published)
	}
	if len(wb.delisted) != 1 || wb.delisted[0] != "uu|G9" {
		t.Fatalf("delist write-back missing: %+v", wb.delisted)
	}
	if len(penalties) != 0 {
		t.Fatalf("no penalty expected on clean run: %+v", penalties)
	}

	// Risk-sentinel failure → penalized, nothing persisted.
	uu2 := &stubAdapter{ch: domain.ChannelUU, pubErr: platform.ErrRateLimited}
	wb2 := &fakeWriteBack{}
	e2 := &Executor{
		DryRun: false, Log: discardLogger(),
		Adapters: map[domain.Channel]platform.Adapter{domain.ChannelUU: uu2},
		Store:    wb2,
		Penalize: func(_ context.Context, ch domain.Channel, err error) {
			penalties = append(penalties, ch)
			_ = err
		},
	}
	applied, failed = e2.Execute(context.Background(), plan[:1])
	if applied != 0 || failed != 1 || len(penalties) != 1 || len(wb2.published) != 0 {
		t.Fatalf("failure path: applied=%d failed=%d penalties=%v wb=%v", applied, failed, penalties, wb2.published)
	}

	// Deterministic failure → counted, but no cooldown penalty.
	nPen := len(penalties)
	uu3 := &stubAdapter{ch: domain.ChannelUU, pubErr: errors.New("bad params")}
	e4 := &Executor{DryRun: false, Log: discardLogger(),
		Adapters: map[domain.Channel]platform.Adapter{domain.ChannelUU: uu3}, Store: &fakeWriteBack{},
		Penalize: func(_ context.Context, ch domain.Channel, _ error) { penalties = append(penalties, ch) }}
	if a, f := e4.Execute(context.Background(), plan[:1]); a != 0 || f != 1 {
		t.Fatalf("deterministic failure: applied=%d failed=%d", a, f)
	}
	if len(penalties) != nPen {
		t.Fatalf("deterministic failure must not penalize: %+v", penalties)
	}

	// Dry-run: audit-only, store untouched.
	wb3 := &fakeWriteBack{}
	e3 := &Executor{DryRun: true, Log: discardLogger(),
		Adapters: map[domain.Channel]platform.Adapter{domain.ChannelUU: uu}, Store: wb3}
	applied, failed = e3.Execute(context.Background(), plan)
	if applied != 2 || failed != 0 || len(wb3.published)+len(wb3.delisted) != 0 {
		t.Fatalf("dry-run must not persist: applied=%d failed=%d wb=%+v", applied, failed, wb3)
	}
}

// A publish whose platform response omits goods_ref cannot be durably keyed —
// it counts as applied but must not fabricate a write-back.
func TestExecutorPublishWithoutGoodsRefSkipsWriteBack(t *testing.T) {
	uu := &stubAdapter{ch: domain.ChannelUU, pubGoodsRef: ""}
	wb := &fakeWriteBack{}
	e := &Executor{DryRun: false, Log: discardLogger(),
		Adapters: map[domain.Channel]platform.Adapter{domain.ChannelUU: uu}, Store: wb}
	applied, _ := e.Execute(context.Background(), []Action{
		{Kind: "publish", Channel: domain.ChannelUU, AssetID: "a1", HashName: "H", Decision: okDecision(1)},
	})
	if applied != 1 || len(wb.published) != 0 {
		t.Fatalf("applied=%d wb=%+v", applied, wb.published)
	}
}

// DryRun must gate the platform entirely: no adapter call may happen, actions
// are audited with the dry_run marker and counted as would-be applied.
func TestExecutorDryRunSkipsPlatformCalls(t *testing.T) {
	uu := &stubAdapter{ch: domain.ChannelUU}
	var audited []domain.AuditEntry
	e := &Executor{
		DryRun: true, Log: discardLogger(),
		Audit:    func(_ context.Context, en domain.AuditEntry) { audited = append(audited, en) },
		Adapters: map[domain.Channel]platform.Adapter{domain.ChannelUU: uu},
	}
	plan := []Action{
		{Kind: "publish", Channel: domain.ChannelUU, AssetID: "a1", HashName: "H", Decision: okDecision(1.5)},
		{Kind: "delist", Channel: domain.ChannelUU, GoodsRef: "G1", HashName: "H"},
	}
	applied, failed := e.Execute(context.Background(), plan)
	if applied != 2 || failed != 0 {
		t.Fatalf("dry-run applied=%d failed=%d", applied, failed)
	}
	if len(uu.pubCalls) != 0 || uu.delCalls != 0 {
		t.Fatalf("dry-run must not touch platform: pub=%d del=%d", len(uu.pubCalls), uu.delCalls)
	}
	if len(audited) != 2 {
		t.Fatalf("audits=%d", len(audited))
	}
	for _, en := range audited {
		dry, ok := en.Detail["dry_run"].(bool)
		if !ok || !dry {
			t.Fatalf("audit entry missing dry_run marker: %+v", en.Detail)
		}
	}
}

// Even in dry-run, a plan targeting an unconfigured channel is a planning error.
func TestExecutorDryRunMissingAdapterFails(t *testing.T) {
	e := &Executor{DryRun: true, Log: discardLogger()}
	plan := []Action{{Kind: "delist", Channel: domain.ChannelECO, GoodsRef: "G1", HashName: "H"}}
	applied, failed := e.Execute(context.Background(), plan)
	if applied != 0 || failed != 1 {
		t.Fatalf("applied=%d failed=%d", applied, failed)
	}
}

// Unknown action kinds must be counted and audited, never silently dropped.
func TestExecutorUnknownKindCountedFailed(t *testing.T) {
	var audited []domain.AuditEntry
	e := &Executor{Log: discardLogger(),
		Audit:    func(_ context.Context, en domain.AuditEntry) { audited = append(audited, en) },
		Adapters: map[domain.Channel]platform.Adapter{domain.ChannelUU: &stubAdapter{ch: domain.ChannelUU}},
	}
	applied, failed := e.Execute(context.Background(), []Action{
		{Kind: "teleport", Channel: domain.ChannelUU, HashName: "H"},
	})
	if applied != 0 || failed != 1 || len(audited) != 1 {
		t.Fatalf("unknown kind: applied=%d failed=%d audits=%d", applied, failed, len(audited))
	}
	if msg, _ := audited[0].Detail["err"].(string); msg != "unknown_kind" {
		t.Fatalf("audit detail err=%v", audited[0].Detail["err"])
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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

// The same Steam asset is synced once per channel that stocks it (uu + eco
// rows share one asset id). The publish pass must dedupe within the plan and
// the copy budget must count distinct assets — real-machine finding
// 2026-08-27: one physical knife was planned for publish on ECO twice.
func TestPlanFromDedupesAssetSyncedByBothChannels(t *testing.T) {
	v := 100.0
	items := []store.RoutableItem{
		{AssetID: "a1", HashName: "H", V: &v, Route: "eco_only"},
		{AssetID: "a1", HashName: "H", V: &v, Route: "eco_only"}, // same asset, other channel's stock row
	}
	snap := Snapshot{
		Items:  items,
		Health: map[string]string{"uu": "ok", "eco": "ok"},
	}
	plan := PlanFrom(context.Background(), snap, time.Now(), DefaultOrphanGrace,
		func(context.Context, domain.Channel, store.RoutableItem) *pricing.Decision { return okDecision(1) })

	pubs := 0
	for _, a := range plan {
		if a.Kind != "publish" {
			t.Fatalf("unexpected delist: %+v", a)
		}
		if a.Channel != domain.ChannelECO || a.AssetID != "a1" {
			t.Fatalf("unexpected publish target: %+v", a)
		}
		pubs++
	}
	if pubs != 1 {
		t.Fatalf("same asset must publish exactly once, got %d (%+v)", pubs, plan)
	}
}

// A leased listing holds no publish-budget slot: one leased + zero live rows
// against two routable copies must still publish the unlisted copy (the old
// len(existing) budget blocked it once the leased row filled the count).
func TestPlanFromPublishBudgetIgnoresLeased(t *testing.T) {
	v := 100.0
	snap := Snapshot{
		Items: []store.RoutableItem{
			{AssetID: "a1", HashName: "H", V: &v, Route: "uu_only"},
			{AssetID: "a2", HashName: "H", V: &v, Route: "uu_only"},
		},
		Listings: []store.ActiveListing{
			{Channel: domain.ChannelUU, HashName: "H", GoodsRef: "G-leased", AssetID: "a9", State: "leased"},
			{Channel: domain.ChannelUU, HashName: "H", GoodsRef: "G1", AssetID: "a1", State: "active"},
		},
		Health: map[string]string{"uu": "ok", "eco": "ok"},
	}
	plan := PlanFrom(context.Background(), snap, time.Now(), DefaultOrphanGrace,
		func(context.Context, domain.Channel, store.RoutableItem) *pricing.Decision { return okDecision(1) })
	var pubs []Action
	for _, a := range plan {
		if a.Kind == "publish" {
			pubs = append(pubs, a)
		} else {
			t.Fatalf("unexpected delist: %+v", a)
		}
	}
	// want=2, live(non-leased)=1 → exactly one publish for the unlisted a2
	// (a1 already listed in any state is never re-published).
	if len(pubs) != 1 || pubs[0].AssetID != "a2" {
		t.Fatalf("publishes: %+v", pubs)
	}
}

// Leased rows occupy no kept budget either: with want=1, a leased copy plus
// two active copies must delist the active surplus — never the legitimately
// kept active copy the old kept-counter sacrificed first.
func TestPlanFromSurplusKeptIgnoresLeased(t *testing.T) {
	v := 100.0
	old := time.Now().Add(-48 * time.Hour)
	snap := Snapshot{
		Items: []store.RoutableItem{
			{AssetID: "a1", HashName: "H", V: &v, Route: "uu_only"},
		},
		Listings: []store.ActiveListing{
			{Channel: domain.ChannelUU, HashName: "H", GoodsRef: "G-leased", AssetID: "a9", State: "leased", SyncedAt: old},
			{Channel: domain.ChannelUU, HashName: "H", GoodsRef: "G1", AssetID: "a1", State: "active", SyncedAt: old},
			{Channel: domain.ChannelUU, HashName: "H", GoodsRef: "G2", AssetID: "a2", State: "active", SyncedAt: old},
		},
		Health: map[string]string{"uu": "ok", "eco": "ok"},
	}
	plan := PlanFrom(context.Background(), snap, old.Add(2*DefaultOrphanGrace), DefaultOrphanGrace,
		func(context.Context, domain.Channel, store.RoutableItem) *pricing.Decision { return okDecision(1) })
	var dels []Action
	for _, a := range plan {
		if a.Kind == "delist" {
			dels = append(dels, a)
		} else {
			t.Fatalf("unexpected publish: %+v", a)
		}
	}
	if len(dels) != 1 || dels[0].GoodsRef != "G2" {
		t.Fatalf("surplus delists: %+v", dels)
	}
}
