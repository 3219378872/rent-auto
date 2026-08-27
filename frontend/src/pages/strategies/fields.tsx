import type { GroupKey, StrategyParams } from './params'
import { pct, q3 } from './params'

export function SliderField(props: {
  label: string; hint: string; value: number
  min: number; max: number; step: number
  display: 'percent' | 'multiplier'; offAtZero?: boolean
  onChange: (v: number) => void
}) {
  const clamp = (v: number) => q3(Math.min(props.max, Math.max(props.min, v)))
  const shown = props.offAtZero && props.value <= 0
    ? '关闭'
    : props.display === 'percent' ? pct(props.value) : `×${props.value}`
  return (
    <div className="field">
      <div className="field-head">
        <span className="field-label">{props.label}</span>
        <span className="field-val">
          <span className="muted">{shown}</span>
          <input
            type="number" className="num-box" aria-label={`${props.label}数值`}
            min={props.min} max={props.max} step={props.step} value={props.value}
            onChange={(e) => {
              const v = Number(e.target.value)
              if (Number.isFinite(v)) props.onChange(clamp(v))
            }}
          />
        </span>
      </div>
      <input
        type="range" className="slider" aria-label={props.label}
        min={props.min} max={props.max} step={props.step} value={props.value}
        onChange={(e) => props.onChange(clamp(Number(e.target.value)))}
      />
      <div className="hint">{props.hint}</div>
    </div>
  )
}

export function NumField(props: {
  label: string; hint: string; value: number
  min: number; max: number; step?: number; unit?: string
  onChange: (v: number) => void
}) {
  const clamp = (v: number) => Math.min(props.max, Math.max(props.min, v))
  return (
    <div className="field">
      <div className="field-head">
        <span className="field-label">{props.label}</span>
        <span className="field-val">
          <input
            type="number" className="num-box wide" aria-label={`${props.label}数值`}
            min={props.min} max={props.max} step={props.step ?? 1} value={props.value}
            onChange={(e) => {
              const v = Number(e.target.value)
              if (Number.isFinite(v)) props.onChange(clamp(v))
            }}
          />
          {props.unit && <span className="muted">{props.unit}</span>}
        </span>
      </div>
      <div className="hint">{props.hint}</div>
    </div>
  )
}

