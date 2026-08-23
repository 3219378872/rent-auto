# M7 统计分析 — 验证证据

日期：2026-08-23 ｜ 分支：feat/m7-analytics

## 交付范围

- `internal/analytics`（纯逻辑域）：
  - AnnualizedROI 年化公式（观测天数下限 1 天防爆炸、Inf/NaN 防护）
  - RollupTerminalOrders：终态未记账订单 → daily_stats 增量合并，幂等
  - BuildDashboard：资产三要素（库存锚点估值/在外押金/钱包）、总收入+分渠道+
    今日、在租件数、年化 ROI、分类成本收益率、30 天收益序列；钱包快照留痕
- `store/analytics.go`：终态订单联查(模板品类+库存成本)、日统计 upsert、
  各类聚合查询、wallet_snapshot 流水
- 迁移 0004：inventory_items.cost_updated_at（成本录入时点=年化观测起点）
- API：GET /api/v1/dashboard（JWT 保护）
- 调度接线：orders_sync 任务尾部自动执行收益记账

## 执行证据

```
单元：年化公式 (500,10000,100d)=0.1825 ✓；单日起步下限 ✓；负收益有限 ✓
集成 TestRollupAndDashboard：
  done 订单入账 → daily_stats(uu,步枪,+14) ✓；重复 rollup 幂等=0 ✓
  dashboard: 库存=100(锚点) 押金uu=120 钱包eco=555.5 总和精确 ✓
  分类收益率 14/80=0.175 ✓；ROI 为负(净亏损)且有限 ✓；序列含今日点 ✓
全仓 -p1 集成 12 包全绿 ✓
```

## 过程中修复的缺陷

- UpsertLeaseOrder 终态插入误置 income_recorded=true → rollup 永远空转（改为恒 false 入库，
  由记账流程翻转）；该缺陷由 M5 的集成测试环境在本里程碑首次暴露并修复
- FirstCostDate 引用不存在的 created_at 列 → 新增 cost_updated_at 迁移修正口径

## 口径提醒（对齐 data-model.md）

- 收入=done/bought_out 订单 order_amount；成本=当前持有库存 cost_basis；
  年化=(收入−成本)/成本×365/观测天数——持有期未售出成本全额参与，保守估计
