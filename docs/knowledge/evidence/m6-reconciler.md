# M6 Reconciler 对账与渠道路由 — 验证证据

日期：2026-08-23 ｜ 分支：feat/m6-reconciler

## 交付范围

- `internal/recon`：
  - PlanFrom 纯函数对账核心（快照输入→动作计划），Planner.Plan 负责取数
  - 渠道路由解析矩阵：uu_only / eco_only / both / uu_primary_eco_fallback(健康感知)
  - 发布决策复用定价引擎（无锚点/无基线/护栏不通过则跳过该 publish）
  - Executor：dry-run 语义、逐动作审计(shelf.publish/delist)、失败计数
- `store`：RoutableInventory(库存×模板×有效策略路由)、AllActiveListings、
  RecordPublishedListing / MarkListingDelisted 回写
- scheduler 新增 reconcile 任务(10±1min)；main.go 组装 Planner+Executor

## 执行证据

```
go test ./internal/recon -coverpkg=./internal/recon → ok, coverage: 76.8%
路由矩阵表驱动 7 用例 ✓（含 fallback 双向切换）
PlanFrom 场景：双渠道补发布、错架下架(not_routed)、失效切换下架原因、决策失败拦截 ✓
Executor 路径：正常/平台拒绝/适配器缺失/delist 失败/审计计数 ✓
```

## 设计说明

- 幂等性：publish 仅对"期望渠道上无 actual 记录"的资产触发；delist 仅对"在架但不在
  期望集"触发；执行结果由 shelf_sync 周期复核收敛，单次失败不阻塞后续周期
- fallback 的 delist 理因区分 not_routed 与 uu_unhealthy_failover，便于面板解释
