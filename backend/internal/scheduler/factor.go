// Package scheduler — feedback-controller wiring (pricing-spec §3):
// finished orders fold into per-listing factors; stale listings step down;
// listings stuck at f_min reset to 1.00 with an operator alert.
package scheduler

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/pricing"
	"github.com/3219378872/rent-auto/backend/internal/store"
	"github.com/jackc/pgx/v5"
)

const (
	factorOrderBatch  = 500
	factorOrderWindow = 72 * time.Hour // bound the join scan to recent finishes
)

// RunFactorEvents folds newly finished orders (rent_success / bought_out)
// into listing factors and processes stale-day step-downs. Idempotent via
// lease_orders.factor_applied.
func (d *Deps) RunFactorEvents(ctx context.Context) error {
	if err := d.foldOrderEvents(ctx); err != nil {
		return err
	}
	return d.runStaleScan(ctx)
}

// foldOrderEvents applies rent_success/bought_out signals. Factor updates and
// their factor_applied markers commit in one transaction (ApplyFactorFolds):
// a crash can never leave folded orders unmarked for replay.
func (d *Deps) foldOrderEvents(ctx context.Context) error {
	orders, err := d.Store.UnhandledFactorOrders(ctx, time.Now().Add(-factorOrderWindow), factorOrderBatch)
	if err != nil {
		return err
	}
	paramsCache := map[string]*pricing.Params{}
	folds := make([]store.FactorFold, 0, len(orders))
	for _, o := range orders {
		p, ok := paramsCache[o.HashName]
		if !ok {
			es, err := d.Store.GetEffectiveStrategy(ctx, o.HashName)
			if err != nil {
				continue // no strategy → nothing to control
			}
			pp, err := pricing.ParseParams(es.GlobalParams, es.Params)
			if err != nil {
				continue
			}
			p = &pp
			paramsCache[o.HashName] = p
		}
		cur, err := d.listingFactor(ctx, o.ListingID)
		if err != nil {
			continue
		}
		var ev pricing.FactorEvent
		switch {
		case o.Status == "bought_out":
			ev = pricing.EventBoughtOut
		case o.Status == "done" && (o.MaxDays <= 0 || o.RentDays < o.MaxDays):
			// 订单完成且未租满整个周期 → 正向信号（spec §3）
			ev = pricing.EventRentSuccess
		default:
			// full-term completion: spec defines no signal — fold silently
		}
		next := cur
		if ev != "" {
			next, _ = pricing.NextFactor(cur, ev, p.Ctrl)
		}
		folds = append(folds, store.FactorFold{OrderID: o.OrderID, ListingID: o.ListingID, Factor: next})
		if ev != "" {
			d.Log.Info("factor event planned", "listing", o.ListingID,
				"event", string(ev), "from", cur, "to", next)
		}
	}
	return d.Store.ApplyFactorFolds(ctx, folds)
}

// runStaleScan steps down factors for listings idle past their stale window.
// A listing already at f_min that still shows no conversion resets to 1.00
// with an operator-facing audit alert ("建议人工检查").
func (d *Deps) runStaleScan(ctx context.Context) error {
	cands, err := d.Store.FactorStaleCandidates(ctx, 24*time.Hour)
	if err != nil {
		return err
	}
	paramsCache := map[string]*pricing.Params{}
	for _, c := range cands {
		p, ok := paramsCache[c.HashName]
		if !ok {
			es, err := d.Store.GetEffectiveStrategy(ctx, c.HashName)
			if err != nil {
				continue
			}
			pp, err := pricing.ParseParams(es.GlobalParams, es.Params)
			if err != nil {
				continue
			}
			p = &pp
			paramsCache[c.HashName] = p
		}
		if c.AnchorAge < time.Duration(p.Ctrl.StaleDays)*24*time.Hour {
			continue
		}
		ev := pricing.EventStaleDay
		if math.Abs(c.Factor-p.Ctrl.FMin) < 1e-9 {
			ev = pricing.EventReset
		}
		next, reason := pricing.NextFactor(c.Factor, ev, p.Ctrl)
		if err := d.Store.SetListingFactor(ctx, c.ListingID, next); err != nil {
			d.Log.Warn("stale factor update failed", "listing", c.ListingID, "err", err)
			continue
		}
		d.Log.Info("stale scan adjusted factor", "listing", c.ListingID,
			"hash", c.HashName, "reason", reason, "from", c.Factor, "to", next)
		if ev == pricing.EventReset {
			if d.Audit != nil {
				d.Audit(ctx, domain.AuditEntry{
					Time: time.Now().UTC(), Actor: "system",
					Action: "pricing.factor_reset", Channel: string(c.Channel),
					Target: c.HashName,
					Detail: map[string]any{
						"listing": c.ListingID,
						"note":    "连续 stale 降价至下限仍无转化，因子已回归 1.00 —— 建议人工检查",
					},
				})
			}
		}
	}
	return nil
}

func (d *Deps) listingFactor(ctx context.Context, listingID int64) (float64, error) {
	var f float64
	err := d.Store.Pool.QueryRow(ctx,
		`SELECT COALESCE(factor,1.0) FROM listings WHERE id=$1`, listingID).Scan(&f)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 1.0, nil
		}
		return 0, err
	}
	return f, nil
}
