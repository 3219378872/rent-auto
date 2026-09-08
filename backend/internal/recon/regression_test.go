package recon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
	"github.com/3219378872/rent-auto/backend/internal/pricing"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

func TestPublishBudgetCountsCurrentPlan(t *testing.T) {
	snap := Snapshot{
		Items:    []store.RoutableItem{{AssetID: "new1", HashName: "H", Route: "uu_only"}, {AssetID: "new2", HashName: "H", Route: "uu_only"}},
		Listings: []store.ActiveListing{{Channel: domain.ChannelUU, HashName: "H", GoodsRef: "old", AssetID: "old", State: "active"}},
	}
	plan := PlanFrom(context.Background(), snap, time.Now(), DefaultOrphanGrace, func(context.Context, domain.Channel, store.RoutableItem) *pricing.Decision { return okDecision(1) })
	if len(plan) != 1 || plan[0].Kind != "publish" {
		t.Fatalf("one-copy deficit: %+v", plan)
	}
}

func TestLeasedAssetCannotPublishOnOtherChannel(t *testing.T) {
	snap := Snapshot{
		Items:    []store.RoutableItem{{AssetID: "leased", HashName: "H", Route: "both"}},
		Listings: []store.ActiveListing{{Channel: domain.ChannelUU, HashName: "H", AssetID: "leased", State: "leased"}},
	}
	plan := PlanFrom(context.Background(), snap, time.Now(), DefaultOrphanGrace, func(context.Context, domain.Channel, store.RoutableItem) *pricing.Decision { return okDecision(1) })
	if len(plan) != 0 {
		t.Fatalf("leased asset mirrored as stock must not publish: %+v", plan)
	}
}

func TestOrphanGraceIgnoresShelfHeartbeat(t *testing.T) {
	start := time.Now().Add(-7 * 24 * time.Hour)
	snap := Snapshot{Listings: []store.ActiveListing{{ID: 1, Channel: domain.ChannelUU, HashName: "H", State: "active", SyncedAt: time.Now(), MismatchSince: &start, MismatchReason: "orphan_not_routable"}}}
	observations := map[int64]string{}
	plan := planFrom(context.Background(), snap, time.Now(), DefaultOrphanGrace, nil, observations)
	if len(plan) != 1 || observations[1] != "orphan_not_routable" {
		t.Fatalf("old mismatch with fresh heartbeat must delist: %+v %v", plan, observations)
	}
	snap.Listings[0].MismatchReason = "surplus_copies"
	if plan := PlanFrom(context.Background(), snap, time.Now(), DefaultOrphanGrace, nil); len(plan) != 0 {
		t.Fatalf("changed reason must restart grace: %+v", plan)
	}
}

type failingWriteBack struct{}

func (failingWriteBack) RecordPublishedListing(context.Context, string, string, string, string, float64, float64, float64, int) error {
	return errors.New("db unavailable")
}
func (failingWriteBack) MarkListingDelisted(context.Context, string, string) error {
	return errors.New("db unavailable")
}

func TestExecutorStopsOnlyRiskyChannelAndReturnsFailures(t *testing.T) {
	uu := &stubAdapter{ch: domain.ChannelUU, pubErr: platform.ErrRateLimited}
	eco := &stubAdapter{ch: domain.ChannelECO, pubGoodsRef: "g"}
	e := Executor{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Adapters: map[domain.Channel]platform.Adapter{domain.ChannelUU: uu, domain.ChannelECO: eco}}
	plan := []Action{{Kind: "publish", Channel: domain.ChannelUU, AssetID: "a", Decision: okDecision(1)}, {Kind: "publish", Channel: domain.ChannelUU, AssetID: "b", Decision: okDecision(1)}, {Kind: "publish", Channel: domain.ChannelECO, AssetID: "c", Decision: okDecision(1)}}
	applied, failed, err := e.ExecuteWithError(context.Background(), plan)
	if err == nil || applied != 1 || failed != 2 || len(uu.pubCalls) != 1 || len(eco.pubCalls) != 1 {
		t.Fatalf("risk isolation: applied=%d failed=%d uu=%d eco=%d err=%v", applied, failed, len(uu.pubCalls), len(eco.pubCalls), err)
	}
	e.Store = failingWriteBack{}
	if applied, failed, err := e.ExecuteWithError(context.Background(), plan[2:]); applied != 1 || failed != 1 || err == nil {
		t.Fatalf("write-back loss must surface without hiding effect: %d %d %v", applied, failed, err)
	}
	e.ChannelReady = func(domain.Channel) bool { return false }
	eco.pubCalls = nil
	if _, _, err := e.ExecuteWithError(context.Background(), plan[2:]); err == nil || len(eco.pubCalls) != 0 {
		t.Fatal("shared cooldown must block before adapter invocation")
	}
}
