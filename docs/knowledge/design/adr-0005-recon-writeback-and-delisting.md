# ADR-0005: recon 写回闭环与下架安全策略

日期：2026-08-24
状态：已接受

## 背景

第二轮审查确认 recon 四项结构性缺陷（2026-08-24-review-and-fixes.md 遗留#1）：

1. `RecordPublishedListing` / `MarkListingDelisted` 为死代码——publish/delist
   成功后不写回 listings 表，下一个 reconcile 周期会再次规划同一动作，
   对 publish 而言即**重复真实上架同一资产**（幂等承诺破裂）；
2. hash_name 归并只保留最后一条 listing——多拷贝库存永不补齐上架、
   delist 只产出一条动作、同 hash 多拷贝产生重复动作；
3. delist 不排除 leased 状态——UU 探活失败的 failover 会把**出租中**的
   商品真实下架；
4. 孤儿 listing（hash 已不在可路由库存中：售出/模板拉黑/资产转出）永不下架。

## 决策

**写回闭环**：`Executor.Store`（`recon.WriteBack` 接口，`*store.Store` 实现）
在平台调用真实成功后立即落库：
- publish 成功 → `RecordPublishedListing`（要求响应回带 goods_ref；缺失则
  跳过写回并告警，shelf_sync 仍为该场景兜底）；
- delist 成功 → `MarkListingDelisted`。
写回失败记 Error 日志（丢失写回意味着下周期重复上架，必须显性暴露）。

**期望状态推导**（按 hash 归并，路由来自策略、同 hash 各拷贝一致）：
- publish：资产在渠道 ch 上架 ⇔ 该资产无 (ch) 精确匹配 listing 且
  (hash,ch) 存量数 < 可路由拷贝数（多拷贝补齐 + 单周期去重）；
- delist：listing 下架 ⇔ 其渠道不在该 hash 期望集（route 变更/failover），
  或存量超出期望拷贝数（surplus_copies），或 hash 已无任何可路由锚
  （orphan_not_routable）；动作按 listing 行生成，天然去重。

**安全护栏**：
- leased 状态一律不下架（租约结束后下一周期自然重新评估）；
- not_routed / failover 维持即时下架（原行为）；
- orphan / surplus 引入 24h 宽限期（`DefaultOrphanGrace`，锚定
  actual_synced_at）：吸收库存同步抖动，避免一次坏周期触发全量真下架；
  同步时间未知（零值）按保守跳过处理。

**门禁接线**（同轮 F3）：reconcileFn 执行前计算
`effectiveDry = DryRunDefault || !global.RealEnabled`（查询失败强制 dry-run），
与 reprice 双层门禁对齐（AC-T1）；plan 按 `Deps.ChannelReady` 过滤风控冷却
渠道，传输错误经 `Executor.Penalize → Deps.NoteChannelError` 回灌冷却。

## 后果

- recon 包幂等契约首次真正成立；跨周期重复上架窗口消除。
- 缺失 adapter / 未知 Kind 从静默计数升级为审计记录。
- shelf_sync 仍为独立事实源（upsert），写回与其收敛方向一致。
