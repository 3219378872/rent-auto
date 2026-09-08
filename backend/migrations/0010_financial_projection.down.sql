DROP TABLE order_income_ledger;
DROP VIEW physical_inventory;
UPDATE inventory_items SET cost_updated_at=last_synced_at WHERE cost_updated_at IS NULL;
ALTER TABLE inventory_items DROP COLUMN cost_modified_at,
    ALTER COLUMN cost_updated_at SET DEFAULT now(),
    ALTER COLUMN cost_updated_at SET NOT NULL;
