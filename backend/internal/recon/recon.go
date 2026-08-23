// Package recon reconciles desired shelf state (strategy × inventory × route)
// against actual per-channel listings, producing idempotent action plans.
package recon

import (
	"context"
	"log/slog"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
	"github.com/3219378872/rent-auto/backend/internal/pricing"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

// HealthFn reports per-channel reachability ("ok" when routable).
type HealthFn func(ctx context.Context) map[string]string

type Planner struct {
	Store  *store.Store
	Log    *slog.Logger
	Health HealthFn // nil = assume all healthy
	Now    time.Time
}

// Action is one reconciliation step.
type Action struct {
	Kind     string // publish | delist
	Channel  domain.Channel
	AssetID  string
	GoodsRef string // delist only
	HashName string
	Reason   string
	Decision *pricing.Decision // publish only
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
	return PlanFrom(Snapshot{Items: items, Listings: listings, Health: health}, p.Now, p.decideFor), nil
}

// PlanFrom is the pure reconciliation core (unit-testable).
func PlanFrom(snap Snapshot, now time.Time, decide decideFn) []Action {
	uuHealthy := snap.Health["uu"] == "ok"

	actual := map[string]map[domain.Channel]string{}
	for _, l := range snap.Listings {
		if actual[l.HashName] == nil {
			actual[l.HashName] = map[domain.Channel]string{}
		}
		actual[l.HashName][l.Channel] = l.GoodsRef
	}

	var plan []Action
	for _, it := range snap.Items {
		channels := desiredChannels(it.Route, uuHealthy)
		present := actual[it.HashName]

		for _, ch := range channels {
			if _, ok := present[ch]; !ok && it.AssetID != "" {
				d := decide(context.Background(), ch, it)
				if d == nil || !d.OK {
					continue
				}
				plan = append(plan, Action{
					Kind: "publish", Channel: ch, AssetID: it.AssetID,
					HashName: it.HashName, Reason: "route:" + it.Route,
					Decision: d,
				})
			}
		}
		for ch, goodsRef := range present {
			if !containsChannel(channels, ch) {
				reason := "not_routed"
				if it.Route == "uu_primary_eco_fallback" && ch == domain.ChannelUU && !uuHealthy {
					reason = "uu_unhealthy_failover"
				}
				plan = append(plan, Action{
					Kind: "delist", Channel: ch, GoodsRef: goodsRef,
					HashName: it.HashName, Reason: reason,
				})
			}
		}
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
	rows, _ := p.Store.RecentQuotes(ctx, it.HashName, time.Now().Add(-30*time.Minute), params.Baseline.TopN*3)
	quotes := ConvertQuotes(rows)
	base, hasBase := pricing.Baseline(quotes, params.Baseline, *it.V)
	in := pricing.Input{
		Channel: ch, HasV: true, V: *it.V,
		Base: base, HasBase: hasBase, Factor: 1.0,
		P: params, Now: p.Now,
		RentMaxDayMin: rentDayMin(ch), RentMaxDayMax: rentDayMax(ch),
	}
	d := pricing.Decide(in)
	return &d
}

// ConvertQuotes maps stored snapshot rows to engine quotes.
func ConvertQuotes(rows []store.QuoteRow) []pricing.Quote {
	out := make([]pricing.Quote, 0, len(rows))
	for _, r := range rows {
		q := pricing.Quote{}
		switch r.Kind {
		case "lease_short":
			q.Short = r.Price
		case "lease_long":
			q.Long = r.Price
		case "deposit":
			q.Deposit = r.Price
		}
		out = append(out, q)
	}
	return out
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

// Executor applies a plan through adapters with dry-run + audit semantics.
type Executor struct {
	DryRun   bool
	Log      *slog.Logger
	Audit    func(context.Context, domain.AuditEntry)
	Adapters map[domain.Channel]platform.Adapter
}

// Execute runs publish/delist actions; returns failures.
func (e *Executor) Execute(ctx context.Context, plan []Action) (applied, failed int) {
	for _, a := range plan {
		ad, ok := e.Adapters[a.Channel]
		if !ok {
			failed++
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
			ok2 := err == nil && len(res) > 0 && res[0].Success
			e.record(ctx, a, ok2, errString(err, res))
			if ok2 && !e.DryRun {
				applied++
			} else if !ok2 {
				failed++
			}
		case "delist":
			err := ad.Delist(ctx, []string{a.GoodsRef})
			e.record(ctx, a, err == nil, errText(err))
			if err == nil && !e.DryRun {
				applied++
			} else if err != nil {
				failed++
			}
		}
	}
	return applied, failed
}

func (e *Executor) record(ctx context.Context, a Action, success bool, errMsg string) {
	detail := map[string]any{"kind": a.Kind, "channel": string(a.Channel), "reason": a.Reason}
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
	if err != nil {
		return err.Error()
	}
	if len(res) > 0 && !res[0].Success {
		return res[0].Error
	}
	return ""
}
