package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/analytics"
	"github.com/3219378872/rent-auto/backend/internal/bench"
	"github.com/3219378872/rent-auto/backend/internal/platform"
	"github.com/3219378872/rent-auto/backend/internal/pricing"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

// Jobs builds the standard job set bound to live dependencies.
func Jobs(d Deps, adapters func() []platform.Adapter, uuQuotes func(ctx context.Context, tplID int64, minP, maxP float64) ([]pricing.Quote, error), ecoDump func(ctx context.Context) (map[string]float64, error), zeroCD func(ctx context.Context) error, reconcile func(ctx context.Context) error, log *slog.Logger) []Job {
	return []Job{
		{Name: "reprice", Kind: KindInterval, Every: 31 * time.Minute, Jitter: 90 * time.Second,
			Fn: func(ctx context.Context) error { return d.RunReprice(ctx, adapters()) }},
		{Name: "inventory_sync", Kind: KindInterval, Every: 30 * time.Minute, Jitter: 60 * time.Second,
			Fn: func(ctx context.Context) error {
				for _, ad := range adapters() {
					if _, err := bench.SyncInventory(ctx, ad, d.Store, log); err != nil {
						log.Error("inventory sync", "channel", string(ad.Channel()), "err", err)
					}
				}
				return nil
			}},
		{Name: "shelf_sync", Kind: KindInterval, Every: 10 * time.Minute, Jitter: 30 * time.Second,
			Fn: func(ctx context.Context) error {
				for _, ad := range adapters() {
					if _, err := bench.SyncShelf(ctx, ad, d.Store, log); err != nil {
						log.Error("shelf sync", "channel", string(ad.Channel()), "err", err)
					}
				}
				return nil
			}},
		{Name: "orders_sync", Kind: KindInterval, Every: 10 * time.Minute, Jitter: 30 * time.Second,
			Fn: func(ctx context.Context) error {
				since := time.Now().Add(-24 * time.Hour)
				for _, ad := range adapters() {
					if _, err := bench.SyncOrders(ctx, ad, d.Store, since, log); err != nil {
						log.Error("orders sync", "channel", string(ad.Channel()), "err", err)
					}
				}
				if _, err := analytics.RollupTerminalOrders(ctx, d.Store, log); err != nil {
					log.Error("income rollup", "err", err)
				}
				return nil
			}},
		{Name: "market_snapshot", Kind: KindInterval, Every: 20 * time.Minute, Jitter: 120 * time.Second,
			Fn: func(ctx context.Context) error { return runMarketSnapshot(ctx, d.Store, uuQuotes, log) }},
		{Name: "value_anchor", Kind: KindInterval, Every: time.Hour, Jitter: 5 * time.Minute,
			Fn: func(ctx context.Context) error {
				if ecoDump != nil {
					if prices, err := ecoDump(ctx); err == nil && len(prices) > 0 {
						n, err := d.Store.UpdateEcoRefPrices(ctx, prices)
						if err == nil {
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
			return err
		}
		captured += len(snaps)
	}
	log.Info("market snapshot done", "templates", len(tpls), "points", captured)
	return nil
}
