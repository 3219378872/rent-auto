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
	factorOrderBatch = 500
	// Bound the join scan to the orders_sync max lookback (100d, ADR-0004):
	// a tighter window permanently skipped late-synced terminal orders —
	// factor_applied stayed false forever but the row was never selected.
	factorOrderWindow = 100 * 24 * time.Hour
	// factorResetEpsilon is the "already at f_min" tolerance for the
	// stale-reset decision. Factors persist through Round4 (1e-4 quantum),
	// so 1e-9 misread a quantized f_min (e.g. 0.8500000x) as "not the floor"
	// and the reset to 1.00 never fired.
	factorResetEpsilon = 1e-4
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
//
// Multiple orders mapping to the same listing within one batch are folded
// sequentially in memory (each NextFactor consumes the previous result) and
// collapse into a single UPDATE per listing — reading cur from the DB per
// order would make every order see the same base value and only the last
// fold would survive the write.
func (d *Deps) foldOrderEvents(ctx context.Context) error {
	orders, err := d.Store.UnhandledFactorOrders(ctx, time.Now().Add(-factorOrderWindow), factorOrderBatch)
	if err != nil {
		return err
	}
	// Order-time terms for event classification (one batched lookup, no N+1).
	terms, err := d.orderTerms(ctx, orderIDsOf(orders))
	if err != nil {
		// The batch stays unmarked, so the next run replays it.
		d.Log.Warn("factor fold: order detail lookup failed", "err", err)
		return err
	}
	// Listing max_days rides on FactorOrder (join snapshot) as the legacy
	// fallback (see classifyFactorEvent) — no extra lookup needed.
	paramsCache := map[string]*pricing.Params{}
	foldIdx := map[int64]int{} // listingID → index into folds
	var folds []store.FactorFold
	var orderIDs []int64
	for _, o := range orders {
		p, ok := paramsCache[o.HashName]
		if !ok {
			es, err := d.Store.GetEffectiveStrategy(ctx, o.HashName)
			if err != nil {
				// The global strategy row is guaranteed by startup; any error
				// here (store outage) must not pass silently — those orders
				// would otherwise be skipped without a trace. The batch stays
				// unmarked, so the next run replays them.
				d.Log.Warn("factor fold: strategy lookup failed", "order", o.OrderID, "hash", o.HashName, "err", err)
				continue
			}
			pp, err := pricing.ParseParams(es.GlobalParams, es.Params)
			if err != nil {
				d.Log.Warn("factor fold: strategy params parse failed", "order", o.OrderID, "hash", o.HashName, "err", err)
				continue
			}
			p = &pp
			paramsCache[o.HashName] = p
		}
		cur, ok := foldIdx[o.ListingID]
		if !ok {
			base, err := d.listingFactor(ctx, o.ListingID)
			if err != nil {
				continue
			}
			cur = len(folds)
			foldIdx[o.ListingID] = cur
			folds = append(folds, store.FactorFold{ListingID: o.ListingID, Factor: base})
		}
		f := &folds[cur]
		ev := classifyFactorEvent(o.Status, terms[o.OrderID], o.MaxDays)
		if ev != "" {
			next, _ := pricing.NextFactor(f.Factor, ev, p.Ctrl)
			d.Log.Info("factor event planned", "listing", o.ListingID,
				"event", string(ev), "from", f.Factor, "to", next)
			f.Factor = next
		}
		orderIDs = append(orderIDs, o.OrderID)
	}
	return d.Store.ApplyFactorFolds(ctx, folds, orderIDs)
}

// orderTerm carries the order-time classification inputs for one lease order.
type orderTerm struct {
	orderType string // short|long|"" (legacy UU rows: UU never sets order_type)
	rentDays  int
	termDays  int // due−started in days; <=0 = unknown
}

