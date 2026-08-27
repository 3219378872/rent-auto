# AGENTS.md — rent-auto 知识索引

> 本文件是面向 Agent 的**唯一入口**。开始任何任务前：先读本文件的「工作流规则」，
> 再按需跳转到五层知识库。完成任务前：必须更新对应层文档并在证据层归档。

## 项目一句话

Golang 后端 + React 前端的 UU/ECO 双渠道 Steam 饰品租赁自动化系统，
长期无人值守运行，按市场状态自动调整押金/租金使收益最大，并统计资产与收益。

## 五层知识库体系

```
意图层 intent    ──为什么做：愿景、用户故事、非目标（变化最慢）
   ↓ 驱动
规格层 spec      ──做什么：功能规格+验收标准、数据模型、API契约、定价规格、安全规格
   ↓ 约束
设计层 design    ──怎么做：架构、ADR、定价引擎设计、平台API逆向笔记
   ↓ 指导
实现层 impl      ──在哪改：仓库结构、开发环境、测试策略、运维runbook
   ↕ 互证
证据层 evidence  ──做没做好：测试报告、加密兼容性验证、回测结果、事故复盘
```

**读取顺序**：新会话 → 本文件 → `intent/vision.md` → 与任务相关的 spec/design 文档。
**写入责任**：
- 改行为 → 先查/改 `spec/`，再同步 `design/`，完成后在 `evidence/` 归档验证证据
- 新技术决策 → 必须新增 ADR（`design/adr-NNNN-*.md`）
- 发现平台 API 未文档化行为 → 更新 `design/platform-*-api-notes.md`

## 知识库地图

| 层 | 文件 | 内容 |
|---|---|---|
| intent | [vision.md](docs/knowledge/intent/vision.md) | 产品愿景与边界 |
| intent | [user-stories.md](docs/knowledge/intent/user-stories.md) | 用户故事清单 |
| intent | [non-goals.md](docs/knowledge/intent/non-goals.md) | 明确不做的事 |
| spec | [functional-spec.md](docs/knowledge/spec/functional-spec.md) | 功能规格 + 验收标准（面板 8 页面、自动化任务） |
| spec | [data-model.md](docs/knowledge/spec/data-model.md) | 数据库 schema 规格与口径定义 |
| spec | [pricing-spec.md](docs/knowledge/spec/pricing-spec.md) | 定价引擎规格：基线公式、反馈控制器、ECO 三元组求解 |
| spec | [security-spec.md](docs/knowledge/spec/security-spec.md) | 凭证管理、加密、审计要求 |
| design | [architecture.md](docs/knowledge/design/architecture.md) | 总体架构、模块职责、渠道适配层 |
| design | adr-*.md | 技术决策记录（纯Go重写/Postgres/双通道等） |
| design | [platform-uu-api-notes.md](docs/knowledge/design/platform-uu-api-notes.md) | UU API 逆向行为规格（端点、加密、风控） |
| design | [platform-eco-api-notes.md](docs/knowledge/design/platform-eco-api-notes.md) | ECO 开放平台规格（签名、押金公式、限频） |
| design | [platform-steam-api-notes.md](docs/knowledge/design/platform-steam-api-notes.md) | Steam 会话/收报价/确认器流程（IAuthentication protobuf 全链路） |
| impl | [repo-layout.md](docs/knowledge/impl/repo-layout.md) | 目录结构与代码归属规则 |
| impl | [dev-setup.md](docs/knowledge/impl/dev-setup.md) | 本地环境搭建 |
| impl | [testing-strategy.md](docs/knowledge/impl/testing-strategy.md) | 测试金字塔、fixture 交叉验证方法、门控说明 |
| impl | [release-runbook.md](docs/knowledge/impl/release-runbook.md) | 构建、部署、备份、故障处理 |
| evidence | [README.md](docs/knowledge/evidence/README.md) | 证据索引（每里程碑归档） |

## 工作流规则（必须遵守）

### Git 迭代流
```bash
# 1. 开始任务：从 main 建 worktree
git worktree add ../rent-auto-wt/<task> -b feat/<task>

# 2. 实现 + 测试 + 更新知识库对应层

# 3. 本地门控（必须全绿）
make gate

# 4. 提交（Conventional Commits: feat/fix/docs/test/chore/ci/refactor）

# 5. 回主分支 rebase 合并并推送
cd /home/dev/projects/rent-auto
git fetch . && git rebase main feat/<task>  # 在 worktree 内执行 git rebase main
git checkout main && git merge --ff-only feat/<task> && git push origin main
```

