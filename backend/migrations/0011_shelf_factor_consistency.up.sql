ALTER TABLE listings ADD COLUMN retired_at timestamptz;
ALTER TABLE lease_orders ADD COLUMN factor_listing_id bigint REFERENCES listings(id) ON DELETE SET NULL;
ALTER TABLE lease_orders ADD COLUMN factor_observed_at timestamptz;
UPDATE lease_orders SET factor_observed_at=updated_at;
ALTER TABLE lease_orders ALTER COLUMN factor_observed_at SET DEFAULT clock_timestamp();
ALTER TABLE lease_orders ALTER COLUMN factor_observed_at SET NOT NULL;
CREATE INDEX idx_listings_factor_identity ON listings(channel,asset_id);
CREATE INDEX idx_orders_factor_pending ON lease_orders(id) WHERE NOT factor_applied;

-- Prevent an older response from introducing rows after a newer full snapshot
-- has already established that they are absent.
CREATE TABLE shelf_observations (
    channel text PRIMARY KEY CHECK (channel IN ('uu','eco')),
    observed_at timestamptz NOT NULL
);