// classifyFactorEvent maps a finished order onto a controller event WITHOUT
// letting a LATER strategy edit rewrite history: long leases use the order-time
// term snapshot (rent_days < due−started); legacy rows without a term snapshot
// fall back to the listing's configured max_days (best effort, documented).
// pricing-spec §3: full-term completion defines no signal.
//   - bought_out → bought_out (double step-up);
//   - done + long with known term → rent_success only on early completion;
//   - done + short/legacy → rent_success only when rent_days < term (known) or
//     < fallback max_days; full term or unknowable term yields no signal.
func classifyFactorEvent(status string, t orderTerm, fallbackMaxDays int) pricing.FactorEvent {
	switch status {
	case "bought_out":
		return pricing.EventBoughtOut
	case "done":
		if t.orderType == "long" {
			if t.termDays > 0 && t.rentDays > 0 && t.rentDays < t.termDays {
				return pricing.EventRentSuccess
			}
			return ""
		}
		if t.termDays > 0 && t.rentDays > 0 {
			if t.rentDays < t.termDays {
				return pricing.EventRentSuccess
			}
			return ""
		}
		if fallbackMaxDays > 0 && t.rentDays > 0 {
			if t.rentDays < fallbackMaxDays {
				return pricing.EventRentSuccess
			}
			return ""
		}
		return pricing.EventRentSuccess
	default:
		return ""
	}
}

func orderIDsOf(orders []store.FactorOrder) []int64 {
	ids := make([]int64, 0, len(orders))
	for _, o := range orders {
		ids = append(ids, o.OrderID)
	}
	return ids
}

// orderTerms batch-loads order-time terms for event classification: order_type
// plus the due−started span as the term snapshot. A missing row yields the
// zero term (classified as short on done) — only reachable under a concurrent
// delete, since the ids come from lease_orders itself.
func (d *Deps) orderTerms(ctx context.Context, ids []int64) (map[int64]orderTerm, error) {
	out := map[int64]orderTerm{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := d.Store.Pool.Query(ctx,
		`SELECT id, COALESCE(order_type,''), COALESCE(rent_days,0),
		        COALESCE(started_at, now()), COALESCE(due_at, now())
		 FROM lease_orders WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var t orderTerm
		var started, due time.Time
		if err := rows.Scan(&id, &t.orderType, &t.rentDays, &started, &due); err != nil {
			return nil, err
		}
		if !started.IsZero() && !due.IsZero() && due.After(started) {
			t.termDays = int(due.Sub(started).Hours() / 24)
		}
		out[id] = t
	}
	return out, rows.Err()
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
				d.Log.Warn("stale scan: strategy lookup failed", "hash", c.HashName, "err", err)
				continue
			}
			pp, err := pricing.ParseParams(es.GlobalParams, es.Params)
			if err != nil {
				d.Log.Warn("stale scan: strategy params parse failed", "hash", c.HashName, "err", err)
				continue
			}
			p = &pp
			paramsCache[c.HashName] = p
		}
		if c.AnchorAge < time.Duration(p.Ctrl.StaleDays)*24*time.Hour {
			continue
		}
		ev := pricing.EventStaleDay
		if isAtFactorMin(c.Factor, p.Ctrl.FMin) {
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
				// NextFactor clamps the 1.0 reset into [FMin,FMax] when the
				// configured floor sits above neutral (reason carries
				// |clamped_min); from/to/reason keep the audit truthful.
				d.Audit(ctx, domain.AuditEntry{
					Time: time.Now().UTC(), Actor: "system",
					Action: "pricing.factor_reset", Channel: string(c.Channel),
					Target: c.HashName,
					Detail: map[string]any{
						"listing": c.ListingID,
						"from":    c.Factor,
						"to":      next,
						"reason":  reason,
						"note":    "连续 stale 降价至下限仍无转化，因子已回归 1.00 —— 建议人工检查",
					},
				})
			}
		}
	}
	return nil
}

// isAtFactorMin reports whether a stored factor sits at the configured floor
// within the Round4 quantum (factorResetEpsilon): quantized persistence means
// exact equality never holds in general.
func isAtFactorMin(factor, fmin float64) bool {
	return math.Abs(factor-fmin) < factorResetEpsilon
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
