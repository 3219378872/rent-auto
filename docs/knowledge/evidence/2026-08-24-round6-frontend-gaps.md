# 2026-08-24 面板缺口清欠轮（round6）

## 范围

第三轮审查登记的前端功能缺口四项全量落地（US-DASH-03 / US-LIST-02 /
US-STRAT-05 / US-AUDIT-02），含必要后端配套与契约同步。

## 交付清单

| 项 | 内容 | 验证 |
|---|---|---|
| 仪表盘渠道健康告警条 | Dashboard 拉取 /channels，任一非 ok 渠道渲染顶部显著 banner（含状态原文+跳转指引）；channelIssues 纯函数 + 组件渲染测试 | vitest 19/19（新增 3 用例） |
| Listings 决策依据列 | ListListings LATERAL 关联最近 price_action（action/ts/new_rent/skip reason）+ listings.factor；列展示 `×1.06 reprice→¥1.62` / `×0.97 跳过:cooldown`；title 悬浮完整时间线 | TestListListingsDecisionContext（reprice+skip 双路径） |
| 模板黑名单管理 | 新端点 PUT /templates/blacklist（body 传 hash——规避路径特殊字符）+ 审计 template.blacklist + Strategies 页模板表（拉黑/解除即时刷新，404 反例） | TestTemplateBlacklistRoundTrip |
| 审计时间过滤+分页 | ListAudit 支持 since/until(RFC3339)+page 偏移（上限 200/页保留）；Audit 页 datetime-local 双输入+真分页控件 | TestAuditSinceUntilPaging |
| openapi v0.4.0 | 新增 /templates/blacklist、audit 参数组更新 —— 21/21 路由 | yaml 解析通过 |
| 存量隐患顺带修复 | ListListings 可空金额列 COALESCE 兜底（NULL 行此前直接扫描崩溃） | 集成套件回归 |

## 执行的命令与结果

```
make gate   # GATE PASSED（unit+integration -race / cover-gate / migrate-check / 前端四件套）
```

## 已知偏差与理由

- golangci-lint 对 rsa.EncryptPKCS1v15 的 Go1.26 弃用告警以 //nolint 定向豁免：
  Steam 密码加密与 UU 信封密钥交换为平台固定线协议，无法改用 OAEP
- 决策依据仅展示最近一条动作：完整历史在审计页按 action=reprice 过滤可查
- 黑名单为软生效：退出未来路由/锚点，已在架商品由 reconcile 按 24h 宽限下架（ADR-0005 语义）

## 移交后续

1. PublishLease 部分失败哨兵化；foldOrderEvents UU 真机字段确认（待办#14）
2. Vite7+Vitest3 升级、gosec 引入评估
3. 特性入口：模板级策略 UI、ECO 回调/WebSocket、出售域适配器、因子参数面板化
