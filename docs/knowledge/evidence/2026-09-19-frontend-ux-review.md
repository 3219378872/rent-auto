# 2026-09-19 前端页面用户体验审查

## 范围与结论

用户请求：审查当前项目前端页面用户体验，并制作改进方案。

审查基线：`5a9b303d3003eed0f3b9d43402b5947d5be328c5`。覆盖登录、仪表盘、库存、货架、订单、策略、渠道、审计八个页面以及共享导航、分页、鉴权、表单与样式。阅读 intent、功能/定价/数据模型规格及与页面相关的 API/查询/调度链。

交付：[改进方案](../design/frontend-ux-improvement-plan-2026-09-19.md)与[交互原型](../design/frontend-ux-prototype-2026-09-19.html)。归并 16 项：P1×6、P2×9、P3×1；详见方案问题矩阵和 AC-UX-01～14。本轮只新增方案、复现材料和知识库索引，没有修改产品前后端代码、迁移、平台凭证或线上配置。

## 证据分层

| 层级 | 本轮已执行 | 可以证明与不能证明 |
|---|---|---|
| 静态 | 八页及共享组件；HTTP 路由、定价记录、任务范围、统计口径追踪 | 能证明代码/合同能力和缺口；不证明生产数据实际出现频次 |
| 浏览器 | Playwright 1.62.1 / Chromium 151.0.7922.34；1440×900、1280×900、390×844；24 个页面/视口组合与针对性交互 | 未修改页面源码，所有 API 在浏览器端拦截为合成数据；证明这些场景的渲染与交互，不是后端 HTTP 联调、真实平台验收或真实手机测试 |
| 工程门控 | 后端 lint/vet/build/race/隔离 PG 集成/覆盖率/迁移；前端 tsc/eslint/vitest/build | 本地基线通过；不能据此推翻 UX 复现问题 |
| 原型 | 独立 HTML 的数字输入、保存、筛选、全局范围确认、决策详情、Escape 关闭、390px 外层溢出检查 | 只验证方案原型的演示交互；没有将原型替换成产品代码 |
| 未执行 | 真实用户访谈/可用性任务、真实账号/平台写入、实际手机/Safari、正式屏幕阅读器审查、长时运行与大库存容量测试 | 不报告用户效率提升、生产缺陷率、完整 WCAG 合规或性能上限 |

浏览器 locale=`zh-CN`，时间显示采用 `Asia/Shanghai`；所有资产、订单、指纹与凭证输入均为虚构。每个集合使用 64 条合成记录，首屏可显示 50 条。字体环境原无中文字体，本轮给浏览器单独配置临时 Noto Sans CJK SC 字体，未改产品 CSS。像素数受字体/视口影响，不作为跨设备恒定值。

## 复现记录

原始结果：[results.json](assets/2026-09-19-ux-review/results.json)。可重跑脚本：[probe.cjs](assets/2026-09-19-ux-review/probe.cjs)。脚本在页面路由外跳转后重新加载，防止 HashRouter 同路由未重挂载影响状态实验；非本站请求和未覆盖 API 均拦截，结果 `unexpectedRequests=[]`，页面异常 `consoleErrors=[]`。

| 实验 | 操作 / fixture | 观测值 | 对应问题 |
|---|---|---|---|
| 全局任务范围 | 货架筛选 ECO → 点击立即重定价 | 唯一请求 `POST /jobs/reprice/run`，query 为空、body=null、确认框 0；文案“已触发立即重定价” | UX-01 |
| 数字输入 | k1 清空后用真实键盘逐字键入 `0.95` | 最终 DOM input 值 `0.81`，随后保存请求也携带 0.81 | UX-02 |
| 健康失败 | dashboard 成功，channels 返回 503 | 仪表盘渠道告警/错误节点数 0，财务卡正常呈现 | UX-04 |
| 初次加载 | inventory 响应延迟 1600ms，250ms 观察 | `共 0 件`、0 行、0 个 aria-busy；并无文字加载说明 | UX-05 |
| 筛选更新 | 已有全渠道结果，切 UU 并延迟响应 | select=uu，第一行仍为 ECO，无更新标记 | UX-05 |
| 策略初次加载 | GET strategies 延迟 1600ms | 内置 topn=15 已呈现且保存可点击；fixture 已保存 topn=20 尚未收到 | UX-05 |
| 保存草稿丢失 | topn 从20改35 → 导航离开 → 返回 | 回到20，无离开提示 | UX-06 |
| 保存反馈位置 | 1440×900 视口点击全局保存 | 保存按钮初始 y=1212；成功消息位于视口 y=-714，不可见；总策略页约2084px高 | UX-06 |
| 慢保存重复点击 | 1200ms 响应延迟，双击保存 | 全局策略 PUT 两次，按钮未禁用；ECO 凭证 PUT 两次同样未禁用 | UX-06/12 |
| 订单类型 | 合法 buyout 类型 + bought_out 状态 | 类型列显示“短租”；状态筛选只有4种具体状态 | UX-10 |
| 审计截断 | 合成长 JSON detail | 单元格 clientWidth=573、scrollWidth=3474、title为空、展开控件0 | UX-11 |
| 敏感输入生命周期 | 模拟 ECO 保存成功 | 合成私钥字符串仍留在明文 textarea；不涉及真实密钥 | UX-12 |
| 桌面横向阅读 | 1280px 视口 | 货架主区1080px，scrollWidth=1119；订单scrollWidth=1154；当前 CSS 横滚作用于整个 main | UX-13 |
| 可见标签与对比 | DOM标签盘点 + CSS颜色计算 | 登录2个、渠道初始9个控件无显式关联label；白字/主蓝色对比3.22:1 | UX-14 |
| 导航上下文 | 库存筛选和搜索后刷新；未登录进订单再登录 | 渠道/搜索清空；登录后回 `#/` | UX-15 |
| 手机宽度 | 390px 视口订单页 | 侧栏200px、main190px、main scrollWidth=1154 | UX-16，可选 |

