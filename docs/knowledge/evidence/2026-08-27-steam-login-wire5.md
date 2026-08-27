# 2026-08-27 Steam 真机登录失败修复（wire type 5）

## 范围

用户配置真实 Steam 账号登录失败，面板报错：

```
{"code":"login_failed","message":"steam login failed: steam: bad protobuf: wire type 5"}
```

根因定位与修复均在 `backend/internal/platform/steam/`（纯逻辑域）。

## 根因

手写 protobuf 解码器 `proto.go` 的 `pbReader.next()` 只支持 wire type 0(varint)/
2(length-delimited)，其他一律报错。而真实 Steam 的
`BeginAuthSessionViaCredentials` 响应携带 **field 3 `interval`，类型 float
（wire type 5，fixed32 LE）**，来源：参考件描述符
`/Steamauto/protobufs/steammessages_auth/steamclient_pb2.py`（Python 反序列化
FileDescriptorSet 实测，见 design/platform-steam-api-notes.md §1.1 批注）。

既有 mock（`login_test.go` TestLoginFullFlow）构造的响应恰好不含该字段，
故测试全绿而真机必挂——登录状态机在步骤 ③ `decodeBeginResponse` 处中断，
guard 分支/轮询/finalize 均未执行。

## 修复

1. `pbReader.next()` 增加 wire type 1(fixed64)/5(fixed32)：按小端解析为 num 返回，
   解码器可按字段号选择性消费、天然跳过无关字段；截断报 `errProto`。
   group(3/4) 保持不支持（现代 proto 不再生成）。
2. `pbWriter` 增加 `f32`/`f64`（仅供测试构形真实响应）。
3. mock 佐证升级：TestLoginFullFlow 的 BeginAuthSession 响应加入
   `interval=3(float)`，与真机形状一致，防止回归再次逃逸。

## 红→绿证据

- 红（仅回退 reader、保留 writer）：
  `go test ./internal/platform/steam/ -run 'TestPBReader|TestDecodeBeginResponse|TestLoginFullFlow'`
  → `TestLoginFullFlow: steam: bad protobuf: wire type 5`（用户原始报错精确复现）、
  `TestPBReaderSkipsFixedFields` / `TestDecodeBeginResponseWithInterval` FAIL
- 绿（恢复 reader 修复）：
  `go test ./internal/platform/steam/ -count=1 -race -cover`
  → `ok ... coverage: 77.7% of statements`（≥70% 门控线上）

## 新增/变更测试

- TestPBReaderSkipsFixedFields：varint→fixed32→fixed64→bytes 混合遍历，跳过语义
- TestPBReaderTruncatedFixedFields：fixed64/fixed32 截断必须报错（防 panic/越界）
- TestPBReaderStillRejectsGroups：wire 3 仍拒绝
- TestDecodeBeginResponseWithInterval：含 interval 的真实形状响应全字段解码正确
- TestLoginFullFlow：mock 响应形状对齐真机

## 知识库同步

- design/platform-steam-api-notes.md §1.1：响应字段补 `interval=3(float32, wire 5)`
  + 解码器约束批注（未文档化平台行为归档）

## 已知偏差与理由

- BeginAuth 请求编码不变；响应中 interval 值本项目不消费（上游用于轮询节拍，
  Go 版 PollAuthSessionStatus 为单次同步轮询，无需节拍参数）
- 前端无改动；openapi 无路由变化

## 遗留提示

修复后真机重试登录仍可能遇到独立问题（密码错误/风控/网络代理）——
那些路径在登录状态机后续步骤，错误文案不同，不在本次范围。
