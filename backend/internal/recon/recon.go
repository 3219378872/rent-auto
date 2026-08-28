// Package recon reconciles desired shelf state (strategy × inventory × route)
// against actual per-channel listings, producing idempotent action plans.
//
// Idempotency contract (restored 2026-08-24 review round 3): every executed
// publish/delist writes its outcome back through Executor.Store, so the next
// planning cycle observes the new actual state and replans to a no-op.
package recon

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
	"github.com/3219378872/rent-auto/backend/internal/pricing"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

// HealthFn reports per-channel reachability ("ok" when routable).
type HealthFn func(ctx context.Context) map[string]string

// DefaultOrphanGrace is how long a listing that no longer anchors to any
// routable inventory item (or exceeds the wanted copy count) must persist
// before reconcile will really delist it. This absorbs transient inventory
// sync states instead of mass-delisting on a single bad cycle.
const DefaultOrphanGrace = 24 * time.Hour

type Planner struct {
	Store  *store.Store
	Log    *slog.Logger
	Health HealthFn // nil = assume all healthy
	// Caps carries per-channel rent-day bounds from adapter capabilities;
	// nil or missing entries fall back to the hardcoded defaults below.
	Caps map[domain.Channel]platform.Capabilities
	Now  time.Time
	// OrphanGrace overrides DefaultOrphanGrace when > 0.
	OrphanGrace time.Duration
}

// Action is one reconciliation step.
type Action struct {
	Kind      string // publish | delist
	Channel   domain.Channel
	AssetID   string
	GoodsRef  string // delist only
	ListingID int64  // delist only: source listings row
	HashName  string
	Reason    string
	State     string            // delist only: actual_state at planning time
	Decision  *pricing.Decision // publish only
}

// Snapshot is the store-gathered input for planning.
type Snapshot struct {
	Items    []store.RoutableItem
	Listings []store.ActiveListing
	Health   map[string]string
}

type decideFn func(ctx context.Context, ch domain.Channel, it store.RoutableItem) *pricing.Decision

// Plan computes the difference between desired and actual shelf state.
func (p *Planner) Plan(ctx context.Context) ([]Action, error) {
	items, err := p.Store.RoutableInventory(ctx)
	if err != nil {
		return nil, err
	}
	listings, err := p.Store.AllActiveListings(ctx)
	if err != nil {
		return nil, err
	}
	health := map[string]string{"uu": "ok", "eco": "ok"}
	if p.Health != nil {
		health = p.Health(ctx)
	}
	grace := p.OrphanGrace
	if grace <= 0 {
		grace = DefaultOrphanGrace
	}
	return PlanFrom(ctx, Snapshot{Items: items, Listings: listings, Health: health}, p.Now, grace, p.decideFor), nil
}

