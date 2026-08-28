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
template_id int64               -- UU 模板 id（行情采集定位，0002）
market_hash_name                -- 冗余展示
mark_price numeric              -- 平台标记价（锚点候选，0002）
status                          -- in_stock|listed|leased|locked|sold
tradable bool; abrade numeric
cost_basis numeric              -- 成本价（录入来源手工/平台）
cost_source                     -- manual|uu_sync|eco_sync|import
cost_updated_at                 -- 最早成本录入时间=年化观测起点（0004）
last_synced_at; raw jsonb       -- 平台原始载荷（排障用）
```

### listings — 货架项（期望态+实际态合一）
```
id PK; channel; asset_id; hash_name FK
goods_ref               -- 渠道内商品标识(UU commodityId / ECO GoodsNum)
desired_state           -- none|active|delisted
actual_state            -- unknown|none|active|leased|stale
rent_price, long_rent_price, max_days, deposit numeric
factor numeric DEFAULT 1.0        -- 反馈控制器状态（pricing-spec §3，0003+）
last_factor_event_at timestamptz  -- 因子事件锚点（stale 阶梯计时起点，0005）
sublet_applied bool DEFAULT false -- ECO 转租策略已被平台接受（0008）：
                                  -- false 时 reprice 豁免噪声下限强制提交一次，
                                  -- 接受后置位（上架成功即置位；仅 ECO 语义）
strategy_id FK
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
started_at, due_at, finished_at, updated_at; raw jsonb
income_recorded bool            -- 终态且已计入收益
factor_applied bool             -- 是否已折算进 listing 因子（0005，防重复折算）
```

**统一状态机**：pending_payment → delivering → leasing → returning → done | bought_out | cancelled | arbitrating | breach
（UU/ECO 原始状态码映射表存 design/platform-*-api-notes.md；
未知码显式落 `unknown` 并由 0006 CHECK 约束兜底——不再允许空串静默入库）

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
- **总收入** = Σ lease_orders(done|bought_out).order_amount − 已售出库存成本（口径 A：毛租金收入；口径 B：净=收入−已售成本，面板展示口径 B）
- **年化收益率** = (Σ净收益 / Σ全量成本基准) × (365d / 观测天数)；观测起点=最早成本录入日(cost_updated_at)；无起点时不外推（显示 0）
- **分类收益率** = 该品类 Σ(订单收入−已售成本) / Σ该品类全量成本基准
- **日界口径**：daily_stats 一律按 UTC 日切分，读写两端一致
