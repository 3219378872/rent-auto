# ADR-0003: 渠道适配器统一接口与能力协商（追认）

日期：2026-08-23（实现）/ 2026-08-24（本文档追认）
状态：已接受

## 背景

UU 与 ECO 两渠道在批量上限、押金语义（显式 vs 派生）、租期天数边界等行为上
存在差异。上层（定价引擎、reconcile）需要知道这些差异，但不得内嵌渠道判断。

## 决策

- `platform.Adapter` 统一接口（Channel/Caps/Healthy/Inventory/LeaseShelf/
  PublishLease/RepriceLease/Delist/LeaseOrders/Wallet），业务层只见接口。
- `platform.Capabilities` 结构体承载渠道差异：`DepositDirect`、
  `LongLeaseThresholdDays`、`MaxBatchPublish/Reprice`、`RentMaxDayMin/Max`。
  上层通过 `Caps()` 读取，不硬编码渠道常量。
- 哨兵错误（ErrUnsupported / ErrAuthExpired / ErrRateLimited /
  ErrPlatformBlocked / ErrPartialFailure）+ `PartialError{Ref,Msg}` 表达逐项失败。

## 后果

- reconcile 的 decideFor 已于 2026-08-24 审查轮改为优先读 `Caps().RentMaxDayMin/Max`，
  消除了与 reprice 路径的口径漂移。
- 遗留：哨兵语义仍有不一致（ECO PartialError 不包装 ErrPartialFailure、
  PublishLease 无哨兵）——见 2026-08-24 第三轮审查遗留清单。