// PlanFrom is the pure reconciliation core (unit-testable).
//
// Desired state is derived per hash_name (route comes from the effective
// strategy keyed by hash, so every copy of a commodity shares one route):
//   - publish: a routable asset gets published on channel ch iff fewer
//     listings exist for (hash, ch) than routable copies AND this exact
//     asset has no listing there yet;
//   - delist: a listing is delisted iff its channel is not desired for its
//     hash (route change / failover / orphaned hash), OR it is a surplus copy
//     beyond the wanted count. Leased listings are NEVER delisted — their
//     re-evaluation happens after the lease ends. Not-routed delists stay
//     immediate (previous behavior); orphan/surplus delists wait out the
//     grace period keyed on the listing's last actual-sync timestamp.
func PlanFrom(ctx context.Context, snap Snapshot, now time.Time, orphanGrace time.Duration, decide decideFn) []Action {
	uuHealthy := snap.Health["uu"] == "ok"

	// wantCopies counts DISTINCT asset ids per (hash, channel): the same Steam
	// asset is synced once per channel that stocks it, so raw row counts would
	// double the wanted budget for items visible on both platforms (real-machine
	// finding 2026-08-27 — one physical knife planned for publish twice).
	wantCopies := map[string]map[domain.Channel]int{}
	distinct := map[string]map[domain.Channel]map[string]bool{}
	desiredByHash := map[string][]domain.Channel{}
	routeByHash := map[string]string{}
	for _, it := range snap.Items {
		chs := desiredChannels(it.Route, uuHealthy)
		desiredByHash[it.HashName] = chs
		routeByHash[it.HashName] = it.Route
		if wantCopies[it.HashName] == nil {
			wantCopies[it.HashName] = map[domain.Channel]int{}
			distinct[it.HashName] = map[domain.Channel]map[string]bool{}
		}
		for _, ch := range chs {
			if distinct[it.HashName][ch] == nil {
				distinct[it.HashName][ch] = map[string]bool{}
			}
			if !distinct[it.HashName][ch][it.AssetID] {
				distinct[it.HashName][ch][it.AssetID] = true
				wantCopies[it.HashName][ch]++
			}
		}
	}

	type key struct {
		hash string
		ch   domain.Channel
	}
	listedByHash := map[key][]store.ActiveListing{}
	for _, l := range snap.Listings {
		k := key{l.HashName, l.Channel}
		listedByHash[k] = append(listedByHash[k], l)
	}

	var plan []Action

	// ---- publish pass ----
	planned := map[key]map[string]bool{} // hash+ch → assets already in this plan
	for _, it := range snap.Items {
		if it.AssetID == "" {
			continue
		}
		for _, ch := range desiredByHash[it.HashName] {
			k := key{it.HashName, ch}
			if planned[k][it.AssetID] {
				continue // same asset reached via another channel's stock row
			}
			existing := listedByHash[k]
			assetListed := false
			for _, l := range existing {
				if l.AssetID == it.AssetID {
					assetListed = true
					break
				}
			}
			if assetListed || len(existing) >= wantCopies[it.HashName][ch] {
				continue
			}
			d := decide(ctx, ch, it)
			if d == nil || !d.OK {
				continue
			}
			if planned[k] == nil {
				planned[k] = map[string]bool{}
			}
			planned[k][it.AssetID] = true
			plan = append(plan, Action{
				Kind: "publish", Channel: ch, AssetID: it.AssetID,
				HashName: it.HashName, Reason: "route:" + it.Route,
				Decision: d,
			})
		}
	}

	// ---- delist pass ----
	kept := map[key]int{}
	cutoff := now.Add(-orphanGrace)
	for _, l := range snap.Listings {
		k := key{l.HashName, l.Channel}
		chs := desiredByHash[l.HashName]
		routed := containsChannel(chs, l.Channel)
		var reason string
		switch {
		case !routed && chs == nil:
			reason = "orphan_not_routable" // hash left the routable inventory entirely
		case !routed:
			reason = "not_routed"
			if l.Channel == domain.ChannelUU && !uuHealthy &&
				routeByHash[l.HashName] == "uu_primary_eco_fallback" {
				reason = "uu_unhealthy_failover"
			}
		case kept[k] >= wantCopies[l.HashName][l.Channel]:
			reason = "surplus_copies" // more live listings than routable assets
		default:
			kept[k]++ // correctly placed within the wanted copy budget
			continue
		}
		if l.State == "leased" {
			continue // never touch a rented listing; revisit after lease end
		}
		if reason != "not_routed" && reason != "uu_unhealthy_failover" {
			// orphan/surplus: only act once the state persisted past the grace
			// window; unknown sync age fails safe (skip).
			if l.SyncedAt.IsZero() || l.SyncedAt.After(cutoff) {
				continue
			}
		}
		plan = append(plan, Action{
			Kind: "delist", Channel: l.Channel, GoodsRef: l.GoodsRef,
			ListingID: l.ID, HashName: l.HashName, Reason: reason, State: l.State,
		})
	}
	return plan
}

