ALTER TABLE inventory_items
    ALTER COLUMN cost_updated_at DROP NOT NULL,
    ALTER COLUMN cost_updated_at DROP DEFAULT,
    ADD COLUMN cost_modified_at timestamptz;
UPDATE inventory_items SET cost_updated_at=NULL WHERE cost_basis IS NULL;
UPDATE inventory_items SET cost_modified_at=cost_updated_at WHERE cost_basis IS NOT NULL;

-- A channel row is an observation of an asset, not another physical asset.
CREATE VIEW physical_inventory AS
WITH keyed AS (
    SELECT i.*, CASE WHEN asset_id<>'' THEN 'asset:' || asset_id
                    ELSE 'row:' || id::text END AS physical_key
    FROM inventory_items i
), observed AS (
    SELECT DISTINCT ON (physical_key) physical_key, asset_id, hash_name, status
    FROM keyed
    ORDER BY physical_key, (status='missing'), last_synced_at DESC, id DESC
), costs AS (
    SELECT DISTINCT ON (physical_key) physical_key, cost_basis, cost_source
    FROM keyed WHERE cost_basis IS NOT NULL
    ORDER BY physical_key, (cost_source='manual') DESC,
             cost_modified_at DESC NULLS LAST, id DESC
), first_cost AS (
    SELECT physical_key, MIN(cost_updated_at) AS cost_updated_at
    FROM keyed WHERE cost_basis IS NOT NULL GROUP BY physical_key
)
SELECT o.*, c.cost_basis, c.cost_source, f.cost_updated_at
FROM observed o LEFT JOIN costs c USING (physical_key)
LEFT JOIN first_cost f USING (physical_key);

CREATE TABLE order_income_ledger (
    order_id bigint PRIMARY KEY REFERENCES lease_orders(id) ON DELETE CASCADE,
    stat_date date NOT NULL,
    channel text NOT NULL CHECK (channel IN ('uu','eco')),
    category text NOT NULL,
    amount numeric(14,2) NOT NULL
);

-- Preserve legacy recognized income, but repair stale sums from canonical orders.
INSERT INTO order_income_ledger(order_id,stat_date,channel,category,amount)
SELECT o.id, (COALESCE(o.finished_at,o.updated_at) AT TIME ZONE 'utc')::date,
       o.channel, COALESCE(NULLIF(t.category,''),'未分类'), o.order_amount
FROM lease_orders o LEFT JOIN templates t ON t.hash_name=o.hash_name
WHERE o.income_recorded AND o.status IN ('done','bought_out');
UPDATE lease_orders SET income_recorded=false
WHERE income_recorded AND status NOT IN ('done','bought_out');
UPDATE daily_stats SET income=0,order_count=0;
INSERT INTO daily_stats(stat_date,channel,category,income,order_count)
SELECT stat_date,channel,category,SUM(amount),COUNT(*)
FROM order_income_ledger GROUP BY stat_date,channel,category
ON CONFLICT(stat_date,channel,category) DO UPDATE SET
income=excluded.income,order_count=excluded.order_count;
