# 证据索引（Evidence）

> 每个里程碑完成时归档验证证据于此。规则：无证据不合并。
> 状态：**M0–M11 全部归档完毕；综合审查修复轮已归档（2026-08-23）**。

| 里程碑 | 证据文档 | 结论 |
|---|---|---|
| M0 仓库地基 | —（结构性交付） | 五层知识库+门控设施上线 |
| M1 后端骨架 | [m1-backend-foundation.md](m1-backend-foundation.md) | 迁移升降级+JWT+health 通过，总覆盖 72.4% |
| M2a UU 客户端 | [m2a-uu-client.md](m2a-uu-client.md) | openssl 向量交叉验证 + mock 契约测试，73.6% |
| M2b ECO 客户端 | [m2b-eco-client.md](m2b-eco-client.md) | 官方示例签名黄金测试 + mock 契约测试，77.8% |
| M3 数据层采集 | [m3-data-collection.md](m3-data-collection.md) | 锚点合成/货架消失标记/终态订单落库 |
| M4 定价引擎 | [m4-pricing-engine.md](m4-pricing-engine.md) | Steamauto 行为黄金测试，90.5% |
| M5 调度器 | [m5-scheduler.md](m5-scheduler.md) | dry-run/真实执行双链路集成验证 |
| M6 Reconciler | [m6-reconciler.md](m6-reconciler.md) | 路由矩阵+失效切换+执行器路径，76.8% |
| M7 统计分析 | [m7-analytics.md](m7-analytics.md) | 年化公式/记账幂等/dashboard 数值断言，83.0% |
| M8 前端 | [m8-frontend.md](m8-frontend.md) | tsc/eslint/vitest/build 四绿 |
| M9 发货+Steam | [m9-auto-delivery.md](m9-auto-delivery.md) | guard 向量交叉验证+protobuf 黄金+全链路 mock，74% |
| M10 ECO 发货报价 | [m10-eco-delivery.md](m10-eco-delivery.md) | OneClickResolve+定向重试，逐单失败审计 |
| M11 报价接受闭环 | [m11-offer-accept.md](m11-offer-accept.md) | SellerOrder 端点+四步交付编排+幂等 |
| 2026-08-23 UU 图形校验 | [2026-08-23-uu-captcha.md](2026-08-23-uu-captcha.md) | HAR 协议基线+mock 三模式覆盖；4 项 App 网关待真机校订 |
| 2026-08-23 综合审查与修复轮 | [2026-08-23-comprehensive-audit.md](2026-08-23-comprehensive-audit.md) | P0×7/P1×9 全修复+控制器接线；逐包覆盖率门控落地 |

## 事故复盘

| 日期 | 文档 | 要点 |
|---|---|---|
| 2026-08-23 | [incidents/2026-08-23-lost-migration.md](incidents/2026-08-23-lost-migration.md) | 迁移 up 文件丢失造成假绿；加载器 orphan-down 熔断加固 |
| 2026-08-23 | [incidents/2026-08-23-uu-sms-uplink.md](incidents/2026-08-23-uu-sms-uplink.md) | 短信上行模式被吞致"假发送成功"；模式判定+GetSmsUpSignInConfig 落地 |
| 2026-08-23 | [incidents/2026-08-23-panel-logout-401.md](incidents/2026-08-23-panel-logout-401.md) | UU验证失败401踢飞面板会话+gzip解压禁用+图形校验误分类 |
| 2026-08-23 | [incidents/2026-08-23-dryrun-bypass.md](incidents/2026-08-23-dryrun-bypass.md) | recon Executor 无视 dry-run 真实上下架；门禁语义重构+测试固化 |

## 归档格式

每份证据包含：范围、执行的命令与结果摘要、覆盖率数字、已知偏差与理由。
