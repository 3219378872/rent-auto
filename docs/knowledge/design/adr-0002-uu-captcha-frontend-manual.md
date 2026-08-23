# ADR-0002: UU 图形校验采用前端内嵌 TCaptcha 的人工通过方案

日期：2026-08-23 ｜ 状态：已采纳 ｜ 关联：platform-uu-api-notes.md 认证节、
事故复盘 2026-08-23-panel-logout-401.md

## 背景

UU 对高频发送登录验证码触发腾讯云天御图形校验（aid=191004049）。
此前 Go 客户端直接报错终止，面板只能提示用户等待/换网，流程中断。
TCaptcha 的 ticket 由官方混淆 JS 在真实浏览器环境中生成（轨迹采集+pow 挑战），
服务端无法伪造。

## 决策

1. 面板前端（React）动态加载官方 TCaptcha.js，风控触发时弹窗由管理员**手动**完成；
   回调所得 `{ticket, randstr}` 与被拦响应下发的 `behaviorVerifyReqTicket`
   一并回传后端。
2. 后端 `SendLoginSmsCode` 增加可选票据参数重发请求；成功后透传
   `loginReqTicket` 给 SmsSignIn。服务端不实现任何自动求解/轨迹伪造。
3. TCaptcha appid 以常量集中于 `frontend/src/lib/tcaptcha.ts`，便于平台轮换时修改。

## 理由

- 不对抗风控（non-goals 显式限制）：人工通过 ≠ 自动化绕过，与平台意图一致。
- 唯一可行路径：ticket 必须出自真实浏览器交互；纯指引方案（去官网完成后回来）
  保留为 SDK 被域名白名单拦截时的兜底文案。
- 冷却秒数（secs:30/60）取自平台响应并驱动 UI 倒计时，从源头减少再次触发。

## 后果

- 新增真机校订项 4 个（见 api-notes 待办）：App 网关字段大小写、Data 结构、
  loginReqTicket 必要性、域名白名单。
- 平台若更换验证码供应商或 appid，仅需改 tcaptcha.ts 常量与 api-notes。
