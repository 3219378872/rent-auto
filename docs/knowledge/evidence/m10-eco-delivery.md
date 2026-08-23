# M10 ECO 发货报价自动化 — 验证证据

日期：2026-08-23 ｜ 分支：feat/m10-eco-delivery

## 交付范围

- `platform/eco/delivery.go`: OneClickResolveOffer(批量发+收) / SellerSendOffer(单订单定向重试)
- Registry.EcoOneClickResolve: 逐单失败判定(ErrorCode!=OK 或 Error 非空)→审计落库
  (order.send_offer_failed / order.accept_offer_failed)
- scheduler 任务 `eco_delivery`(5±0.75min)；main 注入 AuditFn
- 官方流程依据: api-348121359(一键处理)/api-220956666(卖家发送报价),
  ErrorCodes 枚举(OK=1, TooManyPending=108 等)

## 执行证据

```
mock 契约测试: OneClick 双结果数组解析、NeedsMobileConfirmation/Error 字段保真、
  ErrorCode=108 失败判定、SellerSendOffer 载荷(OrderNum/GameId)、空单号拒绝 ✓
覆盖率: platform/eco 78.0% ✓ 全包集成 -p1 全绿 ✓
```

## 设计说明

ECO 的发货与收货均可由开放平台服务端驱动（区别于 UU 收报价需要自有 Steam 会话），
因此 ECO 侧交付闭环不依赖 platform/steam；当逐单返回 NeedsMobileConfirmation=true 时
记录告警，必要时后续把确认动作路由到已有 steam 包。
