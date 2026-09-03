package store

import (
	"context"
	"fmt"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

// RoutableItem is one inventory asset eligible for shelf routing.
type RoutableItem struct {
	AssetID  string   `json:"asset_id"`
	HashName string   `json:"hash_name"`
	V        *float64 `json:"value_anchor"`
	Route    string   `json:"channel_route"`
}

// RoutableInventory returns in-stock tradable items joined with their
// effective strategy route (template override → global) and value anchor.
func (s *Store) RoutableInventory(ctx context.Context) ([]RoutableItem, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT i.asset_id, i.hash_name, t.value_anchor,
		        COALESCE(ts.channel_route, gs.channel_route, 'both')
		 FROM inventory_items i
		 JOIN templates t ON t.hash_name = i.hash_name AND t.blacklisted = false
		 LEFT JOIN strategies ts ON ts.scope='template' AND ts.hash_name = i.hash_name AND ts.enabled
		 LEFT JOIN LATERAL (
		   SELECT channel_route FROM strategies
		   WHERE scope='global' AND enabled ORDER BY priority DESC, id LIMIT 1
		 ) gs ON true
		 WHERE i.status IN ('in_stock') AND i.tradable`)
	if err != nil {
		return nil, fmt.Errorf("routable inventory: %w", err)
	}
	defer rows.Close()
	var out []RoutableItem
	for rows.Next() {
		var it RoutableItem
		var route string
		if err := rows.Scan(&it.AssetID, &it.HashName, &it.V, &route); err != nil {
			return nil, err
		}
		it.Route = route
		out = append(out, it)
	}
	return out, rows.Err()
}

type ActiveListing struct {
	ID       int64          `json:"id"`
	Channel  domain.Channel `json:"channel"`
	HashName string         `json:"hash_name"`
	GoodsRef string         `json:"goods_ref"`
	AssetID  string         `json:"asset_id"`
	State    string         `json:"state"`     // actual_state: active | leased
	SyncedAt time.Time      `json:"synced_at"` // last actual-state sync (grace anchor); zero = unknown
}

// AllActiveListings lists rows whose actual state is active/leased.
func (s *Store) AllActiveListings(ctx context.Context) ([]ActiveListing, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, channel, hash_name, goods_ref, asset_id, actual_state,
		        COALESCE(actual_synced_at, listed_at)
		 FROM listings
		 WHERE actual_state IN ('active','leased')`)
	if err != nil {
		return nil, fmt.Errorf("active listings: %w", err)
	}
	defer rows.Close()
	var out []ActiveListing
	for rows.Next() {
		var l ActiveListing
		if err := rows.Scan(&l.ID, &l.Channel, &l.HashName, &l.GoodsRef, &l.AssetID, &l.State, &l.SyncedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CountActiveListings returns how many rows are currently active/leased for
// the channel (empty-shelf breaker input).
func (s *Store) CountActiveListings(ctx context.Context, channel domain.Channel) (int64, error) {
	var n int64
	err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM listings
		 WHERE channel=$1 AND actual_state IN ('active','leased')`, channel).Scan(&n)
	return n, err
}

// RecordPublishedListing inserts/refreshes a listing row after a successful publish.
// sublet_applied: ECO publishes always carry the channel sublet policy
// (eco.applySubletPolicy), so a fresh ECO row starts as applied; UU rows keep
// the flag false (concept is ECO-specific).
func (s *Store) RecordPublishedListing(ctx context.Context, channel, channelID, hashName, goodsRef string, rent, long, deposit float64, days int) error {
	rent, long, deposit = round2Money(rent), round2Money(long), round2Money(deposit)
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO listings(channel, asset_id, hash_name, goods_ref, desired_state, actual_state,
		                      rent_price, long_rent_price, max_days, deposit, listed_at, actual_synced_at,
		                      sublet_applied)
		 VALUES($1,$2,$3,$4,'active','active',$5,NULLIF($6,0),$7,$8,now(),now(),$1='eco')
		 ON CONFLICT(channel, goods_ref) DO UPDATE SET
		   desired_state='active', actual_state='active',
		   rent_price=EXCLUDED.rent_price, long_rent_price=EXCLUDED.long_rent_price,
		   max_days=EXCLUDED.max_days, deposit=EXCLUDED.deposit, actual_synced_at=now(),
		   sublet_applied=$1='eco'`,
		channel, channelID, hashName, goodsRef, rent, long, days, deposit)
	return err
}

// MarkListingDelisted flips a row to delisted after successful removal.
func (s *Store) MarkListingDelisted(ctx context.Context, channel, goodsRef string) error {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE listings SET desired_state='delisted', actual_state='none', actual_synced_at=now()
		 WHERE channel=$1 AND goods_ref=$2`, channel, goodsRef)
	if err != nil {
		return err
	}
	_ = tag
	return nil
}
