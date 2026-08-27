# 2026-08-27 Steam guard 更新 steamid 编码修复（EResult 8 InvalidParam 复诊）

## 范围

X-eresult 检查上线后，真机登录报错精确定位到 guard 更新一步：
`steam: UpdateAuthSessionWithSteamGuardCode failed (eresult=8 InvalidParam)`。

## 根因（未文档化平台行为 / 编码器 wire type 缺陷）

上游 proto 描述符实测（Steamauto/protobufs/steammessages_auth/steamclient_pb2.py）：

```
CAuthentication_UpdateAuthSessionWithSteamGuardCode_Request
    1 client_id   uint64   (varint)      — Go 版编码正确
    2 steamid     fixed64  (wire 1, LE)  — Go 版误发 varint ✗
    3 code        string
    4 code_type   enum     (varint)
```

`encodeUpdateGuard` 用 varint（wire 0）写 steamid；proto 解析按
(field_number, wire_type) 索引，wire 不匹配 → 字段被视为未知并跳过 →
服务端收到 steamid=0 → **EResult 8 InvalidParam**。
与同日 wire5 事故同属"手写 protobuf 编解码器与 schema 类型不一致"家族。

## 修复

`encodeUpdateGuard`：steamid 改用 `pbWriter.f64`（fixed64 LE，本日 eresult
轮次已为测试加入该 writer）。

## 红→绿证据

- 红：新增黄金断言 TestEncodeUpdateGuardWireTypes（逐字段锁定 field/wire/num），
  旧编码下 FAIL：`steamid must be fixed64: f=2 wire=0 ...`
- 绿：改 f64 后 `go test ./internal/platform/steam/ -count=1 -race` → ok
  （含全流程 mock TestLoginFullFlow / TestGuardUpdateEresultSurfaces 等回归）

## 字段类型全量核对（防同类复发）

| Request 消息 | 字段类型核对结果 |
|---|---|
| GetPasswordRSAPublicKey_Request | 1 account_name string ✓ |
| BeginAuthSessionViaCredentials_Request | 4 encryption_timestamp uint64(varint) ✓ 其余 str/bool/enum ✓ |
| PollAuthSessionStatus_Request | 1 client_id uint64(varint) ✓ 2 request_id bytes ✓ 3 token_to_revoke fixed64（可选，本项目不发）|
| UpdateAuthSessionWithSteamGuardCode_Request | **2 steamid fixed64（本次修复）**，其余 ✓ |

响应侧核对：BeginAuth 响应 client_id/steamid 均为 uint64(varint)，与
decodeBeginResponse 一致；GetPasswordRSAPublicKey.timestamp uint64(varint)
与 decodeRSAKey 的 wire==0 守卫一致。

## 知识库同步

- design/platform-steam-api-notes.md §1.1：guard 更新时序中 steamid 标注
  fixed64(wire1) + 事故批注 + 四请求消息类型核对表

## 已知偏差与理由

- 无行为偏差；mock 服务端从不校验 wire type，故此缺陷无法被现有 mock 全流程
  测试捕获——黄金断言按 wire type 锁定编码输出，是防止复发的正确层级。

## 遗留提示

真机重试预期：guard 更新通过 → poll 拿 token → finalizelogin。
若 TOTP 本身错误（与 InvalidParam 区分），将得到 85/88/89 类错误码。
