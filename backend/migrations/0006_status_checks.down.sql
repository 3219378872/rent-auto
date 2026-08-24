-- +migrate Down
ALTER TABLE inventory_items DROP CONSTRAINT IF EXISTS chk_inventory_items_status;
ALTER TABLE lease_orders DROP CONSTRAINT IF EXISTS chk_lease_orders_status;
-- 不回滚 unknown→'' 的数据归一（空串从未是合法状态，仅是缺失防护）
