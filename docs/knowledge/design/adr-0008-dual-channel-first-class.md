# ADR-0008 — 双通道一等公民适配层（放弃"主从同步"模型）

日期：2026-08-23 ｜ 状态：已接受
编号说明：本决策原内嵌于 adr-0001 文件（当时题作 ADR-0003，与
adr-0003-channel-capabilities 撞号）；2026-08-27 round10 拆分为独立文件。
为不重写历史证据文档中的既有引用，编号顺延至 0008，决策时间仍为本日。
接口细节与能力协商的后续收口见 adr-0003-channel-capabilities.md（追认稿）。

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
