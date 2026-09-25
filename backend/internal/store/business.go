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
	return upsertTemplate(ctx, s.Pool, t)
}

func upsertTemplate(ctx context.Context, db execQuerier, t Template) error {
	// Channel mark prices are money legs: normalize before persistence
	// (AGENTS.md 硬规则). Nil-ness is preserved — absent stays absent.
	round2MoneyPtr(t.UUMarkPrice)
	round2MoneyPtr(t.EcoRefPrice)
	_, err := db.Exec(ctx,
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

// ListTemplates returns the template catalog ordered by hash. Pagination is
// optional: limit<=0 returns the full catalog (the panel needs the complete
// blacklist table and template picker), a positive limit is clamped to 200.
func (s *Store) ListTemplates(ctx context.Context, limit, offset int) ([]Template, error) {
	q := `SELECT hash_name, display_name, category, uu_template_id, uu_mark_price, eco_ref_price, value_anchor, blacklisted, anchor_updated_at
		 FROM templates ORDER BY hash_name`
	if limit > 0 {
		if limit > 200 {
			limit = 200
		}
		if offset < 0 {
			offset = 0
		}
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	}
	rows, err := s.Pool.Query(ctx, q)
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
	Channel                           domain.Channel
	Status                            string
	Search                            string
	Limit                             int
	Offset                            int
	Category, Sort, HashName, AssetID string
	CostMissing                       bool
}

func (s *Store) UpsertInventoryItem(ctx context.Context, it domain.InventoryItem, costBasis *float64) error {
	it.MarkPrice = round2Money(it.MarkPrice)
	round2MoneyPtr(costBasis)
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO inventory_items(channel, asset_id, hash_name, market_hash_name, template_id, mark_price, tradable, status, cost_basis, last_synced_at, cost_updated_at, cost_modified_at)
		 VALUES($1,$2,$3,$4,NULLIF($5,0)::bigint,$6,$7,$8,$9,now(),
		        CASE WHEN $9::numeric IS NOT NULL THEN now() END, CASE WHEN $9::numeric IS NOT NULL THEN now() END)
		 ON CONFLICT(channel, asset_id) DO UPDATE SET
		   hash_name=EXCLUDED.hash_name, market_hash_name=EXCLUDED.market_hash_name,
		   template_id=EXCLUDED.template_id, mark_price=EXCLUDED.mark_price,
		   tradable=EXCLUDED.tradable, status=EXCLUDED.status, last_synced_at=now(),
		   cost_basis=COALESCE(inventory_items.cost_basis,EXCLUDED.cost_basis),
		   cost_updated_at=COALESCE(inventory_items.cost_updated_at,EXCLUDED.cost_updated_at),
		   cost_modified_at=COALESCE(inventory_items.cost_modified_at,EXCLUDED.cost_modified_at)`,
		it.Channel, it.AssetID, it.HashName, it.DisplayName, it.TemplateID, it.MarkPrice, it.Tradable, it.Status, costBasis)
	if err != nil {
		return fmt.Errorf("upsert inventory %s/%s: %w", it.Channel, it.AssetID, err)
	}
	return nil
}

func (s *Store) SetCostBasis(ctx context.Context, channel domain.Channel, assetID string, cost float64) error {
	tag, err := s.Pool.Exec(ctx,
		`WITH target AS (
		   SELECT id,asset_id FROM inventory_items WHERE channel=$1 AND asset_id=$2
		 ), first_cost AS (
		   SELECT MIN(cost_updated_at) AS first_at FROM inventory_items i
		   WHERE cost_basis IS NOT NULL AND EXISTS(SELECT 1 FROM target
		     WHERE target.id=i.id OR (target.asset_id<>'' AND target.asset_id=i.asset_id))
		 )
		 UPDATE inventory_items SET cost_basis=$3,cost_source='manual',cost_modified_at=now(),
		 cost_updated_at=COALESCE((SELECT first_at FROM first_cost),cost_updated_at,now())
		 WHERE EXISTS(SELECT 1 FROM target WHERE
		       target.id=inventory_items.id OR (target.asset_id<>'' AND target.asset_id=inventory_items.asset_id))`,
		channel, assetID, round2Money(cost))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const invCols = `i.id, i.channel, i.asset_id, i.hash_name, i.market_hash_name, i.template_id, i.mark_price,
 i.tradable, i.status, coalesce(i.cost_basis,0), COALESCE(NULLIF(t.category,''),'未分类'),
 COALESCE(i.cost_source,''), i.cost_modified_at, i.last_synced_at`

type InventoryRow struct {
	ID             int64          `json:"id"`
	Channel        domain.Channel `json:"channel"`
	AssetID        string         `json:"asset_id"`
	HashName       string         `json:"hash_name"`
	MarketHash     string         `json:"market_hash_name"`
	TemplateID     *int64         `json:"template_id"`
	MarkPrice      float64        `json:"mark_price"`
	Tradable       bool           `json:"tradable"`
	Status         string         `json:"status"`
	CostBasis      float64        `json:"cost_basis"`
	Category       string         `json:"category"`
	CostSource     string         `json:"cost_source"`
	CostModifiedAt *time.Time     `json:"cost_modified_at"`
	LastSyncedAt   *time.Time     `json:"last_synced_at"`
}

func (s *Store) ListInventory(ctx context.Context, f InventoryFilter) ([]InventoryRow, int, error) {
	limit, offset := normalizePage(f.Limit, f.Offset)
	where := "WHERE true"
	args := []any{}
	if f.Channel.Valid() && f.Channel != "" {
		args = append(args, string(f.Channel))
		where += fmt.Sprintf(" AND i.channel=$%d", len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND i.status=$%d", len(args))
	}
	if f.Search != "" {
		args = append(args, "%"+f.Search+"%")
		where += fmt.Sprintf(" AND (i.market_hash_name ILIKE $%[1]d OR i.hash_name ILIKE $%[1]d OR i.asset_id ILIKE $%[1]d)", len(args))
	}
	for _, v := range []struct{ expression, value string }{
		{"i.hash_name=$%d", f.HashName}, {"i.asset_id=$%d", f.AssetID},
		{"COALESCE(NULLIF(t.category,''),'未分类')=$%d", f.Category},
	} {
		if v.value != "" {
			args = append(args, v.value)
			where += " AND " + fmt.Sprintf(v.expression, len(args))
		}
	}
	if f.CostMissing {
		where += " AND COALESCE(i.cost_basis,0)<=0"
	}
	from := " FROM inventory_items i LEFT JOIN templates t ON t.hash_name=i.hash_name "
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*)"+from+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := map[string]string{"name": "i.hash_name,i.id", "price_desc": "i.mark_price DESC,i.id", "price_asc": "i.mark_price,i.id", "cost_desc": "i.cost_basis DESC NULLS LAST,i.id"}[f.Sort]
	if order == "" {
		order = "i.id"
	}
	q := "SELECT " + invCols + from + where + " ORDER BY " + order + fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]InventoryRow, 0, limit)
	for rows.Next() {
		var r InventoryRow
		if err := rows.Scan(&r.ID, &r.Channel, &r.AssetID, &r.HashName, &r.MarketHash, &r.TemplateID, &r.MarkPrice, &r.Tradable, &r.Status, &r.CostBasis, &r.Category, &r.CostSource, &r.CostModifiedAt, &r.LastSyncedAt); err != nil {
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
	o.RentPrice = round2Money(o.RentPrice)
	o.Amount = round2Money(o.Amount)
	o.Deposits = round2Money(o.Deposits)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO lease_orders(channel, order_ref, asset_id, hash_name, order_type, status, rent_days, rent_price, order_amount, deposits, started_at, due_at, finished_at, income_recorded, raw, updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,
		        CASE WHEN $16 THEN COALESCE($13::timestamptz,now()) END,$14,$15,now())
		 ON CONFLICT(channel, order_ref) DO UPDATE SET
		   asset_id=COALESCE(NULLIF(EXCLUDED.asset_id,''),lease_orders.asset_id),
		   hash_name=COALESCE(NULLIF(EXCLUDED.hash_name,''),lease_orders.hash_name),
		   status=EXCLUDED.status, order_type=EXCLUDED.order_type,
		   rent_days=EXCLUDED.rent_days, rent_price=EXCLUDED.rent_price,
		   order_amount=EXCLUDED.order_amount, deposits=EXCLUDED.deposits,
		   started_at=COALESCE(EXCLUDED.started_at, lease_orders.started_at),
		   due_at=COALESCE(EXCLUDED.due_at, lease_orders.due_at),
		   finished_at=CASE WHEN $16 THEN COALESCE($13::timestamptz,lease_orders.finished_at,now()) END,
		   factor_listing_id=CASE WHEN NOT lease_orders.factor_applied AND (
		     COALESCE(NULLIF(EXCLUDED.asset_id,''),lease_orders.asset_id) IS DISTINCT FROM lease_orders.asset_id OR
		     COALESCE(NULLIF(EXCLUDED.hash_name,''),lease_orders.hash_name) IS DISTINCT FROM lease_orders.hash_name OR
		     COALESCE(EXCLUDED.started_at,lease_orders.started_at) IS DISTINCT FROM lease_orders.started_at)
		     THEN NULL ELSE lease_orders.factor_listing_id END,
		   raw=COALESCE(EXCLUDED.raw, lease_orders.raw), updated_at=now()
		 RETURNING id`,
		o.Channel, o.OrderRef, o.AssetID, o.HashName, o.OrderType, o.Status,
		o.RentDays, o.RentPrice, o.Amount, o.Deposits,
		nullTime(o.StartedAt), nullTime(o.DueAt), nullTimePtr(o.FinishedAt),
		false, o.Raw, isTerminal(o.Status)).Scan(&id)
	if err != nil {
		return fmt.Errorf("upsert order %s/%s: %w", o.Channel, o.OrderRef, err)
	}
	if _, err := tx.Exec(ctx, bindFactorOrdersSQL, id, nil); err != nil {
		return fmt.Errorf("bind order %s/%s: %w", o.Channel, o.OrderRef, err)
	}
	return tx.Commit(ctx)
}

// SetTemplateBlacklist toggles the blacklist flag; blacklisted templates drop
// out of routable inventory and value-anchor synthesis.
func (s *Store) SetTemplateBlacklist(ctx context.Context, hashName string, blacklisted bool) error {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE templates SET blacklisted=$2 WHERE hash_name=$1`, hashName, blacklisted)
	if err != nil {
		return fmt.Errorf("set blacklist %s: %w", hashName, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
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

// EarliestOpenOrderStart returns the oldest started_at among non-terminal
// lease orders (nil when none exist). The order-sync job anchors its lookback
// window on this value so a long-running lease (up to 90 days) can never slide
// out of the upstream query window before it reaches a terminal state —
// a fixed 24h lookback permanently lost those orders' income records.
func (s *Store) EarliestOpenOrderStart(ctx context.Context) (*time.Time, error) {
	var t *time.Time
	err := s.Pool.QueryRow(ctx,
		`SELECT MIN(started_at) FROM lease_orders
		 WHERE status NOT IN ('done','bought_out','cancelled','breach')`).Scan(&t)
	if err != nil {
		return nil, fmt.Errorf("earliest open order start: %w", err)
	}
	return t, nil
}

// EarliestLeasedListingStart returns the oldest listed_at among listings the
// shelf reports as leased (nil when none). An order can never start before
// its listing was published, so this anchors the order-sync lookback for
// pre-existing leases on a fresh deployment — before any lease_orders row
// exists to extend EarliestOpenOrderStart (bootstrap gap, 2026-08-27).
func (s *Store) EarliestLeasedListingStart(ctx context.Context) (*time.Time, error) {
	var t *time.Time
	err := s.Pool.QueryRow(ctx,
		`SELECT MIN(listed_at) FROM listings
		 WHERE actual_state='leased' AND listed_at IS NOT NULL`).Scan(&t)
	if err != nil {
		return nil, fmt.Errorf("earliest leased listing start: %w", err)
	}
	return t, nil
}

func nullTimePtr(t *time.Time) any {
	if t == nil || t.IsZero() {
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
	Channel                                          domain.Channel
	Status                                           string
	Limit                                            int
	Offset                                           int
	Search, HashName, AssetID, OrderType, View, Sort string
	Since, Until                                     time.Time
}

type OrderRow struct {
	ID         int64          `json:"id"`
	Channel    domain.Channel `json:"channel"`
	OrderRef   string         `json:"order_ref"`
	HashName   string         `json:"hash_name"`
	AssetID    string         `json:"asset_id"`
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
	if f.Search != "" {
		args = append(args, "%"+f.Search+"%")
		where += fmt.Sprintf(" AND (hash_name ILIKE $%[1]d OR order_ref ILIKE $%[1]d)", len(args))
	}
	for _, v := range []struct{ expression, value string }{{"hash_name=$%d", f.HashName}, {"asset_id=$%d", f.AssetID}, {"order_type=$%d", f.OrderType}} {
		if v.value != "" {
			args = append(args, v.value)
			where += " AND " + fmt.Sprintf(v.expression, len(args))
		}
	}
	switch f.View {
	case "attention":
		where += " AND status IN ('pending_payment','delivering','returning','arbitrating','breach','unknown')"
	case "active":
		where += " AND status='leasing'"
	case "history":
		where += " AND status IN ('done','bought_out','cancelled')"
	}
	if !f.Since.IsZero() {
		args = append(args, f.Since)
		where += fmt.Sprintf(" AND started_at >= $%d", len(args))
	}
	if !f.Until.IsZero() {
		args = append(args, f.Until)
		where += fmt.Sprintf(" AND started_at < $%d", len(args))
	}
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM lease_orders "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := map[string]string{"due_asc": "due_at ASC NULLS LAST,id DESC", "started_desc": "started_at DESC NULLS LAST,id DESC", "amount_desc": "order_amount DESC,id DESC"}[f.Sort]
	if order == "" {
		order = "id DESC"
	}
	q := `SELECT id, channel, order_ref, hash_name, COALESCE(asset_id,''), order_type, status, rent_days,
	             rent_price, order_amount, deposits, started_at, due_at, finished_at
	      FROM lease_orders ` + where + " ORDER BY " + order + fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]OrderRow, 0, limit)
	for rows.Next() {
		var r OrderRow
		if err := rows.Scan(&r.ID, &r.Channel, &r.OrderRef, &r.HashName, &r.AssetID, &r.OrderType,
			&r.Status, &r.RentDays, &r.RentPrice, &r.Amount, &r.Deposits,
			&r.StartedAt, &r.DueAt, &r.FinishedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}
