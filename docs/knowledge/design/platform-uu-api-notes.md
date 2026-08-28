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
- **图形校验风控（实测 2026-08-23，官网 PC 网关 HAR 全量抓包）**：高频重试后
  发送验证码被拦，要求人工通过腾讯云天御 TCaptcha。完整协议：
  1. 首次 sendSmsCode → `{code:1110205, data:{secs:30, behaviorVerifyReqTicket}}`
     （30s 冷却 + 一次性关联票据）
  2. 页面加载 `turing.captcha.qcloud.com/TCaptcha.js`，
     CaptchaAppID=**191004049**，用户手动完成点选/滑块 → 回调 `{ticket, randstr}`
  3. 重发时请求体追加
     `behaviorVerifyResult:{randstr, ticket, reqTicket}`（reqTicket=第1步票据原样带回）
     → `{code:0, msg:发送成功, data:{secs:60, loginReqTicket}}`
  4. 登录调用疑似需携带 loginReqTicket（第4步行为待真机确认）
  - 该票据链与 sessionId 绑定：重发必须复用首次的 sessionId
    （2026-08-25 事故：面板前端闭包读到过期 session 导致重发丢 session_id、
    后端静默换新 session，票据关联失败 → 图形校验死循环；现 API 层强制校验——
    带 captcha 而缺 session_id 直接 400，见 `handleUUSms`）
  - **2026-08-27 复诊（滑块通过仍被拦）**：审计 7/7 次发送全被拦（含每次首发），
    重试在 +5s/+10s 即到达——落在被拦响应 `secs:30` 冷却窗内。三处偏差已修
    （mock 固化，待真机复验）：
    ①登录端点此前不带 `uk` 头，与参考件 `generate_headers`（全请求含随机 uk）
    指纹不符——现 `loginHeaders` 统一补齐（uk 是风控字段，api-notes 既有条目）；
    ②`parseVerifyData` 改大小写无关 + `*reqticket*` 模糊兜底，App 网关字段名
    无论何种拼写都不再静默取空（空 reqTicket 必再拦）；
    ③前端解滑块后遵守 `secs` 冷却倒计时再自动重发（`Channels.tsx`）。
    被拦响应的原始 Data 已随审计 `channel.uu.captcha_required` 的
    `verify_data` 字段落盘，下次真机触发即可销项待办①②
  - Go 实现：`SendLoginSmsCode(..., *CaptchaResult)` 被拦时返回
    `Mode="captcha"`+ReqTicket/Secs（不报错）；带票重试 payload 键为
    `behaviorVerifyResult`；成功响应解析 LoginReqTicket 并经 SmsSignIn 透传。
    面板前端内嵌 TCaptcha SDK 人工完成后自动重试（ADR-0002），服务端绝不合成票据
  - **App 网关待校订项**（本客户端走 api.youpin898.com AES 加密网关，抓包来自
    pc-api 明文网关；被拦 Data 形状可由审计 `verify_data` 直接销项）：
    ①被拦响应 Data 是否含 BehaviorVerifyReqTicket；
    ②payload 键大小写（camelCase vs PascalCase，改动集中在 SendLoginSmsCode）；
    ③SmsSignIn 是否必须 loginReqTicket；④TCaptcha aid 是否配置域名白名单
- 认证端点（SendSignInSmsCode/SmsSignIn/GetSmsUpSignInConfig）虽匿名可调，但必须携带
  generate_headers 全套设备头（UA/AppVersion/DeviceToken 等），否则有风控拦截风险；
  DeviceToken 与 Sessionid 同源（参考件行为）
- Token 长期有效但可被踢；失效表现：调用抛 KeyError/未登录码 → 面板重新短信登录
- `get_uu_uk`：匿名可取，登录头需带 uk（风控字段）。**deviceW2 真机校订
  （2026-08-27 探针）**：请求体 `{"iud":"<标准UUID v4 带连字符>"}`——36 位随机
  字母数字会得到 **200+空响应体**（静默拒绝）；AES key 必须为 16 位**可打印
  ASCII**（参考件字母数字），二进制随机字节同样空响应体。满足两者后响应
  216 字节密文，解密为 `{"deviceUk":"...","u":"..."}`，登录头取 `u`（65 位）
- `GET /api/common/ClientInfo/AndroidInfo?DeviceToken=X&Sessionid=X`（App 头）
  返回官方版本情报：Android 最新 APK=`5.28.2`（DownloadUrl）、
  `LowestVersion=5.10.1`、`ForceUpdate=false`；iOS App Store 当前 `5.48.0`
  （2026-08-21）。可作为版本情报探针，无需认证
