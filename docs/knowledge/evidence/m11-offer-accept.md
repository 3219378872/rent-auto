# M11 ECO 发货闭环补全：Steam 会话同意报价 — 验证证据

日期：2026-08-23 ｜ 分支：feat/m11-offer-accept

## 背景与缺口

用户指出"发送报价后还需在 Steam 里同意"。逐行核对 Steamauto 确认：
ECOsteam.py __auto_accept_offer 为四步流——SellerOrderList(DetailsState=8)→
SellerSendOffer→SellerOrderDetail.TradeOfferId→steam.accept_trade_offer。
M10 只实现了平台侧发起（+OneClick兜底），缺 ②③ 两步。api_key 全程未参与
（Steamauto api_key=""），"同意"= 会话 cookie + identity_secret 确认器，
该能力已在 M9 steam 包就绪。

## 交付

- eco: SellerOrderList(分页/DetailsState/时间窗) + SellerOrderDetail(TradeOfferId)
- scheduler.EcoDeliveryDeps.RunECODelivery：四步编排纯函数（依赖接口化可测），
  含 TradeOfferId 幂等表、失败审计(order.send_offer_failed/detail_failed/
  accept_offer_failed)、成功审计(order.delivered)
- main 接线：ecoDeps(Eco=registry透传, Steam=SteamSession)；OneClick 降级为兜底
- channels.SteamSession.AcceptTradeOffer 导出（EnsureSession→ResolvePartner→Accept）

## 执行证据

```
RunECODelivery 覆盖率 90%：
  四步正常路径(ZH1发→收号→接受; ZH2直接接受; ZH3无号跳过) ✓
  幂等：accept 失败的 offer 第二轮被 memo 跳过(attempts==1) ✓
  发送失败跳过接受并审计 ✓
eco: SellerOrderList 载荷(DetailsState=8)+Detail 解析 ✓ 77.9%
scheduler 全包测试通过；全量 gate 见提交流水
```

## 已知边界

- 忽略表为进程内存态，重启后首轮可能重复尝试同一 offer——accept 对已处理报价
  返回 Invalid state 类错误，走失败审计不阻塞（上游同样行为）
- details 页 HTML 正则提取仍待真机回归（继承 M9 待办）
