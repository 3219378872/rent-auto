# 2026-09-25 代码质量审查

后续：CQ-01～03 的实现与回归见 [同日修复证据](2026-09-25-code-quality-remediation.md)。
下文保留审查当时的失败结果与基线，不作为修复后状态。

## 范围与结论

- 审查基线：`11585131dac637459d89f8548cc8d32dc3054d0f`；开始时 main 工作区干净。
- 检查了调度并发、货架/库存/订单同步、recon 计划与执行、反馈因子、改价写回、收益投影、前端请求/会话、部署与测试门控。重点是跨任务、跨挂单生命周期的一致性；不是逐文件无遗漏证明。
- 确认 **P1 × 1、P2 × 2**，四个定向用例在当前源码上失败；均为本轮本地复现，而非仅引用历史记录。
- 业务实现未修改。复现用例以 `.txt` 保存于证据目录，不进入正常测试发现范围，不将已知缺陷固化为成功断言。
- 现有完整 `make gate` 通过，不能据此宣称上述缺陷已修复或长期无人值守已验收。

## CQ-01 / P1：旧货架快照覆盖快照采集后的平台写入结果

**位置**：`backend/internal/store/collect.go:34-42,61-66`；调用链为
`scheduler/jobs.go` 的 `shelf_sync` → `bench.SyncShelf` → upsert + missing pass。
`scheduler/scheduler.go` 仅阻止同名任务并发，不阻止 shelf_sync 与 reconcile/reprice 交错。

**触发条件与后果**：

1. 货架请求已取得非空旧快照；在结果入库前，reconcile 成功上架并经 `RecordPublishedListing` 写入新 active 行。
2. `MarkMissingListings` 只按渠道、状态及 seen refs 更新，没有快照开始时间或版本限制，新行不在旧快照中，被改成 `none`。下一轮 recon 无法看到该真实挂单，存在重复上架尝试风险；是否由平台拒绝重复上架未验证。
3. 同一问题还影响改价：若 `UpdateListingDecision` 已写回新租金 2.30，旧快照 upsert 无条件将其覆盖为 2.00，却保留新的 `last_reprice_at`。面板与后续护栏基准暂时不再对应已成功提交的价格。

**本轮证据**：fake adapter 在 `LeaseShelf` 返回旧快照之前插入成功写回，真实 PostgreSQL + `bench.SyncShelf` 验证；无实际平台调用。

```text
listing published after snapshot: actual_state=none; want active
reprice completed after snapshot: rent=2.00; want 2.30
```

**建议**：为货架观测与上下架/改价写回建立统一的版本或串行化边界；既保护 missing pass，也保护已存在行的状态/价格更新。仅把 upsert 与 missing 放进一个事务不能阻止旧观测覆盖先完成的新写入。回归应覆盖 publish、reprice、delist 与 shelf 的交错。

## CQ-02 / P2：旧订单向替代挂单重复归因

**位置**：`backend/internal/store/factor.go:26-33`；`scheduler/factor.go` 遍历连接结果并按 ListingID 分别折算。

**触发条件与后果**：同渠道同资产存在历史挂单和重新上架生成的新 goods_ref，旧终态订单尚未折算时，连接条件仅为 `(channel, asset_id)`，缺少订单对应的 listing/lifecycle 身份。一笔订单同时命中两行，替代挂单从冷启动 1.00 变成 1.03；`factor_applied` 的订单级标记无法阻止同批次一对多折算。此后 reprice 会读取受污染的新因子。

**本轮证据**：真实 PostgreSQL 建立旧挂单 → 终态订单 → 下架 → 同资产新 goods_ref，再执行真实 `RunFactorEvents`：

```text
one terminal order produced 2 listing matches
old factor=1.03, replacement factor=1.03
replacement listing inherited a pre-publish order event: factor=1.03; want 1.00
```

该 fixture 验证相同 asset_id 的挂单更替；未假定所有 Steam 归还都会保留原 asset_id，也未验证真实平台发生频率。

