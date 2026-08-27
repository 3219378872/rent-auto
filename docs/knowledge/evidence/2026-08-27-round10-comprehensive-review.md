# 2026-08-27 第四轮全面审查与修复（round10）

## 范围

六维度全面审查（后端正确性/并发 · 资金域 · 安全 · 前端 · 知识库一致性 · 工程卫生），
P0/P1/P2 全部清欠 + 文档类发现一并修复。审查方式：全量通读资金域与调度管线源码、
安全面定向扫描（凭证泄漏/限流/会话）、前端逐页核对、知识库五层交叉核对。

## 发现与处置清单

### P1（缺陷/合规，全部修复）

| # | 发现 | 修复 | 验证 |
|---|---|---|---|
| P1-1 | 静默吞错：`RunReprice`/`foldOrderEvents`/`runStaleScan` 对 `GetEffectiveStrategy`/`ParseParams` 失败无日志 continue，`loadQuotes` 失败返回 nil——DB 故障时任务假成功无痕 | 三处 + loadQuotes 补 Warn 日志（批次内 continue 保持，错误如实上报日志） | 编译+既有套件 |
| P1-2 | `bench.Round2` 本地复制版丢失 NaN/Inf 防护；`pricePtr` 对 NaN（`v<=0` 为 false）透传进 int64 未定义转换，可污染价值锚点 | 委托 `pricing.Round2`（唯一实现）；`pricePtr` 先 Round2 再判 ≤0；bench 测试补 NaN/±Inf 与 pricePtr 用例 | TestRound2/TestPricePtrNonFinite |
| P1-3 | 凭证变更审计空 detail，违反 security-spec「凭证变更必须带尾号指纹」：`channel.steam.creds_update`/`channel.eco.creds_update`/`channel.uu.login` | Steam 记 username+secret 指纹（SHA-256 前 12 位）；ECO 记 partnerId+key 指纹；UU 记 token 尾 8 位+手机尾 4 位（`VerifyUUSms` 改返回尾号，接口/实现/mock 三处同步） | api 套件回归 |
| P1-4 | Steam `AccessExp==0`（jwtExp 解析失败）永久短路刷新——api-notes round3 备案盲区 | `EnsureSession` 将 exp 未知视为需刷新，强制走刷新→失败回落重登录，并打 Warn；api-notes 已回写销项 | 编译（真机路径由 steam 套件 mock 覆盖会话逻辑） |
| P1-5 | `Server.trustList()` 懒初始化 `defaultTrust` 无锁写——并发请求下数据竞争 | 构造期预解析（NewServer 填充），`SetTrustProxies` 注释明确须在 serve 前调用 | go test -race 全绿 |
| P1-6 | `RandomSessionID` 用 math/rand——UU 短信登录挑战的会话门控标识属于安全用途 | 改 crypto/rand + panic 兜底（不可恢复故障显式暴露）；新增 domain 包首个单测 | TestRandomSessionID（200 次无碰撞） |

### P2（卫生，全部修复）

| # | 发现 | 修复 |
|---|---|---|
| P2-1 | `channels.AuditFn` 包级可变全局（隐式装配依赖） | 改 `Registry.SetAuditFn`/`SteamSession.SetAuditFn` 实例注入；main 装配点同步；architecture.md 措辞更新 |
| P2-2 | `scheduler/limiter.go` 与 `ratelimit.Bucket` 逐行重复 | ChannelLimiter 改用 `ratelimit.New`，删 limiter.go；ratelimit 补 4 项单测（立即/节流/取消/默认 rps） |
| P2-3 | `GET /templates` 无分页（全表返回无上限） | store 支持 limit≤0=全量（面板黑名单表依赖全量，保持缺省行为）、limit>0 钳制 200；openapi v0.6.0 补参数 |
| P2-4 | eco_delivery `accepted` 集合进程生命周期无界增长 | 10000 上限重置（重复 accept 幂等，仅日志噪音） |
| P2-5 | `var _ = domain.ChannelUU` 冗余 ×2；`Trigger` 不维护 failStreak | 删除；Trigger 对齐 runDue 记账 |
| P2-6 | Strategies.tsx 630 行巨型组件 | 拆分 `pages/strategies/{params.ts,fields.tsx,help.ts}`，主文件 ~300 行；行为零变更（既有 6 用例不动全绿） |
| P2-7 | 四页面重复分页样板（Inventory/Listings/Orders/Audit） | `lib/paged.tsx`：usePagedList（alive-guard/分页/错误）+ Pager；四页面重构 |
| P2-8 | App.tsx 500ms 轮询鉴权 | client 发 AUTH_EVENT 自定义事件 + storage 事件，事件驱动零轮询 |
| P2-9 | eslint `no-explicit-any` 关闭（现状零 any，规则放行无约束） | 恢复 error，全量 lint 过 |
| P2-10 | 前端测试缺口：Orders/Inventory/Audit/App 零测试 | 补 13 用例（分页边界/成本校验/过滤编码/鉴权门控事件/登出） |

