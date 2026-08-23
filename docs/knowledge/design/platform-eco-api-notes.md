# ECO（ECOSteam 开放平台）API 规格

> 来源：官方文档 docx.ecosteam.cn（2026-08 抓取，llms.txt 索引）。Go 实现：`backend/internal/platform/eco`。
> 官方文档为权威，本文件记录实现要点与本项目约束。

## 通用

- Base：`https://openapi.ecosteam.cn`，全 POST JSON
- 公共参数：`PartnerId`、`Timestamp`(秒，**5分钟有效**)、`Sign`（业务参数不含 Sign）
- 响应：`{ResultCode, ResultMsg, ResultData}`；ResultCode=0 成功
  关键错误码：5003 时间戳失效 / 5004 验签失败 / 6001 频率过快 / 4004 IP白名单 / 5005 身份ID无效
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
| /Api/Rent/PublishRentAndSaleGoods | 上架(1)/改价(2)，可租可售 TradeTypes=[2] | 单次≤100；RentMaxDay≥8；>21天为长租须填 LongRentPrice |
| /Api/Rent/DelistingRentGoods | 下架出租 | — |
| /Api/Rent/QuerySelfRentGoods | 我的租赁货架 | 分页≤100；State: 已上架=1/出租中=2 |
| /Api/Rent/SellerRentOrderList | 出租订单 | StartTime/EndTime 必填(格式 yyyy-MM-dd HH:mm:ss)；状态枚举见下 |
| /Api/Rent/SellerRentOrderDetails | 订单详情 | — |
| /Api/Market/GetHashNameAndPriceList | **全量在售价 dump** | 两次调用间隔 **≥60s**（平台要求） |
| /Api/Selling/QuerySteamStock + RefreshUserSteamStock | Steam库存 | 刷新为异步 |
| /Api/Merchant/GetMerchantMoney | 钱包余额 | — |
| /Api/Merchant/GetMerchantFundFlow | 资金流水 | 时间窗查询 |

## 状态映射（→ 统一状态机，见 data-model.md）

RentOrderDetailStatus: 1待支付→pending_payment, 2待发货→delivering, 3租赁中→leasing,
4归还中→returning, 5归还超时/10买断违约/11归还违约→breach, 6客服仲裁→arbitrating,
7已归还/12已过户→done, 8已买断→bought_out, 9取消→cancelled
RentGoodsStatus: 1已上架→active, 2出租中→leased, 3完成/4失效/5删除→delisted

## 已知坑

1. Timestamp 秒级且 5 分钟窗口：机器时钟漂移 >2min 必须告警（NTP）
2. 签名串构造对 JSON 序列化敏感：Go 侧用 `json.Marshal`（结构体字段序稳定）并禁 HTML escape
3. QuerySelfRentGoods 返回 GoodsNum(string) 为下架接口定位键；改价(PublishType=2)按 **AssetId** 定位（无 GoodsNum 字段）
4. 长租阈值 21 天为平台当前实现（文档注明"目前为"），需配置化
5. 6001 频率错误：指数退避重试≤3；全局限频默认 2rps

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

