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
- **短信上行分支（实测 2026-08-23）**：`SendSignInSmsCode` 返回 `Code=0` 但 `Msg` 不含
  "成功"时，平台**不下发**验证码短信，该手机号被切换为上行模式；此时须调
  `GET /api/user/Auth/GetSmsUpSignInConfig` 获取 `Data.SmsUpContent`/`Data.SmsUpNumber`，
  由用户从登录手机号手动发送该短信，随后以空 Code 调 `SmsSignIn`（即 `SmsUpSignIn` 端点）。
  未发送即调 `SmsUpSignIn` 会得到 Msg「暂未收到您的短信，请重新点击一键发送后，
  再次点击"我已发送"」。判定依据为 Msg 文案匹配，脆弱点：平台改文案需同步
  `uu.SendLoginSmsCode` 的判定常量
- **图形校验风控（实测 2026-08-23）**：高频重试后 `SendSignInSmsCode` 可能返回
  `Code=0, Msg="需进行图形校验"` ——既非下行也非上行，而是要求滑块/图形验证码；
  Go 实现对此直接报错（含原始 Msg），不得归入 up 模式误导用户
- 认证端点（SendSignInSmsCode/SmsSignIn/GetSmsUpSignInConfig）虽匿名可调，但必须携带
  generate_headers 全套设备头（UA/AppVersion/DeviceToken 等），否则有风控拦截风险；
  DeviceToken 与 Sessionid 同源（参考件行为）
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
3. 行情接口价格区间参数 min/max 影响返回集；Steamauto 用 [模板价, 模板价×2]（过滤条件为押金落在区间）
4. 改价前必须调 pre-change init 接口（change/price/v3/init/info），否则部分商品改价失败
5. 风控：连续高频调用触发 84104；全局限频默认 3rps + 任务间 sleep 抖动
6. compensation_type：0=非会员 7=V1（默认7）

## 发货域端点（M9 增补）

| 端点 | 用途 | 备注 |
|---|---|---|
| /api/youpin/bff/trade/todo/v1/orderTodo/list | 待办列表(含待发货) | message 字段区分动作；"有买家下单，待您发送报价"=需发报价；含"赠送"=赠送单跳过 |
| /api/youpin/bff/trade/v1/order/sell/delivery/send-offer | 平台代发 Steam 报价 | PUT {orderNo,Sessionid} |
| /api/youpin/bff/trade/v1/order/sell/delivery/get-offer-status | 发送状态轮询 | data.status==3 即发送成功（上游 5×1.5s） |

## Go 实现补充约定（M2a 落地结论）

7. 信封字段大小写混乱：库存接口用小写 `code/data`，行情用大写 `Code/Data` ——
   解码层做大小写无关归一（`decodeEnvelope`），业务代码只面对统一信封
8. 租赁货架空列表返回 `code=9004001`（非错误），zeroCD 货架同理
9. 上架结果数组与请求 ItemInfos **按位置对应**（无回传 AssetId 匹配保证）——
   适配器按 index 映射结果
10. 登录失效统一码 84101；HTTP 405 表示 uk 校验失败（上游 sleep 60s 重试，
    Go 版翻译为 ErrUKExpired 由调度器决定退避策略）
11. deviceW2 换取 uk 的流程已实现（GetUUUK）；当前版本每次请求随机 uk 即可通过，
    uk_verify 留作风控升级后的开关
12. 短信登录模式判定：`SendSignInSmsCode` 响应 `Msg` 含"成功"→ 下行（收码），
    否则 → 上行（用户自发短信，配合 GetSmsUpSignInConfig）；实现见
    `uu.SendLoginSmsCode`/`uu.GetSmsUpSignInConfig`，API `POST /channels/uu/sms`
    一次性返回 `{session_id, mode, msg, sms_up_content?, sms_up_number?}`
13. **禁止手动设置 `Accept-Encoding`**：Go net/http 对手动设置的该头不做透明
    gzip 解压，平台返回的 gzip 响应体直达 JSON 解码器（`\x1f` 开头报错）；
    必须交给传输层自动协商。请求头统一由 `generateHeaders()` 构造

