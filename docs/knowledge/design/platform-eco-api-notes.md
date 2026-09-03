# ECO（ECOSteam 开放平台）API 规格

> 来源：官方文档 docx.ecosteam.cn（2026-08 抓取，llms.txt 索引）。Go 实现：`backend/internal/platform/eco`。
> 官方文档为权威，本文件记录实现要点与本项目约束。

## 通用

- Base：`https://openapi.ecosteam.cn`，全 POST JSON
- 公共参数：`PartnerId`、`Timestamp`(秒，**5分钟有效**)、`Sign`（业务参数不含 Sign）
- 响应：`{ResultCode, ResultMsg, ResultData}`；ResultCode=0 成功
  关键错误码：2001 SteamId缺失（确定性调用方 bug，不映射哨兵）/ 5003 时间戳失效
  （签名环境故障→`ErrAuthExpired`）/ 5004 验签失败（保留 generic，可能是本地 bug）/
  6001 频率过快（指数退避+抖动重试≤3）/ 7002 超31天窗（分段后永不触发，误用时按
  `ErrRateLimited` 冷却）/ 4004 IP白名单 / 5005 身份ID无效（后两者→`ErrAuthExpired`）
- **签名算法**：首层参数名按不区分大小写升序（0-9a-zA-Z）拼 `k=v&k=v`；
  数组/对象值序列化为**紧凑 JSON（无空格换行、不重排键）**；SHA256withRSA(PKCS8) 签名后 base64

## 押金公式（核心业务规则）

```
RentDeposits = max( 平台参考价×140%, RentPrice×RentMaxDay, LongRentPrice×RentMaxDay )
```
- 推论：日租金 ~0.1%V 量级时 dep≈1.4V 恒定；护栏 deposit_cap_ratio 校验此派生值

## 本项目使用的端点

