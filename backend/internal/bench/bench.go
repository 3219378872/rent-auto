// Package bench implements the cross-platform price benchmark center:
// template registry normalization and the value anchor V.
package bench

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/3219378872/rent-auto/backend/internal/platform"
	"github.com/3219378872/rent-auto/backend/internal/store"
)

// RecomputeAnchors recalculates value anchors for all templates.
// V = median(non-null uu_mark_price, eco_ref_price) — see pricing-spec §1.
func RecomputeAnchors(ctx context.Context, st *store.Store) (int64, error) {
	return st.RecomputeAnchors(ctx)
}

// SyncInventory pulls channel inventory and upserts items + templates.
func SyncInventory(ctx context.Context, ad platform.Adapter, st *store.Store, log *slog.Logger) (int, error) {
	items, err := ad.Inventory(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		if it.HashName == "" {
			continue
		}
		tpl := store.Template{HashName: it.HashName, DisplayName: it.DisplayName}
		switch ad.Channel() {
		case domain.ChannelUU:
			tpl.UUTemplateID = nilIfZero64(it.TemplateID)
			tpl.UUMarkPrice = pricePtr(it.MarkPrice)
		case domain.ChannelECO:
			tpl.EcoRefPrice = pricePtr(it.MarkPrice)
		}
		if err := st.UpsertTemplate(ctx, tpl); err != nil {
			return n, err
		}
		if err := st.UpsertInventoryItem(ctx, it, nil); err != nil {
			return n, err
		}
		n++
	}
	log.Info("inventory synced", "channel", string(ad.Channel()), "items", n)
	return n, nil
}

func nilIfZero64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func pricePtr(v float64) *float64 {
	if v <= 0 {
		return nil
	}
	p := Round2(v)
	return &p
}

// SyncShelf pulls the channel lease shelf into listings (actual state).
//
// Empty-shelf circuit breaker: a SUCCESSFUL empty payload while the DB still
// tracks active listings for the channel is almost always an upstream anomaly
// (risk-control soft block, partial API outage) — blindly marking everything
// missing cascades into reconcile re-publishing the entire shelf next cycle.
// Such cycles are skipped; genuine disappearances are caught by any later
// non-empty sync, which prunes by seen-refs as usual.
func SyncShelf(ctx context.Context, ad platform.Adapter, st *store.Store, log *slog.Logger) (int, error) {
	shelf, err := ad.LeaseShelf(ctx)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]bool, len(shelf))
	for _, l := range shelf {
		if l.HashName == "" {
			l.HashName = l.DisplayName
		}
		if err := st.UpsertListingFromShelf(ctx, l); err != nil {
			return len(seen), err
		}
		seen[l.GoodsRef] = true
	}
	if len(seen) == 0 {
		if active, cerr := st.CountActiveListings(ctx, ad.Channel()); cerr == nil && active > 0 {
			log.Warn("empty shelf ignored by breaker",
				"channel", string(ad.Channel()), "active_listings", active)
			return 0, nil
		}
	}
	missing, err := st.MarkMissingListings(ctx, ad.Channel(), seen)
	if err != nil {
		return len(seen), err
	}
	if missing > 0 {
		log.Info("shelf sync marked missing listings", "channel", string(ad.Channel()), "missing", missing)
	}
	return len(seen), nil
}

// SyncOrders pulls recent rental orders into lease_orders.
func SyncOrders(ctx context.Context, ad platform.Adapter, st *store.Store, since time.Time, log *slog.Logger) (int, error) {
	orders, err := ad.LeaseOrders(ctx, since)
	if err != nil {
		return 0, err
	}
	for i := range orders {
		o := orders[i]
		if o.HashName == "" && o.AssetID == "" {
			continue
		}
		if o.HashName != "" {
			if err := st.UpsertTemplate(ctx, store.Template{HashName: o.HashName}); err != nil {
				return i, err
			}
		}
		if err := st.UpsertLeaseOrder(ctx, o); err != nil {
			return i, err
		}
	}
	log.Info("orders synced", "channel", string(ad.Channel()), "orders", len(orders))
	return len(orders), nil
}

// Median computes the median of a float slice (copy-safe).
func Median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

// Round2 rounds money to two decimals (half away from zero).
func Round2(v float64) float64 {
	if v >= 0 {
		return float64(int64(v*100+0.5)) / 100
	}
	return float64(int64(v*100-0.5)) / 100
}
