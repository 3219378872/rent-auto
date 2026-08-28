# 2026-08-27 首次真实上架轮：双端点真机校订 + recon 双渠道去重 + ECO-only 仅出租策略落地

## 范围

用户目标：核查三渠道/库存/挂单/订单运行状态；配置「仅 ECOsteam、仅出租」策略；
把 ≥¥500 的仓库饰品全部挂仅出租单（价格按两平台行情基准）。
过程中暴露并修复 3 个真机缺陷（1 个平台端点、1 个平台字段类型、1 个 recon 规划逻辑）。

## 状态核查结论（修复前）

- 三渠道健康（uu/eco/steam ok），ECO 钱包 ¥1735.78；M9 Bayonet|Blue Steel 有一条
  2026-08-23 的 ECO 挂单且**租出中**（未在 lease_orders 中——本轮 orders_sync 后补齐）
- 库存 0 条：inventory_sync 因 ECO 端点 404 整体失败（UU 部分实际可用但未触发成功轮）

## 缺陷与修复（均带 mock/单测佐证）

1. **ECO Steam库存端点 404**：`/Api/Selling/QuerySteamStock` 系转录错误，
   官方 YAML（api-220956670）实际为 **`/Api/Selling/QueryStock`**。
   同类错误第二例（继 GetTotalMoney），已知坑 #7 追加交叉引用。
   连带真机校订响应契约：官方无 `MarkPrice` 字段——改用 `Price`(平台市场价,
   兜底 SteamPrice)+`Tradable`(bool,原硬编码 true)+Status 按 SteamStockStatus
   枚举(1待上架→in_stock, 2/4/6/8/10 上架态→listed, 3/5/7/9/11 交易中→locked)；
   请求体 `GameId`（原 SteamGameId）。mock 按官方 schema 重写并断言路径。
2. **UU 行情字段类型**：`LeaseUnitPrice`/`LongLeaseUnitPrice` 真机为 **JSON string**
   （与 LeaseDeposit 一致），float64 解码致全部模板行情拉取失败 → 0 快照 → 无基线。
   模型改 `*string`+strF（`UnitPrice()`/`LongUnitPrice()`），fixture 改字符串载荷，
   api-notes 已知坑 #7(UU) 记录。修复后 market_snapshot：55 模板 / 907 报价点 / 0 失败。
3. **recon 同一资产重复上架**：同一 Steam 资产在 uu+eco 两渠道各同步一条库存记录，
   PlanFrom 的 wantCopies 按行数计（虚增一倍）、publish pass 不对计划内去重——
   干跑即见 4 把刀每把两条相同 asset_id 的 publish。修复：wantCopies 按
   **distinct asset** 计数 + publish pass 计划内 (hash,ch,asset) 去重；
   新增回归测试 `TestPlanFromDedupesAssetSyncedByBothChannels`。
4. **EnsureGlobalStrategy 无冲突目标**（顺带发现）：`ON CONFLICT DO NOTHING` 无唯一
   约束可依，每次调用插一行——dev 库实测累积 12 条重复全局策略，且高 id 空参数行
   遮蔽迁移种子参数。迁移 0007：去重保留 MIN(id) + `uniq_global_strategy`
   部分唯一索引（scope='global'）；store 改 `ON CONFLICT (scope) WHERE scope='global'`。
5. **ECO SteamId 绑定缺失**：首真实发布轮平台拒绝 `code=2001 SteamId不能为空`——
   凭证保存时未带 steamId，`eco_steam_id` 设置项缺失。补设后发布成功；
   api-notes 新增「SteamId 绑定」节：配置 ECO 渠道必须同时绑定 SteamId。

## 策略配置（最终态）

- 全局策略（单行）：`channel_route=eco_only`，`real_execution_enabled=true`，
  参数保持默认（基线 k1=0.97/k2=0.95/k3=0.98/topn=15；因子 0.85–1.25；
  guardrails：min_rent 0.5 / max_change 15% / cooldown 30min / ECO 押金上限 2×V；
  eco_max_days=30）
