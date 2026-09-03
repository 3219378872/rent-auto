# 2026-09-03 深度审查修复轮（P1×6 + P2×10）

> 范围：2026-09-03 全面审查报告的全部 P1（6 项）与 P2（10 项）+ 文档微差。
> 分支：`feat/fix-review` → main（ff-only）。

## 修复清单

| # | 问题 | 改动 |
|---|---|---|
| P1-1 | UU 信封缺 `Code` 判成功（fail-open） | `uu/client.go:decodeEnvelope` 缺键直接报错；`client_test.go:TestEnvelopeMissingCodeFailsClosed` |
| P1-2 | `ErrAuthExpired` 永不进风控冷却 | `jobs_reprice.go:penalize` 接入 `ErrAuthExpired`→30min 冷却（ECO 4004/5005、UU 84101 即刻生效） |
| P1-3 | `config.JWTTTL` 死字段 | `JWT_TTL` env 解析（默认/非法回落 24h，超 24h 钳制）+ `api.NewServerWithTTL` + `main.go` 接线；`config_test.go:TestJWTTTLParsing` |
| P1-4 | pricing-spec §5 扁平示例与嵌套实现脱节 | spec 示例改嵌套（含 `noise_ratio`、route 归属列说明）+ `Validate` 说明 |
| P1-5 | UU 时间 UTC 直解（系统性早 8h） | `uu/adapter.go:parseUUTime`（CST 墙钟，`ParseInLocation`）+ `TestParseUUTimeIsCSTWallClock`；api-notes 增时间口径节 |
| P1-6 | 变化帽后可突破绝对界 | `engine.go:Decide` cap 后重钳 `min/max_rent`；`TestDecideChangeCapRespectsAbsoluteBounds` |
| P2-1 | recon delist 无条件 penalize | 仅哨兵错误（限频/风控/UK过期/凭证过期）进冷却；publish 同理收敛；单测改用 `ErrRateLimited` 并增确定性失败不惩罚断言 |
| P2-2 | factor 72h 窗漏迟同步终态单 | `factorOrderWindow` 72h→100d（对齐 orders_sync 上限，ADR-0004） |
| P2-3 | reprice 策略查询失败面板无感知 | `stratErrs` 聚合 `errors.Join` 返回，`LastError` 可见 |
| P2-4 | 登录 500 无日志/无失败计数 | `Log.Error` + `fail` + `login.failed` 审计；sign 失败走 `internalError` |
| P2-5 | 登录成功不清 per-IP 桶 | `reset(key, ip)` 双清 |
| P2-6 | UU 短信通道无节流/失败审计 | per-IP 10 次/10min（`smsAllow`，429）+ `channel.uu.sms_failed` 审计 |
| P2-7 | 无 MASTER_KEY 仅 Warn | 提级 `Error` 并明示三渠道不可用 |
| P2-8 | ECO publish 押金未 Round2 / 写库多点靠 DB 兜底 | `derivedDeposit` 加 `Round2`；`store/money.go:round2Money`（避开 store→pricing 依赖倒置）并收敛 `UpsertLeaseOrder/SetCostBasis/UpsertListingFromShelf/RecordPublishedListing/UpdateListingDecision` |
| P2-9 | `Params` 无值域校验 | `Validate`（FMin≤FMax、rent 区间、比例≥0）+ `TestParseParamsRejectsBadRanges` |
| P2-10 | 存量 `finished_at` 旧值赢 | 改与 started/due 一致的新值赢（`COALESCE(EXCLUDED,…)`），治愈 ECO 错位存量 |
| 文档 | UU 注释"未知码落空串"、data-model `strategy_id FK` 措辞 | 注释/口径已订正；security-spec 补 `JWT_TTL` 与短信限流条 |

## 验证

- `gofmt -l` 无输出；`go vet ./...` 通过；`go build ./...` 通过；`golangci-lint run` 0 issues
- 后端：`go test -tags=integration -p 1 ./... -race` 全绿（含集成，TEST_DATABASE_URL=rentauto_test@15432）
- 覆盖率门控（70%）：pricing 92 / platform 100 / uu 80 / eco 79 / steam 78 / recon 80 / analytics 81 / auth 90 / secrets 79 / config 87
- 前端（代码未动，主仓复核）：`tsc`、`eslint --max-warnings=0`、`vitest` 35 passed、`vite build` 全绿
- 无迁移变更：`migrate-check` 不适用

## 已知偏差

- UU 时间 CST 假设：与 ECO 同口径（UTC+8 墙钟）；若真机证明 UU 原生 UTC，按 api-notes 批注回切
- `daily_stats.asset_snapshot` 无写者、面板钱包取实时值而非 `fund_flows` 末态：本次仅记录，未立项（属已知缺口下限）
