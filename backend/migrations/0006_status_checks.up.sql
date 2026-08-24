-- +migrate Up
-- 状态机枚举入 DB（round5 遗留清欠）：此前未知平台状态码在适配器层映射为
-- 空串静默入库，对所有按 status 过滤的查询不可见。现归一为 'unknown'
-- 显式值，并以 CHECK 约束兜底拒绝越界写入。
UPDATE lease_orders SET status = 'unknown' WHERE status = '';
UPDATE inventory_items SET status = 'unknown' WHERE status = '';

ALTER TABLE lease_orders
    ADD CONSTRAINT chk_lease_orders_status CHECK (status IN (
        'pending_payment','delivering','leasing','returning',
        'done','bought_out','cancelled','arbitrating','breach',
        'unknown'));

ALTER TABLE inventory_items
    ADD CONSTRAINT chk_inventory_items_status CHECK (status IN (
        'in_stock','listed','leased','locked','sold',
        'unknown'));
