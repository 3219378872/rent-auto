# 2026-08-24 收尾打磨轮（round9）

## 范围

工程卫生与可观测性收尾：lint 工具链收敛 / 熔断面板可见性 / 审计抗断连 /
任务错误处理 / plugin-react 兼容复测。

## 交付清单

| 项 | 内容 | 验证 |
|---|---|---|
| golangci-lint 版本收敛 | CI 由 v2.6 浮动改**精确锁定 v2.13.1**（与本地一致，当前规则集零告警已验证）；dev-setup 安装指引；Makefile lint 前置漂移告警（软失败） | make gate 全绿 |
| 熔断审计事件化 | bench.SyncShelf 可选 Audit 回调；空货架熔断触发时写 `shelf.empty_breaker` 审计——上游风控软封禁在审计页可见 | 编译+既有 shelf 测试回归 |
| 审计抗断连 | api 层 InsertAudit 改 context.WithoutCancel(r.Context())——客户端断连不再丢写操作审计（round3 P2-9 收口） | 编译+api 套件 |
| 任务错误卫生 | market_snapshot 单模板落库失败改为记账续跑+errors.Join 汇总；value_anchor 的 UpdateEcoRefPrices 失败补 Warn（原静默） | scheduler 套件 |
| plugin-react 复测 | 升 6.1.0 仍 ERR_PACKAGE_PATH_NOT_EXPORTED（vite/internal），回退 ^5.2.0 维持现状 | vite build 四绿 |

## 执行的命令与结果

```
make gate   # GATE PASSED
```

## 已知偏差与理由

- Makefile 版本检查为告警不阻断：避免本地工具链管理方式差异造成硬门槛；
  真正的强制口径由 CI 锁定版本承担
- plugin-react 6.x 兼容问题在上游（其依赖 vite/internal 导出面），待其发版修复后再升

## 移交后续

1. ECO 回调/WebSocket、出售域适配器、因子参数面板化语义澄清（特性入口，需产品输入）
2. foldOrderEvents UU 渠道真机字段确认（api-notes 待办#14）
