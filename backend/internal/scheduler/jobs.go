package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/analytics"
	"github.com/3219378872/rent-auto/backend/internal/bench"
	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
	"github.com/3219378872/rent-auto/backend/internal/pricing"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

// Jobs builds the standard job set bound to live dependencies.
func Jobs(d *Deps, adapters func() []platform.Adapter, uuQuotes func(ctx context.Context, tplID int64, minP, maxP float64) ([]pricing.Quote, error), ecoDump func(ctx context.Context) (map[string]float64, error), zeroCD func(ctx context.Context) error, reconcile, uuDelivery, steamOffers, ecoDelivery func(ctx context.Context) error, log *slog.Logger) []Job {
	return []Job{
		{Name: "reprice", Kind: KindInterval, Every: 31 * time.Minute, Jitter: 90 * time.Second,
			Fn: func(ctx context.Context) error { return d.RunReprice(ctx, adapters()) }},
		{Name: "factor_events", Kind: KindInterval, Every: 17 * time.Minute, Jitter: 60 * time.Second,
			Fn: func(ctx context.Context) error { return d.RunFactorEvents(ctx) }},
		{Name: "inventory_sync", Kind: KindInterval, Every: 30 * time.Minute, Jitter: 60 * time.Second,
			Fn: func(ctx context.Context) error {
				var errs []error
				for _, ad := range adapters() {
					if !d.channelReady(ad.Channel(), time.Now()) {
						continue
					}
					if _, err := bench.SyncInventory(ctx, ad, d.Store, log); err != nil {
						d.penalize(ad.Channel(), err)
						log.Error("inventory sync", "channel", string(ad.Channel()), "err", err)
						errs = append(errs, fmt.Errorf("inventory %s: %w", ad.Channel(), err))
					}
				}
				return errors.Join(errs...)
			}},
		{Name: "shelf_sync", Kind: KindInterval, Every: 10 * time.Minute, Jitter: 30 * time.Second,
			Fn: func(ctx context.Context) error {
				var errs []error
				for _, ad := range adapters() {
					if !d.channelReady(ad.Channel(), time.Now()) {
						continue
					}
					if _, err := bench.SyncShelf(ctx, ad, d.Store, log, bench.SyncShelfOpts{
						Audit: func(msg string, detail map[string]any) {
							if d.Audit != nil {
								d.Audit(ctx, domain.AuditEntry{
									Time: time.Now().UTC(), Actor: "system",
									Action: "shelf.empty_breaker", Channel: string(ad.Channel()),
									Detail: detail,
								})
							}
							_ = msg
						},
					}); err != nil {
						d.penalize(ad.Channel(), err)
						log.Error("shelf sync", "channel", string(ad.Channel()), "err", err)
						errs = append(errs, fmt.Errorf("shelf %s: %w", ad.Channel(), err))
					}
				}
				return errors.Join(errs...)
			}},
		{Name: "orders_sync", Kind: KindInterval, Every: 10 * time.Minute, Jitter: 30 * time.Second,
			Fn: func(ctx context.Context) error {
				since := d.orderSyncSince(ctx)
				var errs []error
				for _, ad := range adapters() {
					if !d.channelReady(ad.Channel(), time.Now()) {
						continue
					}
					if _, err := bench.SyncOrders(ctx, ad, d.Store, since, log); err != nil {
						d.penalize(ad.Channel(), err)
						log.Error("orders sync", "channel", string(ad.Channel()), "err", err)
						errs = append(errs, fmt.Errorf("orders %s: %w", ad.Channel(), err))
					}
				}
				if _, err := analytics.RollupTerminalOrders(ctx, d.Store, log); err != nil {
					log.Error("income rollup", "err", err)
					errs = append(errs, fmt.Errorf("income rollup: %w", err))
				}
				return errors.Join(errs...)
			}},
		{Name: "market_snapshot", Kind: KindInterval, Every: 20 * time.Minute, Jitter: 120 * time.Second,
			Fn: func(ctx context.Context) error { return runMarketSnapshot(ctx, d.Store, uuQuotes, log) }},
		{Name: "value_anchor", Kind: KindInterval, Every: time.Hour, Jitter: 5 * time.Minute,
			Fn: func(ctx context.Context) error {
				if ecoDump != nil {
					if prices, err := ecoDump(ctx); err == nil && len(prices) > 0 {
						n, uerr := d.Store.UpdateEcoRefPrices(ctx, prices)
						if uerr != nil {
							log.Warn("eco ref price persist failed", "err", uerr)
						} else {
							log.Info("eco ref prices updated", "rows", n)
						}
					} else if err != nil {
						log.Warn("eco dump failed", "err", err)
					}
				}
				n, err := bench.RecomputeAnchors(ctx, d.Store)
				log.Info("anchors recomputed", "rows", n, "err", err)
				return err
			}},
		{Name: "reconcile", Kind: KindInterval, Every: 10 * time.Minute, Jitter: 60 * time.Second,
			Fn: func(ctx context.Context) error {
				if reconcile == nil {
					return nil
				}
				return reconcile(ctx)
			}},
		{Name: "uu_delivery", Kind: KindInterval, Every: 5 * time.Minute, Jitter: 45 * time.Second,
			Fn: uuDelivery},
		{Name: "steam_offers", Kind: KindInterval, Every: 5 * time.Minute, Jitter: 45 * time.Second,
			Fn: steamOffers},
		{Name: "eco_delivery", Kind: KindInterval, Every: 5 * time.Minute, Jitter: 45 * time.Second,
			Fn: ecoDelivery},
		{Name: "zero_cd", Kind: KindDaily, At: "23:30",
			Fn: func(ctx context.Context) error {
				if zeroCD == nil {
					return platform.ErrUnsupported
				}
				return zeroCD(ctx)
			}},
	}
}

