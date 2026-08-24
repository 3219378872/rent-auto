# 2026-08-24 全面审查与修复轮（第二轮综合审查）

## 范围

对 M0–M11 全量交付后的第二次全面审查：5 个专项并行深审（平台适配层 / 定价与数据域 /
编排与安全层 / 前端 / 五层知识库一致性）+ 门控实测 + P0/P1 发现逐条人工复核源码确认。
本文件同时归档本轮审查结论与随之落地的修复。

## 审查结论摘要

- 门控全绿（含集成测试 + -race + 逐包覆盖率卡点）；AGENTS「当前状态」覆盖率数字与实跑吻合
- 知识库健康度 7/10：evidence↔实现互证质量高；functional-spec 任务表与 openapi.yaml 滞后待回写
- 前端 7.5/10：strict TS 零 any、类型与后端零漂移；spec 承诺的监控类 UI 有缺口
- 加密/签名/SQL 参数化/dry-run 门禁等既有结论全部复核通过

## 本轮修复清单

| 级别 | 发现 | 修复 | 验证 |
|---|---|---|---|
| P0 | compose Postgres 绑定 0.0.0.0 + 弱默认密码（security-spec:26 违反；实机容器实证暴露） | `deploy/docker-compose.yml` 固定绑 `127.0.0.1:`；runbook 新增端口纪律节 | compose 配置评审 |
| P1 | ECO RepriceLease 结果缺省乐观置成功（adapter.go fail-open） | 缺省 false + 结果数组不等长即 ErrPartialFailure | TestRepriceMissingResultsFailClosed |
| P1 | UU OffShelf 不解码业务信封，200+非0码视为下架成功 | decodeEnvelope+checkEnv("offshelf") | TestOffShelfBusinessFailureEnvelope ×3 反例 |
| P1 | UU EnableZeroCD 同上 | checkEnv("zerocd-open") | TestEnableZeroCDBusinessFailureEnvelope |
| P1 | Steam 确认器 creator_id 无条件后缀匹配可误确认无关交易 | 改精确相等（上游 steampy match_end=False 默认）；sleepShort 可注入 | TestAcceptOfferConfirmationExactMatchOnly |
| P1 | factor_events 折算与打标两步非原子，崩溃后重复步进（连租每次虚高+3%） | 新增 store.ApplyFactorFolds 单事务"折算+打标"，仿 RecordIncomeBatch 模式 | 既有因子集成套件全绿 |
| P1 | TryAdvisoryLock 在池化连接A取锁、连接B解锁——解锁无效、持锁连接可被池回收 | 先 Acquire 专用连接，同连接 try/unlock | cmd/server 双开路径回归 |
| P1 | handleStrategyUpdateGlobal 三条 UPDATE 无事务且部分失败也记成功审计 | store.UpdateGlobalStrategy 单事务；审计记录变更值，失败不记成功 | api 包测试全绿 |
| P2 | Trigger 使用请求 ctx，浏览器断连腰斩平台调用 | context.WithoutCancel + 10min 上限（与 runDue 一致） | 编译+既有调度测试 |

## 执行的命令与结果

```
make gate  # 修复前基线：GATE PASSED（unit+integration, PG_HOST_PORT=25432）
go build ./... && go vet ./...            # 修复后：通过
go test -tags=integration -race -count=1 \
  ./internal/platform/... ./internal/store/ ./internal/scheduler/ ./internal/api/
# → 7 包全部 ok
make gate                                  # 修复后：GATE PASSED
```

新增 mock 反例 6 个全部执行通过（见上表验证列）。

## 已知偏差与理由

- **POSTGRES_PASSWORD 未强制 `:?`**：保留 dev 弱默认以维持「make dev-up 开箱即跑」承诺；
  风险由回环绑定消除。跨机访问必须走容器内网，已在 runbook 明示。
- **Steam details 页校验仍未实现**：本轮仅收紧为精确匹配（最小安全改动）；
  details 页正则需真机 fixture，platform-steam-api-notes §3.3/§5 待办已更新表述。
- **UU OffShelf 信封缺 Code 字段时仍按成功处理**：与全客户端约定一致
  （decodeEnvelope 对无 Code 信封返回 codeOK），真机响应形态留待校订。

## 审查遗留（未在本轮修复，按优先序）

1. recon 结构性缺口：hash_name 归并丢多拷贝 / delist 不排除 leased / 孤儿 listing 永不下架 /
   publish 结果不写回（RecordPublishedListing 死代码）
2. pricing NaN/Inf 防线缺失（Decide 入口 finite guard）
3. functional-spec §3 任务表与 jobs.go 双向漂移；openapi.yaml 停在 M1；
   APP_MASTER_KEY 缺失行为 spec↔代码相悖（拒绝启动 vs Warn 降级）
4. HTTP 面：MaxBytesReader/Server 超时/X-Real-IP 可信代理判定/loginLimiter 清理/JWT 吊销
5. 前端：Dashboard 渠道健康告警条、Listings 决策依据列、Channels 凭证保存后清空、csvCell 测试
