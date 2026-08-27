# 2026-08-27 Steam 登录 X-eresult 检查补齐（"empty refresh token after poll" 复诊）

## 范围

wire5 修复后真机登录推进到轮询一步，报错
`steam login failed: steam: empty refresh token after poll`（login.go decodePoll 后）。
该文案是**误导性下游错误**：guard 更新（`UpdateAuthSessionWithSteamGuardCode`）的
应用层失败被吞，才落到 poll 拿不到 token。

## 根因（未文档化平台行为）

Steam WebAPI 把应用层结果码放在 **`X-eresult` 响应头**，HTTP 200 ≠ 成功。
上游 steampy `steampy/utils.py check_error` 每个调用都读取它：

- 可接受集 = {1 OK, 22 Pending}
- `UpdateAuthSessionWithSteamGuardCode` 额外容忍 29 DuplicateRequest
  （login.py `ignore_error_num=[29]`——同一 30s TOTP 窗口重复提交）
- 其余 EResult 抛 SteamError

Go 版首版完全不读该头 → TOTP 被拒（或限频）时错误被吞，用户看到
`empty refresh token after poll`。结合用户当晚多次重试，真实根因大概率是
TOTP 错误（85/88）或限频（25/84/87），修复后报错会直接给出 eresult 名称。

## 修复（backend/internal/platform/steam/）

1. 新增 `eresult.go`：`checkEresult(method, eresult, ignore...)` +
   全量 EResult 名称表（上游 STEAM_ERROR_CODES 1–119 逐项移植，数据表非代码逻辑）。
2. `login.go`：`doRawFull` 捕获 `X-eresult`（重定向递归后取最终跳的值，缺省 -1）；
   `doRawStatus`/`doRaw` 委托之，签名不变（offers 域零影响）；
   `authAPIGET/authAPIPOST` 检查 eresult（可传 ignore 列表）；
   guard 更新两分支传 ignore=29。
3. 登录四步（RSA/BeginAuth/Guard/Poll）全部接入检查。

## 红→绿证据

- 红（login.go 回退 HEAD、保留新测试）：TestGuardUpdateEresultSurfaces /
  TestBeginAuthSessionEresultSurfaces / TestPollEresultSurfaces 三测全 FAIL
  （老代码对 200+X-eresult:85 的 mock 走完全流程或报别的错）
- 绿（恢复修复）：`go test ./internal/platform/steam/ -count=1 -race` → ok

## 新增测试

- TestGuardUpdateEresultSurfaces：guard 200+eresult=85 → 错误含端点名与
  `eresult=85`，绝不再是 "empty refresh token"
- TestGuardUpdateDuplicateRequest29Tolerated：guard 29 → 容忍，登录成功
- TestBeginAuthSessionEresultSurfaces：BeginAuth 5 → `InvalidPassword` 命名呈现
- TestPollEresultSurfaces：Poll 84 → `RateLimitExceeded` 呈现
- TestCheckEresult：缺席/{1,22}/ignore/命名/未知码分支
- fullFlowMock helper：全流程 mock + 按端点注入 X-eresult 头

## 知识库同步

- design/platform-steam-api-notes.md §1.1：X-eresult 头批注 + 高频登录错误码表
  + ignore-29 语义（未文档化行为归档）

## 已知偏差与理由

- X-error_message 头未消费（上游也不读）；错误文案含 eresult 数字+名称已足够诊断
- 22 Pending 视为可接受系上游行为原样移植；对本域实际响应基本不出现
- offers/community 域未加 X-eresult 检查（上游对 community HTML 端点也不检查，
  且该域已有 http status + JSON 形状双重校验）

## 遗留提示

修复后真机重试，预期报错形如
`steam: UpdateAuthSessionWithSteamGuardCode failed (eresult=84 RateLimitExceeded)`——
若为限频类（25/84/87），等待一段时间（上游 `_compute_wait_interval`）再试即可；
若为 85/88，核对 shared_secret 是否与该账号当前 Steam Guard 绑定一致。
