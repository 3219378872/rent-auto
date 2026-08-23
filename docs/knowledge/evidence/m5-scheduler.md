# M5 调度器与自动化 — 验证证据

日期：2026-08-23 ｜ 分支：feat/m5-scheduler

## 交付范围

- `internal/scheduler`：轻量调度核心（daily HH:MM / interval+jitter、panic 隔离、
  失败记录、手动 Trigger、状态查询）；标准任务集注册：
  reprice(31±1.5min) / inventory_sync(30m) / shelf_sync(10m) / orders_sync(10m) /
  market_snapshot(20m, UU行情topN→快照表) / value_anchor(1h, ECO dump+锚点重算) /
  zero_cd(daily 23:30)
- `internal/ratelimit`：每渠道 token bucket（UU 3rps / ECO 2rps），注入平台客户端
- `internal/channels`：凭证生命周期——UU 短信登录两段式 API、ECO 凭证(AES-GCM 加密落库)、
  adapter 构建/健康探测、行情与 0CD 能力透出
- 改价管线 `Deps.RunReprice`：候选(join 模板锚点/因子/最近动作时间)→有效策略深合并→
  30min 内快照重建基线→Decide→price_actions 全量留痕（含 skip 原因）→
  dry-run 拦截或真实调用→成功后回写 listings
- API：GET/POST jobs、channels health、UU sms/verify、ECO 凭证
- main.go 全量组装：advisory lock → 迁移 → registry → scheduler → HTTP，优雅停机

## 执行证据

```
单元：scheduler 注册/触发/错误记录/daily解析/jitter边界/限频器共享 ✓
集成（真实迁移链 0001→0003）：
  TestRepricePipelineDryRun —— 不触达适配器、价格不变、actions.dry_run=true ✓
  TestRepricePipelineRealExecution —— 适配器收到请求、listings 更新、
    last_reprice_at 落库、决策 jsonb 完整 ✓
全包 -p1 串行：api/auth/bench/config/platform-eco/platform-uu/pricing/
  scheduler/secrets/store 全绿 ✓
```

## 事故与修复（详见 evidence/incidents/2026-08-23-lost-migration.md）

发现 M3 的 0002_business.up.sql 从未入库（shell printf 失败+目录混乱），
此前测试被残留库表掩盖成假绿。本次在全新 schema 下全量重验并修复三个真实缺陷：
1. 迁移文件丢失（已重建 + 加载器 orphan-down 熔断）
2. InsertSnapshots 零值 CapturedAt 入库导致基线取不到行情
3. UpsertLeaseOrder 非终态订单 finished_at 空指针

## 已知偏差

- 渠道路由(uu_only/eco_only/fallback)字段已就绪，执行逻辑在 M6 Reconciler 落地
- 因子事件(rent_success/stale)的自动触发依赖订单状态变化检测，M7 记账时接入
