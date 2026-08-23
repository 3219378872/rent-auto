# UU（悠悠有品 youpin898.com）API 行为规格

> 来源：Steamauto `uuyoupinapi/__init__.py`(1191行) 逆向 + 实测。Go 实现：`backend/internal/platform/uu`。
> 本文件记录**未在官方文档中的行为约束**，实现必须与之一致。

## 传输与加密

- Base：`https://api.youpin898.com`，POST 为主，JSON body
- 请求加密：随机 16 字节 AES key → AES-128-ECB/PKCS7 加密业务 JSON → base64；
  AES key 经平台 RSA 公钥(内置 2048bit) PKCS1v15 加密 → base64，置于 `request` 头或字段
  （实现见 `crypto.go`；fixture 交叉验证见 testing-strategy.md）
- 响应：`{"Code":0,"Msg":...,"Data":...}`；风控码 `84104`=限流/风控（get_least_market_price 实测）
- 必带头：`devicetoken`/`userid`（登录后）等，由 `generate_headers` 构造；User-Agent 模拟安卓端

## 认证

- Token 获取：手机号 → `send_login_sms_code`(可能走 SmsUp 短信上行兜底) → `sms_sign_in` → `Data.Token`
- Token 长期有效但可被踢；失效表现：调用抛 KeyError/未登录码 → 面板重新短信登录
- `get_uu_uk`：匿名可取，登录头需带 uk（风控字段）

## 本项目使用的端点（租赁域）

| 端点 | 用途 | 备注 |
|---|---|---|
| /api/commodity/Inventory/GetUserInventoryDataListV3 | 库存列表 | refresh 参数触发平台刷新；含 TemplateInfo.MarkPrice |
| /api/youpin/bff/new/commodity/v1/commodity/list/lease | 我的租赁货架 | 分页100 |
| /api/youpin/bff/new/commodity/v1/commodity/list/zeroCDLease | 0CD货架 | 同上 |
| /api/homepage/v3/detail/commodity/list/lease | **租赁行情** | templateId+价格区间+cnt，返回 CommodityName/LeaseUnitPrice/LongLeaseUnitPrice/LeaseDeposit 列表 |
| /api/commodity/Inventory/SellInventoryWithLeaseV2 | 上架(可租) | 批量，含 IsCanLease/LeaseDeposit(str!)/LeaseMaxDays/CompensationType |
| /api/commodity/Commodity/PriceChangeWithLeaseV2 | 改价 | 先 `pre_change_lease_price_post` 拿初始化信息再提交 |
| /api/commodity/Commodity/OffShelf | 下架 | 出售/租赁通用 |
| /api/youpin/bff/trade/v1/order/lease/out/list | 出租订单 | 分页50，sortType=0 |
| /api/youpin/bff/order/sublet/open + sublet/canEnable/list | 0CD转租 | 每日一次 |
| /api/youpin/bff/trade/v1/order/query/detail | 订单详情 | 模板ID回查 |

## 已知坑（必须遵守）

1. LeaseDeposit 在上架/改价请求中是 **字符串** 类型
2. `lease_max_days ≤ 8` 时不得传 LongLeaseUnitPrice
3. 行情接口价格区间参数 min/max 影响返回集；Steamauto 用 [模板价, 模板价×2]
4. 改价前必须调 pre-change init 接口，否则部分商品改价失败
5. 风控：连续高频调用触发 84104；全局限频默认 3rps + 任务间 sleep 抖动
6. compensation_type：0=非会员 7=V1（默认7）
