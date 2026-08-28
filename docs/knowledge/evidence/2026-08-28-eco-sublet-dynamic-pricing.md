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
- 首次真实改价生效依赖 reprice 护栏（cooldown/min-step）：转租字段随最近
  一次达标改价一并落平台；不强制立即改价。
- 动态定价为平台侧行为，具体调价算法对本系统不透明（官方文档未展开）；
  若后续发现与转租相关的订单/收益口径变化，回写本文件与 api-notes。
