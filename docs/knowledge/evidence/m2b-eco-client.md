# M2b ECO 客户端 — 验证证据

日期：2026-08-23 ｜ 分支：feat/m2b-eco-client

## 交付范围

- `platform/eco/sign.go`：官方规范签名（首层键名不区分大小写排序、标量原样/复合紧凑 JSON、
  空值剔除）、PKCS8/PKCS1/raw-base64 私钥解析
- `platform/eco/client.go`：PartnerId+Timestamp+Sign 注入、ResultCode 字符串/整型双形态归一、
  6001→ErrRateLimited 翻译、确定性请求体构造
- `platform/eco/endpoints.go`：可租可售上架/改价、自有租赁货架(分页)、下架(OffshelfRentGoods)、
  出租订单(时间窗分页)、钱包余额、市场全量 dump(双形态兼容)
- `platform/eco/adapter.go`：统一 Adapter 实现（Caps: 押金派生/长租21天/批量100/最小8天）、
  ECO 状态码→统一状态机映射、押金派生预估
- `platform/adapter.go`：新增 `Limiter`、`PartialError`（从 uu 包上移为平台公共件）

## 执行证据

```
go test ./internal/platform/eco/ -coverpkg=./internal/platform/eco
→ ok, coverage: 77.8%（≥70%）

签名黄金测试：官方文档"拼接示例"canonical 串逐字符复现 ✓
签名往返：测试密钥对 sign→verify 通过，篡改载荷拒绝 ✓
mock 契约：上架/改价按 PublishType 与 AssetId 定位、货架分页聚合(101条跨2页)、
下架 goodsNumList 载荷、订单状态映射(8→bought_out)、市场 dump 双形态、6001 限频
```

## 已知偏差与理由

- QuerySteamStock 响应结构按开放平台通用分页假设实现，M3 首次真实同步时校订
- 出租订单查询窗口：当前实现为 [since-1d, now]；平台对时间跨度上限未文档化，
  首次真实调用后如遇 7002(查询时间超限) 需改为滚动窗口——已记入 api-notes 待办
