# 定价引擎规格

> 引擎输入：模板行情快照集、价值锚点 V、策略参数、反馈状态。
> 引擎输出：`Decision{rent, long_rent, max_days, deposit, reasons[], guardrails_hit[]}`。
> 所有输出金额 Round2；任何字段无有效解 → Decision.invalid + 原因。

## 1. 价值锚点 V（跨平台基准）

```
V = median(候选集中非空值)
候选 = {uu_mark_price, eco_ref_price}
两者皆缺 → V 无效，该模板跳过自动定价（告警一次/天）
```

## 2. UU 行情基线（移植自 Steamauto get_lease_price，参数化）

对模板拉取租赁行情 topN（默认15，按 LEASE_DEFAULT 排序）：

```
shorts   = 前 min(10,n) 条 LeaseUnitPrice
longs    = 全部 LongLeaseUnitPrice
deposits = shorts 中带 LeaseDeposit 者

base_short  = clamp(mean(shorts)×k1, floor=shorts[0], min=0.01)      k1 默认0.97
base_long   = longs 为空 ? base_short×0.98
            : clamp(min(base_short×0.98, mean(longs)×k2), floor=longs[0])  k2 默认0.95
base_dep    = deposits 空 ? 0 : max(mean(deposits)×k3, min(deposits)) k3 默认0.98
比例下限    : short ≥ min_lease_ratio×V（fix_lease_ratio 移植，默认关闭）
缓存        : 模板级 20 分钟 TTL
```

## 3. 反馈控制器（收益最大化核心）

每 (channel, hash_name) 维护控制状态：

```
factor ∈ [f_min, f_max]（默认 [0.85, 1.25]，初始 1.00）
信号：
  rent_success   （订单完成，租期<max_days）→ factor += step_up(0.03)
  stale          （在架≥stale_days 未出租）  → factor -= step_down(0.05)，每过 stale_days 再降
  bought_out                                 → factor += step_up×2
最终报价 = base × factor，再过护栏与边界
```

- 因子持久化于 listings/decision 历史；重置条件：连续 stale 降价至 f_min 后仍无转化 → 回归 1.00 并告警"建议人工检查"
- 冷启动：新上架商品 factor=1.00，前 cooldown 内不改价

## 4. 渠道分化决策

### UU（押金直控）
```
rent = round2(base_short × factor)
long = round2(min(rent×0.98, base_long))
deposit = max(base_dep, deposit_floor_ratio×V)     -- 风险下限抬升
max_days = strategy.uu_max_days（默认60）
```

### ECO（三元组求解）
ECO 押金为派生值：`dep_eco = max(V×1.4, rent×D, long×D)`（D=max_days）。
观察：日租金通常 ~0.1%V ⇒ rent×D 远小于 1.4V ⇒ **dep_eco 实际≈1.4V 恒定**，
因此 ECO 的真实自由度是 (rent, long, D) 的收益-周期权衡：

```
目标：maximize E[收入/天] = P(rent 成交概率模型) × rent / E[占用天数]
简化工程解（首版）:
  D = strategy.eco_max_days（默认30）
  rent = round2(base_short × factor)
  long = round2(min(rent×0.98, base_long))
  校验: dep_eco ≤ deposit_cap_ratio×V 否则拒绝并告警
后续版本引入成交概率模型（依赖 own_order 快照积累）——见 Open Questions
```

## 5. 策略参数结构（strategies.params jsonb schema）

```jsonc
{
  "topn": 15, "k1": 0.97, "k2": 0.95, "k3": 0.98,
  "min_lease_ratio": 0,           // short 下限 = ratio×V；0=关
  "factor": {"min":0.85,"max":1.25,"step_up":0.03,"step_down":0.05,"stale_days":7},
  "uu_max_days": 60,
  "eco_max_days": 30,
  "guardrails": {
    "min_rent": 0.5, "max_rent": 20000,
    "max_change_ratio": 0.15, "cooldown_minutes": 30,
    "deposit_floor_ratio": 0.3,   // UU 押金下限 =ratio×V
    "deposit_cap_ratio": 2.0      // ECO 押金上限 =ratio×V
  },
  "route": "uu_primary_eco_fallback"
}
```

合并规则：template 策略深覆盖 global；未设字段回落 global→内置默认。

## 6. 黄金测试要求（M4 验收）

- 用 Steamauto 已知行为构造 fixture：给定行情数组 → 断言 base_short/long/dep 与 Python 版一致
- 边界：空行情、单条行情、longs 缺失、deposits 缺失、V 缺失
- 控制器：连租→升到 f_max 封顶；滞销→阶梯降级；冷却期内不动
- ECO：cap 超限拒绝路径；D 边界(8≤D)

## Open Questions
- ECO 成交概率模型需要多少自有订单样本才可信？→ M7 后用真实数据回测定
- UU 0CD 转租对 factor 是否应视为独立收益事件？→ 观察 M7 数据再定