### 文档一致性（5 项，全部修复）

| # | 发现 | 修复 |
|---|---|---|
| D-1 | ADR 撞号：adr-0001 内嵌 0002/0003 两条决策，与独立文件 adr-0002/adr-0003 重号 | 拆分为 adr-0001（纯 Go）/adr-0007（Postgres）/adr-0008（双通道）；**不重写历史证据引用**，新号顺延并在文内+architecture 说明；architecture.md 新增 ADR 索引 |
| D-2 | ECO api-notes 无「待办」节，m2b 声称登记的 7002 项实际未落 | 增设待办节：#E1 7002 滚动窗口 / #E2 PublishType=2 改价边界 / #E3 长租阈值配置化 |
| D-3 | steam api-notes exp=0 盲区条目已过时 | 标记 round10 已修复（删除线+说明）；uu api-notes #15 更新 unknown 落库现状 |
| D-4 | evidence README 状态句滞后（未含 08-25 条目） | 更新状态句 |
| D-5 | 分页口径三处不一致（functional-spec ≤100 / openapi ≤100+参数 200 / store 钳 200） | 统一 ≤200；openapi v0.5.0→v0.6.0；server version 同步 0.6.0 |

### 评审接受（记录不改）

- `sessionEpoch` 每请求一次 DB 读：单管理员规模，注释已备案
- `SetUUHTTPClient` 无锁写：仅测试装配期调用，生产路径不触达
- `bench.Median` 无 NaN 防护：无生产输入路径（锚点合成走 SQL）
- X-Real-IP 不做格式校验：仅在可信代理（Caddy）前提下采用

## 执行的命令与结果

```
make gate PG_HOST_PORT=25432          # GATE PASSED（含 -tags=integration 集成测试、-race）
make migrate-check                    # TestMigrationsUpDown PASS
golangci-lint run ./...               # 0 issues（v2.13.1）
pnpm exec tsc --noEmit                # 0 error
pnpm exec eslint . --max-warnings=0   # 0（含恢复的 no-explicit-any）
pnpm exec vitest run                  # 10 文件 33 用例全绿（round10 新增 13）
pnpm exec vite build                  # 成功
```

逐包覆盖率（门控 ≥70%）：pricing 92% / platform 100% / uu 77% / eco 78% /
steam 76% / recon 79% / analytics 81% / auth 90% / secrets 79% / config 80%
（对照 round9：pricing +1、config +4、recon -1、uu 持平——重构迁移带来的
语句分布变化，全部高于卡点）

## 已知偏差与理由

- ADR 新号 0007/0008 非决策时间序：为避免重写历史证据文档中的既有 ADR-000x
  引用（evidence 文档是不可变审计记录），拆分采用「新号+文内说明」方案，
  完整时序以 architecture.md ADR 索引为准
- `/templates` 缺省仍为全量返回：面板黑名单表与模板选择器依赖完整清单，
  分页仅作为查询参数逃生舱（契约见 openapi v0.6.0）

## 移交后续

1. 真机校订：UU api-notes #14（lease/out/list 资产字段）、#15（状态码映射补全）、
   App 网关 4 项；ECO api-notes #E1/#E2/#E3
2. Strategies HELP_ROWS 与字段定义仍有语义重复（低优先，拆分后已收敛到独立文件）
3. 因子参数面板化、ECO 回调/WebSocket、出售域适配器（round8 移交清单不变）
