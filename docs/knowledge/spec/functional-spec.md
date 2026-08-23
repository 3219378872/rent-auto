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

契约文件：`spec/openapi.yaml`（M1 建立，随里程碑演进）。
通用约定：
- 前缀 `/api/v1`；JSON UTF-8；金额字段一律两位小数 float
- 分页参数 `page`(1-based)、`page_size`(≤100)；响应 `{items,total}`
- 错误体 `{code,message}`；HTTP 语义化

核心端点组：auth / dashboard / inventory / listings / orders / strategies / channels / audit / jobs(手动触发)。

## 3. 自动化任务规格

| 任务 | 默认节奏 | 规格 |
|---|---|---|
| lease_publish | 每日 HH:MM(默认17:30) | 遍历库存→渠道路由→策略定价→护栏→批量上架(UU≤50/批, ECO≤100/批) |
| reprice | 每 N 分钟(默认31±抖动) | 在架商品全量重估；仅当变化超过最小步长才提交；频控限速 |
| zero_cd | 每日 HH:MM(默认23:30) | UU 可转租列表→白名单过滤→开启0CD |
| market_snapshot | 每 20 分钟 | UU 行情 topN 快照入库；模板级缓存复用 |
| value_anchor | 每小时 | ECO 全量价格 dump 单次拉取；与 UU 参考价合成价值锚点 V |
| reconcile | 每 10 分钟 | 期望货架 vs 双渠道实际货架 → 差异动作队列(见 M6) |
| order_sync | 每 10 分钟 | 两渠道出租订单增量入库；终态订单触发收益记账 |
| rollup_daily | 每日 00:10 | 昨日分渠道分品类收益汇总；资产快照 |

### AC-T1 dry-run
- 新策略首次执行必须 dry_run=true：完整走决策链但只写 `price_actions(dry_run=true)` 不调平台
- 面板策略页可切换 enable_real_execution

### AC-T2 护栏（全部任务强制）
- 价格边界 [min_rent, max_rent]；单次改价幅度 ≤ max_change_ratio(默认15%)
- 同一商品两次改价间隔 ≥ cooldown_minutes(默认30)
- ECO：解得三元组后派生押金 > deposit_cap_ratio×V 时拒绝该动作并告警
- UU：押金 < deposit_floor_ratio×V 时抬升至下限

## 4. 渠道路由规格

- 策略字段 route ∈ {uu_only, eco_only, both, uu_primary_eco_fallback}
- fallback 触发条件：UU token 失效 或 该商品在 UU 连续 N 天(默认7)无在架记录
- Reconciler 保证路由意图最终一致（在正确渠道上架、从错误渠道下架）

## 5. Open Questions
- ECO 改价接口是否支持只改租赁不改出售（PublishType=2 的行为边界）→ 待真机验证，结论记 platform-eco-api-notes.md
- UU 长租阈值(8天?)与 ECO(21天) 差异的统一建模口径 → pricing-spec.md §4
