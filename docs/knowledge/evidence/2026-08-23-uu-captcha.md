# 证据：UU 图形校验前端人工通过（2026-08-23）

## 范围

风控触发的 TCaptcha 图形校验在面板内人工通过，避免登录流程中断。
依据：官网 PC 网关 HAR 全量抓包（含三段响应体，由用户提供）。

## 抓包事实（协议基线）

| 步骤 | 请求/响应 | 关键字段 |
|---|---|---|
| ① 首发被拦 | `code:1110205` | `data.behaviorVerifyReqTicket` + `secs:30` |
| ② 人工通过 | TCaptcha aid=191004049 | 回调 `ticket`+`randstr` |
| ③ 带票重发 | `code:0 发送成功` | 请求含 `behaviorVerifyResult{randstr,ticket,reqTicket}`；响应 `secs:60`+`loginReqTicket` |
| SDK | turing.captcha.qcloud.com/TCaptcha.js | 点选模板 drag_ele，pow 挑战 |

## 执行的验证

- 后端 mock：`TestSMSSendCaptchaBlocked`（被拦→Mode=captcha+票据+secs 解析）、
  `TestSMSSendCaptchaRetryPayload`（behaviorVerifyResult 三字段+成功响应
  LoginReqTicket/secs 解析）、`TestSMSLoginFlow`（loginReqTicket 透传
  SmsSignIn、空值省略）、`TestSMSLoginPlatformError`（84104 仍走
  ErrPlatformBlocked）
- `go build/vet/test ./... -race` 全绿；前端 `tsc --noEmit`/`eslint`/`vitest`(8)/`vite build` 全绿
- `make gate` 全绿后合并

## 已知偏差与待真机校订

抓包来自 pc-api.youpin898.com 明文网关；本项目客户端走 api.youpin898.com AES 网关，
以下 4 项以 mock 假设实现、待真机收敛（观测点集中在 uu.SendLoginSmsCode）：

1. 被拦响应 Data 是否含 `BehaviorVerifyReqTicket`
2. payload 键 `behaviorVerifyResult` 大小写（App API 可能 PascalCase）
3. `SmsSignIn` 是否必须携带 `loginReqTicket`
4. TCaptcha aid 是否配置域名白名单（面板 origin 加载被拒时走兜底指引文案）

## 影响面

仅登录引导流程；无货架/资金写操作。审计新增 `channel.uu.captcha_required`。
