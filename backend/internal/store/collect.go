package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

// ---- listings ----

func (s *Store) UpsertListingFromShelf(ctx context.Context, l domain.ShelfListing) error {
	// ensure template exists (shelf rows carry display names)
	if err := s.UpsertTemplate(ctx, Template{
		HashName: l.HashName, DisplayName: l.DisplayName,
		UUTemplateID: nilIfZero(l.TemplateID),
	}); err != nil {
		return err
	}
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO listings(channel, asset_id, hash_name, goods_ref, desired_state, actual_state,
		                      rent_price, long_rent_price, max_days, deposit, listed_at, actual_synced_at)
		 VALUES($1,$2,$3,$4,'active',
		        CASE WHEN $9 THEN 'leased' ELSE 'active' END,
		        $5,$6,$7,$8,$10,now())
		 ON CONFLICT(channel, goods_ref) DO UPDATE SET
		   asset_id=EXCLUDED.asset_id, hash_name=EXCLUDED.hash_name,
		   actual_state=CASE WHEN $9 THEN 'leased' ELSE 'active' END,
		   rent_price=EXCLUDED.rent_price, long_rent_price=EXCLUDED.long_rent_price,
		   max_days=EXCLUDED.max_days, deposit=EXCLUDED.deposit,
		   listed_at=COALESCE(listings.listed_at, EXCLUDED.listed_at),
		   actual_synced_at=now()`,
		l.Channel, l.AssetID, l.HashName, l.GoodsRef,
		l.RentPrice, l.LongRentPrice, l.MaxDays, l.Deposit, l.Leased, nullTime(l.ListedAt))
	if err != nil {
		return fmt.Errorf("upsert listing %s/%s: %w", l.Channel, l.GoodsRef, err)
	}
	return nil
}

func nilIfZero(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

// MarkMissingListings flips actual_state to 'stale'/'none' for channel rows not seen in sync.
func (s *Store) MarkMissingListings(ctx context.Context, channel domain.Channel, seenRefs map[string]bool) (int64, error) {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE listings SET actual_state='none'
		 WHERE channel=$1 AND actual_state IN ('active','leased')
		   AND NOT (goods_ref = ANY($2))`,
		channel, refsToArray(seenRefs))
	if err != nil {
		return 0, fmt.Errorf("mark missing listings: %w", err)
	}
	return tag.RowsAffected(), nil
}

func refsToArray(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	if out == nil {
		out = []string{""}
	}
	return out
}

type ListingFilter struct {
	Channel domain.Channel
	State   string
	Limit   int
	Offset  int
}

type ListingRow struct {
	ID            int64          `json:"id"`
	Channel       domain.Channel `json:"channel"`
	AssetID       string         `json:"asset_id"`
	HashName      string         `json:"hash_name"`
	GoodsRef      string         `json:"goods_ref"`
	DesiredState  string         `json:"desired_state"`
	ActualState   string         `json:"actual_state"`
	RentPrice     float64        `json:"rent_price"`
	LongRentPrice float64        `json:"long_rent_price"`
	MaxDays       int            `json:"max_days"`
	Deposit       float64        `json:"deposit"`
	ListedAt      *time.Time     `json:"listed_at"`
	LastRepriceAt *time.Time     `json:"last_reprice_at"`
}

func (s *Store) ListListings(ctx context.Context, f ListingFilter) ([]ListingRow, int, error) {
	limit, offset := normalizePage(f.Limit, f.Offset)
	where := "WHERE true"
	args := []any{}
	if f.Channel.Valid() && f.Channel != "" {
		args = append(args, string(f.Channel))
		where += fmt.Sprintf(" AND channel=$%d", len(args))
	}
	if f.State != "" {
		args = append(args, f.State)
		where += fmt.Sprintf(" AND actual_state=$%d", len(args))
	}
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM listings "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT id, channel, asset_id, hash_name, goods_ref, desired_state, actual_state,
	             rent_price, long_rent_price, max_days, deposit, listed_at, last_reprice_at
	      FROM listings ` + where + fmt.Sprintf(" ORDER BY id DESC LIMIT %d OFFSET %d", limit, offset)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]ListingRow, 0, limit)
	for rows.Next() {
		var r ListingRow
		if err := rows.Scan(&r.ID, &r.Channel, &r.AssetID, &r.HashName, &r.GoodsRef,
			&r.DesiredState, &r.ActualState, &r.RentPrice, &r.LongRentPrice,
			&r.MaxDays, &r.Deposit, &r.ListedAt, &r.LastRepriceAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// ---- market snapshots ----

type Snapshot struct {
	HashName   string
	Source     string // uu_market|eco_dump|own_order
	Kind       string // lease_short|lease_long|deposit|sell
	Rank       int
	Price      float64
	CapturedAt time.Time
}

func (s *Store) InsertSnapshots(ctx context.Context, snaps []Snapshot) error {
	if len(snaps) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, sn := range snaps {
		batch.Queue(
			`INSERT INTO market_snapshots(hash_name, source, kind, rank, price, captured_at)
			 VALUES($1,$2,$3,$4,$5,$6)`,
			sn.HashName, sn.Source, sn.Kind, sn.Rank, sn.Price, sn.CapturedAt)
	}
	if err := s.Pool.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("insert snapshots: %w", err)
	}
	return nil
}

// ---- fund flows ----

type FundFlow struct {
	Channel    domain.Channel `json:"channel"`
	FlowRef    string         `json:"flow_ref"`
	Amount     float64        `json:"amount"`
	Type       string         `json:"type"`
	OccurredAt time.Time      `json:"occurred_at"`
}

func (s *Store) UpsertFundFlow(ctx context.Context, f FundFlow) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO fund_flows(channel, flow_ref, amount, type, occurred_at)
		 VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT(channel, flow_ref) DO NOTHING`,
		f.Channel, f.FlowRef, f.Amount, f.Type, f.OccurredAt)
	return err
}

// WalletBalance returns the latest known wallet amount per channel from flows.
func (s *Store) WalletBalance(ctx context.Context, channel domain.Channel) (float64, error) {
	var bal float64
	err := s.Pool.QueryRow(ctx,
		`SELECT amount FROM fund_flows WHERE channel=$1 AND type='wallet_snapshot'
		 ORDER BY occurred_at DESC LIMIT 1`, channel).Scan(&bal)
	if err == pgx.ErrNoRows {
		return 0, ErrNotFound
	}
	return bal, err
}
