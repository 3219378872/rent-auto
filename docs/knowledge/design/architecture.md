# 总体架构

```
┌─────────────────────────── frontend (React+Vite+TS) ───────────────────────────┐
│  Login / Dashboard / Inventory / Listings / Orders / Strategies / Channels / Audit │
└───────────────▲──────────────────────────────────────────┬──────────────────────┘
                │ REST /api/v1 (JWT)                       │ SSE: jobs/告警(后续)
┌───────────────┴──────────────────────────────────────────▼──────────────────────┐
│ backend (Go, 单二进制)                                                            │
│  api/     chi 路由 · JWT中间件 · 审计中间件 · handlers                              │
│  scheduler/ 任务调度(daily HH:MM + interval) · 频控(token bucket/渠道) · dry-run    │
│  pricing/  基线行情聚合 → 反馈控制器 → 渠道分化决策(UU押金直控/ECO三元组) → 护栏       │
│  bench/    价格基准中心: TemplateRegistry·价值锚点合成·行情快照查询                   │
│  recon/    Reconciler: desired vs actual 货架差异 → 动作队列                        │
│  analytics/ 收益记账·资产构成·年化ROI·分渠道分品类rollup                             │
│  audit/    审计写入与查询                                                          │
│  store/    pgx/v5 · migrations(嵌入, 启动自升级)                                   │
│  platform/ ChannelAdapter 接口                                                    │
│   ├─ uu/  AES-ECB+RSA 加密客户端 · 短信登录流 · 租赁端点集                          │
│   └─ eco/ SHA256withRSA 签名客户端 · 租赁四件套 · 库存 · 钱包流水 · 市场dump          │
└───────────────┬──────────────────────────────┬───────────────────────────────────┘
                │ HTTPS(限频+重试+抖动)          │
        ┌───────▼────────┐            ┌────────▼─────────┐
        │ openapi UU      │            │ openapi.ecosteam │        ┌────────────┐
        │ youpin898.com   │            │ .cn              │        │ PostgreSQL │
        └────────────────┘            └──────────────────┘        └────────────┘
```

## 模块职责边界

| 模块 | 只负责 | 明确不管 |
|---|---|---|
| platform/* | 协议、加密、签名、分页、错误码翻译 | 业务语义（什么该上架） |
| pricing | 给定输入算出 Decision（纯函数域） | 网络、持久化 |
| scheduler | 触发节奏、频控、dry-run 开关、并发隔离 | 决策逻辑 |
| recon | 差异计算与动作队列生成 | 执行动作（交回 scheduler） |
| analytics | 口径计算与 rollup | 数据采集 |

## 关键数据流

1. **改价链**：market_snapshot → bench.V → pricing.Decision(+factor) → guardrails → price_actions(dry_run?) → adapter.Reprice → listings.actual 更新
2. **对账链**：strategies.desired × adapters.actual → diff → actions[] → scheduler 执行 → 复核
3. **收益链**：lease_orders 终态 → analytics 记账 → daily_stats → dashboard API

## 可靠性设计

- 单实例运行（Postgres advisory lock 防双开）
- 任务 panic recover + 结构化日志(slog)；任务失败退避重试≤3，连续失败告警入库
- 渠道凭证失效：标记 channel unhealthy → 路由 fallback 生效 → 仪表盘告警条
- 全局限频器：UU 默认 3 rps、ECO 默认 2 rps + 端点级最小间隔（市场 dump ≥60s）

## 演进方向（非本期）
- ECO 回调/WebSocket 替代轮询；多 Steam 账号；出售域适配器
