# ADR-0001 — 纯 Go 重写平台对接（弃用 Python sidecar）

日期：2026-08-23 ｜ 状态：已接受

## 背景
上游 Steamauto 为 Python 实现。复用路径有二：(A) Python 引擎作为 sidecar 进程由 Go 驱动；(B) 以 Python 为行为规格，Go 原生重写所需 API 客户端。

## 决策
选 B。理由：
1. 单二进制部署是"长期无人值守"的最小运维面（无 Python 运行时/venv/pip 版本地狱）
2. 加密方案简单且可验证：UU=AES-128-ECB+RSA-PKCS1v15（stdlib 可实现）；ECO=SHA256withRSA（stdlib）
3. 我们需要的租赁域端点约 15~20 个，远小于全量移植成本
4. sidecar 方案的进程管理/日志解析/配置同步复杂度高于重写

## 后果
- 必须建立 fixture 交叉验证机制（用原 Python 实现生成密文/签名样例，Go 解码回归）——见 testing-strategy.md
- 未文档化平台行为存在踩坑风险——以 design/platform-*-api-notes.md 承接逆向知识
- 上游后续新功能需手动跟随

# ADR-0002 — PostgreSQL 作为唯一存储

日期：2026-08-23 ｜ 状态：已接受

## 背景
需要持久化历史行情（时序增长）、订单流水、财务 rollup；未来可能多账号。
SQLite 与 Postgres 权衡。

## 决策
PostgreSQL（用户明确选择）。pgx/v5 直连 SQL（不用 ORM）；
golang-migrate 以库形式嵌入二进制（启动时自动升级），迁移文件双向可逆。

## 后果
- 部署含一个 Postgres 容器（docker-compose 提供）
- 集成测试以 testcontainers-go 起 ephemeral Postgres
- 行情快照按月分区暂缓（量级 <1M rows/年），建索引即可

# ADR-0003 — 双通道一等公民适配层（放弃"主从同步"模型）

日期：2026-08-23 ｜ 状态：已接受

## 背景
初版设想 ECO 仅做 UU 货架镜像（Steamauto 模型）。需求升级为：
ECO 全量独立操作 + 无 UU 订单时 ECO 兜底上架 + 双渠道数据合成价格基准。

## 决策
定义统一 ChannelAdapter 接口，UU/ECO 均为一等公民；
跨渠道差异（押金 direct vs derived、长租阈值、批量上限）收敛到 Capabilities()，
由上层 pricing/scheduler 处理；渠道路由作为策略字段而非硬编码。

## 后果
- Steamauto 的 compare_shelves 同步算法不再直接移植，改为更通用的 Reconciler 对账模型
- 价格基准中心成为新模块（TemplateRegistry + value anchor jobs）
- ECO 三元组求解进入定价规格（pricing-spec §4）
