package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

// ---- templates ----

type Template struct {
	HashName      string     `json:"hash_name"`
	DisplayName   string     `json:"display_name"`
	Category      string     `json:"category"`
	UUTemplateID  *int64     `json:"uu_template_id"`
	UUMarkPrice   *float64   `json:"uu_mark_price"`
	EcoRefPrice   *float64   `json:"eco_ref_price"`
	ValueAnchor   *float64   `json:"value_anchor"`
	Blacklisted   bool       `json:"blacklisted"`
	AnchorUpdated *time.Time `json:"anchor_updated_at,omitempty"`
}

func (s *Store) UpsertTemplate(ctx context.Context, t Template) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO templates(hash_name, display_name, category, uu_template_id, uu_mark_price, eco_ref_price)
		 VALUES($1,$2,$3,$4,$5,$6)
		 ON CONFLICT(hash_name) DO UPDATE SET
		   display_name = CASE WHEN excluded.display_name <> '' THEN excluded.display_name ELSE templates.display_name END,
		   category     = CASE WHEN excluded.category <> '' THEN excluded.category ELSE templates.category END,
		   uu_template_id = COALESCE(excluded.uu_template_id, templates.uu_template_id),
		   uu_mark_price  = COALESCE(excluded.uu_mark_price, templates.uu_mark_price),
		   eco_ref_price  = COALESCE(excluded.eco_ref_price, templates.eco_ref_price),
		   updated_at = now()`,
		t.HashName, t.DisplayName, t.Category, t.UUTemplateID, t.UUMarkPrice, t.EcoRefPrice)
	if err != nil {
		return fmt.Errorf("upsert template %s: %w", t.HashName, err)
	}
	return nil
}

func (s *Store) GetTemplate(ctx context.Context, hash string) (*Template, error) {
	row := s.Pool.QueryRow(ctx,
		`SELECT hash_name, display_name, category, uu_template_id, uu_mark_price, eco_ref_price, value_anchor, blacklisted, anchor_updated_at
		 FROM templates WHERE hash_name=$1`, hash)
	return scanTemplate(row)
}

func (s *Store) ListTemplates(ctx context.Context) ([]Template, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT hash_name, display_name, category, uu_template_id, uu_mark_price, eco_ref_price, value_anchor, blacklisted, anchor_updated_at
		 FROM templates ORDER BY hash_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Template
	for rows.Next() {
		t, err := scanTemplateRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// RecomputeAnchors sets value_anchor = median(non-null uu_mark_price, eco_ref_price).
func (s *Store) RecomputeAnchors(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE templates SET
		   value_anchor = CASE
		     WHEN uu_mark_price IS NOT NULL AND eco_ref_price IS NOT NULL THEN LEAST(uu_mark_price, eco_ref_price) + ABS(uu_mark_price - eco_ref_price)/2
		     ELSE COALESCE(uu_mark_price, eco_ref_price) END,
		   anchor_updated_at = now()
		 WHERE (uu_mark_price IS NOT NULL OR eco_ref_price IS NOT NULL)
		   AND blacklisted = false`)
	if err != nil {
		return 0, fmt.Errorf("recompute anchors: %w", err)
	}
	return tag.RowsAffected(), nil
}

type rowIface interface{ Scan(dest ...any) error }

func scanTemplate(row rowIface) (*Template, error) {
	t := &Template{}
	err := row.Scan(&t.HashName, &t.DisplayName, &t.Category, &t.UUTemplateID,
		&t.UUMarkPrice, &t.EcoRefPrice, &t.ValueAnchor, &t.Blacklisted, &t.AnchorUpdated)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

func scanTemplateRows(rows pgx.Rows) (*Template, error) {
	t := &Template{}
	err := rows.Scan(&t.HashName, &t.DisplayName, &t.Category, &t.UUTemplateID,
		&t.UUMarkPrice, &t.EcoRefPrice, &t.ValueAnchor, &t.Blacklisted, &t.AnchorUpdated)
	return t, err
}

// ---- inventory ----

type InventoryFilter struct {
	Channel domain.Channel
	Status  string
	Search  string
	Limit   int
	Offset  int
}

func (s *Store) UpsertInventoryItem(ctx context.Context, it domain.InventoryItem, costBasis *float64) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO inventory_items(channel, asset_id, hash_name, market_hash_name, template_id, mark_price, tradable, status, cost_basis, last_synced_at)
		 VALUES($1,$2,$3,$4,NULLIF($5,0)::bigint,$6,$7,$8,$9,now())
		 ON CONFLICT(channel, asset_id) DO UPDATE SET
		   hash_name=EXCLUDED.hash_name, market_hash_name=EXCLUDED.market_hash_name,
		   template_id=EXCLUDED.template_id, mark_price=EXCLUDED.mark_price,
		   tradable=EXCLUDED.tradable, status=EXCLUDED.status, last_synced_at=now()`,
		it.Channel, it.AssetID, it.HashName, it.DisplayName, it.TemplateID, it.MarkPrice, it.Tradable, it.Status, costBasis)
	if err != nil {
		return fmt.Errorf("upsert inventory %s/%s: %w", it.Channel, it.AssetID, err)
	}
	return nil
}

func (s *Store) SetCostBasis(ctx context.Context, channel domain.Channel, assetID string, cost float64) error {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE inventory_items SET cost_basis=$3, cost_source='manual' WHERE channel=$1 AND asset_id=$2`,
		channel, assetID, cost)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const invCols = `id, channel, asset_id, hash_name, market_hash_name, template_id, mark_price, tradable, status, coalesce(cost_basis,0)`

