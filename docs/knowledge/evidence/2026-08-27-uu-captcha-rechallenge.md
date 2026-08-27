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

## 第二轮复诊（2026-08-27 下午，5050 版本门禁）

修复部署重启后用户重试，错误从「需进行图形校验」变为
`Code=5050「请更新至最新版本APP进行注册」`。一次性 Go 探针（临时 cmd/uu-probe，
已删除）完成系统实验：

| 实验 | 结果 |
|---|---|
| App-Version 5.28.3 / 5.48.0（iOS 商店当前版） | 均 5050 |
| GetUUUK 修复后真 uk + 5.48.0 | 5050 |
| + AndroidInfo 设备注册前置 | 5050 |
| pc-api web 网关 + 浏览器指纹 | `Code=-1` 同类拦截 |
| 无 uk 头（修复前的用户请求） | 图形校验分支（非 5050） |

- **GetUUUK 两个真 bug 已修**（真机证据：修复后成功取得 65 位 uk）：
  ①`iud` 必须标准 UUID v4 格式；②AES key 必须 16 位可打印 ASCII。
  违反任一条 → 平台 200+空响应体静默拒绝（原实现随机 36 位字母数字 iud +
  二进制 key 双踩）。
- **结论**：版本字符串不是 5050 判据，门禁疑似在服务端校验「uk 与客户端
  版本」的绑定（真实 APP 的 deviceW2 设备指纹携带版本信息）。上游 Steamauto
  同病（issue #246 无解）。平台在 8-23（web 抓包尚可）~8-27 之间收紧。
- **决定路线的关键实验（移交用户）**：浏览器登录官网 youpin898.com——
  能登录 → 面板加「手动粘贴 token」入口；也拦 → 只能真机 APP。
- api-notes：deviceW2 协议、AndroidInfo 版本探针、5050 待办⑤ 已归档。

## 第三轮（2026-08-27 傍晚）：手动 Token 导入路径落地

用户在官网登录成功并取回 JWT（getUserInfo 验证：UserId=1415273，
NickName=3145），证实 5050 门禁**只压登录入口，已签发 token 完全可用**。
实现替代路径：

- 后端 `PUT /api/v1/channels/uu`（`handleUUToken`）：token 透传
  `Registry.SetUUToken`（getUserInfo 验证 → AES-256-GCM 加密落库 → 重建
  适配器），审计 `channel.uu.creds_update` 仅记 `token_tail` 尾 8 位
  （security-spec 凭证展示规则），复用既有 `source: manual_import` 标记
- 前端 Channels 页新增「手动导入 Token」区块（textarea + 导入按钮，
  成功后清空输入并刷新健康徽章）；短信登录区块标注「当前被平台风控拦截」
- openapi v0.7.0 新增该路由；`cmd/server` version 同步 0.7.0
- 测试：api 层 `TestUUTokenImport`（空 token 400 且不触达 registry、
  有效 token 原样透传）；前端导入用例（PUT 原样 token + 清空断言）；
  `TestUUID4Shape`/`TestGetUUUKEnvelopeAndFailClosed` 固化 GetUUUK 协议
- 顺带清欠：删除误入 main 的临时探针 `cmd/uu-probe`（1ea8c4c 引入）
