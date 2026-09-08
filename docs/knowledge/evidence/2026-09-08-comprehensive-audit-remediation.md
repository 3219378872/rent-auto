# 2026-09-08 全面审查修复与验证

## 范围与状态

- 审查基线：`main@37bfe04`，原审查提出 R01-R36（P1 17 项、P2 19 项）。
- 实施分支：`feat/fix-audit-20260908`，从干净 main 创建独立 worktree；未修改开发/生产业务库。
- R01-R36 本地修复与专项验证完成；两轮完整 gate 均通过，第二轮包含交叉复核追加的回归。
- 原基线 gate 本来可以通过。此次追加的生产调用链、组合边界、竞态和财务回归用于覆盖旧门控遗漏，
  不能把“旧门控绿色”解释为“原缺陷不存在”，也不能把本轮 mock 成功解释为真实平台验收。

## 问题闭环

| 编号 | 原问题与修复 | 可重跑回归入口 |
|---|---|---|
| R01 | 撤销纪元读取失败回退 0：改为 401；登录失败传播，登出数据库原子递增 | api/session_safety_test.go；store/session_safety_integration_test.go |
| R02 | Steam 嵌套 url.Error 泄漏 token：脱敏 URL 与保留错误分类分开 | platform/steam/reliability_test.go |
| R03 | ECO 校验非最终押金：先确定最终租金，再重算并验证最终三元组 | pricing/guardrails_regression_test.go |
| R04 | 零变化帽放行大幅变价：总是使用限幅结果，再判无变化 | pricing/guardrails_regression_test.go |
| R05 | Plan 使用零时间：每轮取得单一当前时间，行情 TTL 与宽限共用 | recon/regression_test.go；lifecycle_integration_test.go |
| R06 | 首风控后继续同批请求：逐调用共享冷却，并立即停止风险渠道批次 | recon/regression_test.go；scheduler/reprice_integration_test.go；eco_delivery_test.go；platform/uu/reliability_test.go |
| R07 | 正常 listed 资产不进入期望货架：维持库存与可发布库存分开 | recon/lifecycle_integration_test.go |
| R08 | ECO 下架缺项误判成功：验证每个请求 ref、数量、唯一性和成功状态 | platform/eco/reliability_test.go |
| R09 | 非法平台 Code 被解释为 0：严格检查类型、转换错误和 null | platform/uu/reliability_test.go；platform/eco/reliability_test.go |
| R10 | Steam token/sessionid 竞态：私有状态、快照 getter、公共操作串行 | platform/steam/reliability_test.go；channels/reliability_test.go |
| R11 | 稀疏模板编辑覆盖继承：显示有效值但仅持久化显式改动，保留 priority | frontend/src/pages/Strategies.test.tsx |
| R12 | 双渠道实物重复估值与成本：physical_inventory 按非空 asset_id 归一，空 ID 按行隔离 | store/financial_projection_integration_test.go |
| R13 | SITE_ADDRESS 未入容器：显式传入并持久化 Caddy data/config | 隔离 Compose 渲染与 Caddy adapt 验证 |
| R14 | 迁移测试回退业务库：显式测试 URL + 名称校验 + 包串行；给定但不可达必须失败 | testutil/database_test.go；make migrate-check 失败路径 |
| R15 | 原持锁连接丢失仍运营：检查原 PID/连接、根取消、停止新调度/手动任务并退出 | store/session_safety_integration_test.go；cmd/server/safety_integration_test.go；整进程失锁试验 |
| R16 | ECO 库存仅首页：完整分页，分页/完整性错误不交付部分快照 | platform/eco/reliability_test.go |
| R17 | UU 初始化瞬态失败无法自愈：独立 1-30 分钟退避恢复；凭证更新只重建目标渠道 | channels/recovery_integration_test.go |
| R18 | 孤儿宽限随同步心跳刷新：持久化首次不一致时间/原因，正常状态清除 | recon/lifecycle_integration_test.go；迁移0009 |
| R19 | 消失库存永久有效：仅成功完整快照做事务 missing 标记，missing 非 sold | recon/lifecycle_integration_test.go；bench/pipeline_integration_test.go |
| R20 | 已规划发布不占预算：同轮计划占用份数且按实物去重 | recon/regression_test.go |
| R21 | 未知短租/legacy 租期制造正反馈：证据不足保持中性 | scheduler/scheduler_test.go |
| R22 | 对账失败仍 LastOK、改价审计误报：传播执行/持久化错误，成功字段与最终结果统一 | recon/regression_test.go；scheduler/reprice_integration_test.go |
| R23 | 全局可存非法参数：验证类型、范围及所有已启用模板有效组合 | api/strategy_review_integration_test.go |
| R24 | UU userID 元数据竞态：getter/setter 使用一致锁 | platform/uu/reliability_test.go |
| R25 | Steam 登录页200假健康：检查最终认证路径，撤销会话进入恢复 | platform/steam/reliability_test.go |
| R26 | ECO发货审计过时：统一 Failed/FailedSends，明确失败不能被确认标志覆盖 | channels/reliability_test.go；platform/eco/reliability_test.go |
| R27 | 模板只显示50个：恢复无分页参数返回完整目录，显式分页仍限200 | api/strategy_review_integration_test.go；浏览器61模板 |
| R28 | 模板按内置默认校验：在事务锁内使用真实全局继承值 | api/strategy_review_integration_test.go |
| R29 | 已有模板不能切换 real：编辑路径支持开关，仍受全局总闸约束 | frontend/src/pages/Strategies.test.tsx |
| R30 | CSV 用旧总数截断：导出按自身分页响应 total 续取 | frontend/src/pages/Orders.test.tsx；浏览器120单下载 |
| R31 | 审计 offset/limit 不一致：先统一分页规范化再计算 offset | api/strategy_review_integration_test.go |
| R32 | Steam ok:id 被显示为告警：健康判断兼容规范响应 | frontend/src/pages/Dashboard.test.tsx |
| R33 | 订单首缺 asset/hash 永不补回：非空数据补全且不被后续空值覆盖 | store/financial_projection_integration_test.go |
| R34 | 已记账金额修订不生效：逐订单 ledger 冲销旧投影并原子应用新投影 | store/financial_projection_integration_test.go；迁移0010 |
| R35 | 初始密码进入日志且无法改密：0600独占文件、中断恢复、改密CAS+吊销、前端入口 | config/bootstrap_test.go；api/password_integration_test.go；cmd/server/safety_integration_test.go |
| R36 | 文档所称备份不存在：增加非root备份服务，原子dump、14天保留、失败重试及恢复演练 | deploy/backup.sh；隔离部署演练 |

