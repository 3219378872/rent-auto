# 2026-08-25 UU 短信登录：图形校验死循环（session 丢失）修复归档

## 现象

面板「渠道账号」发送 UU 登录验证码触发风控后，用户完成 TCaptcha 滑块、
系统自动重发，但平台**再次**返回「需进行图形校验」，弹窗反复出现无法进入收码流程。

审计佐证（dev 库 audit_log）：08:12:58 / 08:13:03 两条 `channel.uu.captcha_required`，
间隔恰为人工解滑块耗时——每次重发都被重新拦截。

## 根因

前端 `Channels.tsx` 的 `sendSms` 递归重试闭包读到**过期的 `session` state**：

1. 首次发送时 `session` 为空 → 后端 `handleUUSms` 随机生成 session A 并随响应返回；
2. 平台回「需进行图形校验」，`reqTicket` 与 **session A 绑定**（api-notes §认证域：
   「该票据链与 sessionId 绑定」）；
3. 前端 `setSession(A)` 触发重渲染，但正在执行的函数闭包中 `session` 仍是旧值；
4. 解滑块后递归调用 `sendSms({ticket,...})` → POST 漏带 `session_id`；
5. 后端见空 session 又生成新 session B；
6. UU 校验 `behaviorVerifyResult.reqTicket`(A) vs `Sessionid`(B) 失败 → 再要图形校验 → 死循环。

后端链路无缺陷（`parseVerifyData`/重试 payload 均有 mock 覆盖），纯前端状态时序问题。

## 修复

| 层 | 改动 |
|---|---|
| frontend/src/pages/Channels.tsx | `sendSms` 增加 `smsSessionId` 显式参数；滑块重试传 `r.session_id`，不再依赖闭包 |
| backend/internal/api/handlers_jobs.go | 带 `captcha` 而缺 `session_id` 直接 400——票据链断裂的请求不得触达平台客户端，禁止静默轮换 session 掩盖错误 |

## 验证

- 新增 `backend/internal/api/handlers_jobs_test.go`：
  - `TestUUSmsCaptchaRetryRequiresOriginalSession`：缺 session 的 captcha 重试 400 且
    不触达 platform client；带 session 时 phone/session/ticket 三元组原样透传
  - `TestUUSmsFirstSendMintsSession`：首次发送仍自动生成 session 并回显
- 新增 `frontend/src/pages/Channels.test.tsx`：
  - 图形校验后第二跳 POST 必须含 `{session_id:'sess-A', captcha:{...,req_ticket:'rt-1'}}`
    （该用例对修复前代码必失败，为真回归测试）
  - 首次发送不带 session_id、冷却倒计时渲染
- 后端定向：`go test ./internal/api -run TestUUSms -race` 全绿
- 前端定向：`pnpm exec vitest run src/pages/Channels.test.tsx` 全绿
- 全量门控 `make gate` 全绿（含 Postgres 集成套件）

## 文档同步

- design/platform-uu-api-notes.md §认证域：票据链绑定条目补记事故与 API 层强制校验
- spec/openapi.yaml `/channels/uu/sms`：captcha 参数描述补「必须同时回传 session_id，否则 400」

## 移交

- 真机复验项：解滑块重发一次通过后收到下行短信（本次仅 mock/单测验证协议层），
  结果可回填 api-notes 待办③（SmsSignIn 是否必须 loginReqTicket）
