import { useCallback, useEffect, useState } from 'react'
import { api } from '../api/client'
import type { StrategyRow } from '../api/client'

interface StrategyParams {
  baseline: { topn: number; k1: number; k2: number; k3: number; min_lease_ratio: number }
  factor: { min: number; max: number; step_up: number; step_down: number; stale_days: number }
  guardrails: {
    min_rent: number; max_rent: number; max_change_ratio: number; noise_ratio: number
    cooldown_minutes: number; deposit_floor_ratio: number; deposit_cap_ratio: number
  }
  uu_max_days: number
  eco_max_days: number
}

const DEFAULTS: StrategyParams = {
  baseline: { topn: 15, k1: 0.97, k2: 0.95, k3: 0.98, min_lease_ratio: 0 },
  factor: { min: 0.85, max: 1.25, step_up: 0.03, step_down: 0.05, stale_days: 7 },
  guardrails: {
    min_rent: 0.5, max_rent: 20000, max_change_ratio: 0.15, noise_ratio: 0.02,
    cooldown_minutes: 30, deposit_floor_ratio: 0.3, deposit_cap_ratio: 2,
  },
  uu_max_days: 60,
  eco_max_days: 30,
}

const cloneDefaults = (): StrategyParams => JSON.parse(JSON.stringify(DEFAULTS)) as StrategyParams

const ROUTES = [
  { value: 'both', label: '双渠道' },
  { value: 'uu_only', label: '仅 UU' },
  { value: 'eco_only', label: '仅 ECO' },
  { value: 'uu_primary_eco_fallback', label: 'UU 优先，失效转 ECO' },
]

const pct = (v: number): string => `${+(v * 100).toFixed(1)}%`
const q3 = (v: number): number => Math.round(v * 1000) / 1000

type GroupKey = 'baseline' | 'factor' | 'guardrails'

function applyGroup<T extends object>(base: T, src: unknown): T {
  const out: Record<string, unknown> = { ...(base as Record<string, unknown>) }
  if (src && typeof src === 'object') {
    for (const [k, v] of Object.entries(src as Record<string, unknown>)) {
      if (k in out && typeof v === 'number' && Number.isFinite(v)) out[k] = v
    }
  }
  return out as T
}

function flatOf(raw: Record<string, unknown>, keys: string[]): Record<string, unknown> | undefined {
  const flat: Record<string, unknown> = {}
  let hit = false
  for (const k of keys) {
    if (typeof raw[k] === 'number' && Number.isFinite(raw[k])) {
      flat[k] = raw[k]
      hit = true
    }
  }
  return hit ? flat : undefined
}

function normalizeParams(raw: Record<string, unknown>): StrategyParams {
  const d = cloneDefaults()
  return {
    baseline: applyGroup(d.baseline, raw.baseline ?? flatOf(raw, ['topn', 'k1', 'k2', 'k3', 'min_lease_ratio'])),
    factor: applyGroup(d.factor, raw.factor ?? flatOf(raw, ['min', 'max', 'step_up', 'step_down', 'stale_days'])),
    guardrails: applyGroup(
      d.guardrails,
      raw.guardrails ?? flatOf(raw, [
        'min_rent', 'max_rent', 'max_change_ratio', 'noise_ratio',
        'cooldown_minutes', 'deposit_floor_ratio', 'deposit_cap_ratio',
      ]),
    ),
    uu_max_days: typeof raw.uu_max_days === 'number' && Number.isFinite(raw.uu_max_days) ? Math.round(raw.uu_max_days) : d.uu_max_days,
    eco_max_days: typeof raw.eco_max_days === 'number' && Number.isFinite(raw.eco_max_days) ? Math.round(raw.eco_max_days) : d.eco_max_days,
  }
}

