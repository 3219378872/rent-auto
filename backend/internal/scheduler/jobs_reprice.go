package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
	"github.com/3219378872/rent-auto/backend/internal/platform/uu"
	"github.com/3219378872/rent-auto/backend/internal/pricing"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

// Deps wires the reprice pipeline to storage and adapters.
type Deps struct {
	Store  *store.Store
	Log    *slog.Logger
	DryRun bool // global default; effective = DryRun || !strategy.RealEnabled
	// Audit receives controller events worth operator attention (factor resets).
	Audit func(context.Context, domain.AuditEntry)

	mu        sync.Mutex
	cooldowns map[domain.Channel]time.Time // risk-control backoff per channel
}

// Risk-control backoff windows (api-notes: 平台风控信号由调度器决定退避).
const (
	rateLimitCooldown    = 5 * time.Minute
	platformBlockCool    = 15 * time.Minute
	ukExpiredCooldownMin = 2 * time.Minute
	authExpiredCooldown  = 30 * time.Minute
)

// channelReady reports whether a channel has served out its risk-control cooldown.
func (d *Deps) channelReady(ch domain.Channel, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	until, ok := d.cooldowns[ch]
	return !ok || now.After(until)
}

// penalize puts a channel into cooldown when the platform signalled risk
// control (rate limit / block / UK expiry / auth expiry); other errors are ignored.
func (d *Deps) penalize(ch domain.Channel, err error) {
	var until time.Time
	switch {
	case errors.Is(err, platform.ErrRateLimited):
		until = time.Now().Add(rateLimitCooldown)
	case errors.Is(err, platform.ErrPlatformBlocked):
		until = time.Now().Add(platformBlockCool)
	case errors.Is(err, uu.ErrUKExpired):
		until = time.Now().Add(ukExpiredCooldownMin)
	case errors.Is(err, platform.ErrAuthExpired):
		until = time.Now().Add(authExpiredCooldown)
	default:
		return
	}
	d.mu.Lock()
	if d.cooldowns == nil {
		d.cooldowns = map[domain.Channel]time.Time{}
	}
	prev := d.cooldowns[ch]
	if until.After(prev) {
		d.cooldowns[ch] = until
	}
	d.mu.Unlock()
	d.Log.Warn("channel risk-control cooldown",
		"channel", string(ch), "until", until.Format(time.RFC3339), "err", err.Error())
}

// ChannelReady reports whether ch has served out its risk-control cooldown.
func (d *Deps) ChannelReady(ch domain.Channel) bool { return d.channelReady(ch, time.Now()) }

// NoteChannelError feeds a platform transport error into risk-control backoff.
// Write pipelines outside the reprice job (the reconcile executor) use this to
// respect the same per-channel cooldown discipline.
func (d *Deps) NoteChannelError(_ context.Context, ch domain.Channel, err error) { d.penalize(ch, err) }

// quoteWindow bounds how old market snapshots may be for baselines.
const quoteWindow = 30 * time.Minute

// RunReprice reprices active listings on every configured channel.
// Channels in risk-control cooldown are skipped; real failures are aggregated
// into the returned error so the panel LastError reflects them.
func (d *Deps) RunReprice(ctx context.Context, adapters []platform.Adapter) error {
	now := time.Now().UTC()
	var errs []error
	for _, ad := range adapters {
		if !d.channelReady(ad.Channel(), now) {
			continue
		}
		if err := d.repriceChannel(ctx, ad, now); err != nil {
			d.Log.Error("reprice channel failed", "channel", string(ad.Channel()), "err", err)
			d.penalize(ad.Channel(), err)
			errs = append(errs, fmt.Errorf("reprice %s: %w", ad.Channel(), err))
		}
	}
	return errors.Join(errs...)
}

