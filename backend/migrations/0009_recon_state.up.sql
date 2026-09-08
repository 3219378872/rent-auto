ALTER TABLE listings
    ADD COLUMN recon_mismatch_since timestamptz,
    ADD COLUMN recon_mismatch_reason text,
    ADD CONSTRAINT chk_recon_mismatch CHECK (
        (recon_mismatch_since IS NULL AND recon_mismatch_reason IS NULL) OR
        (recon_mismatch_since IS NOT NULL AND recon_mismatch_reason IS NOT NULL AND recon_mismatch_reason IN
            ('orphan_not_routable', 'surplus_copies')));

ALTER TABLE inventory_items DROP CONSTRAINT chk_inventory_items_status;
ALTER TABLE inventory_items ADD CONSTRAINT chk_inventory_items_status CHECK (
    status IN ('in_stock','listed','leased','locked','sold','unknown','missing'));

CREATE INDEX idx_inventory_asset_status ON inventory_items(asset_id,status);
CREATE INDEX idx_listings_asset_state ON listings(asset_id,actual_state);
