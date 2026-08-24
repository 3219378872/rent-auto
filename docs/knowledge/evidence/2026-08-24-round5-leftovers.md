# 2026-08-24 遗留清欠轮 round5

## 范围

round4 遗留清单续：可信代理判定 / per-IP 二级限流 / 空货架熔断 /
状态枚举 DB CHECK / CI 加固 / api-notes 回写。

## 交付清单

| 项 | 内容 | 验证 |
|---|---|---|
| X-Real-IP 可信代理判定 | `clientIP` 仅在传输对端命中可信 CIDR 时采纳头；默认私网+回环（compose 下仅 Caddy 可达）；`TRUST_PROXY_CIDRS` 显式覆写 | TestClientIPTrustedProxy |
| 登录限流 per-IP 二级桶 | `ipFails` 30 次/10min 封顶——轮换用户名的全局爆破被截断；清扫覆盖双 map | TestLoginLimiterPerIPTier |
| 空货架熔断 | SyncShelf 成功返回零行且仍有 active listing → 跳过 MarkMissingListings 并告警；上游风控软封禁不再级联为全量下架+重新上架；真实消失由非空同步按 seen-refs 修剪 | pipeline 集成测试双路径改写（含旧语义测试修正） |
| 迁移 0006 状态 CHECK | lease_orders/inventory_items.status 枚举约束（九态+unknown），空串脏数据归一，越界写入被 DB 拒绝；适配器 default 分支改落 `unknown` | migrate-check + 全套件 -race 全绿 |
| CI 加固 | permissions contents:read / concurrency 取消组 / timeout 上限；eslint 接入 react-hooks 双规则（存量零违规）；`make hooks` 目标接线 pre-push 门控 | gate 前端四件套全绿 |
| api-notes/data-model 回写 | 两平台 HTTP 状态码处理约定成节；steam-notes 记录 Go 版刷新节奏有意偏离+已知盲区；data-model 状态机补 unknown 说明 | 文档评审 |
| dev-setup 修正 | 钩子接线指引（新 clone 必做）+ 测试库名纠正（rentauto_test） | — |

## 执行的命令与结果

```
make gate   # GATE PASSED
# coverage: pricing91 platform100 uu77 eco78 steam76 recon80 analytics81
#           auth90 secrets79 config76(新增解析用例后回达标)
```

## 已知偏差与理由

- 默认信任私网段而非仅回环：compose 部署中 Caddy 经容器网桥访问 backend，
  对端是 172.x 私网地址；仅回环会使限流键退化为单一代理 IP。
  backend 端口不发布是前提，公网直连部署必须显式收紧 TRUST_PROXY_CIDRS
- golangci-lint 保持 v2.6 minor 浮动 + goinstall：官方二进制以 go1.25 构建，
  与 go.mod 目标 1.26 不兼容（历史 commit d278b1a/287d62f 的结论），无法切二进制模式精确锁定
- 空货架熔断为软跳过（返回 nil 非 error）：面板 LastError 不告警，仅日志 Warn；
  若需面板可见性可在后续轮次升级为审计事件

## 移交后续轮次

1. PublishLease 部分失败哨兵化（调用方契约评估）
2. foldOrderEvents UU 渠道依赖真机字段确认（api-notes 待办#14）
3. 前端缺口：仪表盘告警条/Listings 决策依据列/模板级策略 UI/黑名单管理/审计时间过滤
4. Vite7+Vitest3 升级；gosec linter 引入评估
5. ECO 回调/WebSocket、出售域适配器、因子参数面板化（特性迭代入口）