无显式标签的统计是 DOM 关联检查，不等同于完整无障碍树审计；placeholder 可能成为部分辅助技术的后备名称，但不会解决输入后标签消失的问题。3.22:1 对比是 `#ffffff` 与 `#4f8cff` 的 WCAG sRGB 计算，没有执行整页自动 WCAG 扫描。

## 关键源码交叉验证

- `frontend/src/pages/Listings.tsx:16` 调全局任务；`backend/internal/scheduler/jobs_reprice.go:98` 遍历已配置适配器，再列出该渠道候选商品，确认页面筛选不约束任务。
- `backend/internal/api/server.go:104` 普通审计路由交给 `handleAuditList`；`handlers_admin.go:256` 查询 `ListAudit`。`jobs_reprice.go` 把逐条模拟/成功/失败写入 `price_actions`；注册路由中没有完整定价记录读取端点。
- `backend/internal/store/collect.go:93` 的 `LastDecision` 只提供 action/at/new_rent/skip；侧向查询没有带出 dry_run/success/error。前端 `fmtDecision` 因而无法分辨模拟与真实成功。
- `frontend/src/pages/Dashboard.tsx:36` 的健康请求失败被忽略，effect 依赖为空，既无轮询也无获取时间。
- `backend/internal/analytics/analytics.go:17` 定义净收入；`BuildDashboard` 中 Total=原始合计−已售成本，Today 和 ByChannel 沿用记账收入。当前界面未解释不同口径，不能直接判定底层账算错。
- `frontend/src/pages/strategies/fields.tsx:23` 与 `:54` 在每次 onChange 时转换并 clamp 数值；当前组件测试主要使用一次性 fireEvent.change，不能覆盖本轮真实逐字输入路径。
- `frontend/src/styles.css` 所有表格单元格 nowrap、main 整体 overflow-x:auto，侧栏无响应式断点；textarea outline:none 后没有对应 focus 规则。

## 截图索引

以下是当前产品页面，均为模拟数据，不能当作真实账户经营情况。每列为一种视口。

| 页面 | 1440×900 | 1280×900 | 390×844 |
|---|---|---|---|
| 登录 | [截图](assets/2026-09-19-ux-review/1440-login.png) | [截图](assets/2026-09-19-ux-review/1280-login.png) | [截图](assets/2026-09-19-ux-review/390-login.png) |
| 仪表盘 | [截图](assets/2026-09-19-ux-review/1440-dashboard.png) | [截图](assets/2026-09-19-ux-review/1280-dashboard.png) | [截图](assets/2026-09-19-ux-review/390-dashboard.png) |
| 库存 | [截图](assets/2026-09-19-ux-review/1440-inventory.png) | [截图](assets/2026-09-19-ux-review/1280-inventory.png) | [截图](assets/2026-09-19-ux-review/390-inventory.png) |
| 货架 | [截图](assets/2026-09-19-ux-review/1440-listings.png) | [截图](assets/2026-09-19-ux-review/1280-listings.png) | [截图](assets/2026-09-19-ux-review/390-listings.png) |
| 订单 | [截图](assets/2026-09-19-ux-review/1440-orders.png) | [截图](assets/2026-09-19-ux-review/1280-orders.png) | [截图](assets/2026-09-19-ux-review/390-orders.png) |
| 策略 | [截图](assets/2026-09-19-ux-review/1440-strategies.png) | [截图](assets/2026-09-19-ux-review/1280-strategies.png) | [截图](assets/2026-09-19-ux-review/390-strategies.png) |
| 渠道 | [截图](assets/2026-09-19-ux-review/1440-channels.png) | [截图](assets/2026-09-19-ux-review/1280-channels.png) | [截图](assets/2026-09-19-ux-review/390-channels.png) |
| 审计 | [截图](assets/2026-09-19-ux-review/1440-audit.png) | [截图](assets/2026-09-19-ux-review/1280-audit.png) | [截图](assets/2026-09-19-ux-review/390-audit.png) |