### 门控定义（make gate）
1. 后端：`gofmt -l` 无输出、`golangci-lint run` 零告警、`go vet` 通过、`go build ./...`、
   `go test ./... -race` 全绿（本地有 Postgres 时自动含 `-tags=integration` 集成测试）、
   **纯逻辑域**包覆盖率 ≥70%（pricing / platform/* / recon / analytics / auth / secrets / config）；
   编排层（store / api / bench / scheduler）由集成测试套件验证，不设行配额
2. 前端：`tsc --noEmit`、`eslint`、`vitest run`、`vite build`
3. 迁移可升降级（有迁移变更时）：`make migrate-check`
4. 提交信息符合 Conventional Commits；不包含任何密钥/token/私钥

### 其他硬规则
- 平台客户端的任何外部可见行为改动必须先有 mock 测试或 fixture 佐证
- 所有写操作（上架/改价/下架）必须经过审计日志；dry-run 模式默认开启于新策略首次运行
- 金额一律 `float64` 且展示层保留两位小数；计算后必须 `pricing.Round2`
- 禁止把上游 Steamauto 的 Python 代码复制进仓库——只移植行为，参考件在 `/Steamauto`（已 gitignore）

## 命令速查

| 命令 | 用途 |
|---|---|
| `make gate` | 全量门控 |
| `make test` | 后端单元测试(-race+coverage) |
| `make test-integration` | 集成测试（需 TEST_DATABASE_URL，Docker） |
| `make dev-up` / `make dev-down` | docker-compose 起/停 Postgres |
| `make server` / `make web` | 本地起后端 / 前端 |
| `make worktree-new NAME=x` | 新建任务 worktree |
| `make migrate-new NAME=x` | 新建迁移文件对 |

## 当前状态

**M0–M11 全部交付；迭代打磨轮 round4–9 完成
（JWT 纪元吊销 ADR-0006 / 哨兵统一 / openapi 全量回写 / Refresh 锁外构建 / 分页上限 /
可信代理判定+per-IP 限流 / 空货架熔断 / 迁移 0006 状态 CHECK / CI 加固 /
面板四缺口（告警条·决策依据列·模板黑名单·审计时间过滤，openapi v0.4.0）/
PublishLease 哨兵统一 / gosec 门控零告警 / Vite7+Vitest3 升级 /
模板级策略 UI（US-STRAT-02，openapi v0.5.0；含 GetEffectiveStrategy
route/real 深覆盖缺陷修复）/ lint 工具链收敛 v2.13.1 / 熔断审计事件化 /
审计抗断连，见 evidence/2026-08-24-round{4,5,6,7,8,9}-*.md）**。
**第四轮全面审查 round10 完成（2026-08-27）：六维度审查 P1×6+P2×10+文档×5 全清欠——
静默吞错补日志 / bench.Round2 委托 pricing（NaN 防护收回）/ 凭证审计尾号指纹 /
steam exp=0 短路修复 / trustList 竞争 / RandomSessionID crypto/rand /
AuditFn 实例注入 / 限流器合一 / /templates 可选分页（openapi v0.6.0，分页口径统一 ≤200）/
ADR 撞号拆分（0007/0008 新号方案）/ ECO api-notes 待办节 #E1-E3 /
前端 Strategies 拆分+usePagedList+事件驱动鉴权+补 13 用例，openapi v0.6.0**。
纯逻辑域逐包覆盖率（make gate 数值卡点 ≥70%）：pricing 92% / platform 100% /
uu 77% / eco 78% / steam 76% / recon 79% / analytics 81% / auth 90% / secrets 79% / config 80%

- 系统可运行：`make dev-up && make server && make web`；首次启动日志打印一次性管理员密码
- 生产部署：deploy/.env 设 `SITE_ADDRESS=<域名>` 启用 Caddy 自动 HTTPS + 安全响应头
- 反馈控制器已接线（factor_events 任务）：订单/stale 事件折算 listings.factor，
  f_min 回归 1.00 并审计告警——见 pricing-spec §3「已接线」；
  UU 渠道折算依赖 lease/out/list 资产字段真机确认（api-notes 待办#14）
- 本地门控纪律：`make test`/`migrate-check` 默认连 **rentauto_test** 测试库，
  永不触碰开发库 rentauto（迁移检查会 DROP 全表）
- 待真机校订项（见各 api-notes「待办」）：UU 订单状态码映射与资产字段、
  QuerySteamStock 字段、ECO 订单时间窗上限、ECO 改价边界、CSP 下 TCaptcha 域白名单核对
- 后续迭代入口：ECO 回调/WebSocket、出售域适配器、因子参数面板化——
  完整清单见 round8 证据文档移交节
- 里程碑进度与证据索引见 [docs/knowledge/evidence/README.md](docs/knowledge/evidence/README.md)
  （含三轮综合审查+round4–9+round10 归档）
- 事故复盘归档于 [evidence/incidents/](docs/knowledge/evidence/incidents/)
  （最新：dryrun-bypass——写操作必须在执行入口短路 dry-run）