type InventoryRow struct {
	ID         int64          `json:"id"`
	Channel    domain.Channel `json:"channel"`
	AssetID    string         `json:"asset_id"`
	HashName   string         `json:"hash_name"`
	MarketHash string         `json:"market_hash_name"`
	TemplateID *int64         `json:"template_id"`
	MarkPrice  float64        `json:"mark_price"`
	Tradable   bool           `json:"tradable"`
	Status     string         `json:"status"`
	CostBasis  float64        `json:"cost_basis"`
}

func (s *Store) ListInventory(ctx context.Context, f InventoryFilter) ([]InventoryRow, int, error) {
	limit, offset := normalizePage(f.Limit, f.Offset)
	where := "WHERE true"
	args := []any{}
	if f.Channel.Valid() && f.Channel != "" {
		args = append(args, string(f.Channel))
		where += fmt.Sprintf(" AND channel=$%d", len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND status=$%d", len(args))
	}
	if f.Search != "" {
		args = append(args, "%"+f.Search+"%")
		where += fmt.Sprintf(" AND market_hash_name ILIKE $%d", len(args))
	}
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM inventory_items "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := "SELECT " + invCols + " FROM inventory_items " + where +
		fmt.Sprintf(" ORDER BY id LIMIT %d OFFSET %d", limit, offset)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]InventoryRow, 0, limit)
	for rows.Next() {
		var r InventoryRow
		if err := rows.Scan(&r.ID, &r.Channel, &r.AssetID, &r.HashName, &r.MarketHash, &r.TemplateID, &r.MarkPrice, &r.Tradable, &r.Status, &r.CostBasis); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func normalizePage(limit, offset int) (int, int) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// ---- lease orders ----

func (s *Store) UpsertLeaseOrder(ctx context.Context, o domain.LeaseOrder) error {
	finished := isTerminal(o.Status)
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO lease_orders(channel, order_ref, asset_id, hash_name, order_type, status, rent_days, rent_price, order_amount, deposits, started_at, due_at, finished_at, income_recorded, raw, updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,now())
		 ON CONFLICT(channel, order_ref) DO UPDATE SET
		   status=EXCLUDED.status, order_type=EXCLUDED.order_type,
		   rent_days=EXCLUDED.rent_days, rent_price=EXCLUDED.rent_price,
		   order_amount=EXCLUDED.order_amount, deposits=EXCLUDED.deposits,
		   finished_at=COALESCE(lease_orders.finished_at, EXCLUDED.finished_at),
		   income_recorded = lease_orders.income_recorded OR $14,
		   raw=COALESCE(EXCLUDED.raw, lease_orders.raw), updated_at=now()`,
		o.Channel, o.OrderRef, o.AssetID, o.HashName, o.OrderType, o.Status,
		o.RentDays, o.RentPrice, o.Amount, o.Deposits,
		nullTime(o.StartedAt), nullTime(o.DueAt), nullTimePtr(finishedAt(o, finished)),
		isTerminal(o.Status), o.Raw)
	if err != nil {
		return fmt.Errorf("upsert order %s/%s: %w", o.Channel, o.OrderRef, err)
	}
	return nil
}

func isTerminal(status string) bool {
	switch status {
	case "done", "bought_out", "cancelled", "breach":
		return true
	}
	return false
}

func finishedAt(o domain.LeaseOrder, terminal bool) *time.Time {
	if !terminal {
		return nil
	}
	if o.DueAt.IsZero() {
		t := time.Now().UTC()
		return &t
	}
	t := o.DueAt
	return &t
}

func nullTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

type OrderFilter struct {
	Channel domain.Channel
	Status  string
	Limit   int
	Offset  int
}

type OrderRow struct {
	ID         int64          `json:"id"`
	Channel    domain.Channel `json:"channel"`
	OrderRef   string         `json:"order_ref"`
	HashName   string         `json:"hash_name"`
	OrderType  string         `json:"order_type"`
	Status     string         `json:"status"`
	RentDays   int            `json:"rent_days"`
	RentPrice  float64        `json:"rent_price"`
	Amount     float64        `json:"order_amount"`
	Deposits   float64        `json:"deposits"`
	StartedAt  *time.Time     `json:"started_at"`
	DueAt      *time.Time     `json:"due_at"`
	FinishedAt *time.Time     `json:"finished_at"`
}

func (s *Store) ListOrders(ctx context.Context, f OrderFilter) ([]OrderRow, int, error) {
	limit, offset := normalizePage(f.Limit, f.Offset)
	where := "WHERE true"
	args := []any{}
	if f.Channel.Valid() && f.Channel != "" {
		args = append(args, string(f.Channel))
		where += fmt.Sprintf(" AND channel=$%d", len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND status=$%d", len(args))
	}
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM lease_orders "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT id, channel, order_ref, hash_name, order_type, status, rent_days,
	             rent_price, order_amount, deposits, started_at, due_at, finished_at
	      FROM lease_orders ` + where + fmt.Sprintf(" ORDER BY id DESC LIMIT %d OFFSET %d", limit, offset)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]OrderRow, 0, limit)
	for rows.Next() {
		var r OrderRow
		if err := rows.Scan(&r.ID, &r.Channel, &r.OrderRef, &r.HashName, &r.OrderType,
			&r.Status, &r.RentDays, &r.RentPrice, &r.Amount, &r.Deposits,
			&r.StartedAt, &r.DueAt, &r.FinishedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}