func (p *Planner) decideFor(ctx context.Context, ch domain.Channel, it store.RoutableItem) *pricing.Decision {
	if it.V == nil || *it.V <= 0 {
		return &pricing.Decision{SkipReason: "no_value_anchor"}
	}
	es, err := p.Store.GetEffectiveStrategy(ctx, it.HashName)
	if err != nil {
		return &pricing.Decision{SkipReason: "no_strategy"}
	}
	params, err := pricing.ParseParams(es.GlobalParams, es.Params)
	if err != nil {
		return &pricing.Decision{SkipReason: "bad_strategy_params"}
	}
	mq, err := p.Store.RecentMergedQuotes(ctx, it.HashName, p.Now.Add(-30*time.Minute), params.Baseline.TopN*3)
	if err != nil {
		return &pricing.Decision{SkipReason: "no_baseline"}
	}
	quotes := make([]pricing.Quote, 0, len(mq))
	for _, m := range mq {
		quotes = append(quotes, pricing.Quote{Short: m.Short, Long: m.Long, Deposit: m.Deposit})
	}
	base, hasBase := pricing.Baseline(quotes, params.Baseline, *it.V)
	dayMin, dayMax := rentDayMin(ch), rentDayMax(ch)
	if c, ok := p.Caps[ch]; ok {
		dayMin, dayMax = c.RentMaxDayMin, c.RentMaxDayMax
	}
	in := pricing.Input{
		Channel: ch, HasV: true, V: *it.V,
		Base: base, HasBase: hasBase,
		// Cold start (spec §3): publish targets assets with no listing row yet,
		// so the controller starts neutral at 1.00; reprice reads the stored
		// listings.factor afterwards.
		Factor: 1.0,
		P:      params, Now: p.Now,
		RentMaxDayMin: dayMin, RentMaxDayMax: dayMax,
	}
	d := pricing.Decide(in)
	return &d
}

func rentDayMin(ch domain.Channel) int {
	if ch == domain.ChannelECO {
		return 8
	}
	return 1
}

func rentDayMax(ch domain.Channel) int { return 90 }

// desiredChannels resolves the routing rule to an ordered channel list.
func desiredChannels(route string, uuHealthy bool) []domain.Channel {
	switch route {
	case "uu_only":
		return []domain.Channel{domain.ChannelUU}
	case "eco_only":
		return []domain.Channel{domain.ChannelECO}
	case "uu_primary_eco_fallback":
		if uuHealthy {
			return []domain.Channel{domain.ChannelUU}
		}
		return []domain.Channel{domain.ChannelECO}
	default: // both
		return []domain.Channel{domain.ChannelUU, domain.ChannelECO}
	}
}

func containsChannel(list []domain.Channel, c domain.Channel) bool {
	for _, x := range list {
		if x == c {
			return true
		}
	}
	return false
}

// WriteBack persists execution outcomes so the next planning cycle sees the
// updated actual state (idempotency anchor). *store.Store implements it.
type WriteBack interface {
	RecordPublishedListing(ctx context.Context, channel, channelID, hashName, goodsRef string, rent, long, deposit float64, days int) error
	MarkListingDelisted(ctx context.Context, channel, goodsRef string) error
}

// Executor applies a plan through adapters with dry-run + audit semantics.
type Executor struct {
	DryRun   bool
	Log      *slog.Logger
	Audit    func(context.Context, domain.AuditEntry)
	Adapters map[domain.Channel]platform.Adapter
	// Store receives success write-backs; nil skips persistence (tests only).
	Store WriteBack
	// Penalize feeds platform transport errors into risk-control backoff; nil ignores.
	Penalize func(ctx context.Context, ch domain.Channel, err error)
}