**建议**：在订单归属确定时绑定挂单生命周期，保证单个反馈事件有唯一归属；历史歧义数据采用明确迁移/降级策略。避免仅选最新挂单，否则仍会把旧订单归给新挂单。加入旧单延迟同步、替代挂单与批次分页边界用例。

## CQ-03 / P2：迟到的旧请求 401 注销新会话

**位置**：`frontend/src/api/client.ts:57-68`。

**触发条件与后果**：请求发出时携带 old-token，期间用户重新登录（或其他标签页写入 new-token），旧请求随后返回面板 `401/unauthorized`。处理器直接 `clearToken()` 并跳登录页，没有比较被拒绝的 tok 与当前令牌，新登录被误注销。页面数据层的 active 标记不保护 API client 内部的全局鉴权副作用。

**本轮证据**：Vitest 中延迟 fetch，断言请求确实携带 old-token，再写入 new-token 后放行 401；预期保留 new-token，实际令牌为空。当前测试在令牌断言处失败，未依赖后续跳转断言证明缺陷。

**建议**：处理 401 时仅撤销仍与被拒绝请求相同的当前令牌，并保护跳转副作用；补入重登录/跨标签页及旧响应迟到测试。

## 验证记录

使用本轮创建的可丢弃 PostgreSQL 16 容器 `rent-auto-quality-20260925`，仅监听
`127.0.0.1:35432`，测试库 `rentauto_quality_test`。未连接业务数据库，未使用生产凭证、未执行真实平台写入或生产发布。

在独立任务 worktree 中执行：

```bash
pnpm --dir frontend install --frozen-lockfile --offline
TEST_DATABASE_URL='postgres://rentauto:rentauto@127.0.0.1:35432/rentauto_quality_test?sslmode=disable' make gate PG_HOST_PORT=35432
```

结果：格式、golangci-lint、vet/build、后端 race + 隔离集成、迁移 up/down/up、逐包覆盖门槛，以及前端 tsc/ESLint/51 项 Vitest/Vite build 全部通过。后端总体覆盖率 77.4%；纯逻辑域如下。

| 包 | 覆盖率 |
|---|---|
| pricing | 93% |
| platform | 100% |
| platform/uu | 81% |
| platform/eco | 83% |
| platform/steam | 83% |
| recon | 91% |
| analytics | 82% |
| auth | 93% |
| secrets | 79% |
| config | 87% |

随后临时将证据用例放入对应测试目录，使用同一隔离库顺序运行后端三个用例、独立运行前端一个用例：**3 + 1 均在预期业务断言失败**，未出现构建/环境失败。复现完成后删除临时测试文件，保留证据副本。

```bash
cp docs/knowledge/evidence/assets/2026-09-25-code-quality/quality_probe_test.go.txt backend/internal/scheduler/quality_probe_test.go
cp docs/knowledge/evidence/assets/2026-09-25-code-quality/quality_probe.test.ts.txt frontend/src/api/quality_probe.test.ts
# 在 backend 目录，显式传入可丢弃 TEST_DATABASE_URL：
go test -tags=integration ./internal/scheduler -run '^TestQuality' -count=1 -race -v
# 在 frontend 目录：
pnpm exec vitest run src/api/quality_probe.test.ts
# 完成后在仓库根目录删除刚复制的两个临时文件。
```

证据资产：

- [完整 gate 日志](assets/2026-09-25-code-quality/gate.log)
- [后端复现用例](assets/2026-09-25-code-quality/quality_probe_test.go.txt)、[失败日志](assets/2026-09-25-code-quality/probe-backend.log)
- [前端复现用例](assets/2026-09-25-code-quality/quality_probe.test.ts.txt)、[失败日志](assets/2026-09-25-code-quality/probe-frontend.log)

未执行本轮浏览器/设备验收、真实平台验收、容量压测及连续运行验收。建议优先修复 CQ-01，再处理 CQ-02/CQ-03，并将相应行为断言纳入正式回归。
