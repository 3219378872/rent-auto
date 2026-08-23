# M4 定价引擎 — 验证证据

日期：2026-08-23 ｜ 分支：feat/m4-pricing-engine

## 交付范围

- `internal/pricing`（纯逻辑域，零网络零存储）：
  - Round2 / Baseline（Steamauto get_lease_price 行为移植，参数化 K1/K2/K3/TopN/MinLeaseRatio）
  - NextFactor 反馈控制器（rent_success/stale/bought_out/reset，[FMin,FMax] 封顶）
  - Decide 统一决策：锚点校验→基线×因子→绝对边界→冷却→改价幅度帽(±15%)+噪声地板(2%)→
    渠道分化（UU 押金直控+下限；ECO 三元组+派生押金上限护栏）
  - ParseParams 深合并（默认→全局 jsonb→模板 jsonb）

## 黄金测试算术追踪

fixture quotes = [(2.0,1.8,200),(2.2,2.0,210),(2.5,2.2,220),(3.0,-,260)]：
- shorts=[2.0,2.2,2.5,3.0] mean=2.425 ×0.97=2.35225 → floor shorts[0]=2.0 → **short=2.35**
- longs=[1.8,2.0,2.2] mean=2.0 ×0.95=1.9; short×0.98=2.303 → min=1.9; floor longs[0]=1.8 → **long=1.9**
- deposits=[200,210,220,260] mean=222.5 ×0.98=218.05 vs min 200 → **deposit=218.05**
边界：空行情/全零行情无基线；无 longs→long=short−0.01；单条行情 floor 主导；
比例下限生效；ECO 最小天数 8 由 Capabilities 注入强制。

## 执行证据

```
go test ./internal/pricing/ -coverpkg=./internal/pricing
→ ok, coverage: 90.5%
护栏矩阵表驱动：no_anchor/no_baseline/cooldown/noise/deposit_cap_exceeded 全命中
改价幅度帽：raw 5.0 vs old 2.0 → clamp 至 2.30 (+15%) ✓
深合并：模板嵌套覆盖全局、未触字段保持默认 ✓
```

## 设计说明

- ECO 基线复用 UU 公开租赁行情快照（同 hash_name 跨平台基准），渠道差异仅体现在
  因子独立演化与押金派生规则——这正是"跨平台价格基准"的落地形式
- 改价噪声地板 NoiseRatio=2% 防止无意义高频提交（规格 §3 护栏的补充实现）
