# 2026-08-24 全面审查与修复轮（第三轮综合审查）

## 范围

M0–M11 交付后的第三次全面审查。六个专项并行深审（recon+pricing+scheduler /
store+bench+analytics+migrations / platform 三客户端增量复核 / HTTP+auth+
secrets+config+deploy+channels / 前端+知识库一致性 / CI+Makefile+工程化），
关键 P0/P1 发现逐条人工复核源码确认后修复。

## 审查结论摘要

- 门控基线全绿（unit+integration+-race+逐包覆盖率）；前两轮全部修复点仍有
  测试锁定、无退化；认证链/SQL 参数化/AES-GCM/审计覆盖等既有正面结论复核通过
- 上轮遗留 5 项全部确认未修复，且新发现更严重的资金域缺陷（orders_sync 长租
  漏单、recon 写回缺失致重复上架）——本轮全部清零

## 本轮修复清单

| # | 级别 | 发现 | 修复 | 验证 |
|---|---|---|---|---|
| F1 | P0 | orders_sync 固定 24h 回看+UU StartedAt−30d 过滤：≤90d 长租终态滑出窗口→收入记账与因子折算永久漏单 | 动态窗口锚定最早未终态订单−24h（上限 100d，ADR-0004）；store.EarliestOpenOrderStart | 全套件回归+migrate-check |
| F2 | P0 | publish/delist 成功不写回 listings（RecordPublishedListing/MarkListingDelisted 死代码）→跨周期重复真实上架 | Executor.Store（WriteBack 接口）成功即写回；无 goods_ref 回显则跳过并告警 | TestExecutorWriteBackAndPenalize 等 |
| F3 | P0 | reconcile 绕过策略级 dry-run 与风控退避 | effectiveDry=DryRunDefault‖!RealEnabled（fail-closed）；plan 按 ChannelReady 过滤；错误经 NoteChannelError 回灌退避 | 编译+调度测试 |
| F4 | P1 | recon 结构三重缺陷：hash 归并丢多拷贝/delist 不排 leased/孤儿永不下架 | PlanFrom 重写：多拷贝补齐+按 listing 行去重+leased 一律豁免+orphan/surplus 24h 宽限（ADR-0005） | TestPlanFromMultiCopyDeficit/DelistSkipsLeased/OrphanGrace/SurplusCopies |
| F5 | P1 | Steam protobuf 长度 varint≥2^63→int 为负绕守卫→远程 slice panic | uint64 域比较 | TestPBReaderRejectsHostileLength |
| F6 | P1 | ECO 客户端无视 HTTP 状态码+信封缺 ResultCode 视为成功→幽灵下架 | 非200 fail-closed+ResultCode 必须存在 | TestClientRejectsNon200EnvelopelessBody/MissingResultCode |
| F7 | P1 | Registry 构建失败半注册：适配器已删、裸客户端残留继续打平台 | dropUU/dropECO 同步清空 uuClient/ecoClient/ecoSteamID；SetECOCreds fail-closed 检查前置 | TestRefreshBuildFailureLeavesNoHalfState |
| F8 | P1 | UU 订单从不填 AssetID→因子控制器对 UU 渠道静默失效 | LeasedOutOrder 多候选资产字段解析（assetId/steamAssetId），api-notes 待办#14 登记真机校订 | TestLeasedOutOrderAssetRefMapping |
| F9 | P1 | foldOrderEvents 批内同 listing 多订单各读同一 cur→N 次只折 1 次 | 批内顺序累计，每 listing 单条 fold；ApplyFactorFolds(folds, orderIDs) 签名重构 | 因子集成套件全绿 |
| F10 | P1 | 公开端点无 body 上限→无鉴权内存 DoS；Server 缺 Read/Write/IdleTimeout | withBodyLimit(1MiB MaxBytesReader) 中间件+四超时齐备 | TestBodyLimitRejectsOversized |
| F11 | P1 | make migrate-check 缺 -tags=integration 空转；test 默认连开发库（down-all 会 DROP 全部表） | migrate-check 补 tag 并强制指向 rentauto_test；test 目标默认库同步分离 | migrate-check 首次真实运行 PASS |
| F12 | P1 | pricing 无 NaN/Inf 防线（Round2(NaN)→int64 UB） | Decide 入口 finite guard/Baseline 过滤非有限报价/Round2 溢出归零/NextFactor 自愈 reset | TestNonFiniteDefenseLines |
| F13 | P2 | 手动 Trigger 不入停机 WaitGroup；loginLimiter 无界增长；csvCell 零测试 | Trigger 纳入 done WaitGroup+Stop 幂等计数在锁内；limiter 过期清扫；ui.test.ts 补齐 | TestLoginLimiterSweepEvictsExpired 等 |

