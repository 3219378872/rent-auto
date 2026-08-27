# 2026-08-27 UU 短信登录：滑块通过仍被拦的复诊与三处偏差修复

## 现象

管理员在面板完成 TCaptcha 滑块后自动重发，平台**再次**返回「需进行图形校验」。
与 2026-08-25 事故（前端闭包丢 session）症状相同，但该修复已在代码中生效
（前端显式传 session、后端 400 门禁）——本次是新的根因组合。

## 审计证据（dev 库 audit_log）

- 全史 `channel.uu.sms_sent` 共 **7 条，全部 mode=captcha**（08-25×4、08-27×3），
  该流程经面板**从未成功过**——不是「重试时偶尔再拦」，而是每次首发即被风控标记。
- 08-27 15:25:17 → 15:25:22 → 15:25:32 三连拦，间隔 5s/10s 恰为解滑块耗时：
  重试请求已带票据抵达平台（若缺 session 会被 API 层 400 拦截，不会触达客户端），
  但被平台再次拦截；且重发落在被拦响应 `secs:30` 冷却窗内。

## 根因（三处偏差叠加）

1. **登录端点缺 `uk` 头**（指纹偏差，首发即被标记的头号嫌疑）：
   参考件 Steamauto 的 `generate_headers` 对**所有**请求都带 `uk`（uk_verify 关闭时
   随机 65 位），短信登录端点包括在内；本项目只有已登录的 `(*Client).do` 路径带
   uk，`SendLoginSmsCode`/`SmsSignIn`/`GetSmsUpSignInConfig` 完全没有——
   恰是风控盯防的端点。参考件未实现滑块重试（无 behaviorVerifyResult），
   票据链协议仅来自 pc-api HAR，App 网关行为属盲区（api-notes 待办①②）。
2. **`parseVerifyData` 精确匹配键名**：App 网关若返回其他大小写/拼写
   （待办①②未校订），票据静默取空 → 重试携带 `reqTicket:""` → 平台必然再拦。
3. **前端解滑块后立即重发**：HAR 基线中被拦响应带 `secs:30` 冷却，
   +5s/+10s 的重发落在冷却窗内。

## 修复

| 层 | 改动 |
|---|---|
| backend/internal/platform/uu/client.go | 新增 `loginHeaders(device)`：登录三端点统一补 `uk` 随机 65 位（对齐参考件基线） |
| backend/internal/platform/uu/login.go | ①三端点换用 `loginHeaders`；②`parseVerifyData` 大小写无关 + `*reqticket*` 模糊兜底（含 `_`/`-` 归一）；③`SmsCodeResult.VerifyRaw` 携带被拦响应原始 Data 供审计诊断 |
| backend/internal/api/handlers_jobs.go | `channel.uu.captcha_required` 审计 detail 增加 `verify_data`（内容仅 ticket+secs，无 PII）——下次真机触发即可销项待办①② |
| frontend/src/pages/Channels.tsx | 解滑块后按 `secs` 逐秒倒计时显示「平台冷却中」，等满后再自动重发 |

## 验证

- 后端定向：`go test ./internal/platform/uu -run 'TestSMS|TestParseVerifyData' -race` 全绿
  - 新增 `TestSMSLoginCarriesUKHeader`：三登录端点 uk 头存在且 65 位
    （对修复前代码必失败）
  - 新增 `TestParseVerifyDataCasingTolerant`：camel/Pascal/lower/未知拼写
    （`behavior_verify_req_ticket_v2`）/空/无票据 六例全过
  - `TestSMSSendCaptchaBlocked` 补 VerifyRaw 断言
- 前端定向：`pnpm exec vitest run src/pages/Channels.test.tsx` 3/3 全绿
  - 新增「图形校验通过后遵守 secs 冷却才自动重发」：冷却期内 postMock 仍为 1 次，
    冷却结束后自动第二跳且 session/req_ticket 原样透传
- 全量门控 `make gate` 全绿（见当日 gate 输出）

## 文档同步

- design/platform-uu-api-notes.md §认证域：新增 2026-08-27 复诊块；
  待办①② 标注可由审计 `verify_data` 销项

## 附带修复（门控清障，均与 UU 改动无关）

1. **集成测试清表泄漏（真缺陷）**：api 包 4 处 `t.Cleanup(TRUNCATE)` 在
   `defer cleanup()` 关闭连接池之后执行，TRUNCATE 静默失败、行数据跨运行泄漏
   （实测 `TestAuditSinceUntilPaging` 探针行累计 12 行致 total=12≠3 必败，
   main 同样中招）。修法：truncate 改为 `defer`（LIFO 先于关池执行）+
   `truncateTables` helper（带错误上报），复合清表补 `CASCADE`
   （strategies→templates 外键）。
2. **环境注意（非代码缺陷）**：本机 Postgres 映射在 25432，而 Makefile
   `PG_HOST_PORT ?= 15432`——shell 未导出该变量时 `make test` 走「unit only」
   分支，集成测试被跳过会把 analytics 覆盖率拖到 10% 触发 cover-gate 误报。
   运行门控须 `make gate PG_HOST_PORT=25432`（或 `export PG_HOST_PORT`），
   与 `make dev-up` 启动时所用端口一致。

## 移交

- **真机复验**：修复部署后重新走一次短信登录。若仍被拦，
  查 `audit_log` 中 `channel.uu.captcha_required.detail->'verify_data'`：
  - `verify_data` 含票据键 → 客户端解析已对齐，剩下是 uk/指纹或手机号风控等级问题，
    需等待风控冷却或换网络出口再试；
  - `verify_data` 为空/形状异常 → 平台改了协议，按实际键名收敛 parseVerifyData。
- 手机号当前可能处于风控高压状态（7 连拦），修复后建议间隔 ≥30 分钟再试，
  期间勿反复触发首发。
- 若真机通过：顺手确认 SmsSignIn 带/不带 loginReqTicket 的行为，销项待办③。
