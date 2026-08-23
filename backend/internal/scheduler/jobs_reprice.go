package scheduler

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
	"github.com/3219378872/rent-auto/backend/internal/pricing"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

// Deps wires the reprice pipeline to storage and adapters.
type Deps struct {
	Store  *store.Store
	Log    *slog.Logger
	DryRun bool // global default; effective = DryRun || !strategy.RealEnabled
}

// quoteWindow bounds how old market snapshots may be for baselines.
const quoteWindow = 30 * time.Minute

// RunReprice reprices active listings on every configured channel.
func (d *Deps) RunReprice(ctx context.Context, adapters []platform.Adapter) error {
	now := time.Now().UTC()
	for _, ad := range adapters {
		if err := d.repriceChannel(ctx, ad, now); err != nil {
			d.Log.Error("reprice channel failed", "channel", string(ad.Channel()), "err", err)
		}
	}
	return nil
}

func (d *Deps) repriceChannel(ctx context.Context, ad platform.Adapter, now time.Time) error {
	cands, err := d.Store.ListRepriceCandidates(ctx, ad.Channel())
	if err != nil {
		return err
	}
	strategyCache := map[string]*pricing.Params{}

	for _, c := range cands {
		es, err := d.Store.GetEffectiveStrategy(ctx, c.HashName)
		if err != nil {
			continue
		}
		key := string(es.GlobalParams) + "|" + string(es.Params)
		p, ok := strategyCache[key]
		if !ok {
			pp, err := pricing.ParseParams(es.GlobalParams, es.Params)
			if err != nil {
				d.Log.Warn("strategy params parse failed", "hash", c.HashName, "err", err)
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
		} else if err != nil {
			d.Log.Warn("reprice call failed", "goods", c.GoodsRef, "err", err)
		}
	}
	return nil
}

// loadQuotes rebuilds the ranked quote slice from stored snapshots.
func (d *Deps) loadQuotes(ctx context.Context, hash string, topn int) []pricing.Quote {
	rows, err := d.Store.RecentQuotes(ctx, hash, time.Now().Add(-quoteWindow), topn*3)
	if err != nil {
		return nil
	}
	out := make([]pricing.Quote, 0, topn)
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
	if len(out) > topn {
		out = out[:topn]
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

var _ = domain.ChannelUU
