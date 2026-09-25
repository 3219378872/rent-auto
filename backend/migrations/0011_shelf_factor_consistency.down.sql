DROP TABLE shelf_observations;
DROP INDEX idx_orders_factor_pending;
DROP INDEX idx_listings_factor_identity;
ALTER TABLE lease_orders DROP COLUMN factor_observed_at;
ALTER TABLE lease_orders DROP COLUMN factor_listing_id;
ALTER TABLE listings DROP COLUMN retired_at;
