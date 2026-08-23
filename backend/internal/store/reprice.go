package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

// ---- migration 0003 support: factor column lives on listings ----

// RepriceCandidate is one listing joined with its template benchmark state.
type RepriceCandidate struct {
	ListingID    int64          `json:"listing_id"`
	Channel      domain.Channel `json:"channel"`
	HashName     string         `json:"hash_name"`
	GoodsRef     string         `json:"goods_ref"`
	AssetID      string         `json:"asset_id"`
	RentPrice    float64        `json:"rent_price"`
	LongPrice    float64        `json:"long_rent_price"`
	Deposit      float64        `json:"deposit"`
	Factor       float64        `json:"factor"`
	V            *float64       `json:"value_anchor"`
	UUTemplateID *int64         `json:"uu_template_id"`
	LastActionAt *time.Time     `json:"last_action_at"`
}

const repriceCols = `l.id, l.channel, l.hash_name, l.goods_ref, l.asset_id,
	l.rent_price, coalesce(l.long_rent_price,0), coalesce(l.deposit,0), coalesce(l.factor,1.0),
	t.value_anchor, t.uu_template_id,
	COALESCE(l.last_reprice_at, l.listed_at)`

func (s *Store) ListRepriceCandidates(ctx context.Context, channel domain.Channel) ([]RepriceCandidate, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT `+repriceCols+` FROM listings l JOIN templates t ON t.hash_name=l.hash_name
		 WHERE l.channel=$1 AND l.actual_state IN ('active','leased') AND t.blacklisted=false`,
		channel)
	if err != nil {
		return nil, fmt.Errorf("list reprice candidates: %w", err)
	}
	defer rows.Close()
	var out []RepriceCandidate
	for rows.Next() {
		var c RepriceCandidate
		if err := rows.Scan(&c.ListingID, &c.Channel, &c.HashName, &c.GoodsRef, &c.AssetID,
			&c.RentPrice, &c.LongPrice, &c.Deposit, &c.Factor, &c.V, &c.UUTemplateID, &c.LastActionAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateListingDecision(ctx context.Context, listingID int64, d struct {
	Rent, Long, Deposit float64
	Days                int
}) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE listings SET rent_price=$2, long_rent_price=$3, deposit=$4, max_days=$5,
		        last_reprice_at=now(), actual_synced_at=now() WHERE id=$1`,
		listingID, d.Rent, nullIf(d.Long), d.Deposit, d.Days)
	return err
}

func (s *Store) SetListingFactor(ctx context.Context, listingID int64, factor float64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE listings SET factor=$2 WHERE id=$1`, listingID, factor)
	return err
}

// ---- price actions ----

type PriceAction struct {
	Channel                domain.Channel `json:"channel"`
	HashName               string         `json:"hash_name"`
	AssetID                string         `json:"asset_id"`
	ListingID              int64          `json:"listing_id"`
	Action                 string         `json:"action"` // publish|reprice|delist|skip
	OldRent, NewRent       *float64
	OldLong, NewLong       *float64
	OldDays, NewDays       *int
	OldDeposit, NewDeposit *float64
	Decision               json.RawMessage `json:"decision"`
	DryRun                 bool            `json:"dry_run"`
	Success                bool            `json:"success"`
	Error                  string          `json:"error,omitempty"`
}

func ptrF(v float64) *float64 { x := v; return &x }
func PtrF(v float64) *float64 { return ptrF(v) }

func (s *Store) InsertPriceAction(ctx context.Context, pa PriceAction) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO price_actions(channel, hash_name, asset_id, listing_id, action,
		   old_rent,new_rent, old_long,new_long, old_days,new_days, old_deposit,new_deposit,
		   decision, dry_run, success, error)
		 VALUES($1,$2,$3,NULLIF($4,0),$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,NULLIF($17,''))
		 RETURNING id`,
		pa.Channel, pa.HashName, pa.AssetID, pa.ListingID, pa.Action,
		pa.OldRent, pa.NewRent, pa.OldLong, pa.NewLong,
		pa.OldDays, pa.NewDays, pa.OldDeposit, pa.NewDeposit,
		pa.Decision, pa.DryRun, pa.Success, pa.Error).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert price action: %w", err)
	}
	return id, nil
}

