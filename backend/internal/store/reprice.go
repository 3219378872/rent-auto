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
	ListingID     int64          `json:"listing_id"`
	Channel       domain.Channel `json:"channel"`
	HashName      string         `json:"hash_name"`
	GoodsRef      string         `json:"goods_ref"`
	AssetID       string         `json:"asset_id"`
	RentPrice     float64        `json:"rent_price"`
	LongPrice     float64        `json:"long_rent_price"`
	Deposit       float64        `json:"deposit"`
	Factor        float64        `json:"factor"`
	V             *float64       `json:"value_anchor"`
	UUTemplateID  *int64         `json:"uu_template_id"`
	LastActionAt  *time.Time     `json:"last_action_at"`
	SubletApplied bool           `json:"sublet_applied"`
}

const repriceCols = `l.id, l.channel, l.hash_name, l.goods_ref, l.asset_id,
	l.rent_price, coalesce(l.long_rent_price,0), coalesce(l.deposit,0), coalesce(l.factor,1.0),
	t.value_anchor, t.uu_template_id,
	COALESCE(l.last_reprice_at, l.listed_at), l.sublet_applied`

func (s *Store) ListRepriceCandidates(ctx context.Context, channel domain.Channel) ([]RepriceCandidate, error) {
	// Leased listings are excluded: the asset is rented out, repricing the
	// shelf row mid-lease is at best a no-op and at worst a platform reject;
	// their price re-evaluation happens after the lease ends (recon never
	// delists leased rows either).
	rows, err := s.Pool.Query(ctx,
		`SELECT `+repriceCols+` FROM listings l JOIN templates t ON t.hash_name=l.hash_name
		 WHERE l.channel=$1 AND l.actual_state='active' AND t.blacklisted=false`,
		channel)
	if err != nil {
		return nil, fmt.Errorf("list reprice candidates: %w", err)
	}
	defer rows.Close()
	var out []RepriceCandidate
	for rows.Next() {
		var c RepriceCandidate
		if err := rows.Scan(&c.ListingID, &c.Channel, &c.HashName, &c.GoodsRef, &c.AssetID,
			&c.RentPrice, &c.LongPrice, &c.Deposit, &c.Factor, &c.V, &c.UUTemplateID,
			&c.LastActionAt, &c.SubletApplied); err != nil {
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
	// NOTE: actual_synced_at is deliberately NOT refreshed here. It anchors
	// the orphan/surplus grace window in recon (AllActiveListings.SyncedAt);
	// a reprice is not an actual-state sync, and refreshing it would stretch
	// the grace period on every price move and delay legitimate delists.
	_, err := s.Pool.Exec(ctx,
		`UPDATE listings SET rent_price=$2, long_rent_price=$3, deposit=$4, max_days=$5,
		        last_reprice_at=now() WHERE id=$1`,
		listingID, round2Money(d.Rent), nullIf(round2Money(d.Long)), round2Money(d.Deposit), d.Days)
	return err
}

// MarkListingSubletApplied records that a reprice/publish payload carrying the
// ECO sublet policy (SupportSublet=1 + SubletPricingMethod=2) was accepted by
// the platform for this listing; the one-shot noise-floor exemption ends here.
func (s *Store) MarkListingSubletApplied(ctx context.Context, listingID int64) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE listings SET sublet_applied=true WHERE id=$1`, listingID)
	return err
}

// SetListingFactor persists a controller factor and stamps the stale-anchor
// so stepped listings wait another full window before stepping again.
func (s *Store) SetListingFactor(ctx context.Context, listingID int64, factor float64) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE listings SET factor=$2, last_factor_event_at=now() WHERE id=$1`, listingID, factor)
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
	// Every money leg normalizes through round2Money before INSERT.
	round2MoneyPtr(pa.OldRent)
	round2MoneyPtr(pa.NewRent)
	round2MoneyPtr(pa.OldLong)
	round2MoneyPtr(pa.NewLong)
	round2MoneyPtr(pa.OldDeposit)
	round2MoneyPtr(pa.NewDeposit)
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
// uniq_global_strategy (migration 0007) makes the singleton contract real:
// without it, `ON CONFLICT DO NOTHING` had no constraint to target and every
// call inserted a duplicate 'default' row that shadowed the tuned params.
func (s *Store) EnsureGlobalStrategy(ctx context.Context, defaultParams string) (int64, string, error) {
	tag, err := s.Pool.Exec(ctx,
		`INSERT INTO strategies(name, scope, params) VALUES('default','global',$1::jsonb)
		 ON CONFLICT (scope) WHERE scope='global' DO NOTHING`, defaultParams)
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

// StrategyGlobalPatch carries optional field updates; nil fields stay unchanged.
type StrategyGlobalPatch struct {
	Params      []byte // raw JSON, validated by the caller
	Route       *string
	RealEnabled *bool
}

// UpdateGlobalStrategy applies every requested field change in ONE transaction.
// Strategies drive live repricing directly — a half-applied patch (new params
// with a stale real_execution_enabled) must be impossible.
func (s *Store) UpdateGlobalStrategy(ctx context.Context, id int64, p StrategyGlobalPatch) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin strategy tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if p.Params != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE strategies SET params=$2, updated_by='user', updated_at=now() WHERE id=$1`,
			id, p.Params); err != nil {
			return err
		}
	}
	if p.Route != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE strategies SET channel_route=$2, updated_by='user', updated_at=now() WHERE id=$1`,
			id, *p.Route); err != nil {
			return err
		}
	}
	if p.RealEnabled != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE strategies SET real_execution_enabled=$2, updated_by='user', updated_at=now() WHERE id=$1`,
			id, *p.RealEnabled); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type EffectiveStrategy struct {
	ID           int64
	Params       json.RawMessage
	GlobalParams json.RawMessage
	RealEnabled  bool
	Route        string
}

// TemplateStrategy is one template-scope strategy override payload.
type TemplateStrategy struct {
	HashName    string
	Route       string
	Params      json.RawMessage // partial object; deep-merges over global
	RealEnabled *bool           // nil = keep existing on update / false on insert
	Priority    int
}

// UpsertTemplateStrategy creates or replaces the template-scope override for
// one hash (uniq_template_strategy allows at most one row per hash).
// A brand-new override starts dry-run (real_execution_enabled=false) unless
// explicitly requested — AC-T1 applies per strategy row.
func (s *Store) UpsertTemplateStrategy(ctx context.Context, ts TemplateStrategy) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO strategies(name, scope, hash_name, channel_route, params, priority,
		                        real_execution_enabled, updated_by)
		 VALUES($1,'template',$2,$3,$4,$5,COALESCE($6,false),'user')
		 ON CONFLICT (scope, hash_name) WHERE scope='template' DO UPDATE SET
		   channel_route=EXCLUDED.channel_route,
		   params=EXCLUDED.params,
		   priority=EXCLUDED.priority,
		   real_execution_enabled=COALESCE($6, strategies.real_execution_enabled),
		   enabled=true,
		   updated_by='user', updated_at=now()
		 RETURNING id`,
		"tpl:"+ts.HashName, ts.HashName, ts.Route, []byte(ts.Params), ts.Priority, ts.RealEnabled).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert template strategy %s: %w", ts.HashName, err)
	}
	return id, nil
}

