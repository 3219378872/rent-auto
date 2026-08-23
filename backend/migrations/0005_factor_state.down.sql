-- +migrate Down
ALTER TABLE listings DROP COLUMN IF EXISTS last_factor_event_at;
ALTER TABLE lease_orders DROP COLUMN IF EXISTS factor_applied;
