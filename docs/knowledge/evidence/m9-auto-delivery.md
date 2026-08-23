# M9 自动发货与 Steam 收报价 — 验证证据

日期：2026-08-23 ｜ 分支：feat/m9-auto-delivery

## 交付范围

1. **UU 自动发货**：`platform/uu/delivery.go`——待办列表分页、平台代发报价、
   状态轮询(3=成功)；任务 `uu_delivery`(5±0.75min)
2. **Steam 会话**：`platform/steam`——IAuthenticationService protobuf 手写编解码
   （字段号自上游 pb2 提取）、三级登录策略(access_token 恢复→refresh_token→账密)、
   JWT exp 解析、token 自适应刷新节奏
3. **Steam 收报价**：GetTradeOffers(access_token 免 API key) → items_to_give 为空才接受
   （覆盖礼物+租赁归还）→ 接受 → mobileconf 二次确认(conf/details/ajaxop,
   identity_secret HMAC 签名) → 七天暂挂检测；任务 `steam_offers`(5±0.75min)
4. 凭证管理：Steam 四要素 AES-GCM 加密落库，面板 Channels 页表单 + 健康徽章

## 执行证据

```
guard 向量交叉验证（执行上游 guard.py 生成）：OTP/confirmation_key/device_id 全部一致 ✓
BeginAuthSession protobuf 编码 vs Python 序列化 byte-equal ✓
mock 全链路：登录(RSA→guard→poll→finalize→acknowledge) ✓
            token 刷新(GenerateAccessTokenForApp) ✓
            接受报价(accept→getlist→ajaxop allow) 表单与签名参数断言 ✓
覆盖率: platform/steam 74.0%（纯逻辑域 ≥70% 达标）
```

## 待真机校订（记入 api-notes 待办）

- 确认器 details 页 HTML 的正则提取（bs4 等价实现）需真实页面回归
- GetTradeOffers 失败时的 HTML 兜底抓取暂未移植（告警+下轮重试）
- DeviceConfirmation(4) 分支的 "ok" code 语义需真机验证
