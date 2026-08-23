# Steam 会话与交易报价 API 行为规格

> 来源：Steamauto `steampy/*`(vendored) + `utils/steam_client.py` 逐行追踪，2026-08-23。
> Go 实现：`backend/internal/platform/steam`。本文件是该域的权威行为规格。

## 0. 总览：两条自动化链路

| 链路 | 触发 | 凭证 | 动作 |
|---|---|---|---|
| UU 发货报价 | 租赁订单付款 → UU 待办列表出现"待您发送报价" | 仅 UU token | 调 UU API 让平台代发 Steam 报价 |
| Steam 收报价 | 对方发来 incoming offer（礼物/租赁归还） | **Steam 会话**(access_token cookie) | community 接受 + 手机确认器二次确认 |

## 1. Steam 登录（IAuthenticationService protobuf 流）

登录策略三级降级（steam_client.py:297）：**缓存 access_token 恢复 → refresh_token 换新 → 账密全流程**。
所有认证接口走 `https://api.steampowered.com/IAuthenticationService/<Method>/v1/`，
POST body 为 `input_protobuf_encoded=<base64(protobuf)>`，响应体是裸 protobuf。

### 1.1 账密登录时序
```
GET  steamcommunity.com                      → 取得 sessionid cookie
GET  IAuthenticationService/GetPasswordRSAPublicKey {account_name=1}
     → {publickey_mod=1(str-hex), publickey_exp=2(str-hex), timestamp=3(uint64)}
RSA-PKCS1v15(password ascii) with (mod,exp) → base64 = encrypted_password
POST IAuthenticationService/BeginAuthSessionViaCredentials
     {device_friendly_name=1, account_name=2, encrypted_password=3,
      encryption_timestamp=4(u64), remember_login=5(bool,true),
      platform_type=6(enum k_EAuthTokenPlatformType_MobileApp=3),
      persistence=7(enum k_ESessionPersistence_Persistent=1),
      website_id=8("Community")}
     → {client_id=1(u64), request_id=2(bytes), steamid=5(u64),
        allowed_confirmations=4(repeated){confirmation_type=1(enum)}}
分支:
  confirmation_type == DeviceCode(3)    → code = TOTP(shared_secret)
                                        UpdateAuthSessionWithSteamGuardCode{client_id=1,steamid=2(u64),code=3,code_type=4(3)}
  confirmation_type == EmailCode(2)     → 需人工提供邮件码（本项目报错终止）
  confirmation_type == DeviceConfirmation(4) → 同上但 code_type=4、code 置 "ok"
POST IAuthenticationService/PollAuthSessionStatus {client_id=1(u64), request_id=2(bytes)}
     → {refresh_token=3, access_token=4, account_name=6}
POST login.steampowered.com/jwt/finalizelogin {nonce=refresh_token, sessionid, redir}
     → transfer_info[]{url, params{nonce, auth}}
POST 每个 transfer url {steamID, auth, nonce}（跟随302）
     → 各域名写入 steamLoginSecure = "<steamid>%7C%7C<access_token>"
POST steamcommunity.com/trade/new/acknowledge {sessionid, message=1}   ← 新设备交易确认页应答
```

### 1.2 Token 维护
- `steamLoginSecure` cookie 即会话载体；`steamRefresh_steam` cookie 存 refresh_token
- 刷新：POST `IAuthenticationService/GenerateAccessTokenForApp/v1`（**form 表单**非 protobuf）
  `{steamid, refresh_token}` → access_token → 重写 steamLoginSecure
- JWT exp：access_token 是 JWT，解析 payload.exp 得过期时间；
  后台刷新节奏（>6h→3h后再查；1~6h→1h；<1h→10min；已过期→5min重试）
- 持久化：token 三元组 + 过期时间戳存 `app_settings`(AES-GCM 加密)

## 2. Steam Guard 双算法（guard.py，已做 Python 向量交叉验证）

```python
one_time_code(shared_secret, ts):            # 30s TOTP 变体
  hmac_sha1(b64d(shared_secret), u64be(ts//30)) → begin=digest[19]&0xF
  code30 = u32be(digest[begin:begin+4]) & 0x7FFFFFFF
  字母表 "23456789BCDFGHJKMNPQRTVWXY" 迭代 divmod 取5位

confirmation_key(identity_secret, tag, ts):  # 确认器签名
  b64( hmac_sha1(b64d(identity_secret), u64be(ts)+tag) )

device_id(steamid64):
  "android:" + sha1(steamid).hex 按 8-4-4-4-12 分组连字符
```

## 3. 交易报价：查询 / 接受 / 确认

### 3.1 查询活跃报价
```
GET api.steampowered.com/IEconService/GetTradeOffers/v1
    ?access_token=<steamLoginSecure里的JWT>&get_received_offers=1&get_sent_offers=1
    &get_descriptions=1&active_only=1&language=english
```
注意：用 **access_token 参数**即可，无需 WebAPI key。失败兜底走 HTML 抓取（本项目首版不做兜底，记录告警）。

### 3.2 自动接受规则（SteamAutoAcceptOffer 插件行为）
仅处理 `trade_offers_received` 且 **`items_to_give` 为空数组** 的报价：
- 覆盖两类业务动作：纯礼物报价；租赁归还报价（对方把饰品还给我们，我们零支出）
- `items_to_give` 非空 → 跳过并记日志（绝不自动付出资产）
- 已被处理(state≠Active=2/ConfirmationNeed=3) → 加入忽略名单防抖

### 3.3 接受 + 二次确认时序
```
GET  steamcommunity.com/tradeoffer/{id}/           ← Referer 用；页面含 g_ulTradePartnerSteamID='...'
     （若出现 "logged in from a new device ... 7 days" 文案 → 七天暂挂异常）
POST steamcommunity.com/tradeoffer/{id}/accept
     form: sessionid, tradeofferid, serverid=1, partner=<partnerSteamID>, captcha=""
POST → resp.needs_mobile_confirmation == true 时进入确认器 ↓
GET  steamcommunity.com/mobileconf/getlist?p=<device_id>&a=<steamid>&k=<confirmation_key(tag="conf")>&t=<ts>&m=android&tag=conf
     → conf[]{id, nonce, creator_id}
GET  .../mobileconf/details/{id}?...tag="details{id}"   ← 从HTML提取 tradeoffer id 匹配目标
     （bs4 选 .tradeoffer[0].id.split("_")[1]；Go 用正则等价实现）
GET  .../mobileconf/ajaxop?op=allow&cid=<conf.id>&ck=<nonce>&<同款签名参数 tag="allow">
```
确认器错误语义：`Incorrect Steam Guard codes` 文案=identity_secret 错误；
三次重试后仍找不到匹配确认项 → ConfirmationExpected 放弃本轮。

## 4. UU 发货链路（uu/delivery.go）

```
POST /api/youpin/bff/trade/todo/v1/orderTodo/list {userId,pageIndex,pageSize:20,Sessionid}
  → data[]: {orderNo, commodityName, message}
  message=="有买家下单，待您发送报价" → PUT /api/youpin/bff/trade/v1/order/sell/delivery/send-offer {orderNo,Sessionid}
       → 轮询 get-offer-status 至 status==3（上游5次×1.5s）
  message 含 "赠送" → 计数跳过（不碰赠送单）
```

## 5. 本项目实现边界

- 不做：出售发货(BUFF/C5)、市场挂刀、聊天、库存快照(get_my_inventory 仅排障用途)
- 确认器 details 页 HTML 解析用正则近似 bs4 选择器，真实页面回归留待真机校订
- 七天暂挂检测保留文案匹配
