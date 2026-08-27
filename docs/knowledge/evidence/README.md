# 证据索引（Evidence）

> 每个里程碑完成时归档验证证据于此。规则：无证据不合并。
> 状态：**M0–M11 全部归档完毕；三轮综合审查修复归档（2026-08-23/24）；迭代打磨轮 round4–9 归档（2026-08-24）；事故复盘 4 份（2026-08-23）；2026-08-25 UU 图形校验死循环修复；第四轮全面审查 round10 归档（2026-08-27）；2026-08-27 Steam 真机登录 wire5 修复归档**。

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
| 2026-08-24 第二轮全面审查与修复 | [2026-08-24-review-and-fixes.md](2026-08-24-review-and-fixes.md) | P0 compose 端口暴露+P1 写操作判定×4+事务化×3；遗留清单移交后续轮次 |
| 2026-08-24 第三轮全面审查与修复 | [2026-08-24-review-round3.md](2026-08-24-review-round3.md) | 资金域 P0×3（长租漏单/重复上架/门禁绕过）+P1×9 全清零；ADR-0003/4/5；遗留清单移交 |
| 2026-08-24 遗留清欠轮（round4） | [2026-08-24-round4-leftovers.md](2026-08-24-round4-leftovers.md) | JWT 纪元吊销(ADR-0006)/哨兵统一/openapi 20/20/Refresh 锁外构建/分页上限 |
| 2026-08-24 遗留清欠轮（round5） | [2026-08-24-round5-leftovers.md](2026-08-24-round5-leftovers.md) | 可信代理判定/per-IP 限流/空货架熔断/0006 CHECK 迁移/CI 加固 |
| 2026-08-24 面板缺口清欠（round6） | [2026-08-24-round6-frontend-gaps.md](2026-08-24-round6-frontend-gaps.md) | 告警条/决策依据列/模板黑名单/审计时间过滤；openapi v0.4.0 21 路由 |
| 2026-08-24 工程收口（round7） | [2026-08-24-round7-hardening.md](2026-08-24-round7-hardening.md) | PublishLease 哨兵统一/gosec 零告警/Vite7+Vitest3 升级 |
| 2026-08-24 模板级策略（round8） | [2026-08-24-round8-template-strategy.md](2026-08-24-round8-template-strategy.md) | US-STRAT-02 全栈落地；GetEffectiveStrategy 深覆盖缺陷修复；openapi v0.5.0 23 路由 |
| 2026-08-24 收尾打磨（round9） | [2026-08-24-round9-polish.md](2026-08-24-round9-polish.md) | golangci v2.13.1 收敛/熔断审计事件化/审计抗断连/任务错误卫生 |
| 2026-08-25 UU 图形校验死循环修复 | [2026-08-25-uu-captcha-session-loop.md](2026-08-25-uu-captcha-session-loop.md) | 滑块重试闭包丢 session_id 致票据关联失败死循环；前端显式透传+API 层 400 门禁 |
| 2026-08-27 第四轮全面审查与修复（round10） | [2026-08-27-round10-comprehensive-review.md](2026-08-27-round10-comprehensive-review.md) | 六维度审查 P1×6+P2×10+文档×5 全清欠；ADR 撞号拆分；openapi v0.6.0 |
| 2026-08-27 Steam 真机登录 wire5 修复 | [2026-08-27-steam-login-wire5.md](2026-08-27-steam-login-wire5.md) | 解码器支持 wire 1/5 跳过（interval=float32）；红→绿佐证；api-notes §1.1 批注 |
| 2026-08-27 Steam X-eresult 检查补齐 | [2026-08-27-steam-eresult-check.md](2026-08-27-steam-eresult-check.md) | WebAPI X-eresult 头检查接入登录四步；guard 29 容忍；误导性空 token 报错根除 |
| 2026-08-27 Steam guard steamid fixed64 修复 | [2026-08-27-steam-guard-f64.md](2026-08-27-steam-guard-f64.md) | UpdateAuth steamid 改 fixed64(wire1) 编码；EResult 8 InvalidParam 根除；黄金断言锁 wire type |
| 2026-08-27 UU 滑块通过仍被拦复诊 | [2026-08-27-uu-captcha-rechallenge.md](2026-08-27-uu-captcha-rechallenge.md) | 登录端点补 uk 头+parseVerifyData 大小写无关+前端遵守 secs 冷却；审计 verify_data 销项待办①② |

## 事故复盘

| 日期 | 文档 | 要点 |
|---|---|---|
| 2026-08-23 | [incidents/2026-08-23-lost-migration.md](incidents/2026-08-23-lost-migration.md) | 迁移 up 文件丢失造成假绿；加载器 orphan-down 熔断加固 |
| 2026-08-23 | [incidents/2026-08-23-uu-sms-uplink.md](incidents/2026-08-23-uu-sms-uplink.md) | 短信上行模式被吞致"假发送成功"；模式判定+GetSmsUpSignInConfig 落地 |
| 2026-08-23 | [incidents/2026-08-23-panel-logout-401.md](incidents/2026-08-23-panel-logout-401.md) | UU验证失败401踢飞面板会话+gzip解压禁用+图形校验误分类 |
| 2026-08-23 | [incidents/2026-08-23-dryrun-bypass.md](incidents/2026-08-23-dryrun-bypass.md) | recon Executor 无视 dry-run 真实上下架；门禁语义重构+测试固化 |

## 归档格式

每份证据包含：范围、执行的命令与结果摘要、覆盖率数字、已知偏差与理由。
