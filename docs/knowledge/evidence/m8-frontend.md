# M8 React 前端 — 验证证据

日期：2026-08-23 ｜ 分支：feat/m8-frontend

## 交付范围

- 技术栈：Vite + React 18 + TypeScript(strict) + react-router(hash) + 手写 API 客户端
  （JWT 注入/401 自动登出）；零重型 UI 库，自绘 SVG 收益曲线
- 8 页面（对应 functional-spec §1）：
  1. Login——JWT 获取与持久化
  2. Dashboard——总资产/总收入/年化ROI/在租件数卡片、押金与钱包分渠道、
     近30天收益曲线、分渠道收入表、分类成本收益率表
  3. Inventory——渠道/状态/关键字筛选、分页、行内成本录入(PUT cost)
  4. Listings——双渠道货架、期望态vs实际态、立即重定价按钮(触发 job)
  5. Orders——渠道/状态筛选、CSV 导出(含 BOM 兼容 Excel)
  6. Strategies——全局策略 JSON 编辑器、渠道路由选择、真实执行开关、策略行列表
  7. Channels——双渠道健康徽章、UU 短信两段式登录、ECO 凭证加密保存
  8. Audit——动作/渠道过滤的审计流水
- 配套后端端点：PUT /inventory/{ch}/{asset}/cost、GET /strategies、
  PUT /strategies/global、GET /audit

## 执行证据

```
pnpm exec tsc --noEmit        → 通过 (strict, ES2022)
pnpm exec eslint .            → 0 错误（typescript-eslint flat config）
pnpm exec vitest run          → 2 文件 3 用例通过（Login 渲染、Sparkline 缩放/平线防护）
pnpm exec vite build          → 成功 (gzip 后 ~60KB)
make gate（后端全套+前端四项）→ GATE PASSED
```

## 已知偏差与理由

- 模板级覆盖策略 UI 暂以"仅全局层"呈现（数据模型已支持），模板覆盖编辑器列入后续迭代
- 图表为轻量自绘折线；ECharts 级交互图列为增强项
