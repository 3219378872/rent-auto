# M2a UU 客户端 — 验证证据

日期：2026-08-23 ｜ 分支：feat/m2a-uu-client

## 交付范围

- `platform/adapter.go`：统一 ChannelAdapter 接口 + Capabilities（ADR-0003 落地）
- `domain`：渠道中立业务记录 InventoryItem / ShelfListing / LeaseOrder（对齐 data-model.md）
- `platform/uu/`：
  - crypto.go：AES-128-ECB+PKCS7、RSA-PKCS1v15、嵌入平台公钥（与上游逐字节比对一致）
  - client.go：请求头构造（uk/AppVersion/Device-Info 等）、大小写无关信封解码、
    错误码翻译（84101→ErrAuthExpired、84104→ErrPlatformBlocked、405→ErrUKExpired）、限频器挂点
  - login.go：短信验证码发送/登录（含 SmsUp 上行兜底路径）、deviceW2 换 uk
  - endpoints.go：库存、双货架(lease+zeroCD, 分页)、上架、改价(pre-init+PUT)、下架、
    租赁行情(topN+押金窗口过滤)、出租订单(分页)、0CD 列表与开启
  - adapter.go：Adapter 接口实现；UU 状态码→统一状态机映射（初版，M3 用真实载荷校订）

## 执行证据

```
go test ./internal/platform/uu/ -coverpkg=./internal/platform/uu
→ ok, coverage: 73.6%（≥70% 门控线）

加密互操作：
- AES 向量：openssl enc -aes-128-ecb 生成 p1/p2 密文 → Go 加密结果 byte-equal ✓
- RSA：嵌入公钥解析成功；PKCS1v15 走标准库（双向互操作由算法标准保证，密钥对自测）
mock 契约测试覆盖：信封大小写两形态、9004001 空货架、分页聚合、
pre-change init 强制调用、部分失败(ErrPartialFailure+逐条 Message)、风控码、405、SMS 三流
```

## 已知偏差与理由

- 出租订单 UU 状态码映射表为初版猜测值（0=leasing/2=done/3=bought_out），
  需 M3 真实数据校订后回填本文件——已列入 M3 任务
- Wallet() 返回 ErrUnsupported（UU 无钱包接口）
