package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

// ShelfObservationTime must be obtained before making the remote request.
// Writes and observations share the database clock, not the application clock.
func (s *Store) ShelfObservationTime(ctx context.Context) (time.Time, error) {
	var at time.Time
	err := s.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&at)
	return at, err
}

// ApplyShelfSnapshot atomically applies a complete observation. It returns
// missing rows and the active-row count when the empty-shelf breaker fires.
func (s *Store) ApplyShelfSnapshot(ctx context.Context, ch domain.Channel, shelf []domain.ShelfListing, at time.Time) (missing, emptyActive int64, err error) {
	if !ch.Valid() || at.IsZero() {
		return 0, 0, fmt.Errorf("invalid shelf observation")
	}
	seen := make(map[string]bool, len(shelf))
	for _, l := range shelf {
		if l.Channel != ch || l.GoodsRef == "" || seen[l.GoodsRef] {
			return 0, 0, fmt.Errorf("invalid or duplicate shelf row for %s", ch)
		}
		seen[l.GoodsRef] = true
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "shelf:"+string(ch)); err != nil {
		return 0, 0, err
	}
	var previous time.Time
	err = tx.QueryRow(ctx, `SELECT observed_at FROM shelf_observations WHERE channel=$1`, ch).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, err
	}
	if !previous.IsZero() && !at.After(previous) {
		return 0, 0, nil // a newer complete snapshot already superseded this one
	}
	if len(shelf) == 0 {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM listings WHERE channel=$1 AND actual_state IN ('active','leased')`, ch).Scan(&emptyActive); err != nil {
			return 0, 0, err
		}
		if emptyActive > 0 {
			return 0, emptyActive, nil
		}
	}
	for _, l := range shelf {
		if l.HashName == "" {
			l.HashName = l.DisplayName
		}
		if err := upsertListingFromShelf(ctx, tx, l, &at); err != nil {
			return 0, 0, err
		}
	}
	missing, err = markMissingListings(ctx, tx, ch, seen, &at)
	if err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO shelf_observations(channel,observed_at) VALUES($1,$2)
	 ON CONFLICT(channel) DO UPDATE SET observed_at=excluded.observed_at`, ch, at); err != nil {
		return 0, 0, err
	}
	return missing, 0, tx.Commit(ctx)
}