表内 Go 回归路径均相对 `backend/internal/`，标有 cmd 的路径相对 `backend/`。
基线详细审查报告保留在本机 `/tmp/rent-auto-audit-fEpyr5/review.md`，本表为仓库内持久归档摘要。

## 伴随契约与交叉复核

- 全局 real_execution_enabled 定义为总闸；模板显式 true 不能越过全局 false。
  reconcile 分类使用刚读到的 GlobalRealEnabled，reprice 遵循同一规则。
- 收益底线与单步变价帽无法同时满足时跳过并记录冲突，不能静默破坏任一护栏。
  噪声判断覆盖完整提交字段，不能挡住仅押金/租期变化。
- Steam 标准状态 Accepted=3、ConfirmationNeed=9；state9 恢复指定确认，不再次 POST accept。
  自动零成本分支在单报价回读后再次检查 outgoing items。只用参考件与模拟传输验证。
- 订单 finished_at 未获明确事实时，使用稳定首次终态观测，不用 due_at 推定且不随同步漂移。
  历史已有时间不能由代码恢复真实完成事实；现有渠道尚无已校订的实际 finished_at 映射。
- 成本起算为首次实际录入，成本修订独立计时；无成本的旧库存时间被清空，已知历史时间保留。
- 登录先读纪元再读密码，避免 bcrypt 校验期间改密而旧密码取得新纪元 token。
- OpenAPI 0.9.0 增加改密接口、missing 与策略契约；修复原 flow-map 中未引号 path placeholder
  造成的 YAML 解析错误，48 个引用/25 个 operation 结构验证通过。
- 新增 .dockerignore，防止本地 backend/.env 进入 Docker COPY/build 层。
- 三条独立 lane 交叉审查了财务/授权/锁/主入口/平台/自动化；末轮发现的审计成功不一致、
  较新全局总闸被忽略、匿名成本时间混用、不可达测试库假绿均已修复并定向验证。

