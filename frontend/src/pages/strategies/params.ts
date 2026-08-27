// Strategy parameter model shared by the global form and the template
// override editor (US-STRAT-02). Mirrors pricing.Params in
// backend/internal/pricing/engine.go — keep field names in lockstep.
export interface StrategyParams {
  baseline: { topn: number; k1: number; k2: number; k3: number; min_lease_ratio: number }
  factor: { min: number; max: number; step_up: number; step_down: number; stale_days: number }
  guardrails: {
    min_rent: number; max_rent: number; max_change_ratio: number; noise_ratio: number
    cooldown_minutes: number; deposit_floor_ratio: number; deposit_cap_ratio: number
  }
  uu_max_days: number
  eco_max_days: number
}

export const DEFAULTS: StrategyParams = {
  baseline: { topn: 15, k1: 0.97, k2: 0.95, k3: 0.98, min_lease_ratio: 0 },
  factor: { min: 0.85, max: 1.25, step_up: 0.03, step_down: 0.05, stale_days: 7 },
  guardrails: {
    min_rent: 0.5, max_rent: 20000, max_change_ratio: 0.15, noise_ratio: 0.02,
    cooldown_minutes: 30, deposit_floor_ratio: 0.3, deposit_cap_ratio: 2,
  },
  uu_max_days: 60,
  eco_max_days: 30,
}

export const cloneDefaults = (): StrategyParams => JSON.parse(JSON.stringify(DEFAULTS)) as StrategyParams

export const ROUTES = [
  { value: 'both', label: '双渠道' },
  { value: 'uu_only', label: '仅 UU' },
  { value: 'eco_only', label: '仅 ECO' },
  { value: 'uu_primary_eco_fallback', label: 'UU 优先，失效转 ECO' },
]

export const pct = (v: number): string => `${+(v * 100).toFixed(1)}%`
export const q3 = (v: number): number => Math.round(v * 1000) / 1000

export type GroupKey = 'baseline' | 'factor' | 'guardrails'

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

export function normalizeParams(raw: Record<string, unknown>): StrategyParams {
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

export function validateParams(f: StrategyParams): string {
  if (f.factor.min >= f.factor.max) return '反馈因子下限必须小于上限'
  if (f.guardrails.min_rent >= f.guardrails.max_rent) return '租金下限必须小于上限'
  if (f.guardrails.max_change_ratio <= 0) return '单次改价幅度上限必须大于 0'
  if (f.eco_max_days < 8) return 'ECO 最长租期不可低于 8 天'
  if (f.baseline.topn < 1) return '行情取样条数至少为 1'
  return ''
}
