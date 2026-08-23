# 数据模型规格（PostgreSQL）

> 迁移即真相：`backend/migrations/` 为唯一权威。本文件定义语义与口径。
> 金额：`numeric(12,2)` 存储；Go 侧 `float64`，写库前必须 `Round2`。
> 时间：一律 `timestamptz`（UTC）；渠道枚举 `uu|eco`。

## 表

### templates — 模板登记表（跨渠道归一锚）
```
hash_name PK            -- Steam market_hash_name，跨渠道唯一键
display_name, category  -- 展示名/品类(刀/手套/枪/印花…)
uu_template_id          -- UU 模板ID（可空）
eco_ref_price           -- ECO 全量dump参考价（可空）
uu_mark_price           -- UU 库存模板参考价
value_anchor            -- 合成价值锚点 V（见 pricing-spec §2）
anchor_updated_at
blacklisted bool        -- 黑名单：永不自动上架
created_at/updated_at
```

### inventory_items — 库存项
```
id PK; channel; asset_id        -- UNIQUE(channel, asset_id)
hash_name FK→templates
market_hash_name                -- 冗余展示
status                          -- in_stock|listed|leased|locked|sold
tradable bool; abrade numeric
cost_basis numeric              -- 成本价（录入来源手工/平台）
cost_source                     -- manual|uu_sync|eco_sync|import
last_synced_at; raw jsonb       -- 平台原始载荷（排障用）
```

### listings — 货架项（期望态+实际态合一）
```
id PK; channel; asset_id; hash_name FK
goods_ref               -- 渠道内商品标识(UU commodityId / ECO GoodsNum)
desired_state           -- none|active|delisted
actual_state            -- unknown|none|active|leased|stale
rent_price, long_rent_price, max_days, deposit numeric
strategy_id FK; last_action_id FK→price_actions
listed_at, last_reprice_at, actual_synced_at
UNIQUE(channel, goods_ref)
```

### lease_orders — 出租订单流水
```
id PK; channel; order_ref       -- UNIQUE(channel, order_ref)
asset_id, hash_name FK
order_type                      -- short|long|buyout
status                          -- 平台原始状态映射到统一状态机(见§状态映射)
rent_days int; rent_price; order_amount; deposits numeric
started_at, due_at, finished_at; raw jsonb
income_recorded bool            -- 终态且已计入收益
```

**统一状态机**：pending_payment → delivering → leasing → returning → done | bought_out | cancelled | arbitrating | breach
（UU/ECO 原始状态码映射表存 design/platform-*-api-notes.md）

### market_snapshots — 行情快照
```
id bigserial PK; hash_name FK; source(uu_market|eco_dump|own_order)
kind                            -- lease_short|lease_long|deposit|sell
rank int                        -- 在该次行情中的排名（自有订单 rank=0）
price numeric; captured_at
INDEX (hash_name, kind, captured_at DESC)
```

### price_actions — 定价动作审计（含 dry-run 模拟）
```
id bigserial PK; ts; channel; hash_name; asset_id; listing_id?
action                          -- publish|reprice|delist|skip
old_*/new_* (rent,long,days,deposit)
decision jsonb                  -- 完整决策依据：V/分位/因子/护栏命中/策略版本
dry_run bool; success bool; error text
```

### strategies — 策略
```
id PK; name; enabled
scope                           -- global|template
hash_name NULL                  -- scope=template 时必填
channel_route                   -- uu_only|eco_only|both|uu_primary_eco_fallback
params jsonb                    -- 见 pricing-spec §5 Params 结构
priority int                    -- template > global；同级取 priority 大者
real_execution_enabled bool     -- false=永远 dry-run
updated_by                      -- user|system
```

### fund_flows — 资金流水（ECO 钱包等）
```
id PK; channel; flow_ref UNIQUE(channel,flow_ref); amount; type; occurred_at; raw
```

### daily_stats — 日汇总
```
stat_date, channel, category → income, order_count, avg_rent_yield
UNIQUE(stat_date, channel, category)
asset_snapshot jsonb             -- 当日总资产构成
```

### app_settings — KV 设置（含加密值）
```
key PK; value_enc bytea NULL; value_plain jsonb NULL; updated_at
-- 凭证类值用 AES-GCM 加密于 value_enc（主密钥 APP_MASTER_KEY）
```

### audit_log — 操作审计
```
id bigserial PK; ts; actor(system|user:<name>); channel?; action; target?; detail jsonb
```

## 口径定义

- **总资产** = Σ inventory(在库+在架+在租) × value_anchor + Σ 在租订单 deposits(在外押金) + Σ 渠道钱包余额(fund_flows 末态)
- **总收入** = Σ lease_orders(done|bought_out).order_amount − 已售出库存成本（口径 A：毛租金收入；面板同时给出口径 B：净=收入−成本）
- **年化收益率** = (Σ净收益 / Σ成本基准) × (365d / 观测天数)；观测起点=最早成本录入日
- **分类收益率** = 该品类 Σ(订单收入−成本) / Σ成本
