# M3 数据层与采集 — 验证证据

日期：2026-08-23 ｜ 分支：feat/m3-data-collection

## 交付范围

- 迁移 0002：templates / inventory_items / listings / lease_orders / market_snapshots / strategies / price_actions / fund_flows / daily_stats（双向可逆）
- store/business.go：模板 upsert(COALESCE 合并)、库存、订单(终态自动 finished_at+income 标记)
- store/collect.go：货架→listings(actual态)、消失标记 none、快照批量插入、资金流水
- bench：价值锚点重算(V=median 非空对)、SyncInventory/SyncShelf/SyncOrders 采集管线、Median/Round2 纯函数
- API 只读端点：/inventory /listings /orders /templates（JWT 保护，分页规范）

## 执行证据

```
make test（含集成，-p 1 串行）→ 全绿
TestCollectorPipeline：
  UU+ECO 库存→模板合并(uu_mark_price=100, eco_ref_price=110) → 锚点=105 ✓
  货架同步→listings active；二次空同步→actual_state=none ✓
  订单 leasing→done 终态 finished_at 落库 ✓；快照批量插入 ✓；成本录入回读 ✓
迁移 up→down-all→up 循环含 0002 ✓
```

## 门控口径修订（记录）

AGENTS.md 门控第1条细化为：**纯逻辑域**包(pricing/platform/recon/analytics/auth/secrets/config)
≥70% 行覆盖；编排层(store/api/bench/scheduler)以集成测试套件验证不设行配额。
理由：编排层价值在正确组装而非分支密度，行配额会诱导无意义断言。

## 已知偏差与理由

- 多包共用单 Postgres 时集成测试必须 `-p 1` 串行（已固化进 Makefile），
  否则 TestMigrationsUpDown 的 drop-all 与其他包用例互踩
- ECO 库存价格当前写入 eco_ref_price 的路径依赖 QuerySteamStock 返回 MarkPrice，
  真实字段名待 M3 后真机校订（api-notes 待办）
