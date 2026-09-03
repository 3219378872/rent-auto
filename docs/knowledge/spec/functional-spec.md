# 功能规格与验收标准

> 每节含验收标准（Given/When/Then 简式）。实现完成时在证据层链接对应测试。

## 1. 页面清单（8 个）

| # | 路由 | 页面 | 对应故事 |
|---|---|---|---|
| 1 | /login | 登录 | US-CHAN-00 |
| 2 | / | 仪表盘 | US-DASH-* |
| 3 | /inventory | 库存状态 | US-INV-* |
| 4 | /listings | 上架状态（双渠道） | US-LIST-* |
| 5 | /orders | 租赁订单 | US-ORDER-* |
| 6 | /strategies | 上架/改价策略 | US-STRAT-* |
| 7 | /channels | 渠道账号 | US-CHAN-* |
| 8 | /audit | 审计日志 | US-AUDIT-* |

### AC-P1 登录
- 错误凭证 → 401，前端提示；正确凭证 → JWT(≤24h) 存 localStorage
- 所有 /api/* （除 /api/auth/login、/api/health）未带有效 token → 401

## 2. REST API 契约

契约文件：`docs/knowledge/spec/openapi.yaml`（M1 建立，随里程碑演进；当前 v0.8.0）。
通用约定：
- 前缀 `/api/v1`；JSON UTF-8；金额字段一律两位小数 float
- 分页参数 `page`(1-based)、`page_size`(≤200，round10 与 store 钳制/openapi 统一口径)；响应 `{items,total}`
- 错误体 `{code,message}`；HTTP 语义化

核心端点组：auth / dashboard / inventory / listings / orders / strategies / channels / audit / jobs(手动触发)。

## 3. 自动化任务规格

> 表格与 `scheduler/jobs.go` 注册表对齐（2026-08-24 审查轮回写）。
> 批量上架任务（lease_publish）由 reconcile 周期任务逐条实现替代；
> rollup 并入 orders_sync 内联步骤——见下表备注与 ADR-0004。

| 任务 | 默认节奏 | 规格 |
|---|---|---|
| reprice | 每 31m±90s 抖动 | 在架商品全量重估；仅当变化超过最小步长才提交；频控限速；风控退避；策略级 dry-run 双门禁；ECO 转租补齐：sublet_applied=false 豁免噪声下限提交一次（冷却/幅度护栏仍守，接受后置位，迁移 0008） |
| factor_events | 每 17m±60s | 终态订单折算 listings.factor（同 listing 批内顺序累计，单事务落库）；stale 步降；f_min 回归 1.00+审计告警 |
| inventory_sync | 每 30m±60s | 两渠道库存拉取 upsert；成本价维护 |
| shelf_sync | 每 10m±30s | 货架快照 upsert listings.actual_state |
| orders_sync | 每 10m±30s | 两渠道订单入库：回看窗口动态锚定最早未终态订单−24h（上限100d，ADR-0004）；内联 RollupTerminalOrders 收益记账 |
| market_snapshot | 每 20m±120s | UU 行情 topN 快照入库；模板级缓存复用 |
| value_anchor | 每 1h±5m | ECO 全量价格 dump 单次拉取；与 UU 参考价合成价值锚点 V |
| reconcile | 每 10m±60s | 期望货架 vs 实际货架 → 差异动作（publish 补齐/delist 含 leased 豁免、orphan/surplus 24h 宽限、写回闭环 ADR-0005）；双层 dry-run 门禁+风控冷却过滤 |
| uu_delivery | 每 5m±45s | UU 待发货待办→发送报价（审计逐单） |
| steam_offers | 每 5m±45s | Steam 收报价→0 成本单自动接受 |
| eco_delivery | 每 5m±45s | ECO 四步交付编排（幂等） |
| zero_cd | 每日 23:30 | UU 可转租列表→开启0CD |

### AC-T1 dry-run
- 新策略首次执行必须 dry_run=true：完整走决策链但只写 `price_actions(dry_run=true)` 不调平台
- 面板策略页可切换 enable_real_execution
- （2026-08-24 起）reconcile 与 reprice 同受 `DRY_RUN_DEFAULT || !real_execution_enabled` 双层门禁；门禁查询失败强制 dry-run
- （2026-09-03 起）reconcile 按模板逐 Action 门禁：全局 dry-run 兜底，模板非 real 进 dry 分批；发货/0CD（uu_delivery/steam_offers/eco_delivery/zero_cd）同门 skipped 审计

### AC-T2 护栏（全部任务强制）
- 价格边界 [min_rent, max_rent]；单次改价幅度 ≤ max_change_ratio(默认15%)
- 同一商品两次改价间隔 ≥ cooldown_minutes(默认30)
- ECO：解得三元组后派生押金 > deposit_cap_ratio×V 时拒绝该动作并告警
- UU：押金 < deposit_floor_ratio×V 时抬升至下限

## 4. 渠道路由规格

- 策略字段 route ∈ {uu_only, eco_only, both, uu_primary_eco_fallback}
- fallback 触发条件：UU token 失效 或 该商品在 UU 连续 N 天(默认7)无在架记录
- Reconciler 保证路由意图最终一致（在正确渠道上架、从错误渠道下架）
- ECO 出租上架一律开启转租且转租价由平台动态定价（SupportSublet=1 +
  SubletPricingMethod=2，上架与改价载荷均携带；自定义转租价字段不传）→
  枚举语义记 platform-eco-api-notes.md

## 5. Open Questions
- ECO 改价接口是否支持只改租赁不改出售（PublishType=2 的行为边界）→ 待真机验证，结论记 platform-eco-api-notes.md
- UU 长租阈值(8天?)与 ECO(21天) 差异的统一建模口径 → pricing-spec.md §4
