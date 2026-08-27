# ADR-0007 — PostgreSQL 作为唯一存储

日期：2026-08-23 ｜ 状态：已接受
编号说明：本决策原内嵌于 adr-0001 文件（当时题作 ADR-0002，与 adr-0002-uu-captcha
撞号）；2026-08-27 round10 拆分为独立文件。为不重写历史证据文档中的既有引用，
编号顺延至 0007，决策时间仍以本日为准。索引见 architecture.md「ADR 索引」。

## 背景
需要持久化历史行情（时序增长）、订单流水、财务 rollup；未来可能多账号。
SQLite 与 Postgres 权衡。

## 决策
PostgreSQL（用户明确选择）。pgx/v5 直连 SQL（不用 ORM）；
golang-migrate 以库形式嵌入二进制（启动时自动升级），迁移文件双向可逆。

## 后果
- 部署含一个 Postgres 容器（docker-compose 提供）
- 集成测试复用 compose Postgres（rentauto_test 库，迁移检查 DOWN 全表永不指向开发库）
- 行情快照按月分区暂缓（量级 <1M rows/年），建索引即可