## 本地验证

```bash
TEST_DATABASE_URL=postgres://rentauto:rentauto@localhost:15432/rentauto_fix_root_test?sslmode=disable make gate
cd backend
govulncheck -show verbose ./...
cd ../frontend
pnpm audit --prod
```

- 完整 gate：gofmt、golangci-lint 0 issues、vet/build、全后端 integration + race、
  0001-0010 全迁移 up/down/up、前端 tsc/eslint/Vitest/Vite build 两轮均通过。
- 最终全 internal 口径覆盖率 76.6%（第一轮76.5%）；规则限定包分别为 pricing93/platform100/uu81/eco83/
  steam83/recon91/analytics81/auth93/secrets79/config87%，全部高于70%。
- 前端 10 文件、48 用例；已消除原 act/key/router 警告。
- React Router 升级 7.18.3 后 production audit 无已知漏洞。
  x/crypto 升级0.56.0，govulncheck 当前调用符号/导入包为0；仍有不可达 openpgp 模块级
  GO-2026-5932，无修补版本，不将“未调用”写作已修补该模块问题。
- 显式不可达但名称合法的测试库执行 make migrate-check 正确非零退出（2）；
  未设置 TEST_DATABASE_URL 的 make test-integration 在任何数据库连接前拒绝。
- 浏览器 Chromium 使用拦截/mock API：7条受保护路由、61模板、稀疏编辑与开关、
  120单CSV、1440x1000及390x844改密对话框、焦点/Esc/清token，通过且无console/page errors。
- 实际HTTP空库 dry-run：健康0.9.0、登录/改密/旧token401、五个手动任务、dashboard/audit通过。
  实例无平台凭证；仅终止其隔离库持锁PID后，主进程记录失锁、退出码1，未继续运营。
- Docker backend 与 backup 镜像构建通过。backend UID10001、backup UID999 对新卷的目录0700/
  文件0600验证通过。直接构建曾遇代理外网络超时，使用进程代理 build args 后通过，未持久化代理秘密。
- 使用假域名与假密钥渲染 Compose，SITE_ADDRESS、TLS卷、bootstrap卷、backup卷及DB回环绑定均符合契约。
  Caddy 在禁网容器内 adapt 出443及自动HTTPS配置，不声称已取得公网证书。
- 备份失败无成品/部分文件残留、不清旧恢复点；成功成品0600、原子发布，仅清理固定命名的超14天顶层普通文件，
  不碰符号链接/嵌套/手工/近期文件。恢复到新建隔离库约1125ms，合成行、金额、模板/订单数及迁移状态一致。

执行日志与浏览器截图在本机：

- `/tmp/rent-auto-fix-evidence.MMGcJI/`：gate、最终gate、govulncheck、HTTP smoke、进程失锁日志。
- `/tmp/rent-auto-frontend-fixes.s2CFHO/`：桌面/手机截图与浏览器结果。
- `/tmp/rent-auto-deploy-fixes.Eunytl/`：镜像构建、部署/备份/恢复/OpenAPI 结构验收结果。

## 数据与外部边界

- 全部数据库测试仅使用本次新建的 rentauto_fix_*_test 隔离库；不读取业务表数据，不对开发库
  rentauto、既有 rentauto_test 或生产库执行破坏操作。恢复演练临时库与测试卷已删除，合成数据可重建。
- 迁移0010会基于现存已确认订单修正 daily_stats 的 income/order_count，保留其他日汇总字段；
  无法重建已经丢失的订单事实。生产升级前必须完成备份，升级后需核对历史成本、完成日和收益。
- 原锁连接监控是有界 fail-stop，不是远端 fencing；不能撤销已经到达平台的请求。
- 尚未执行 UU/ECO/Steam 真实凭证认证、真实上下架/改价/发货、生产数据校订、ACME签发、公网连通、
  7天无人值守或收益对照实验。备份的本机恢复演练不等于生产定时作业已经运行，也不替代异机备份。
- 没有自动撤销或轮换外部平台/Git凭证；此前已经进入日志或共享介质的凭证仍应按原安全流程轮换。
- 历史 evidence 保留为当时记录；关于 fail-closed、全并发分支/未知因子的现行依据以本轮回归为准。