- 双平台价格源：UU 租赁行情快照（market_snapshot，20min 周期）+ ECO 全量在售价
  参考（value_anchor，1h 周期）→ 基线/锚点；符合「按两个平台价格」要求
- ≥¥500 门槛：模板黑名单落地——39 个 <¥500 模板拉黑（recon 路由仅含非黑名单模板）；
  ≥¥500 可交易 4 件：Karambit BW(3650)/M9 Night(2164)/Karambit Night(3192)/
  Flip BL(868)，另 M9 Blue Steel(3198) 原租出中挂单继续
- 执行序列遵守 dry-run 门禁：dry-run 校验计划=4 → env `DRY_RUN_DEFAULT=false`
  → 实跑 4/4 成功 → shelf_sync 回读 actual_state=active

## 验证

- `make gate` 全绿（lint/vet/build/test -race/coverage；前端 35 用例；迁移检查）
- 实跑审计：`shelf.publish` ×4（dry_run=false, success=true）；
  listings 5 行 desired=active/actual=active（含原租出中 1 条）
- 上架结果：M9 Night ¥1.96/日 押 3028.90；Karambit BW ¥3.33/日 押 5110.04；
  Karambit Night ¥2.22/日 押 4468.86；Flip BL ¥0.72/日 押 1215.23（30 天期）

## 第二波：租赁订单发货链路真机校订（同日）

首查"租赁订单是否正常运行"发现：8-23 的 M9 Blue Steel 租单停在
等待发货(delivering) 已 4 天——租赁发货链路缺失，逐层修复：

1. **orders_sync 冷启动缺口**：回看窗扩展依赖 lease_orders 已有行（先有鸡还是
   先有蛋）。修复：`EarliestLeasedListingStart`（租出中挂单的 listed_at 为锚，
   订单不可能早于挂单）并入 `orderSyncWindow` 纯函数 + 单测。首跑即拉回该租单。
2. **ECO 租赁单不在出售订单视图**：SellerOrderList(DetailsState=8，带/不带
   SteamId) 均查不到租赁单（真机实测）——既有交付环（Steamauto 对齐的出售
   四步流）对租赁单完全失明。补齐出租域路径：
   `SellerRentOrderDetail`（api-220956684，OfferId=平台预创建的租赁报价ID，
   SendOfferRole 指示发送责任方）+ 交付环 rent pass（Status=2 → 取报价 →
   Steam 确认器接受，跨周期幂等）。mock：route 钉死 + `TestECODeliveryRentPass`。
3. **SellerRentOrderList 31 天窗口上限（销项 #E1）**：`code=7002
   最大支持查询31天内数据`——客户端改 30 天分段聚合（≤365d 硬钳制），
   orders_sync 长租回看窗（≤100d）无感兼容；mock 断言每段 ≤31d。
4. **Steam 确认器 creator_id 是字符串**：getlist 返回 `"creator_id":"<digits>"`
   （api-notes §3 已批注），int64 解码致 confirmlist 整体失败；改 string 精确匹配。
5. **OneClick 兜底日志语义**：`ErrorCode=0 + Error="" + NeedMobileConfirmation=true`
   是「待移动端确认」（我们的确认器即处理此事）而非失败——registry 区分
   info 日志，不再误报 accept_offer_failed。

**结果**：eco_delivery 实跑 `delivered 2026051208378389491433473/9328619377`——
卡 4 天的租单完成卖方确认，进入待租客接单；orders_sync 后续轮次将跟进状态流转。
make gate 全绿。

## 遗留与移交

- 黑名单是运营侧一次性门槛：**新入库 <¥500 模板不会自动拉黑**，会进入路由并发布。
  若要持久策略需在 pricing.Params 增加 min_list_mark 门槛参数（待办，未实现）
- ECO QueryStock 单页 ≤100（与 UU 行为对称）；库存超 100 需分页
- 未提交：本轮代码/文档变更在工作区，待用户确认后按 Conventional Commits 提交
- 租单 M9 Blue Steel 的最终状态（租赁中/归还）由租客接单后 orders_sync 跟进