func nullIf(v float64) any {
	if v == 0 {
		return nil
	}
	return v
}

// ---- strategies ----

// EnsureGlobalStrategy seeds the singleton global strategy if absent.
func (s *Store) EnsureGlobalStrategy(ctx context.Context, defaultParams string) (int64, string, error) {
	tag, err := s.Pool.Exec(ctx,
		`INSERT INTO strategies(name, scope, params) VALUES('default','global',$1::jsonb)
		 ON CONFLICT DO NOTHING`, defaultParams)
	if err != nil {
		return 0, "", err
	}
	_ = tag
	var id int64
	var params string
	err = s.Pool.QueryRow(ctx,
		`SELECT id, params::text FROM strategies WHERE scope='global' ORDER BY priority DESC, id LIMIT 1`).
		Scan(&id, &params)
	return id, params, err
}

type EffectiveStrategy struct {
	ID           int64
	Params       json.RawMessage
	GlobalParams json.RawMessage
	RealEnabled  bool
	Route        string
}

func (s *Store) GetEffectiveStrategy(ctx context.Context, hash string) (*EffectiveStrategy, error) {
	row := s.Pool.QueryRow(ctx,
		`SELECT g.id, g.params::text, g.real_execution_enabled, g.channel_route,
		        COALESCE(t.params::text,'')
		 FROM strategies g
		 LEFT JOIN LATERAL (
		   SELECT params, real_execution_enabled, channel_route FROM strategies
		   WHERE scope='template' AND hash_name=$1 AND enabled
		   ORDER BY priority DESC LIMIT 1) t ON true
		 WHERE g.scope='global' AND g.enabled`, hash)
	var es EffectiveStrategy
	var tplParams string
	if err := row.Scan(&es.ID, &es.GlobalParams, &es.RealEnabled, &es.Route, &tplParams); err != nil {
		return nil, fmt.Errorf("effective strategy: %w", err)
	}
	if tplParams != "" {
		es.Params = json.RawMessage(tplParams)
	}
	return &es, nil
}

// ---- market quotes for baseline ----

// RecentQuotes returns ranked lease quotes captured since cutoff for one hash.
func (s *Store) RecentQuotes(ctx context.Context, hash string, since time.Time, limit int) ([]QuoteRow, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT kind, rank, price FROM market_snapshots
		 WHERE hash_name=$1 AND source='uu_market' AND captured_at >= $2
		 ORDER BY rank ASC, captured_at DESC LIMIT $3`,
		hash, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QuoteRow
	for rows.Next() {
		var q QuoteRow
		if err := rows.Scan(&q.Kind, &q.Rank, &q.Price); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

type QuoteRow struct {
	Kind  string
	Rank  int
	Price float64
}

// TemplatesNeedingQuotes lists active UU-mapped templates for snapshot collection.
func (s *Store) TemplatesNeedingQuotes(ctx context.Context) ([]struct {
	HashName     string
	UUTemplateID int64
	MarkPrice    float64
}, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT hash_name, uu_template_id, coalesce(uu_mark_price,0)
		 FROM templates WHERE uu_template_id IS NOT NULL AND blacklisted=false`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		HashName     string
		UUTemplateID int64
		MarkPrice    float64
	}
	for rows.Next() {
		var r struct {
			HashName     string
			UUTemplateID int64
			MarkPrice    float64
		}
		if err := rows.Scan(&r.HashName, &r.UUTemplateID, &r.MarkPrice); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateEcoRefPrices bulk-applies the market dump to templates.
func (s *Store) UpdateEcoRefPrices(ctx context.Context, prices map[string]float64) (int64, error) {
	var n int64
	for h, p := range prices {
		tag, err := s.Pool.Exec(ctx,
			`UPDATE templates SET eco_ref_price=$2, updated_at=now()
			 WHERE hash_name=$1 AND (eco_ref_price IS NULL OR eco_ref_price <> $2)`, h, p)
		if err != nil {
			return n, err
		}
		n += tag.RowsAffected()
	}
	return n, nil
}
