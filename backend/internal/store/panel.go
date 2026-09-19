package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// panelWhere binds user values; callers supply only constant SQL expressions.
type panelWhere struct {
	clauses []string
	args    []any
}

func (p *panelWhere) add(expression string, value any) {
	p.args = append(p.args, value)
	p.clauses = append(p.clauses, fmt.Sprintf(expression, len(p.args)))
}

func (p *panelWhere) sql() string {
	if len(p.clauses) == 0 {
		return "true"
	}
	return strings.Join(p.clauses, " AND ")
}

type PriceActionFilter struct {
	Channel, HashName, AssetID, Action, Mode, Result string
	ListingID, ID                                    int64
	Since, Until                                     time.Time
	Limit, Offset                                    int
}

type PriceActionRow struct {
	ID         int64           `json:"id"`
	Time       time.Time       `json:"ts"`
	Channel    string          `json:"channel"`
	HashName   string          `json:"hash_name"`
	AssetID    string          `json:"asset_id"`
	ListingID  *int64          `json:"listing_id"`
	Action     string          `json:"action"`
	OldRent    *float64        `json:"old_rent"`
	NewRent    *float64        `json:"new_rent"`
	OldLong    *float64        `json:"old_long"`
	NewLong    *float64        `json:"new_long"`
	OldDays    *int            `json:"old_days"`
	NewDays    *int            `json:"new_days"`
	OldDeposit *float64        `json:"old_deposit"`
	NewDeposit *float64        `json:"new_deposit"`
	Decision   json.RawMessage `json:"decision"`
	DryRun     bool            `json:"dry_run"`
	Success    bool            `json:"success"`
	Error      string          `json:"error,omitempty"`
}

func (s *Store) ListPriceActions(ctx context.Context, f PriceActionFilter) ([]PriceActionRow, int, error) {
	p := panelWhere{}
	for _, field := range []struct{ expression, value string }{
		{"channel=$%d", f.Channel}, {"hash_name=$%d", f.HashName}, {"asset_id=$%d", f.AssetID}, {"action=$%d", f.Action},
	} {
		if field.value != "" {
			p.add(field.expression, field.value)
		}
	}
	if f.ListingID > 0 {
		p.add("listing_id=$%d", f.ListingID)
	}
	if f.ID > 0 {
		p.add("id=$%d", f.ID)
	}
	if f.Mode != "" {
		p.add("dry_run=$%d", f.Mode == "dry_run")
	}
	switch f.Result {
	case "skip":
		p.clauses = append(p.clauses, "action='skip'")
	case "success":
		p.clauses = append(p.clauses, "action<>'skip' AND success")
	case "failed":
		p.clauses = append(p.clauses, "NOT success")
	}
	if !f.Since.IsZero() {
		p.add("ts >= $%d", f.Since)
	}
	if !f.Until.IsZero() {
		p.add("ts < $%d", f.Until)
	}
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM price_actions WHERE "+p.sql(), p.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit, offset := normalizePage(f.Limit, f.Offset)
	rows, err := s.Pool.Query(ctx, `SELECT id,ts,channel,hash_name,COALESCE(asset_id,''),listing_id,action,
	 old_rent,new_rent,old_long,new_long,old_days,new_days,old_deposit,new_deposit,
	 COALESCE(decision,'{}'::jsonb),dry_run,success,
	 CASE WHEN error IS NOT NULL AND error<>'' THEN '执行失败，请检查渠道状态或服务端日志' ELSE '' END
	 FROM price_actions WHERE `+p.sql()+fmt.Sprintf(" ORDER BY id DESC LIMIT %d OFFSET %d", limit, offset), p.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]PriceActionRow, 0, limit)
	for rows.Next() {
		var r PriceActionRow
		if err := rows.Scan(&r.ID, &r.Time, &r.Channel, &r.HashName, &r.AssetID, &r.ListingID, &r.Action,
			&r.OldRent, &r.NewRent, &r.OldLong, &r.NewLong, &r.OldDays, &r.NewDays,
			&r.OldDeposit, &r.NewDeposit, &r.Decision, &r.DryRun, &r.Success, &r.Error); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func (s *Store) SearchTemplates(ctx context.Context, search, blacklist string, limit, offset int) ([]Template, int, error) {
	p := panelWhere{}
	if search != "" {
		p.add("(hash_name ILIKE $%[1]d OR display_name ILIKE $%[1]d)", "%"+search+"%")
	}
	if blacklist != "" {
		p.add("blacklisted=$%d", blacklist == "true")
	}
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM templates WHERE "+p.sql(), p.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit, offset = normalizePage(limit, offset)
	rows, err := s.Pool.Query(ctx, `SELECT hash_name,display_name,category,uu_template_id,uu_mark_price,
	 eco_ref_price,value_anchor,blacklisted,anchor_updated_at FROM templates WHERE `+p.sql()+
		fmt.Sprintf(" ORDER BY hash_name LIMIT %d OFFSET %d", limit, offset), p.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Template, 0, limit)
	for rows.Next() {
		r, err := scanTemplateRows(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *r)
	}
	return out, total, rows.Err()
}

func (s *Store) InventoryCategories(ctx context.Context) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT COALESCE(NULLIF(t.category,''),'未分类')
	 FROM inventory_items i JOIN templates t ON t.hash_name=i.hash_name ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var category string
		if err := rows.Scan(&category); err != nil {
			return nil, err
		}
		out = append(out, category)
	}
	return out, rows.Err()
}

func (s *Store) CostCoverage(ctx context.Context) (total, costed int, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER(WHERE cost_basis>0) FROM physical_inventory`).Scan(&total, &costed)
	return
}
