# 证据索引（Evidence）

> 每个里程碑完成时归档验证证据于此。规则：无证据不合并。

| 里程碑 | 证据文档 | 结论 |
|---|---|---|
| M0 仓库地基 | — | 结构性交付 |
| M1 后端骨架 | m1-backend-foundation.md | 迁移升降级+JWT+health 通过 |
| M2a UU 客户端 | m2a-uu-client.md | fixture 交叉验证 + mock 契约测试 |
| M2b ECO 客户端 | m2b-eco-client.md | 签名往返 + mock 契约测试 |
| M3 数据层采集 | m3-data-collection.md | 集成测试 + 锚点合成单测 |
| M4 定价引擎 | m4-pricing-engine.md | 黄金测试 + 与 Steamauto 行为一致性 |
| M5 调度器 | m5-scheduler.md | dry-run 全链路冒烟 |
| M6 Reconciler | m6-reconciler.md | 差异矩阵表驱动测试 |
| M7 统计分析 | m7-analytics.md | 口径计算用例 + rollup 集成 |
| M8 前端 | m8-frontend.md | vitest + tsc + build 证据 |

## 归档格式

每份证据包含：范围、执行的命令与结果摘要、覆盖率数字、已知偏差与理由。
