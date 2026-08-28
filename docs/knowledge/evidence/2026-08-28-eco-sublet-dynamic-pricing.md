# 2026-08-28 ECO 出租全量开启转租（平台动态定价）

## 范围

运营决策：所有 ECO 出租商品支持转租，转租价格不自行计算，交由 ECOSteam
平台「动态定价」随市场自动调整。

## 平台侧依据（官方 OpenAPI，docx.ecosteam.cn）

- PublishRentAndSaleItemModel（schema-123578183，端点 api-220956685
  可租可售上架/改价）含转租组字段：`SupportSublet`（0关闭/1开启/99禁用）、
  `SubletPricingMethod`（1自定义价格/**2动态定价**）、`SubletSellerFreeRentDay`、
  `SubletPrice`/`SubletLongRentPrice`/`SubletMinPriceRatio`（仅自定义价时相关）。
- 选用 `SupportSublet=1 + SubletPricingMethod=2`，不传自定义转租价字段——
  动态定价下转租价由平台维护，与本系统「按市场状态自动调整」的目标一致
  且免维护。

## 实现

- `backend/internal/platform/eco/endpoints.go`：`RentPublishItem` 新增
  `SupportSublet`/`SubletPricingMethod` 字段与常量组；
  `applySubletPolicy` 统一盖章。
- `adapter.go`：`PublishLease`（PublishType=1）与 `RepriceLease`（PublishType=2）
  两条载荷构建路径均调用——改价是全量项体，存量挂单将在下一次 reprice
  周期（31m±90s）自动补上转租策略，无需人工重新上架。

## 验证（mock 佐证先行，硬规则）

- `TestAdapterPublishAndReprice` 扩展：上架与改价两组 Assets 逐项断言
  `SupportSublet=SubletOn(1)` 且 `SubletPricingMethod=SubletPricingDynamic(2)`。
- `gofmt -l` 无输出；`go test ./internal/platform/eco/ -race` 全绿。
- 全量门控 `make gate` 结果见提交记录。

## 已知偏差与理由

- 转租策略硬编码于渠道适配层而非策略参数：用户指令为「全部设置」，属
  渠道级固定政策；若未来需按模板差异化，可将 SupportSublet/SubletPricingMethod
  提升为 strategies.params 字段（规格未预先承诺，避免过度设计）。
- `SubletSellerFreeRentDay` 未设置：用户未要求免租金天数，保持平台默认。
- 动态定价为平台侧行为，具体调价算法对本系统不透明（官方文档未展开）；
  若后续发现与转租相关的订单/收益口径变化，回写本文件与 api-notes。

## 存量补齐轮（同日追加，用户决策「立即补齐」）

初版实现依赖自然改价携带转租字段，但 reprice 的噪声护栏（价格变化
< NoiseRatio 默认 2% 即 skip，engine.go 步骤4）会让存量四件挂单在价格
不动时迟迟带不上策略（最长等 7 天 stale 步降）。用户选择立即补齐：

- **迁移 0008**：`listings.sublet_applied bool NOT NULL DEFAULT false`
  （货架回读 RentGoodsItem 官方 schema 不含转租字段，无法平台侧比对，
  只能本地记账）。
- **引擎**：`pricing.Input.IgnoreNoiseFloor` 仅豁免噪声下限——
  冷却与幅度帽照常生效；`TestDecideIgnoreNoiseFloor` 三段断言
  （基线 skip / 强制提交价格不变 / 冷却仍守）。
- **调度器**：`IgnoreNoiseFloor = channel==eco && !sublet_applied`；
  真实改价被平台接受后 `MarkListingSubletApplied` 置位（失败仅留日志，
  下轮自然重试强制提交，不会静默丢补齐）。
- **上架路径**：`RecordPublishedListing` 以 `$1='eco'` 直接置位
  （ECO 上架载荷按构造携带策略），shelf sync 的 UpsertListingFromShelf
  不触碰该位，避免把已补齐的挂单打回 false。
- **集成测试**：`TestRepriceSubletBackfillForcedOnce`（rentauto_test 真库）——
  2.02→目标 2.04（0.99% 噪声区间）首次强制提交 1 次、置位、倒回冷却后
  第二轮恢复 noise skip、末条 price_action=skip。
- 单元/集成/race 全绿；`make gate` 与 `make migrate-check` 见提交。
