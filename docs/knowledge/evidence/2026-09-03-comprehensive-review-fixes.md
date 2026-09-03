# 2026-09-03 全面深度审查修复轮（P0×4 + P1×N + P2×N）

> 范围：2026-09-03 深度审查报告全部发现。分支 `feat/fix-all` → main（ff-only）。

## P0（4 项，全修）

| # | 问题 | 改动 |
|---|---|---|
| P0-1 | reconcile 模板级 dry-run 被忽略（仅看全局） | `main.go:reconcileFn` 按 hash 查 GetEffectiveStrategy 分 dry/live 两批 Execute；失败 fail-closed |
| P0-2 | MarkMissingListings 把 leased 翻 none | `store/collect.go` 只翻 active + 注释；`recon PlanFrom` publish/surplus 预算只计非 leased |
| P0-3 | 发货/0CD 无 dry-run 门 | uu/steam/eco/zero_cd 在 dry 下 log+`dry_run_skip` 审计返回 nil |
| P0-4 | Registry 并发 map 写/Type race | auditFn/drop/uuOptions/SetUUHTTP 加锁；server trust/epoch 加锁；race 单测 |

## P1（核心，全修）

- Round2：快照/ECO dump/inventory/template/fund/wallet/priceAction 全经 round2Money；wallet ref 纳秒+序列
- 租中改价：ListRepriceCandidates 仅 active；UpdateListingDecision 不碰 actual_synced_at
- 因子：容差 1e-4；long 用 term 快照、legacy 回落 join MaxDays；未知不伪造信号
- 分析：Income.Total 净口径；AssetValuation 去 mark 兜底；HeldDeposits 仅 leasing
- 平台：ECO dump 垃圾报错；UU 登录/改价走信封校验；RepriceLease 长度门控；5050/1110205 新哨兵；Steam X-eresult+重定向上限；ECO 6001 抖动；UU 时间兜底 CST
- 安全：JWT 拒 none/验 sub+iat漂移/store fail-closed；MASTER_KEY 统一 500；上游原文不直回；429 审计；job/channel 写限流；verify 限流；安全头中间件；sms ticket 透传

## P2（全修）

- 护栏顺序：绝对界→ratio→重钳；押金cap/floor 移 cooldown/noise 前
- openapi v0.8.0：global channel_route 双收；ListingRow/Inventory/Dashboard/UUSms 补字段；pageParams 钳 200；ListAudit 钳 200
- 文档：契约路径、SSE→轮询、api-notes 哨兵/时间回写
- 可靠性：advisory 心跳；Stop 超时；WriteTimeout 120s；Sleep/After ctx 化；goroutine recover

## 验证

- `make gate` GATE PASSED（含 -tags=integration，-race）
- 后端集成全绿；覆盖率：pricing 92 / platform 100 / uu 81 / eco 81 / steam 82 / recon 81 / analytics 81 / auth 93 / secrets 79 / config 87
- 前端：tsc/eslint/vitest 35 passed/vite build 全绿
- 无迁移变更

## 已知偏差

- legacy 无 term 快照行依赖 join MaxDays 回落（注释说明，非精确下单时 D）
- HSTS 仍留 Caddy 层；直连 :8080 仅基础头
