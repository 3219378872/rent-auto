DROP INDEX IF EXISTS idx_inventory_asset_status;
DROP INDEX IF EXISTS idx_listings_asset_state;

UPDATE inventory_items SET status='unknown' WHERE status='missing';
ALTER TABLE inventory_items DROP CONSTRAINT chk_inventory_items_status;
ALTER TABLE inventory_items ADD CONSTRAINT chk_inventory_items_status CHECK (
    status IN ('in_stock','listed','leased','locked','sold','unknown'));

ALTER TABLE listings DROP CONSTRAINT chk_recon_mismatch,
    DROP COLUMN recon_mismatch_since,
    DROP COLUMN recon_mismatch_reason;
