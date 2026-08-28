# 2026-08-28 ECO 订单同步时区错位（新订单晚 8 小时入库）复盘与修复

## 现象

面板「租赁订单」页显示不全：`lease_orders` 仅 2 单（M9 Blue Steel / Flip Knife），
但 `listings` 有 3 个 `actual_state='leased'`——Karambit | Night 的租赁单
`2026051208423609054609409`（2026-08-27 23:41 UTC 已由 eco_delivery 发货成功）
从未入库。orders_sync 每 10 分钟正常执行、无错误日志，`orders synced eco:2` 恒定。

## 排查

1. 排除展示层：`/orders` → `ListOrders` 查询与前端分页均正常；
2. 排除落库层：UpsertLeaseOrder 无冲突/无 CHECK 违约（状态 'delivering' 合法），
   且 SyncOrders 返回的 len(orders)=2 说明**平台响应里就没有**该单；
3. 真机探针（临时程序直调 SellerRentOrderList，事后删除）：窗口放宽后平台
   返回 **5 单**（含 Karambit 订单与 2 笔 Status=9 已取消单）——单存在，
   是**查询窗口没盖住它**。

## 根因：请求窗与平台时钟差 8 小时（UTC vs 北京时间）

- SellerRentOrderList 的 `StartTime/EndTime` 由客户端 `Format` 自 **UTC**
  time.Time；平台将其与 **北京时间（UTC+8）墙钟**的 CreateTime 字符串比较。
- 于是 `EndTime=now(UTC)` 恒比平台时钟慢 8h：新订单 CreateTime（CST）
  = UTC+8，要等 UTC 时钟"追上"该字符串才进窗——**每单固定晚 8 小时可见**。
  证据闭环：
  - Karambit 单 CreateTime="2026-08-28 07:40:06"（CST）=23:40 UTC 08-27 创建；
    02:18 UTC 的 sync 窗 EndTime="…02:18:18" < 07:40:06 → 不可见；
    预计 07:40 UTC 后才会出现（未等复跑即修复）。
  - Flip Knife 单 CreateTime="2026-08-28 01:49:09"（CST）=17:49 UTC 08-27 创建；
    恰在 01:57 UTC（首个跨过 01:49 的周期）才首次入库——晚 8h 实锤。
- 发货任务 RunECODelivery 因 `end=now+1d` 缓冲侥幸不受影响，故发货正常、同步滞后。
- **连带 bug**：adapter 用 `time.Parse`（UTC 语义）解析响应的 CST 字符串，
  `started_at/due_at/listed_at` 系统性偏早 8 小时（存量 2 单已带偏，
  影响收入归组与 factor 事件的时间口径）。

## 修复

- `platform/eco/endpoints.go`：新增 `ecoCST`（FixedZone UTC+8）与
  `formatEcoTime`/`parseEcoTime`；sellerRentOrderPage 的 StartTime/EndTime
  改经 `formatEcoTime` 渲染。
- `platform/eco/seller_orders.go`：SellerOrderList（出售单列表，日期粒度）
  同样转换（跨日界日期正确偏移）。
- `platform/eco/adapter.go`：LeaseShelf 的 CreateTime、LeaseOrders 的
  CreateTime/RentExpire 改经 `parseEcoTime`（+08 解析→真实 UTC 时刻入库）。
- `store/business.go` UpsertLeaseOrder：ON CONFLICT 增加
  `started_at/due_at=COALESCE(EXCLUDED.x, lease_orders.x)`——存量偏移行
  下一轮同步自动治愈；同时顺带正确处理 UU 续租导致的 due_at 延伸；
  空时间戳载荷不会清空已有值。
- mock 佐证（硬规则）：
  - `TestSellerRentOrderListWindowInPlatformZone`：断言请求窗为 +08 墙钟串；
  - `TestSellerOrderListWindowInPlatformZone`：日期粒度 + 跨日界断言；
  - `TestOffshelfAndOrders` 扩展：Create/RentExpire CST 串 → StartedAt/DueAt
    等于真实 UTC 时刻；
  - `TestAdapterCapsAndShelfMapping` 扩展：货架 ListedAt 同口径断言；
  - 集成测试 `TestUpsertLeaseOrderHealsTimestamps`（store）：重同步治愈偏移、
    空载荷不清空。

## 验证

- 定向：`go test ./internal/platform/eco/` 全绿；集成
  `TEST_DATABASE_URL=…15432/rentauto_test go test -tags=integration ./internal/store/ -run TestUpsertLeaseOrderHeals` 通过。
- `make gate` 全绿（见提交记录；coverage 门控 platform ≥70% 不变）。

## 文档同步

- `design/platform-eco-api-notes.md`：已知坑 #8（时间全为北京时间墙钟）、
  端点表 SellerRentOrderList 行标注 UTC+8。

## 遗留观察（非本次处理）

- SellerRentOrderList **请求 Status 过滤枚举 ≠ 响应 Status 字段枚举**：
  真机探针显示 Status=[2]（等待发货）返回 0 单，而 Status=[3] 返回 3 笔
  响应 Status=2 的单——请求侧枚举疑似为 Progress 语义。发货流当前工作正常
  （发货后订单自然离开等待发货集），暂按"黑盒行为"记录，待平台确认后回写。
