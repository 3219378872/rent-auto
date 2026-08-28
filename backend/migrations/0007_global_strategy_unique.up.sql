-- +migrate Up
-- EnsureGlobalStrategy relied on `ON CONFLICT DO NOTHING` without a matching
-- unique constraint: name+scope had no index on global scope, so every call
-- inserted another 'default' row (12 duplicates observed on dev, 2026-08-27).
-- The highest-id empty-params duplicate was shadowing the migration-seeded
-- tuned params. Dedupe keeping the original row, then make the singleton
-- contract real with a unique partial index.
DELETE FROM strategies
WHERE scope='global'
  AND id <> (SELECT MIN(id) FROM strategies WHERE scope='global');

CREATE UNIQUE INDEX uniq_global_strategy ON strategies (scope) WHERE scope='global';