| 端点 | 用途 | 约束 |
|---|---|---|
| /Api/Rent/PublishRentAndSaleGoods | 上架(1)/改价(2)，可租可售 TradeTypes=[2] | 单次≤100；RentMaxDay≥8；>21天为长租须填 LongRentPrice；转租策略见下节 |
| /Api/Rent/DelistingRentGoods | 下架出租 | — |
| /Api/Rent/QuerySelfRentGoods | 我的租赁货架 | 分页≤100；State: 已上架=1/出租中=2 |
| /Api/Rent/SellerRentOrderList | 出租订单 | StartTime/EndTime 必填(格式 yyyy-MM-dd HH:mm:ss，**北京时间 UTC+8**，见已知坑#8)；状态枚举见下 |
| /Api/Rent/SellerRentOrderDetails | 订单详情 | — |
| /Api/Market/GetHashNameAndPriceList | **全量在售价 dump** | 两次调用间隔 **≥60s**（平台要求） |
| /Api/Selling/QueryStock + RefreshUserSteamStock | Steam库存 | 2026-08-27 真机校订：QuerySteamStock 系转录 404，实际 QueryStock（api-220956670）；刷新为异步 |
| /Api/Merchant/GetTotalMoney | 钱包余额 | ResultData 另含 LockMoney/PurchaseMoney/WaitSettlementMoney（当前仅消费 Money） |
| /Api/Merchant/GetFundFlow | 资金流水 | 时间窗查询 |

## 状态映射（→ 统一状态机，见 data-model.md）

RentOrderDetailStatus: 1待支付→pending_payment, 2待发货→delivering, 3租赁中→leasing,
4归还中→returning, 5归还超时/10买断违约/11归还违约→breach, 6客服仲裁→arbitrating,
7已归还/12已过户→done, 8已买断→bought_out, 9取消→cancelled
RentGoodsStatus: 1已上架→active, 2出租中→leased, 3完成/4失效/5删除→delisted
SteamStockStatus（QueryStock 响应，api-220956670 权威）: 1待上架→in_stock,
2出售上架/4出租上架/6租售上架/8预售上架/10打包上架→listed,
3出售交易中/5出租交易中/7租售交易中/9预售交易中/11打包交易中→locked
QueryStock 响应字段真机校订（2026-08-27）：**无 MarkPrice 字段**——价格用
Price(平台市场价)+SteamPrice(Steam市场价)，另有 Tradable/CanPublish(bool)、
PaintWear(string) 等；请求体用 `GameId`（非 SteamGameId），PageSize≤100。

## 转租（Sublet）策略（2026-08-28）

PublishRentAndSaleItemModel（官方 OpenAPI schema-123578183）含转租组字段：

- `SupportSublet`：关闭=0 / 开启转租=1 / 禁用=99
- `SubletPricingMethod`：自定义价格=1 / **动态定价=2**（平台随市场自动调转租价，
  此时无需传 SubletPrice / SubletLongRentPrice / SubletMinPriceRatio）
- `SubletSellerFreeRentDay`：转租卖家免租金天数（可选，本项目不传）

本项目渠道策略（固定，非策略参数）：**所有 ECO 出租项开启转租并采用平台
动态定价**——上架与改价载荷均携带 `SupportSublet=1 + SubletPricingMethod=2`，
自定义转租价字段一律不传（改价 PublishType=2 是全量项体，改价时同样重申）。
Go 实现锁定于 `eco.applySubletPolicy`，mock 契约测试断言两组字段。

存量补齐（2026-08-28，迁移 0008）：货架回读（QuerySelfRentGoods/RentGoodsItem）
**不含**转租字段，无法做状态比对；以 `listings.sublet_applied` 位追踪——
false 的挂单在 reprice 时豁免噪声下限（价格不变也提交，冷却/幅度护栏仍守），
平台接受后置位；上架成功即置位。

## SteamId 绑定（PublishRentAndSaleGoods 前置）

发布/改价/下架均要求 SteamId 已绑定：适配器从 app_settings `eco_steam_id`
（value_plain {"steam_id":...}，SetECOCreds 时一并保存）读取；缺省报
`code=2001 SteamId不能为空`。2026-08-27 真机复盘：凭证保存时未带 steamId
则后续全部发布失败——配置 ECO 渠道必须同时绑定 SteamId。
权威绑定关系可调「查询已绑定Steam账号分页列表」(api-461308273)核对。

## 已知坑

1. Timestamp 秒级且 5 分钟窗口：机器时钟漂移 >2min 必须告警（NTP）
2. 签名串构造对 JSON 序列化敏感：Go 侧用 `json.Marshal`（结构体字段序稳定）并禁 HTML escape
3. QuerySelfRentGoods 返回 GoodsNum(string) 为下架接口定位键；改价(PublishType=2)按 **AssetId** 定位（无 GoodsNum 字段）
4. 长租阈值 21 天为平台当前实现（文档注明"目前为"），需配置化
5. 6001 频率错误：指数退避重试≤3；全局限频默认 2rps
6. PublishRentAndSaleGoods 的逐项结果数组**不保证与请求等长/非空**：
   缺省项必须按失败处理（fail-closed → ErrPartialFailure），禁止乐观置成功——
   2026-08-24 起 RepriceLease 已按此固化并带 mock 反例
7. **端点路径以官方 OpenAPI 块为准**：docx.ecosteam.cn 首页索引只有功能名不带路径，
   每个接口页的 OpenAPI YAML 才是权威（2026-08-27 真机 404 复盘：
   GetMerchantMoney 系早期转录错误，实际为 GetTotalMoney；资金流水为 GetFundFlow；
   同日 QuerySteamStock → 实际 **QueryStock**，同类转录错误第二例——
   新端点上线前必须先抓 YAML 核对路径与字段名）
8. **所有时间参数与时间字符串均为北京时间（UTC+8）墙钟**（2026-08-28 真机确诊）：
   SellerRentOrderList/SellerOrderList 的 StartTime/EndTime 按平台 CST 时钟
   字符串比较——发送 UTC 墙钟格式会使新订单在 orders_sync 中**晚约 8 小时可见**
   （Karambit 订单 23:40 UTC 创建，直到 07:40 UTC 才进窗）；
   响应 CreateTime/RentExpire/RevertExpire 亦为 CST 字符串，须按 +08 解析，
   否则 started_at/due_at 系统性偏早 8 小时。客户端已统一走
   `formatEcoTime`/`parseEcoTime`（`ecoCST`）， SellerOrderList 的
   日期粒度参数同样转换（跨日界日期会变）。

## Go 实现补充约定（M2b 落地结论）

6. 签名串规则已按官方"拼接示例"黄金测试锁定：首层键名不区分大小写升序；
   顶层字符串值**不带引号**参与签名，数组/对象为紧凑 JSON（键不重排、禁 HTML 转义）；
   空串与 nil 不参与签名
7. ResultCode 存在字符串/整型两种形态，解码层归一
8. 下架端点实际路径为 `/Api/Rent/OffshelfRentGoods`（文档目录名"下架出租商品"），
   载荷 `{goodsNumList:[{GoodsNum|AssetId,SteamGameId}]}` ≤100
9. 市场全量 dump 的 ResultData 存在 `{"List":[…]}` 与裸数组两种形态，解码层兼容
10. 押金派生公式在服务端重算；客户端提交的 RentDeposits 仅为预估提示，
    护栏校验以 QuerySelfRentGoods 回读的 Deposits 为准（M3 落地）


## HTTP 状态码处理约定

2026-08-24 起 ECO 客户端与 UU 对齐为严格策略：非 HTTP 200 一律 fail-closed；
200 响应信封缺 `ResultCode` 键视为协议错误（此前默认 code=0 曾致幽灵下架，
round3 修复并固化反例）。凭据/环境级失败码 4004/5005/5003 映射 `ErrAuthExpired`
哨兵，调度器风控冷却得以介入（round4；5003 为 review-fix 轮追加：时钟偏移属
签名环境故障）；7002 映射 `ErrRateLimited`（分段后正常流程永不触发，见 #E1）；
2001（SteamId 缺失）故意保持 generic——确定性调用方 bug，重试/冷却永无帮助，
不得喂给风控退避通道。6001 退避追加抖动（review-fix 轮）：无抖动的同步重试会
与其它被限流 worker 同 tick 再触发 6001（thundering herd）。
发货域逐单判定（review-fix 轮）：`SendOfferResult/AcceptOfferResult.Failed()` 按
ErrorCode 判定（显式非 OK 码权威；send  legacy 无码载荷回退 Error 文案），
`SellerSendOffer` 单项失败直接返回 error（调用方忽略结果体也不再误记"已发送"），
批量结果用 `FailedSends()/FailedAccepts()` 分支。

## 待办（待真机校订）

> 规则：mock 无法覆盖、需要真实平台会话确认的行为在此登记；结论确认后回写正文并销项。
> （本节 2026-08-27 round10 建立此前散落于 evidence/functional-spec 的待办统一归口。）

- **#E1 ✅ 已销项（2026-08-27 真机确认）**：SellerRentOrderList 单次查询窗口上限
  **31 天**，超限返回 `code=7002 msg=最大支持查询31天内数据`。客户端已实现
  30 天分段自动聚合（`SellerRentOrderList` chunk 循环，≤365d 硬钳制），
  orders_sync 长租回看窗（≤100d）无需改动即兼容。
- **#E2 改价边界 PublishType=2**：PublishRentAndSaleGoods 以 PublishType=2 提交时
  是否支持只改租赁价不影响已配置的出售域字段，待真机验证；结论回写本文件
  「本项目使用的端点」表（functional-spec §5 Open Question 的结论落点）。
- **#E3 长租阈值配置化**：>21 天为长租须填 LongRentPrice 是平台"目前"实现
  （已知坑#4）；真机确认后如平台调整，改价前需校验 LongRentPrice 必填性。

## 出租发货流（2026-08-27 真机校订，重要）

租赁订单**不会出现在** `/Api/open/order/SellerOrderList`（出售订单视图）——
无论 DetailsState=8 还是带 SteamId 过滤，租赁单一律查不到（真机实测）。
租赁发货必须走出租域接口：

1. `/Api/Rent/SellerRentOrderList`（Status 过滤 等待发货=2）发现待发货租赁单；
2. `/Api/Rent/SellerRentOrderDetail`（按 OrderNum）取 `OfferId`——
   等待发货阶段即**平台预创建的租赁报价 ID**；`SendOfferRole`（买家=1/卖家=2）
   指示发报价责任方；`ProgressStatus`（等待发货=2/发货中=3/…/租赁中=5）；
3. 卖家用 Steam 移动确认器（identity_secret confirmlist）允许该报价，
   租客接单后平台置为租赁中。

OneClickResolveOffer 批量兜底的 AcceptOffers 项 `ErrorCode=0 + Error="" +
NeedMobileConfirmation=true` 表示「待移动端确认」（我们的确认器流程即处理此事），
**不是失败**——registry 已按此区分日志与审计。
