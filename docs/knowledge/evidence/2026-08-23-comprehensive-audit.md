# 综合审查与修复轮证据（2026-08-23）

## 范围

五路并行审查（平台客户端 / 后端核心 / 纯逻辑域 / 前端 / 知识库互证），
共确认 P0 严重 7 项、P1 中等 9 组；分三批修复并逐批 `make gate` 全绿合入 main。

## 交付批次

### 批次 1 · P0 止血（feat/p0-safety-harness，65f271b）
| 缺陷 | 修复 | 验证 |
|---|---|---|
| recon dry-run 门禁失效（真实上下架） | Executor 入口短路 | 单测×3（零平台调用断言）；复盘归档 incidents/2026-08-23-dryrun-bypass.md |
| SteamSession nil logger 必然 panic | 构造器注入+兜底 | 编译期保证非 nil |
| UU token 明文落库 | UpsertSettingEnc + 存量惰性迁移 + enc 重写清 plain | 集成测试×4 |
| AcceptOffer 把失败当成功 | 状态码/JSON/状态三元校验 | 反例 mock 测试×6 |
| 面板明文 HTTP+零安全头 | Caddy SITE_ADDRESS 自动 HTTPS + CSP/XFO/HSTS + 静态资源 immutable | caddy validate 双模式通过 |
| tcaptcha rejected Promise 永久缓存 | loader 重置 | —（UI 行为） |
| uu 发货链路零测试 | 信封校验补齐 + mock 测试×6 | 测试先行暴露 SendDeliveryOffer 吞错缺陷并修复 |

### 批次 2 · P1 正确性（feat/p1-correctness，bc8fc19）
- 收入汇总单事务化（防重复计账）+ 口径 B ROI/分类收益率 + UTC 日界统一
- 基线采样按 (rank,kind) 合并对齐 pricing-spec §2（重叠批次去重）
- ECO 6001 指数退避≤3（mock 计数断言）；调度器风控冷却（限频5min/封禁15min/UK过期2min）
- 任务错误如实上报 LastError；eco_delivery 失败可重试、成功才去重；ECO 凭据动态生效
- 写操作审计补齐（UU 发货/0CD/EcoOneClick 成功分支/Steam 零成本接受）
- 登录防爆破（5次/10min 锁定 + 常数时间比较）；500 错误脱敏
- eco 状态码5→breach；GetInventory 翻页；前端竞态/超时/CSV 全量导出/secret 遮蔽/client.test×6

### 批次 3 · 接线与收尾（feat/pricing-controller）
- **反馈控制器接线**：`factor_events` 任务——终态订单折算（factor_applied 幂等）、
  stale 阶梯降价（last_factor_event_at 锚点）、f_min 回归 1.00 + 审计告警；
  迁移 0005；集成测试×3（含幂等与重置告警断言）
- 覆盖率门控落地：scripts/coverage-gate.sh 逐包 ≥70%（Makefile cover-gate 进 gate 链 + CI 步骤）
- CI：govulncheck 软门控步骤；pnpm 版本改由 packageManager 字段驱动
- migrate-new 改递增序号命名（对齐 repo-layout 约定）；清 TEST_MIGRATE_CHECK 死变量
- 知识库同步：repo-layout/architecture/data-model/pricing-spec/security-spec/evidence 索引
- 卫生：遗留 worktree 与已合并分支清理、空目录处置

## 最终门控数字（make gate PG_HOST_PORT=25432）

```
gofmt/lint(0 issues)/vet/build 全绿
go test -race -tags=integration -p 1 ./... 全绿
coverage-gate: pricing 91% platform 100% uu 77% eco 77% steam 75%
               recon 75% analytics 81% auth 90% secrets 79% config 76%
frontend tsc/eslint/vitest(19 tests)/build 四绿
migrate-check（0005 可升降级）绿
```

## 已知残留

- 真机校订项不变（见各 api-notes 待办）：UU 订单状态码映射、QuerySteamStock 字段、
  ECO 订单时间窗上限、ECO 改价边界
- CSP 中 TCaptcha 域白名单（turing.captcha.qcloud.com / t.captcha.qq.com）需首次真机
  图形验证时核对控制台无拦截
- ACME 签发需公网可达的域名与 80/443 端口；离线部署走自定义 Caddyfile 挂载