function SliderField(props: {
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

function NumField(props: {
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

const HELP_ROWS: { group: string; name: string; def: string; desc: string }[] = [
  { group: '基线定价', name: 'topn 行情取样条数', def: '15', desc: '每次为模板拉取的租赁行情条数，按平台排序取前 N 条计算价格基线。越大越平滑，越小对最新行情反应越快。' },
  { group: '基线定价', name: 'k1 短租基线系数', def: '97%', desc: '短租基线 = 行情短租均价 × k1（且不低于最低在售价）。调低更易出租，调高单价更高但成交变慢。' },
  { group: '基线定价', name: 'k2 长租基线系数', def: '95%', desc: '长租基线 = min(短租基线 × 98%, 行情长租均价 × k2)。长租价始终略低于短租以引导长期订单。' },
  { group: '基线定价', name: 'k3 押金基线系数', def: '98%', desc: '押金基线 = max(行情押金均值 × k3, 行情最低押金)。跟随市场押金水位。' },
  { group: '基线定价', name: 'min_lease_ratio 短租下限比例', def: '0（关闭）', desc: '短租价不得低于该比例 × 价值锚点 V（跨平台基准价），防止行情异常时贱租。' },
  { group: '反馈控制器', name: 'factor.min / factor.max 因子区间', def: '85% ~ 125%', desc: '最终报价 = 价格基线 × 反馈因子。因子初始 100%，随成交上调、滞销下调，在此区间内浮动。' },
  { group: '反馈控制器', name: 'step_up 成功加价步长', def: '3%', desc: '每笔租赁订单成交后因子上浮幅度；被买断时上浮幅度翻倍。' },
  { group: '反馈控制器', name: 'step_down 滞销降价步长', def: '5%', desc: '在架每超过 stale_days 天仍未出租，因子下调该幅度，阶梯式降价促销。' },
  { group: '反馈控制器', name: 'stale_days 滞销判定天数', def: '7 天', desc: '在架多少天未出租视为滞销，触发 step_down 降价。' },
  { group: '护栏', name: 'min_rent / max_rent 租金区间', def: '¥0.5 ~ ¥20000', desc: '单日租金允许区间，超出直接跳过上架/改价并记录原因，拦截异常报价。' },
  { group: '护栏', name: 'max_change_ratio 单次改价幅度上限', def: '15%', desc: '单次改价相对当前价的 最大变动比例，防止一次调价过猛。' },
  { group: '护栏', name: 'noise_ratio 改价防抖阈值', def: '2%', desc: '新价格与现价差异小于该比例时不改价，避免无意义的频繁微调。' },
  { group: '护栏', name: 'cooldown_minutes 改价冷却时间', def: '30 分钟', desc: '同一挂单两次改价之间的最短间隔分钟数。' },
  { group: '护栏', name: 'deposit_floor_ratio UU 押金下限比例', def: '30%', desc: 'UU 渠道押金不低于该比例 × V，控制在外押金风险敞口。' },
  { group: '护栏', name: 'deposit_cap_ratio ECO 押金上限比例', def: '200%', desc: 'ECO 渠道派生押金超过该比例 × V 时拒绝上架并告警，防止押金虚高无人可租。' },
  { group: '租期', name: 'uu_max_days UU 最长租期', def: '60 天', desc: 'UU 渠道允许的单次最长租赁天数。' },
  { group: '租期', name: 'eco_max_days ECO 最长租期', def: '30 天', desc: 'ECO 渠道允许的最长租赁天数，参与 ECO 押金派生公式，最低 8 天。' },
]

export default function Strategies() {
  const [list, setList] = useState<StrategyRow[]>([])
  const [globalRow, setGlobalRow] = useState<StrategyRow | null>(null)
  const [form, setForm] = useState<StrategyParams>(cloneDefaults)
  const [route, setRoute] = useState('both')
  const [real, setReal] = useState(false)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const load = useCallback(() => {
    api.get<StrategyRow[]>('/strategies').then((rows) => {
      setList(rows)
      const g = rows.find((r) => r.scope === 'global')
      if (g) {
        setGlobalRow(g)
        setForm(normalizeParams(g.params ?? {}))
        setRoute(g.channel_route)
        setReal(g.real_execution_enabled)
      }
    }).catch((e) => setErr(e.message))
  }, [])

  useEffect(load, [load])

  const patchGroup = (group: GroupKey, key: string, value: number) =>
    setForm((f) => ({ ...f, [group]: { ...f[group], [key]: value } }) as StrategyParams)

  const patchInt = (key: 'uu_max_days' | 'eco_max_days', value: number) =>
    setForm((f) => ({ ...f, [key]: Math.round(value) }))

  const validate = (f: StrategyParams): string => {
    if (f.factor.min >= f.factor.max) return '反馈因子下限必须小于上限'
    if (f.guardrails.min_rent >= f.guardrails.max_rent) return '租金下限必须小于上限'
    if (f.guardrails.max_change_ratio <= 0) return '单次改价幅度上限必须大于 0'
    if (f.eco_max_days < 8) return 'ECO 最长租期不可低于 8 天'
    if (f.baseline.topn < 1) return '行情取样条数至少为 1'
    return ''
  }

  const save = async () => {
    setErr(''); setMsg('')
    const invalid = validate(form)
    if (invalid) { setErr(invalid); return }
    try {
      await api.put('/strategies/global', {
        params: form,
        channel_route: route,
        real_execution_enabled: real,
      })
      setMsg('策略已保存')
      load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  const b = form.baseline
  const c = form.factor
  const g = form.guardrails

  return (
    <div>
      <h2>上架 / 改价策略</h2>
      {err && <div className="error">{err}</div>}
      {msg && <div className="ok-msg">{msg}</div>}

      <div className="section">
        <h3 style={{ marginTop: 0 }}>渠道与执行</h3>
        <div className="row" style={{ flexWrap: 'wrap' }}>
          <span className="field-label">渠道路由</span>
          <div className="seg" role="radiogroup" aria-label="渠道路由">
            {ROUTES.map((r) => (
              <button
                key={r.value} type="button" role="radio" aria-checked={route === r.value}
                className={route === r.value ? 'active' : ''}
                onClick={() => setRoute(r.value)}
              >
                {r.label}
              </button>
            ))}
          </div>
          <label className="switch">
            <input type="checkbox" checked={real} onChange={(e) => setReal(e.target.checked)} />
            <span className="track" />
            允许真实执行（关闭 = 永远 dry-run）
          </label>
        </div>
        <div className="hint" style={{ marginTop: 8 }}>
          新策略首次执行必须保持 dry-run：完整走决策链但只写模拟记录、不调平台接口；
          在「审计」页核对模拟决策无误后再开启真实执行。
        </div>
      </div>

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

      <div className="toolbar">
        <button onClick={save}>保存全局策略</button>
        <button className="ghost" onClick={() => setForm(cloneDefaults())}>恢复默认值</button>
        <span className="muted">
          最近更新：{globalRow ? new Date(globalRow.updated_at).toLocaleString('zh-CN') : '—'}
        </span>
      </div>

      <details className="help section">
        <summary>字段详细说明（作用 · 默认值 · 调整影响）</summary>
        <table className="grid">
          <thead><tr><th>分组</th><th>字段</th><th>默认值</th><th>说明</th></tr></thead>
          <tbody>
            {HELP_ROWS.map((r, i) => (
              <tr key={i}>
                <td>{r.group}</td><td>{r.name}</td>
                <td className="muted">{r.def}</td>
                <td style={{ whiteSpace: 'normal' }}>{r.desc}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <div className="hint" style={{ marginTop: 10 }}>
          合并规则：模板级策略深覆盖全局策略，未设字段回落全局 → 内置默认值。
          完整公式见 docs/knowledge/spec/pricing-spec.md。
        </div>
      </details>

      <div className="section">
        <h3 style={{ marginTop: 0 }}>全部策略行</h3>
        <table className="grid">
          <thead><tr><th>ID</th><th>名称</th><th>范围</th><th>路由</th><th>真实执行</th><th>优先级</th></tr></thead>
          <tbody>
            {list.map((s) => (
              <tr key={s.id}>
                <td>{s.id}</td><td>{s.name}</td><td>{s.scope}</td>
                <td>{s.channel_route}</td>
                <td>{s.real_execution_enabled ? '是' : '否'}</td>
                <td>{s.priority}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
