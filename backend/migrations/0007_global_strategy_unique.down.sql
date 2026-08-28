-- +migrate Down
DROP INDEX IF EXISTS uniq_global_strategy;
-- The deduplicated duplicate rows are intentionally not restored: they were
-- accidental inserts from the missing-conflict-target bug, not user data.
