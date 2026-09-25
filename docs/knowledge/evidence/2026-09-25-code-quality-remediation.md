# 2026-09-25 代码质量审查修复

## 范围与状态

修复 [同日审查](2026-09-25-code-quality-review.md) CQ-01～03（P1×1/P2×2）。
起点为 `add07e5`，在 `feat/fix-quality-20260925` 独立 worktree 实现。
新增迁移 **0011** 与 **ADR-0013**，不改变对外 API 或平台客户端协议。

本轮完成实现、正式回归、规格/设计/实现文档同步及本地完整门控。
未部署生产，也未在真实 UU/ECO/Steam 平台执行写操作。

## 修复行为

| 问题 | 修复后的行为 | 关键实现 |
|---|---|---|
| CQ-01 旧货架覆盖新写入 | 请求前取得数据库观测时间；整个快照原子应用，upsert/missing 均保护后续上下架/改价；倒序快照整体丢弃；新快照仍正常收敛 | `bench.SyncShelf`、`store.ApplyShelfSnapshot`、`shelf_observations`、写回数据库 wall time |
| CQ-02 订单归属一对多 | 在同步事务内绑定唯一 factor_listing_id，待补全事件重试；时间界限排除未来挂单及已退架区间；歧义事件不折算、不占可执行批次；提交前核对订单绑定及版本 | `store.UpsertLeaseOrder`、`UnhandledFactorOrders`、`ApplyFactorFolds`、`scheduler.foldOrderEvents` |
| CQ-03 旧401注销新登录 | 响应体解析完成后，只有被拒绝的请求令牌仍等于当前令牌时才清除会话并跳转 | `frontend/src/api/client.ts` |

货架原子性包含模板/挂单 upsert、缺失标记与渠道水位；任一写入失败整体回滚。
空货架熔断、leased 豁免及 dry-run/审计调用路径保留。
新上架商品不会受到旧终态订单对其他 goods_ref 的反馈折算影响。

## 回归与门控证据

先在未修复的 `add07e5` 上安装原审查的三个后端与一个前端用例，全部在预期业务断言失败：
新挂单变 none、新租金 2.30 被覆盖为 2.00、替代挂单 factor 变 1.03、新令牌被清空。
修复后将其保留为正式测试，并扩展到 **11 个后端集成回归 + 4 个前端回归**。

- `scheduler/quality_regression_integration_test.go`（4）：publish/reprice/delist 与货架快照交错；旧订单绑定、替代挂单不折算及重复周期幂等。
- `store/shelf_snapshot_integration_test.go`（3）：倒序响应不能补回行；正常新观测更新/清退；中途失败原子回滚并允许重试；leased 与空货架熔断。
- `store/factor_binding_integration_test.go`（4）：旧单延迟到达、新旧时间归属、歧义事件不阻塞 limit=1 批次、时间补全；延迟货架/首次观测稳定/空资产不绑定；并发订单校订使旧因子计划回滚；0011 保留历史 factor 与已应用标记。
- `frontend/src/api/session_race.test.ts`（4）：同页重登录、跨标签页令牌更新、匿名旧请求、延迟401响应体；旧响应既不清令牌也不触发鉴权事件或跳转。

原 `TestFinancialMigrationRepairsLegacyProjection` 从“回退最新一步”改为回退至目标
0010 之前，确保新增迁移后仍在验证真实的旧财务数据升级过程；没有降低原断言。

测试使用专用 PostgreSQL 16 容器 `rent-auto-quality-fix-20260925`，仅映射
`127.0.0.1:35432`，库名 `rentauto_quality_fix_test`；未连接业务数据库。

```bash
# worktree 根目录
TEST_DATABASE_URL='postgres://rentauto:rentauto@127.0.0.1:35432/rentauto_quality_fix_test?sslmode=disable' make gate PG_HOST_PORT=35432

# 此前的定向验证，在 backend 目录：
TEST_DATABASE_URL='postgres://rentauto:rentauto@127.0.0.1:35432/rentauto_quality_fix_test?sslmode=disable' go test -tags=integration -p 1 ./internal/scheduler ./internal/bench ./internal/store -race -count=1
# 在 frontend 目录：
pnpm exec vitest run src/api/client.test.ts src/api/session_race.test.ts
```

完整 gate **通过**：gofmt、golangci-lint 零告警、vet、build、全后端 race + 集成、
迁移 up/down/up、纯逻辑包覆盖率门槛、tsc、ESLint、前端 **11 文件 / 55 测试**、Vite build。
后端总体覆盖率 77.4%；pricing 93%、platform 100%、uu 81%、eco 83%、steam 83%、
recon 91%、analytics 82%、auth 93%、secrets 79%、config 87%。

日志：

- [修复前后端失败](assets/2026-09-25-code-quality-remediation/before-backend.log)
- [修复前前端失败](assets/2026-09-25-code-quality-remediation/before-frontend.log)
- [修复后后端定向验证](assets/2026-09-25-code-quality-remediation/targeted-backend.log)
- [修复后完整 gate](assets/2026-09-25-code-quality-remediation/gate.log)

## 迁移与验证边界

- 0011 添加 `listings.retired_at`、`lease_orders.factor_listing_id/factor_observed_at`、
  `shelf_observations` 及索引。存量 factor_observed_at 来源于既有 updated_at，
  不声称恢复了真实首次观测；旧退架时间保持未知，已应用事件不重放。
- 缺少时间/身份的历史事件可能继续未绑定；任务记录数量告警，后续事实补全后重试。
  唯一候选属于本地历史证据约束，不能证明本地未曾记录的其他平台挂单不存在。
  不自动回算此前可能已污染的历史因子，避免用不完整事实重写经营状态。
- 数据库观测边界修复本地调用交错，不能替代上游平台版本号或线性一致性保证；
  在本地写入之后才发出的请求若仍收到平台缓存旧值，不属于本轮可识别的时间倒序。
- 同一 goods_ref 对应现有 listing 主键；新 goods_ref 为独立归属。平台复用标识的额外
  生命周期语义未被本轮真实平台验证。
- 前端为受控 fetch/DOM 回归；本轮未执行真实浏览器多标签页、真实平台、容量或长稳验收。
  发布/回退与歧义订单处置见 [runbook](../impl/release-runbook.md)。
