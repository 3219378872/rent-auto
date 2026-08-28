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

> ⚠️ **X-eresult 响应头（未文档化，2026-08-27 真机事故归档）**：Steam WebAPI 把应用层
> 结果码放在 `X-eresult` 头里，**HTTP 200 ≠ 成功**。上游 steampy `check_error` 语义：
> 可接受集 = {1 OK, 22 Pending}；`UpdateAuthSessionWithSteamGuardCode` 额外容忍
> 29 DuplicateRequest（`ignore_error_num=[29]`，同一 30s 窗口重复提交同一 code）。
> 不检查该头会把真实失败（TOTP 被拒/限频）吞成下游误导性错误——
> 本次真机表现为 `empty refresh token after poll`（实为 TOTP 被拒/RateLimit）。
> Go 实现在 `eresult.go`（checkEresult + 全量 EResult 名称表）+ `doRawFull`；
> 登录各步（RSA/BeginAuth/Guard/Poll）均已接入。
> 高频登录错误码：5 InvalidPassword / 25 LimitExceeded / 84 RateLimitExceeded /
> 85 AccountLoginDeniedNeedTwoFactor / 87 AccountLoginDeniedThrottle / 88 TwoFactorCodeMismatch。
> 佐证：evidence/2026-08-27-steam-eresult-check.md（红→绿 mock 注入）

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
     → {client_id=1(u64), request_id=2(bytes), interval=3(float32,**wire type 5**),
        allowed_confirmations=4(repeated){confirmation_type=1(enum)}, steamid=5(u64)}

> ⚠️ **protobuf 解码器必须支持 wire type 1(fixed64)/5(fixed32) 并跳过无关字段**
> （2026-08-27 真机登录失败事故）：`interval` 是 float（fixed32 LE），首版手写解码器
> 只认 varint/length-delimited，真实 Steam 响应必然报 `bad protobuf: wire type 5`。
> 上游 proto（Steamauto/protobufs/steammessages_auth/steamclient_pb2.py 描述符实测）：
> GetPasswordRSAPublicKey.timestamp=uint64(varint)、BeginAuth 的 client_id/steamid=uint64(varint)、
> PollAuthSessionStatus.had_remote_interaction=bool(varint)。group 类型(3/4)保持不支持。
> 修复与红→绿佐证：`proto_test.go` TestPBReaderSkipsFixedFields /
> TestDecodeBeginResponseWithInterval + evidence/2026-08-27-steam-login-wire5.md
分支:
  confirmation_type == DeviceCode(3)    → code = TOTP(shared_secret)
                                        UpdateAuthSessionWithSteamGuardCode{client_id=1(u64,varint),steamid=2(**fixed64**,wire1),code=3,code_type=4(3)}
  confirmation_type == EmailCode(2)     → 需人工提供邮件码（本项目报错终止）
  confirmation_type == DeviceConfirmation(4) → 同上但 code_type=4、code 置 "ok"

> ⚠️ **UpdateAuthSessionWithSteamGuardCode.steamid 是 fixed64（wire type 1，8 字节 LE）**
> （2026-08-27 真机事故）：上游 proto 描述符实测（steamclient_pb2.py）。varint 编码的
> steamid 会因 wire type 不匹配被服务端当未知字段丢弃 → steamid=0 → **EResult 8 InvalidParam**。
> 该错误码此前被 X-eresult 检查暴露（见上条批注）；修复：`encodeUpdateGuard` 用 `f64` 写入，
> 黄金断言 `TestEncodeUpdateGuardWireTypes` 逐字段锁定 wire type。
> 全部四个 Request 消息字段类型已逐项核对（仅此一处 fixed64；
> PollAuthSessionStatus.token_to_revoke=3 亦为 fixed64，本项目不发该可选字段）。
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
- **Go 版实现偏离（有意简化，2026-08-24 记录）**：不做三级后台轮询，
  采用单一阈值惰性刷新——每次访问会话时检查 `exp-now < 3600s` 则同步刷新；
  steam_offers 任务每 5 分钟运行，实际效果≈到期前 1 小时内完成续期。
  ~~已知盲区（round3 备案）：`jwtExp` 解析失败返回 0 时刷新条件被永久短路~~
  → round10（2026-08-27）修复：`AccessExp==0` 视为"过期未知"强制走刷新路径
  （刷新便宜、失败回落重登录），并打 Warn 日志。
  持久化：token 三元组 + 过期时间戳存 `app_settings`(AES-GCM 加密)

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
     （bs4 选 .tradeoffer[0].id.split("_")[1]；Go 尚未实现此步，见 §5）
GET  .../mobileconf/ajaxop?op=allow&cid=<conf.id>&ck=<nonce>&<同款签名参数 tag="allow">
```
确认器错误语义：`Incorrect Steam Guard codes` 文案=identity_secret 错误；
三次重试后仍找不到匹配确认项 → ConfirmationExpected 放弃本轮。
**creator_id 匹配必须精确相等**（上游 steampy 默认 match_end=False）——
2026-08-24 起移除无条件后缀匹配（曾可能误确认 creator_id 恰为报价号数字后缀的无关交易）。
**creator_id 是 JSON 字符串**（2026-08-27 真机校订）：getlist 返回
`creator_id:"<digits>"` 而非数字——按 int64 解码会让整个 confirmlist 解码失败。

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
- 确认器 details 页 HTML 解析（§3.3 第3步）**未实现**：当前仅 getlist 的 creator_id
  与报价号精确匹配；details 正则校验留待真机校订后补齐
- 七天暂挂检测保留文案匹配
