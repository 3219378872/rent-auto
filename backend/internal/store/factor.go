package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

// ---- feedback-controller state (pricing-spec §3) ----

// FactorOrder is one terminal rental order mapped onto its listing, awaiting
// factor folding. Each order has one persisted listing identity.
type FactorOrder struct {
	OrderID   int64
	ListingID int64
	Status    string // done | bought_out
	RentDays  int
	MaxDays   int
	HashName  string
	UpdatedAt time.Time
}

// bindFactorOrdersSQL deliberately requires one candidate, never the newest.
// Without an order start, first observation only excludes later publications;
// it is not a substitute rental timestamp, so retired_at cannot disambiguate.
const bindFactorOrdersSQL = `WITH candidates AS (
 SELECT o.id, o.updated_at, min(l.id) AS listing_id FROM lease_orders o
 JOIN listings l ON l.channel=o.channel AND o.asset_id<>'' AND l.asset_id=o.asset_id
   AND (o.hash_name='' OR l.hash_name=o.hash_name)
   AND (l.listed_at IS NULL OR l.listed_at<=COALESCE(o.started_at,o.factor_observed_at))
   AND (o.started_at IS NULL OR l.retired_at IS NULL OR o.started_at<l.retired_at)
 WHERE NOT o.factor_applied AND o.factor_listing_id IS NULL
   AND ($1::bigint IS NULL OR o.id=$1)
   AND ($2::timestamptz IS NULL OR (o.status IN ('done','bought_out')
        AND COALESCE(o.finished_at,o.due_at,o.updated_at)>=$2))
 GROUP BY o.id HAVING count(*)=1
) UPDATE lease_orders o SET factor_listing_id=c.listing_id FROM candidates c
 WHERE o.id=c.id AND NOT o.factor_applied AND o.factor_listing_id IS NULL
   AND o.updated_at=c.updated_at`

// UnhandledFactorOrders lists terminal orders not yet folded into a factor,
// finished within the lookback window.
func (s *Store) UnhandledFactorOrders(ctx context.Context, since time.Time, limit int) ([]FactorOrder, error) {
	// Orders can arrive before their shelf row. Retry unbound identities before
	// limiting executable work, so ambiguous old orders cannot starve a batch.
	if _, err := s.Pool.Exec(ctx, bindFactorOrdersSQL, nil, since); err != nil {
		return nil, fmt.Errorf("bind pending factor orders: %w", err)
	}
	rows, err := s.Pool.Query(ctx,
		`SELECT o.id, l.id, o.status, o.rent_days, COALESCE(l.max_days,0), o.hash_name, o.updated_at
		 FROM lease_orders o
		 JOIN listings l ON l.id=o.factor_listing_id AND l.channel=o.channel AND l.asset_id=o.asset_id
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
		if err := rows.Scan(&f.OrderID, &f.ListingID, &f.Status, &f.RentDays, &f.MaxDays, &f.HashName, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// UnresolvedFactorOrders counts eligible events lacking a unique identity.
// They remain replayable when missing shelf/order information is repaired.
func (s *Store) UnresolvedFactorOrders(ctx context.Context, since time.Time) (int64, error) {
	var n int64
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM lease_orders
	 WHERE status IN ('done','bought_out') AND NOT factor_applied AND factor_listing_id IS NULL
	 AND COALESCE(finished_at,due_at,updated_at)>=$1`, since).Scan(&n)
	return n, err
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

// FactorFold is one computed controller step awaiting atomic application.
// Multiple orders folding into the same listing collapse into a single fold
// carrying the sequentially-accumulated factor.
type FactorFold struct {
	ListingID int64
	Factor    float64
}

// ErrFactorOrdersChanged rejects feedback planned before an order correction.
var ErrFactorOrdersChanged = errors.New("factor order changed during planning; retry required")

// ApplyFactorFolds persists per-listing factors and flips the orders'
// factor_applied markers inside ONE transaction. A crash between folding and
// marking would otherwise replay already-folded orders on the next pass and
// double-step the factor (e.g. repeated +3% on chained rentals).
func (s *Store) ApplyFactorFolds(ctx context.Context, folds []FactorFold, orders []FactorOrder) error {
	if len(folds) == 0 && len(orders) == 0 {
		return nil
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin factor tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Re-sync can correct an order's identity/terms while factors are computed.
	// Lock and verify the exact order snapshot before changing any listing.
	sorted := append([]FactorOrder(nil), orders...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].OrderID < sorted[j].OrderID })
	orderIDs := make([]int64, 0, len(sorted))
	for _, o := range sorted {
		var matches bool
		if err := tx.QueryRow(ctx, `SELECT COALESCE(NOT factor_applied AND factor_listing_id=$2 AND updated_at=$3,false)
		 FROM lease_orders WHERE id=$1 FOR UPDATE`, o.OrderID, o.ListingID, o.UpdatedAt).Scan(&matches); err != nil {
			return fmt.Errorf("check factor order: %w", err)
		}
		if !matches {
			return ErrFactorOrdersChanged
		}
		orderIDs = append(orderIDs, o.OrderID)
	}
	for _, f := range folds {
		if _, err := tx.Exec(ctx,
			`UPDATE listings SET factor=$2, last_factor_event_at=now() WHERE id=$1`,
			f.ListingID, f.Factor); err != nil {
			return fmt.Errorf("apply factor to listing %d: %w", f.ListingID, err)
		}
	}
	if len(orderIDs) > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE lease_orders SET factor_applied=true WHERE id = ANY($1)`, orderIDs); err != nil {
			return fmt.Errorf("mark factor applied: %w", err)
		}
	}
	return tx.Commit(ctx)
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