针对性场景：[加载中显示0](assets/2026-09-19-ux-review/loading-inventory.png)、[订单空集合](assets/2026-09-19-ux-review/empty-orders.png)、[健康请求失败](assets/2026-09-19-ux-review/dashboard-health-error.png)、[保存反馈不在视口](assets/2026-09-19-ux-review/strategy-save-feedback.png)。

以下是**改进方案原型**，与上面的当前实现严格区分：[运营概览](assets/2026-09-19-ux-review/prototype-overview.png)、[策略编辑](assets/2026-09-19-ux-review/prototype-strategy.png)、[决策详情](assets/2026-09-19-ux-review/prototype-decision.png)；[原型检查结果](assets/2026-09-19-ux-review/prototype-check.json)。

## 命令与门控结果

1. 从 main 建独立工作树，前端按锁文件安装依赖，Vite 只监听 `127.0.0.1:4179`。实际产品源码保持基线版本。
2. 本轮第一次 `env -u TEST_DATABASE_URL -u DATABASE_URL make gate`：lint/vet/build/后端 race 单测通过；无 Postgres 时总覆盖率51.3%，analytics纯逻辑包11%，覆盖门控失败，后续前端目标未执行。未将该轮记作通过。
3. 创建独立临时容器 `rent-auto-ux-review-20260919-db`，镜像 `postgres:16-alpine`，只绑定 `127.0.0.1:25463`，数据库名 `rentauto_ux_20260919_test`，没有业务数据卷。
4. 显式测试库补跑 `env -u DATABASE_URL TEST_DATABASE_URL='postgres://rentauto:rentauto@127.0.0.1:25463/rentauto_ux_20260919_test?sslmode=disable' make gate`。全部通过，原始日志：[gate-full.txt](assets/2026-09-19-ux-review/gate-full.txt)。URL 仅为本轮临时数据库的虚构测试凭证。

| 门控 | 结果 |
|---|---|
| gofmt / golangci-lint / go vet / go build | 通过，lint 0 issues |
| Go race + integration | 全部通过，总覆盖率76.6% |
| 纯逻辑域覆盖率 | pricing93 / platform100 / uu81 / eco83 / steam83 / recon91 / analytics81 / auth93 / secrets79 / config87（%） |
| 迁移 up/down/up | `TestMigrationsUpDown` 通过，限定在独立测试库 |
| TypeScript / ESLint | 通过 |
| Vitest | 10文件，48测试通过 |
| Vite | 56模块，JS231.50kB（gzip74.83kB），CSS5.15kB（gzip1.67kB） |

这些结果是当前基线的工程检查，不是已经修复本报告的问题。原型只新增文档 HTML，不参加产品前端构建。

浏览器复跑方法（Playwright由本机工具提供，不加入产品依赖）：

```bash
# 终端一，在仓库根目录
cd frontend
pnpm install --frozen-lockfile
pnpm exec vite --host 127.0.0.1 --port 4179 --strictPort

# 终端二，在仓库根目录，PW_MODULE 替换为本机 Playwright 安装路径
PW_MODULE=/home/dev/.local/lib/node_modules/playwright \
  node docs/knowledge/evidence/assets/2026-09-19-ux-review/probe.cjs
```

脚本默认更新自身目录的结果与截图；如需保留本次证据，传 `UX_OUTPUT=/独立输出目录`。中文字体缺失时可通过 `UX_FONTCONFIG` 指向仅用于该浏览器的 fontconfig 文件。新机器无需复用本轮临时目录。

## 执行偏差

第一次工作树位于 `/home/dev/projects/rent-auto-wt/ux-review-20260919`。完整 gate 成功后，该目录与既有 sibling worktree 的文件在会话外被清理，原因未确认。主仓库仍完整、HEAD未变。

在新的隔离目录从同一 main 基线恢复本轮自建复现脚本，重新生成全部24组截图及交互结果；保留清理前已经完成的原始完整 gate 日志。没有把路径变化当作源代码变化，没有对业务库或其他工作树做恢复/回滚。方案及证据最终保存为仓库内文档。
