# 事故复盘：UU 短信登录"验证码已发送"假成功，实为短信上行模式

日期：2026-08-23 ｜ 发现于：真机面板登录 ｜ 严重级：中（阻塞渠道接入，不损数据）

## 现象

面板 UU 短信登录点击「发送验证码」后提示成功，手机始终收不到短信；
留空验证码点「登录」报错。

## 追因证据

1. audit_log 唯一相关记录：
   `channel.uu.login_failed {"error": "uu: sms signin failed: 暂未收到您的短信，
   请重新点击一键发送后，再次点击“我已发送”"}`
   —— 该文案来自 SmsUpSignIn 路径，说明平台处于上行模式
2. 参考件 `Steamauto/utils/uu_helper.py:60-77`：`SendSignInSmsCode` 后按响应
   `Msg` 是否含"成功"分流；非"成功"→ `GetSmsUpSignInConfig` 取
   `SmsUpContent`/`SmsUpNumber` 让用户自发短信 → 空 Code 走 `SmsUpSignIn`

## 根因

1. `uu.SendLoginSmsCode` 丢弃整个响应体（`_, err := postJSON(...)`），
   平台返回的"需上行"信息被吞掉，API 无条件报成功 → 前端显示"验证码已发送"
2. 未实现 `GetSmsUpSignInConfig`，用户无从得知要发什么内容、发到哪个号码
3. 附带缺陷：认证端点以裸 postJSON(nil headers) 发送，无 UA/DeviceToken 设备头

## 修复与防御

1. `SendLoginSmsCode` 解析信封并判定模式（Msg 含"成功"→ down，否则 up；
   Code≠0 直接报错），返回 `SmsCodeResult{Mode, Msg}`
2. 新增 `GetSmsUpSignInConfig`；`POST /channels/uu/sms` 在 up 模式下自动拉取
   上行配置并随响应返回 `{session_id, mode, msg, sms_up_content?, sms_up_number?}`
3. 认证端点统一走 `generateHeaders()`（从 Client.headers 抽取），DeviceToken
   与 Sessionid 同源对齐参考件
4. 前端 Channels 页按 mode 展示上行指引；审计新增 `channel.uu.sms_sent`
   （记录 mode 与平台 Msg，不含手机号）
5. mock 测试覆盖三分支：downlink / uplink+GetSmsUpSignInConfig / 风控码报错，
   并断言认证端点请求头（endpoints_test.go TestSMSUplinkFlow 等）

## 影响评估

仅影响首次 UU 登录引导流程，无资金/货架写操作风险；行为规格已沉淀至
platform-uu-api-notes.md「认证」节与补充约定 #12。遗留：Msg 文案匹配脆弱，
真机校订时若平台改文案仅需调整一处判定。
