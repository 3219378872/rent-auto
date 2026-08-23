ALTER TABLE inventory_items ADD COLUMN IF NOT EXISTS cost_updated_at timestamptz NOT NULL DEFAULT now();
UPDATE inventory_items SET cost_updated_at = last_synced_at;