// Execute runs publish/delist actions; returns failures.
// When DryRun is set, no platform call is made at all: every planned action is
// audited with a dry_run marker and counted as applied (i.e. "would apply").
// On real success the outcome is written back through Store so subsequent
// cycles do not replay the same action.
func (e *Executor) Execute(ctx context.Context, plan []Action) (applied, failed int) {
	for _, a := range plan {
		ad, ok := e.Adapters[a.Channel]
		if !ok {
			e.record(ctx, a, false, "no_adapter_configured")
			failed++
			continue
		}
		if e.DryRun {
			e.record(ctx, a, true, "")
			applied++
			continue
		}
		switch a.Kind {
		case "publish":
			req := platform.PublishLeaseRequest{
				AssetRef:      a.AssetID,
				RentPrice:     a.Decision.Rent,
				LongRentPrice: a.Decision.Long,
				MaxDays:       a.Decision.MaxDays,
				Deposit:       a.Decision.Deposit,
			}
			res, err := ad.PublishLease(ctx, []platform.PublishLeaseRequest{req})
			// round7 contract: per-item failures surface as ErrPartialFailure
			// with results still authoritative — judge by the item verdict.
			partial := errors.Is(err, platform.ErrPartialFailure)
			ok2 := len(res) > 0 && res[0].Success && (err == nil || partial)
			e.record(ctx, a, ok2, errString(err, res))
			if err != nil && !partial {
				e.penalize(ctx, a.Channel, err)
			}
			if ok2 {
				applied++
				e.writeBackPublish(ctx, a, res[0])
			} else {
				failed++
			}
		case "delist":
			err := ad.Delist(ctx, []string{a.GoodsRef})
			e.record(ctx, a, err == nil, errText(err))
			if err != nil {
				e.penalize(ctx, a.Channel, err)
				failed++
			} else {
				applied++
				e.writeBackDelist(ctx, a)
			}
		default:
			e.record(ctx, a, false, "unknown_kind")
			failed++
		}
	}
	return applied, failed
}

func (e *Executor) penalize(ctx context.Context, ch domain.Channel, err error) {
	if e.Penalize != nil {
		e.Penalize(ctx, ch, err)
	}
}

func (e *Executor) writeBackPublish(ctx context.Context, a Action, res platform.PublishLeaseResult) {
	if e.Store == nil || a.Decision == nil {
		return
	}
	goodsRef := res.GoodsRef
	if goodsRef == "" {
		// Platform did not echo the listing id; nothing durable to key on.
		// Shelf sync remains the fallback recorder for this case.
		e.Log.Warn("publish write-back skipped: no goods_ref echoed",
			"channel", string(a.Channel), "asset", a.AssetID)
		return
	}
	if err := e.Store.RecordPublishedListing(ctx, string(a.Channel), a.AssetID, a.HashName,
		goodsRef, a.Decision.Rent, a.Decision.Long, a.Decision.Deposit, a.Decision.MaxDays); err != nil {
		// A lost write-back risks a duplicate publish next cycle — surface loudly.
		e.Log.Error("publish write-back failed", "channel", string(a.Channel),
			"goods_ref", goodsRef, "err", err)
	}
}

func (e *Executor) writeBackDelist(ctx context.Context, a Action) {
	if e.Store == nil {
		return
	}
	if err := e.Store.MarkListingDelisted(ctx, string(a.Channel), a.GoodsRef); err != nil {
		e.Log.Error("delist write-back failed", "channel", string(a.Channel),
			"goods_ref", a.GoodsRef, "err", err)
	}
}

func (e *Executor) record(ctx context.Context, a Action, success bool, errMsg string) {
	detail := map[string]any{
		"kind": a.Kind, "channel": string(a.Channel), "reason": a.Reason,
	}
	if a.AssetID != "" {
		detail["asset_id"] = a.AssetID
	}
	if a.GoodsRef != "" {
		detail["goods_ref"] = a.GoodsRef
	}
	if a.ListingID != 0 {
		detail["listing_id"] = a.ListingID
	}
	if errMsg != "" {
		detail["err"] = errMsg
	}
	if e.DryRun {
		detail["dry_run"] = true
	}
	if a.Decision != nil {
		detail["decision"] = a.Decision
	}
	entry := domain.AuditEntry{
		Time: time.Now().UTC(), Actor: "system",
		Action: "shelf." + a.Kind, Channel: string(a.Channel),
		Target: a.HashName, Detail: detail,
	}
	if e.Audit != nil {
		e.Audit(ctx, entry)
	}
	e.Log.Info("recon action", "kind", a.Kind, "channel", string(a.Channel),
		"hash", a.HashName, "success", success, "dry_run", e.DryRun, "err", errMsg)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func errString(err error, res []platform.PublishLeaseResult) string {
	if errors.Is(err, platform.ErrPartialFailure) && len(res) > 0 && res[0].Error != "" {
		return res[0].Error // item remark beats the generic sentinel text
	}
	if err != nil {
		return err.Error()
	}
	if len(res) > 0 && !res[0].Success {
		return res[0].Error
	}
	return ""
}