func (d *Deps) repriceChannel(ctx context.Context, ad platform.Adapter, now time.Time) error {
	cands, err := d.Store.ListRepriceCandidates(ctx, ad.Channel())
	if err != nil {
		return err
	}
	strategyCache := map[string]*pricing.Params{}
	var stratErrs []error

	for _, c := range cands {
		es, err := d.Store.GetEffectiveStrategy(ctx, c.HashName)
		if err != nil {
			// A store outage here must not look like "nothing to do": candidates
			// silently skipped with a nil error upstream would report a
			// successful reprice cycle. Surface loudly, keep the loop alive.
			d.Log.Warn("effective strategy lookup failed", "hash", c.HashName, "err", err)
			stratErrs = append(stratErrs, fmt.Errorf("strategy %s: %w", c.HashName, err))
			continue
		}
		key := string(es.GlobalParams) + "|" + string(es.Params)
		p, ok := strategyCache[key]
		if !ok {
			pp, err := pricing.ParseParams(es.GlobalParams, es.Params)
			if err != nil {
				d.Log.Warn("strategy params parse failed", "hash", c.HashName, "err", err)
				stratErrs = append(stratErrs, fmt.Errorf("strategy %s: %w", c.HashName, err))
				continue
			}
			p = &pp
			strategyCache[key] = p
		}
		effectiveDry := d.DryRun || !es.RealEnabled

		quotes := d.loadQuotes(ctx, c.HashName, p.Baseline.TopN)
		base, hasBase := pricing.Baseline(quotes, p.Baseline, valOr0(c.V))

		in := pricing.Input{
			Channel: c.Channel, HasV: c.V != nil, V: valOr0(c.V),
			Base: base, HasBase: hasBase,
			Factor:        c.Factor,
			Cur:           pricing.Current{RentPrice: c.RentPrice, LastActionAt: tsOrZero(c.LastActionAt)},
			P:             *p,
			Now:           now,
			RentMaxDayMin: ad.Caps().RentMaxDayMin,
			RentMaxDayMax: ad.Caps().RentMaxDayMax,
			// ECO listings that have never had the sublet policy accepted by
			// the platform must push a payload even at an unchanged price —
			// cooldown and change-rate cap still apply (2026-08-28 backfill).
			IgnoreNoiseFloor: c.Channel == domain.ChannelECO && !c.SubletApplied,
		}
		decision := pricing.Decide(in)

		pa := store.PriceAction{
			Channel: c.Channel, HashName: c.HashName, AssetID: c.AssetID, ListingID: c.ListingID,
			Action:  "reprice",
			OldRent: store.PtrF(c.RentPrice), OldLong: store.PtrF(c.LongPrice),
			OldDays: intPtr(0), OldDeposit: store.PtrF(c.Deposit),
			DryRun: effectiveDry,
		}
		if !decision.OK {
			pa.Action = "skip"
			pa.Decision, _ = json.Marshal(map[string]any{"skip": decision.SkipReason, "reasons": decision.Reasons})
			pa.Success = true
			_, _ = d.Store.InsertPriceAction(ctx, pa)
			continue
		}
		pa.NewRent = store.PtrF(decision.Rent)
		pa.NewLong = store.PtrF(decision.Long)
		pa.NewDays = intPtr(decision.MaxDays)
		pa.NewDeposit = store.PtrF(decision.Deposit)
		pa.Decision, _ = json.Marshal(map[string]any{
			"reasons": decision.Reasons, "rent": decision.Rent,
			"long": decision.Long, "days": decision.MaxDays, "deposit": decision.Deposit,
			"factor": in.Factor, "v": in.V,
		})

		if effectiveDry {
			pa.Success = true
			_, _ = d.Store.InsertPriceAction(ctx, pa)
			continue
		}

		res, err := ad.RepriceLease(ctx, []platform.RepriceLeaseRequest{{
			GoodsRef: c.GoodsRef, AssetRef: c.AssetID,
			RentPrice: decision.Rent, LongRentPrice: decision.Long,
			MaxDays: decision.MaxDays, Deposit: decision.Deposit,
		}})
		if len(res) > 0 {
			pa.Success = res[0].Success
			pa.Error = res[0].Error
		} else if err != nil {
			pa.Error = err.Error()
		}
		if _, ierr := d.Store.InsertPriceAction(ctx, pa); ierr != nil {
			d.Log.Error("price action insert failed", "err", ierr)
		}
		if err == nil && (len(res) == 0 || res[0].Success) {
			newVals := struct {
				Rent, Long, Deposit float64
				Days                int
			}{decision.Rent, decision.Long, decision.Deposit, decision.MaxDays}
			if uerr := d.Store.UpdateListingDecision(ctx, c.ListingID, newVals); uerr != nil {
				d.Log.Error("listing update failed", "listing", c.ListingID, "err", uerr)
			}
			if in.IgnoreNoiseFloor {
				if merr := d.Store.MarkListingSubletApplied(ctx, c.ListingID); merr != nil {
					// Leaving the flag false is safe: the next cycle retries the
					// forced submission instead of silently skipping backfill.
					d.Log.Error("sublet flag update failed", "listing", c.ListingID, "err", merr)
				}
			}
		} else if err != nil {
			d.Log.Warn("reprice call failed", "goods", c.GoodsRef, "err", err)
		}
	}
	return errors.Join(stratErrs...)
}

// loadQuotes rebuilds the per-commodity ranked quote slice from stored
// snapshots (pricing-spec §2: one quote per market position; overlapping
// capture batches must not double-count).
func (d *Deps) loadQuotes(ctx context.Context, hash string, topn int) []pricing.Quote {
	rows, err := d.Store.RecentMergedQuotes(ctx, hash, time.Now().Add(-quoteWindow), topn*3)
	if err != nil {
		// Empty quotes read as "no baseline" and produce a skip record; the
		// underlying store failure must still be visible in logs.
		d.Log.Warn("quote load failed", "hash", hash, "err", err)
		return nil
	}
	out := make([]pricing.Quote, 0, len(rows))
	for _, r := range rows {
		out = append(out, pricing.Quote{Short: r.Short, Long: r.Long, Deposit: r.Deposit})
	}
	return out
}

// ApplyFactorEvent persists controller factor transitions after order events.
func (d *Deps) ApplyFactorEvent(ctx context.Context, listingID int64, cur float64, ev pricing.FactorEvent, p pricing.ControllerParams) error {
	f, _ := pricing.NextFactor(cur, ev, p)
	return d.Store.SetListingFactor(ctx, listingID, f)
}

func valOr0(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func tsOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func intPtr(v int) *int { return &v }
