-- +migrate Up
-- 反馈控制器状态支撑（pricing-spec §3）：
--   lease_orders.factor_applied : 终态订单是否已折算进 listing 因子（幂等标记）
--   listings.last_factor_event_at : 最近一次因子事件时间，stale 阶梯降价的锚点
ALTER TABLE lease_orders ADD COLUMN IF NOT EXISTS factor_applied boolean NOT NULL DEFAULT false;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS last_factor_event_at timestamptz;
