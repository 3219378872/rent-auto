package store

import (
	"context"
	"fmt"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

// ---- feedback-controller state (pricing-spec §3) ----

// FactorOrder is one terminal rental order mapped onto its listing, awaiting
// factor folding. Orders only map when the same channel+asset has a listing.
type FactorOrder struct {
	OrderID   int64
	ListingID int64
	Status    string // done | bought_out
	RentDays  int
	MaxDays   int
	HashName  string
}

// UnhandledFactorOrders lists terminal orders not yet folded into a factor,
// finished within the lookback window.
func (s *Store) UnhandledFactorOrders(ctx context.Context, since time.Time, limit int) ([]FactorOrder, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT o.id, l.id, o.status, o.rent_days, COALESCE(l.max_days,0), o.hash_name
		 FROM lease_orders o
		 JOIN listings l ON l.channel=o.channel AND l.asset_id=o.asset_id
		 WHERE o.status IN ('done','bought_out') AND NOT o.factor_applied
		   AND COALESCE(o.finished_at, o.due_at, o.updated_at) >= $1
		 ORDER BY o.id LIMIT $2`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("unhandled factor orders: %w", err)
	}
	defer rows.Close()
	var out []FactorOrder
	for rows.Next() {
		var f FactorOrder
		if err := rows.Scan(&f.OrderID, &f.ListingID, &f.Status, &f.RentDays, &f.MaxDays, &f.HashName); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// MarkFactorApplied flips the fold marker after successful factor updates.
func (s *Store) MarkFactorApplied(ctx context.Context, orderIDs []int64) error {
	if len(orderIDs) == 0 {
		return nil
	}
	_, err := s.Pool.Exec(ctx,
		`UPDATE lease_orders SET factor_applied=true WHERE id = ANY($1)`, orderIDs)
	return err
}

// StaleCandidate is an active listing with its factor and the age of its last
// controller event anchor (last_factor_event_at → last_reprice_at → listed_at).
type StaleCandidate struct {
	ListingID int64
	Channel   domain.Channel
	HashName  string
	Factor    float64
	AnchorAge time.Duration
}

// FactorStaleCandidates lists active listings that have not seen any pricing
// activity for at least minAge — candidates for stale-day step-downs.
func (s *Store) FactorStaleCandidates(ctx context.Context, minAge time.Duration) ([]StaleCandidate, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, channel, hash_name, COALESCE(factor,1.0),
		        now()-COALESCE(last_factor_event_at, last_reprice_at, listed_at)
		 FROM listings
		 WHERE actual_state='active'
		   AND COALESCE(last_factor_event_at, last_reprice_at, listed_at) <= now() - $1::interval`,
		formatInterval(minAge))
	if err != nil {
		return nil, fmt.Errorf("factor stale candidates: %w", err)
	}
	defer rows.Close()
	var out []StaleCandidate
	for rows.Next() {
		var c StaleCandidate
		var age time.Duration
		if err := rows.Scan(&c.ListingID, &c.Channel, &c.HashName, &c.Factor, &age); err != nil {
			return nil, err
		}
		c.AnchorAge = age
		out = append(out, c)
	}
	return out, rows.Err()
}

// formatInterval renders a duration as a Postgres interval literal.
func formatInterval(d time.Duration) string {
	return fmt.Sprintf("%.0f seconds", d.Seconds())
}