// ParamGroupsEditor renders the four parameter sections (baseline / factor /
// guardrails / rent terms). Shared by the global strategy form and the
// template-override editor so both stay in lockstep (US-STRAT-02).
export function ParamGroupsEditor(props: {
  form: StrategyParams
  patchGroup: (group: GroupKey, key: string, value: number) => void
  patchInt: (key: 'uu_max_days' | 'eco_max_days', value: number) => void
}) {
  const { form, patchGroup, patchInt } = props
  const b = form.baseline
  const c = form.factor
  const g = form.guardrails

  return (
    <>
      <div className="section">
        <h3 style={{ marginTop: 0 }}>基线定价</h3>
        <div className="form-grid">
          <NumField
            label="topn 行情取样条数" hint="每次为模板拉取的租赁行情条数，取排名前 N 条计算基线"
            value={b.topn} min={1} max={100} unit="条"
            onChange={(v) => patchGroup('baseline', 'topn', Math.round(v))}
          />
          <SliderField
            label="k1 短租基线系数" hint="短租基线 = 行情短租均价 × k1（不低于最低在售价）"
            value={b.k1} min={0.8} max={1.05} step={0.01} display="multiplier"
            onChange={(v) => patchGroup('baseline', 'k1', v)}
          />
          <SliderField
            label="k2 长租基线系数" hint="长租基线 = min(短租基线 × 98%, 行情长租均价 × k2)"
            value={b.k2} min={0.8} max={1.05} step={0.01} display="multiplier"
            onChange={(v) => patchGroup('baseline', 'k2', v)}
          />
          <SliderField
            label="k3 押金基线系数" hint="押金基线 = max(行情押金均值 × k3, 行情最低押金)"
            value={b.k3} min={0.8} max={1.05} step={0.01} display="multiplier"
            onChange={(v) => patchGroup('baseline', 'k3', v)}
          />
          <SliderField
            label="min_lease_ratio 短租价下限" hint="短租价不得低于该比例 × 价值锚点 V，0 表示关闭"
            value={b.min_lease_ratio} min={0} max={1} step={0.01} display="percent" offAtZero
            onChange={(v) => patchGroup('baseline', 'min_lease_ratio', v)}
          />
        </div>
      </div>

      <div className="section">
        <h3 style={{ marginTop: 0 }}>反馈控制器</h3>
        <div className="form-grid">
          <SliderField
            label="factor.min 因子下限" hint="最终报价 = 价格基线 × 反馈因子，因子在此区间内浮动"
            value={c.min} min={0.5} max={1.2} step={0.01} display="percent"
            onChange={(v) => patchGroup('factor', 'min', v)}
          />
          <SliderField
            label="factor.max 因子上限" hint="连续出租后因子封顶值，防止价格脱离市场"
            value={c.max} min={1} max={2} step={0.01} display="percent"
            onChange={(v) => patchGroup('factor', 'max', v)}
          />
          <SliderField
            label="step_up 成功加价步长" hint="每笔订单成交后因子上浮幅度；被买断时上浮翻倍"
            value={c.step_up} min={0} max={0.1} step={0.01} display="percent"
            onChange={(v) => patchGroup('factor', 'step_up', v)}
          />
          <SliderField
            label="step_down 滞销降价步长" hint="在架每超过滞销判定天数未出租，因子下调该幅度"
            value={c.step_down} min={0} max={0.2} step={0.01} display="percent"
            onChange={(v) => patchGroup('factor', 'step_down', v)}
          />
          <NumField
            label="stale_days 滞销判定天数" hint="在架多少天未出租视为滞销并触发降价"
            value={c.stale_days} min={1} max={90} unit="天"
            onChange={(v) => patchGroup('factor', 'stale_days', Math.round(v))}
          />
        </div>
      </div>

      <div className="section">
        <h3 style={{ marginTop: 0 }}>护栏</h3>
        <div className="form-grid">
          <NumField
            label="min_rent 租金下限" hint="租金低于该值直接跳过，避免亏本单"
            value={g.min_rent} min={0} max={100000} step={0.5} unit="¥"
            onChange={(v) => patchGroup('guardrails', 'min_rent', v)}
          />
          <NumField
            label="max_rent 租金上限" hint="租金高于该值直接跳过，拦截异常高价"
            value={g.max_rent} min={1} max={100000} step={1} unit="¥"
            onChange={(v) => patchGroup('guardrails', 'max_rent', v)}
          />
          <SliderField
            label="max_change_ratio 单次改价上限" hint="单次改价相对当前价的最大变动幅度"
            value={g.max_change_ratio} min={0.01} max={1} step={0.01} display="percent"
            onChange={(v) => patchGroup('guardrails', 'max_change_ratio', v)}
          />
          <SliderField
            label="noise_ratio 改价防抖阈值" hint="新旧价格差小于该比例时不改价，防止频繁微调"
            value={g.noise_ratio} min={0} max={0.1} step={0.005} display="percent"
            onChange={(v) => patchGroup('guardrails', 'noise_ratio', v)}
          />
          <NumField
            label="cooldown_minutes 改价冷却" hint="同一挂单两次改价之间的最短间隔"
            value={g.cooldown_minutes} min={0} max={1440} step={5} unit="分钟"
            onChange={(v) => patchGroup('guardrails', 'cooldown_minutes', Math.round(v))}
          />
          <SliderField
            label="deposit_floor_ratio UU 押金下限" hint="UU 押金不低于该比例 × V，控制在外押金风险"
            value={g.deposit_floor_ratio} min={0} max={2} step={0.05} display="percent"
            onChange={(v) => patchGroup('guardrails', 'deposit_floor_ratio', v)}
          />
          <SliderField
            label="deposit_cap_ratio ECO 押金上限" hint="ECO 派生押金超过该比例 × V 时拒绝上架并告警"
            value={g.deposit_cap_ratio} min={0.1} max={5} step={0.1} display="percent"
            onChange={(v) => patchGroup('guardrails', 'deposit_cap_ratio', v)}
          />
        </div>
      </div>

      <div className="section">
        <h3 style={{ marginTop: 0 }}>租期</h3>
        <div className="form-grid">
          <NumField
            label="uu_max_days UU 最长租期" hint="UU 渠道允许的单次最长租赁天数"
            value={form.uu_max_days} min={1} max={365} unit="天"
            onChange={(v) => patchInt('uu_max_days', v)}
          />
          <NumField
            label="eco_max_days ECO 最长租期" hint="参与 ECO 押金派生公式，最低 8 天"
            value={form.eco_max_days} min={8} max={365} unit="天"
            onChange={(v) => patchInt('eco_max_days', v)}
          />
        </div>
      </div>
    </>
  )
}
