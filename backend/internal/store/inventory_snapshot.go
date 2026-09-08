package store

import (
	"context"
	"fmt"

	"github.com/3219378872/rent-auto/backend/internal/domain"
)

// ApplyInventorySnapshot accepts only a complete successful channel snapshot.
// Missing is an observation, never evidence that the asset was sold.
func (s *Store) ApplyInventorySnapshot(ctx context.Context, channel domain.Channel, items []domain.InventoryItem) error {
	if !channel.Valid() || channel == "" {
		return fmt.Errorf("invalid inventory channel")
	}
	seen := make(map[string]bool, len(items))
	refs := make([]string, 0, len(items))
	for _, it := range items {
		if it.Channel != channel || it.HashName == "" || it.AssetID == "" || seen[it.AssetID] {
			return fmt.Errorf("invalid or duplicate inventory row for %s", channel)
		}
		seen[it.AssetID] = true
		refs = append(refs, it.AssetID)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Snapshot writers for a channel must never interleave their missing pass.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "inventory:"+string(channel)); err != nil {
		return err
	}
	for _, it := range items {
		mark := round2Money(it.MarkPrice)
		if _, err := tx.Exec(ctx, `INSERT INTO templates(hash_name, display_name, uu_template_id, uu_mark_price, eco_ref_price)
		 VALUES($1,$2,CASE WHEN $3='uu' THEN NULLIF($4,0)::bigint END,
		        CASE WHEN $3='uu' AND $5>0 THEN $5::numeric END,
		        CASE WHEN $3='eco' AND $5>0 THEN $5::numeric END)
		 ON CONFLICT(hash_name) DO UPDATE SET
		 display_name=CASE WHEN excluded.display_name<>'' THEN excluded.display_name ELSE templates.display_name END,
		 uu_template_id=COALESCE(excluded.uu_template_id,templates.uu_template_id),
		 uu_mark_price=COALESCE(excluded.uu_mark_price,templates.uu_mark_price),
		 eco_ref_price=COALESCE(excluded.eco_ref_price,templates.eco_ref_price),updated_at=now()`,
			it.HashName, it.DisplayName, string(channel), it.TemplateID, mark); err != nil {
			return fmt.Errorf("inventory template: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO inventory_items(channel,asset_id,hash_name,market_hash_name,template_id,mark_price,tradable,status,last_synced_at)
		 VALUES($1,$2,$3,$4,NULLIF($5,0)::bigint,$6,$7,$8,now())
		 ON CONFLICT(channel,asset_id) DO UPDATE SET hash_name=excluded.hash_name,
		 market_hash_name=excluded.market_hash_name,template_id=excluded.template_id,
		 mark_price=excluded.mark_price,tradable=excluded.tradable,status=excluded.status,last_synced_at=now()`,
			channel, it.AssetID, it.HashName, it.DisplayName, it.TemplateID, mark, it.Tradable, it.Status); err != nil {
			return fmt.Errorf("inventory row: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE inventory_items SET status='missing',tradable=false
	 WHERE channel=$1 AND status<>'sold' AND NOT(asset_id=ANY($2))`, channel, refs); err != nil {
		return fmt.Errorf("inventory missing: %w", err)
	}
	return tx.Commit(ctx)
}