// runMarketSnapshot collects ranked UU lease quotes per template.
func runMarketSnapshot(ctx context.Context, st *store.Store, fetch func(ctx context.Context, tplID int64, minP, maxP float64) ([]pricing.Quote, error), log *slog.Logger) error {
	tpls, err := st.TemplatesNeedingQuotes(ctx)
	if err != nil {
		return err
	}
	const defaultMax = 20000.0
	captured := 0
	var errs []error
	for _, tp := range tpls {
		minP := tp.MarkPrice
		maxP := tp.MarkPrice * 2
		if minP <= 0 {
			minP = 0
			maxP = defaultMax
		}
		quotes, err := fetch(ctx, tp.UUTemplateID, minP, maxP)
		if err != nil {
			log.Warn("quote fetch failed", "hash", tp.HashName, "err", err)
			continue
		}
		var snaps []store.Snapshot
		now := time.Now().UTC()
		for i, q := range quotes {
			rank := i + 1
			if q.Short > 0 {
				snaps = append(snaps, store.Snapshot{HashName: tp.HashName, Source: "uu_market", Kind: "lease_short", Rank: rank, Price: q.Short, CapturedAt: now})
			}
			if q.Long > 0 {
				snaps = append(snaps, store.Snapshot{HashName: tp.HashName, Source: "uu_market", Kind: "lease_long", Rank: rank, Price: q.Long, CapturedAt: now})
			}
			if q.Deposit > 0 {
				snaps = append(snaps, store.Snapshot{HashName: tp.HashName, Source: "uu_market", Kind: "deposit", Rank: rank, Price: q.Deposit, CapturedAt: now})
			}
		}
		if err := st.InsertSnapshots(ctx, snaps); err != nil {
			// 单模板落库失败不放弃本轮剩余模板；错误计入返回值供面板 LastError
			log.Error("snapshot insert failed", "hash", tp.HashName, "err", err)
			errs = append(errs, fmt.Errorf("snapshot %s: %w", tp.HashName, err))
			continue
		}
		captured += len(snaps)
	}
	log.Info("market snapshot done", "templates", len(tpls), "points", captured,
		"failed_templates", len(errs))
	return errors.Join(errs...)
}

// Order-sync lookback bounds (ADR-0003): the window must cover every order
// that can still turn terminal. A fixed 24h lookback silently dropped
// long-lease (≤90d) terminal states, so their income was never recorded.
const (
	orderSyncFloor     = 24 * time.Hour       // default coverage for brand-new orders
	orderSyncMargin    = 24 * time.Hour       // clock-skew / timezone safety around the anchor
	orderSyncMaxWindow = 100 * 24 * time.Hour // hard cap: > max rent term (90d) + margins
)

// orderSyncSince computes the orders_sync lookback anchor: the default floor,
// extended down to the earliest anchor that can prove an open order exists —
// either an unfinished lease_orders row, or a leased listing (an order cannot
// predate its listing, so this covers pre-existing leases before any
// lease_orders row has been synced; bootstrap gap fixed 2026-08-27). The
// result is capped at orderSyncMaxWindow so one stuck row cannot make the
// sync unbounded.
func (d *Deps) orderSyncSince(ctx context.Context) time.Time {
	now := time.Now()
	earliest, err := d.Store.EarliestOpenOrderStart(ctx)
	if err != nil {
		d.Log.Warn("order sync window anchor lookup failed; using default lookback", "err", err)
		earliest = nil
	}
	// Consulted regardless of whether lease_orders already has rows: the
	// bootstrap case is exactly when it has none (order rows can never enter
	// the DB if the window never reaches back to their start time).
	leased, lerr := d.Store.EarliestLeasedListingStart(ctx)
	if lerr != nil {
		d.Log.Warn("order sync leased-listing anchor lookup failed", "err", lerr)
		leased = nil
	}
	return orderSyncWindow(now, earliest, leased)
}

// orderSyncWindow is the pure anchor computation (unit-testable).
func orderSyncWindow(now time.Time, openStart, leasedStart *time.Time) time.Time {
	since := now.Add(-orderSyncFloor)
	for _, t := range []*time.Time{openStart, leasedStart} {
		if t == nil || t.IsZero() {
			continue
		}
		if lo := t.Add(-orderSyncMargin); lo.Before(since) {
			since = lo
		}
	}
	if floor := now.Add(-orderSyncMaxWindow); since.Before(floor) {
		since = floor
	}
	return since
}