// DeleteTemplateStrategy removes a template-scope override; the hash falls
// back to the global strategy immediately.
func (s *Store) DeleteTemplateStrategy(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx,
		`DELETE FROM strategies WHERE id=$1 AND scope='template'`, id)
	if err != nil {
		return fmt.Errorf("delete template strategy %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetEffectiveStrategy(ctx context.Context, hash string) (*EffectiveStrategy, error) {
	row := s.Pool.QueryRow(ctx,
		`SELECT g.id,
		        g.params::text,
		        COALESCE(t.real_execution_enabled, g.real_execution_enabled),
		        COALESCE(t.channel_route, g.channel_route),
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
// MergedQuote is one market position (rank) carrying its latest price per kind.
type MergedQuote struct {
	Rank    int
	Short   float64 // lease_short; 0 when absent
	Long    float64 // lease_long;  0 when absent
	Deposit float64 // deposit;     0 when absent
}

// RecentMergedQuotes returns the top `limit` ranked market positions within the
// window, one entry per commodity rank. When overlapping capture batches exist,
// each (rank, kind) resolves to the newest sample so batches never double-count.
// This matches pricing-spec §2: quotes are a per-commodity ranked list where
// "first 10 LeaseUnitPrice" means the first 10 commodities offering a short price.
func (s *Store) RecentMergedQuotes(ctx context.Context, hash string, since time.Time, limit int) ([]MergedQuote, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT kind, rank, price FROM (
		   SELECT DISTINCT ON (rank, kind) kind, rank, price
		   FROM market_snapshots
		   WHERE hash_name=$1 AND source='uu_market' AND captured_at >= $2
		   ORDER BY rank ASC, kind ASC, captured_at DESC
		 ) t
		 ORDER BY rank ASC LIMIT $3`,
		hash, since, limit)
	if err != nil {
		return nil, fmt.Errorf("recent merged quotes: %w", err)
	}
	defer rows.Close()
	byRank := map[int]*MergedQuote{}
	var order []int
	for rows.Next() {
		var q QuoteRow
		if err := rows.Scan(&q.Kind, &q.Rank, &q.Price); err != nil {
			return nil, err
		}
		m, ok := byRank[q.Rank]
		if !ok {
			m = &MergedQuote{Rank: q.Rank}
			byRank[q.Rank] = m
			order = append(order, q.Rank)
		}
		switch q.Kind {
		case "lease_short":
			m.Short = q.Price
		case "lease_long":
			m.Long = q.Price
		case "deposit":
			m.Deposit = q.Price
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]MergedQuote, 0, len(order))
	for _, r := range order {
		out = append(out, *byRank[r])
	}
	return out, nil
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
		p = round2Money(p)
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
