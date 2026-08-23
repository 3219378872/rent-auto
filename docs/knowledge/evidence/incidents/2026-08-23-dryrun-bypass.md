# 事故复盘：recon Executor 无视 dry-run，默认配置下真实上下架

- **日期**：2026-08-23（综合审查发现，合并前拦截）
- **严重级别**：高（资金/账号风险；违反 AGENTS.md「新策略首次运行默认 dry-run」硬规则）
- **状态**：已修复并测试固化

## 经过

M6 交付的 `recon.Executor.Execute` 中，`DryRun` 字段只影响 `applied` 计数与审计
detail 标记——publish/delist 动作**无条件真实调用平台适配器**。默认配置
`DRY_RUN_DEFAULT=true` 下，reconcile 任务每 10 分钟就会对 UU/ECO 真实上架、下架。
对照同文件族的 `jobs_reprice.go:100-104`（effectiveDry 时 continue 不触达平台），
两处 dry-run 语义不一致，Executor 是错误的一方。

同期审查还发现同类"乐观成功"缺陷群：Steam `AcceptOfferWithPartner` 把非 JSON
响应一律当接受成功；`SendDeliveryOffer` 吞掉平台错误码。三者共同根因是
**写操作缺少失败判定路径**。

## 根因

1. Executor 的 dry-run 判断放在计数处而非执行入口，语义倒置；
2. 缺少"dry-run 必须零平台调用"的集成断言，门控无法拦截。

## 修复

- `Execute`：DryRun 时直接短路——审计照写（detail 带 `dry_run:true`）+ 计入 applied，
  不触碰任何适配器；未配置渠道仍计 failed（规划问题如实暴露）
- Steam accept：检查 HTTP 状态码 + JSON 解码失败报错 + 非 accepted 状态报错；
  ajaxop 解码失败不再视为成功
- UU send-offer：补信封校验（checkEnv）
- 测试固化：`TestExecutorDryRunSkipsPlatformCalls`（零平台调用断言）、
  steam accept 反例×6、uu delivery 失败路径×3、channels 加密迁移集成×4

## 教训

- **开关类语义必须在执行入口短路，而不是在统计口径上打补丁**；
- 写操作函数的返回值必须能表达失败，"无错误即成功"只允许出现在只读路径；
- 门控需要行为级断言（mock 调用次数），仅看返回值/日志不足以防回归。