- **5050 版本/注册门禁（2026-08-27 复诊，未解决）**：`SendSignInSmsCode` 返回
  `Code=5050「为了保证您的账户安全，请更新至最新版本APP进行注册」`（浏览器
  指纹访问 pc-api 网关则 `Code=-1「请下载最新版本App进行注册/登录」`）。
  实验矩阵：App-Version 5.28.3/5.48.0、真 uk、AndroidInfo 设备注册前置、
  双网关——**全部 5050**；版本字符串不是判据。上游 Steamauto 同样报错
  （issue #246，2025-06，无解关闭）。特征：无 uk 头 → 走图形校验分支；
  带 uk → 5050 分支，疑似 uk 与客户端版本在服务端绑定校验（真实 APP 的
  deviceW2 上报的设备指纹携带版本信息，第三方简化请求无法证明）。
  平台 8-23~8-27 之间收紧（8-23 的 web 网关 HAR 尚可走通滑块流程）。
  待办⑤：真机 APP/官网浏览器登录抓包对齐，或验证「手动粘贴 token」替代路径

## 本项目使用的端点（租赁域）

| 端点 | 用途 | 备注 |
|---|---|---|
| /api/commodity/Inventory/GetUserInventoryDataListV3 | 库存列表 | refresh 参数触发平台刷新；含 TemplateInfo.MarkPrice |
| /api/youpin/bff/new/commodity/v1/commodity/list/lease | 我的租赁货架 | 分页100 |
| /api/youpin/bff/new/commodity/v1/commodity/list/zeroCDLease | 0CD货架 | 同上 |
| /api/homepage/v3/detail/commodity/list/lease | **租赁行情** | templateId+价格区间+cnt，返回 CommodityName/LeaseUnitPrice/LongLeaseUnitPrice/LeaseDeposit 列表 |
| /api/commodity/Inventory/SellInventoryWithLeaseV2 | 上架(可租) | 批量，含 IsCanLease/LeaseDeposit(str!)/LeaseMaxDays/CompensationType |
| /api/commodity/Commodity/PriceChangeWithLeaseV2 | 改价 | 先 `pre_change_lease_price_post` 拿初始化信息再提交 |
| /api/commodity/Commodity/OffShelf | 下架 | 出售/租赁通用；响应信封 Code≠0 必须判失败（2026-08-24 起 fail-closed，mock 反例固化） |
| /api/youpin/bff/trade/v1/order/lease/out/list | 出租订单 | 分页50，sortType=0 |
| /api/youpin/bff/order/sublet/open + sublet/canEnable/list | 0CD转租 | 每日一次；open 响应信封同样 fail-closed（同上） |
| /api/youpin/bff/trade/v1/order/query/detail | 订单详情 | 模板ID回查 |

## 已知坑（必须遵守）

1. LeaseDeposit 在上架/改价请求中是 **字符串** 类型
2. `lease_max_days ≤ 8` 时不得传 LongLeaseUnitPrice
3. 行情接口价格区间参数 min/max 影响返回集；Steamauto 用 [模板价, 模板价×2]（过滤条件为押金落在区间）
4. 改价前必须调 pre-change init 接口（change/price/v3/init/info），否则部分商品改价失败
5. 风控：连续高频调用触发 84104；全局限频默认 3rps + 任务间 sleep 抖动
6. compensation_type：0=非会员 7=V1（默认7）
7. **行情接口金额字段全部是字符串**（2026-08-27 真机校订）：commodity/list/lease 返回的
   `LeaseUnitPrice`/`LongLeaseUnitPrice`/`LeaseDeposit` 均为 JSON string——
   按 float64 解码会让每一条行情请求都 unmarshal 失败、基线 starving（0 快照）。
   Go 模型统一 `*string` + strF 解析（MarketLeaseItem.UnitPrice/LongUnitPrice）。

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


## 待真机校订（2026-08-24 第三轮审查增补）

14. **lease/out/list 响应的资产字段名未定**：`LeasedOutOrder` 现按多候选
    解析（顶层 `assetId`、`commodityInfo.steamAssetId`），任一非空即写入
    `LeaseOrder.AssetID`——因子控制器的 `UnhandledFactorOrders` JOIN 依赖
    该字段映射 listing；字段确认后收敛为单一解析并删除本条
15. 订单状态码映射仍只有 {0:leasing, 2:done, 3:bought_out}；未知码自 round5 起
    显式落 `'unknown'`（0006 CHECK 放行，不再以空串隐身，但面板不可见性问题仍在：
    unknown 非终态会拉长 orders_sync 动态回看窗口直至 100d 上限）。真机抓包补全后
    同步 `mapUUOrderStatus` 并销项

## HTTP 状态码处理约定（两平台统一策略，2026-08-24 round5 成文）

- UU 客户端（`uu/client.go`）：**严格 200 + JSON body** 才进入信封解码；
  非 200/非 JSON 一律 fail-closed 报错
- ECO 客户端（`eco/client.go`，round3 起）：**严格 200** 且信封必须含
  `ResultCode` 键——缺失视为协议错误而非 code=0（防代理错误页伪装成功）
- 传输层失败不产生业务副作用；写操作调用方以 `err != nil || !res[i].Success`
  双重判定为准（round2/3 fail-closed 系列修复的总结约定）
