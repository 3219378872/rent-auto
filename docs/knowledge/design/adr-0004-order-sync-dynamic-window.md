# ADR-0004: orders_sync 动态回看窗口

日期：2026-08-24
状态：已接受

## 背景

orders_sync 原实现固定回看 `now−24h`（scheduler/jobs.go），且各适配器窗口语义
叠加后更短：UU 客户端再按 StartedAt≥since−30d 过滤，ECO 走服务端创建时间窗
再减 1 天。而租约最长可达 90 天（`RentMaxDayMax=90`）：一笔长租在其开始
~59 天后到期终态时，早已滑出所有查询窗口——终态永不入库，
**收入记账（RollupTerminalOrders）与因子折算（UnhandledFactorOrders）永久漏单**。
上游平台不提供 updated-at 检索，真增量不可行。

## 决策

回看锚点改为动态下探：

```
since = max( earliest_open_started_at − 24h_margin , now − 100d )
floor = now − 24h   （无未终态订单时的默认覆盖）
since = min(floor, since)
```

- `EarliestOpenOrderStart`：`MIN(started_at) WHERE status NOT IN terminal`
  （terminal = done/bought_out/cancelled/breach）；
- 只要一笔租约仍未终态，它自身的 started_at 就把窗口钉在足够早的位置，
  到期终态必然落入下一次同步；
- 终态后该订单不再撑住窗口，随其他未终态订单自然收缩；
- 硬上限 100 天（>90d 最大租期 + 双侧 margin），防止一条卡死的非终态脏行
  把同步拖成全量重扫；
- 锚点查询失败降级为默认 24h 并 Warn（不阻塞同步）。

## 后果

- 长租收入不再漏记；spec §3「增量入库」在长租场景恢复成立。
- 同步量上界可控（活跃订单数 × 窗口内订单），限频器保护下游。