## 执行的命令与结果

```
make gate   # 修复前基线：GATE PASSED（PG_HOST_PORT=25432, rentauto_test）
make gate   # 修复后：GATE PASSED（fmt/lint/vet/build/unit+integration -race/
            #          cover-gate/migrate-check/前端四件套全绿）
```

新增 mock 反例与单元测试 14 个（见上表验证列），全部通过。

## 已知偏差与理由

- **lease_publish/rollup_daily 未按 spec 恢复为独立任务**：reconcile 周期
  逐条上架已覆盖批量语义的最终一致目标，rollup 内联于 orders_sync 每 10m
  运行比每日一次更及时；functional-spec §3 已回写实况（含备注）
- **UU 订单资产字段多候选解析**：字段名待真机校订期间以兼容方式先行打通
  映射链路（api-notes 待办#14）
- **loginLimiter 仅修内存增长**：per-(IP,user) 粒度下全局爆破次数仍无上限，
  需 per-IP 二级桶设计，移交遗留清单
- **未知路由字符串默认 both** 保持原行为（有测试固化），改动需产品决策

## 审查遗留（未在本轮修复，按优先序）

1. JWT 无吊销机制（登出/改密不失效旧 token，TTL 24h）
2. X-Real-IP 盲信无可信代理判定（现靠 backend 不发布端口纪律兜底）；
   loginLimiter per-IP 二级上限
3. openapi.yaml 3/19 路由覆盖率——需一轮完整回写
4. 哨兵错误语义统一：ECO PartialError 不包装 ErrPartialFailure、PublishLease
   无哨兵、ECO 凭据失效无 ErrAuthExpired 映射（不触发风控冷却）
5. 分页循环无最大页数上限；ECO TotalRecord 虚低时提前截断漏页
6. Refresh 持写锁期间网络 IO（~20s 阻塞全部渠道读取）；空货架同步熔断；
   DB CHECK 约束迁移（状态机枚举入 DB）；foldOrderEvents 的 UU 渠道依赖
   待办#14 真机确认
7. 前端缺口：仪表盘渠道健康告警条/Listings 决策依据列/模板级策略 UI/黑名单
   管理/审计时间过滤与分页
8. CI 强化：集成步缺 -race/golangci-lint 版本浮动 goinstall 慢/react-hooks
   插件缺失/Vite5+Vitest1 EOL 升级计划/workflow concurrency+timeout+
   permissions/pre-push 钩子接线文档化
9. api-notes 回写：Steam 刷新节奏 Go 版简化偏离未记录；两平台 HTTP 状态码
   处理约定缺席文档

## 附：推送后 CI 修复（同轮）

合并推送时发现 main 上 CI 此前已红（前端 job 必败）+本轮暴露的集成竞态：

| 问题 | 修复 | 验证 |
|---|---|---|
| pnpm/action-setup 在仓库根找不到 packageManager（字段在 frontend/ 下）→ 前端 job 必败 | `package_json_file: frontend/package.json` | run 32731619571 frontend ✓ |
| 集成步缺 -p 1：多包并行对共享单库跑迁移升降级→pg_type 竞争/死锁/迁移残留 | 两处集成步补 `-p 1` 对齐 make test 约定 | run 32731619571 backend ✓ |

最终 CI：**frontend ✓ backend ✓**（Node20 deprecation 警告属上游 action，非阻塞）。
