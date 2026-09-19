import { useId, useState } from 'react'
import type { GroupKey, StrategyParams } from './params'
import { pct, q3 } from './params'

type FieldSpec = {
  group?: GroupKey
  key: string
  label: string
  hint: string
  min: number
  max: number
  step?: number
  display?: 'percent' | 'multiplier'
  unit?: string
  offAtZero?: boolean
}
export const FIELDS: FieldSpec[] = [
  {
    group: 'baseline',
    key: 'topn',
    label: 'topn 行情取样条数',
    hint: '取排名前 N 条租赁行情计算基线',
    min: 1,
    max: 100,
    unit: '条',
  },
  {
    group: 'baseline',
    key: 'k1',
    label: 'k1 短租基线系数',
    hint: '短租基线 = 行情短租均价 × k1',
    min: 0.8,
    max: 1.05,
    step: 0.01,
    display: 'multiplier',
  },
  {
    group: 'baseline',
    key: 'k2',
    label: 'k2 长租基线系数',
    hint: '长租基线不超过短租基线的 98%',
    min: 0.8,
    max: 1.05,
    step: 0.01,
    display: 'multiplier',
  },
  {
    group: 'baseline',
    key: 'k3',
    label: 'k3 押金基线系数',
    hint: '押金基线不低于行情最低押金',
    min: 0.8,
    max: 1.05,
    step: 0.01,
    display: 'multiplier',
  },
  {
    group: 'baseline',
    key: 'min_lease_ratio',
    label: 'min_lease_ratio 短租价下限',
    hint: '短租价不低于此比例 × 价值锚点，0 关闭',
    min: 0,
    max: 1,
    step: 0.01,
    display: 'percent',
    offAtZero: true,
  },
  {
    group: 'factor',
    key: 'min',
    label: 'factor.min 因子下限',
    hint: '最终报价 = 价格基线 × 反馈因子',
    min: 0.5,
    max: 1.2,
    step: 0.01,
    display: 'percent',
  },
  {
    group: 'factor',
    key: 'max',
    label: 'factor.max 因子上限',
    hint: '连续出租后的因子封顶值',
    min: 1,
    max: 2,
    step: 0.01,
    display: 'percent',
  },
  {
    group: 'factor',
    key: 'step_up',
    label: 'step_up 成功加价步长',
    hint: '每次成交的因子上浮幅度，买断时翻倍',
    min: 0,
    max: 0.1,
    step: 0.01,
    display: 'percent',
  },
  {
    group: 'factor',
    key: 'step_down',
    label: 'step_down 滞销降价步长',
    hint: '超过滞销天数时的因子下调幅度',
    min: 0,
    max: 0.2,
    step: 0.01,
    display: 'percent',
  },
  {
    group: 'factor',
    key: 'stale_days',
    label: 'stale_days 滞销判定天数',
    hint: '在架未出租达到此天数即视为滞销',
    min: 1,
    max: 90,
    unit: '天',
  },
  {
    group: 'guardrails',
    key: 'min_rent',
    label: 'min_rent 租金下限',
    hint: '低于此金额跳过操作',
    min: 0,
    max: 100000,
    step: 0.01,
    unit: '元',
  },
  {
    group: 'guardrails',
    key: 'max_rent',
    label: 'max_rent 租金上限',
    hint: '高于此金额跳过操作',
    min: 1,
    max: 100000,
    step: 0.01,
    unit: '元',
  },
  {
    group: 'guardrails',
    key: 'max_change_ratio',
    label: 'max_change_ratio 单次改价上限',
    hint: '单次改价相对当前价的最大幅度，0 表示不允许变化',
    min: 0,
    max: 1,
    step: 0.01,
    display: 'percent',
  },
  {
    group: 'guardrails',
    key: 'noise_ratio',
    label: 'noise_ratio 改价防抖阈值',
    hint: '差价小于此比例时不改价',
    min: 0,
    max: 0.1,
    step: 0.005,
    display: 'percent',
  },
  {
    group: 'guardrails',
    key: 'cooldown_minutes',
    label: 'cooldown_minutes 改价冷却',
    hint: '同一挂单两次改价的最短间隔',
    min: 0,
    max: 1440,
    unit: '分钟',
  },
  {
    group: 'guardrails',
    key: 'deposit_floor_ratio',
    label: 'deposit_floor_ratio UU 押金下限',
    hint: 'UU 押金不低于此比例 × 价值锚点',
    min: 0,
    max: 2,
    step: 0.05,
    display: 'percent',
  },
  {
    group: 'guardrails',
    key: 'deposit_cap_ratio',
    label: 'deposit_cap_ratio ECO 押金上限',
    hint: 'ECO 派生押金超过此比例 × 价值锚点时拒绝操作',
    min: 0.1,
    max: 5,
    step: 0.1,
    display: 'percent',
  },
  {
    key: 'uu_max_days',
    label: 'uu_max_days UU 最长租期',
    hint: 'UU 单次租赁的最长天数',
    min: 1,
    max: 365,
    unit: '天',
  },
  {
    key: 'eco_max_days',
    label: 'eco_max_days ECO 最长租期',
    hint: '参与 ECO 押金派生公式，最低 8 天',
    min: 8,
    max: 365,
    unit: '天',
  },
]

export function NumericField(
  props: Omit<FieldSpec, 'key'> & {
    value: number
    onChange: (v: number) => void
  },
) {
  const id = useId(),
    [draft, setDraft] = useState<{ text: string; valueAtEdit: number } | null>(
      null,
    ),
    [invalid, setInvalid] = useState(false)
  const shown =
    draft && props.value === draft.valueAtEdit
      ? draft.text
      : String(props.value)
  const description =
    props.offAtZero && props.value === 0
      ? '关闭'
      : props.display === 'percent'
        ? pct(props.value)
        : props.display
          ? `×${props.value}`
          : props.unit
  return (
    <div className="field">
      <div className="field-head">
        <label className="field-label" htmlFor={id}>
          {props.label}
        </label>
        <span className="field-val">
          <span className="muted">{description}</span>
          <input
            id={id}
            aria-label={`${props.label}数值`}
            aria-describedby={`${id}-hint`}
            aria-invalid={invalid}
            required
            type="number"
            className="num-box wide"
            min={props.min}
            max={props.max}
            step={props.step ?? 1}
            value={shown}
            onChange={(e) => {
              const text = e.target.value,
                valid = e.target.validity.valid && text !== '',
                n = Number(text)
              setDraft({ text, valueAtEdit: valid ? n : props.value })
              setInvalid(false)
              if (valid) props.onChange(n)
            }}
            onBlur={(e) => setInvalid(!e.currentTarget.validity.valid)}
            onInvalid={() => setInvalid(true)}
          />
        </span>
      </div>
      {props.display && (
        <input
          type="range"
          className="slider"
          aria-label={props.label}
          min={props.min}
          max={props.max}
          step={props.step}
          value={props.value}
          onChange={(e) => {
            setDraft(null)
            setInvalid(false)
            props.onChange(q3(Number(e.target.value)))
          }}
        />
      )}
      <div id={`${id}-hint`} className="hint">
        {props.hint}
      </div>
      {invalid && (
        <span role="alert" className="field-error">
          请输入 {props.min}–{props.max} 之间的有效数值，步长 {props.step ?? 1}
        </span>
      )}
    </div>
  )
}

export function ParamGroupsEditor({
  form,
  patchGroup,
  patchInt,
  overrides,
  inherited,
  resetField,
}: {
  form: StrategyParams
  patchGroup: (group: GroupKey, key: string, value: number) => void
  patchInt: (key: 'uu_max_days' | 'eco_max_days', value: number) => void
  overrides?: Record<string, unknown>
  inherited?: StrategyParams
  resetField?: (group: GroupKey | undefined, key: string) => void
}) {
  return (
    <>
      {(
        [
          ['baseline', '基线定价'],
          ['factor', '反馈控制器'],
          ['guardrails', '护栏'],
          ['', '租期'],
        ] as const
      ).map(([group, title]) => (
        <section className="section" key={group}>
          <h3>{title}</h3>
          <div className="form-grid">
            {FIELDS.filter((f) => (f.group ?? '') === group).map((f) => {
              const value = f.group
                ? (form[f.group] as unknown as Record<string, number>)[f.key]
                : form[f.key as 'uu_max_days' | 'eco_max_days']
              const groupOverrides = f.group
                ? (overrides?.[f.group] as Record<string, unknown> | undefined)
                : overrides
              const overridden =
                !!groupOverrides && Object.hasOwn(groupOverrides, f.key)
              const inheritedValue = inherited
                ? f.group
                  ? (inherited[f.group] as unknown as Record<string, number>)[
                      f.key
                    ]
                  : inherited[f.key as 'uu_max_days' | 'eco_max_days']
                : undefined
              const { key: _fieldKey, ...fieldProps } = f
              return (
                <div
                  className={overridden ? 'parameter overridden' : 'parameter'}
                  key={f.key}
                >
                  <NumericField
                    {...fieldProps}
                    value={value}
                    onChange={(v) =>
                      f.group
                        ? patchGroup(f.group, f.key, v)
                        : patchInt(f.key as 'uu_max_days' | 'eco_max_days', v)
                    }
                  />
                  {overrides && (
                    <div className="provenance">
                      <span className={`badge ${overridden ? 'warn' : ''}`}>
                        {overridden ? '模板覆盖' : '继承全局'} ·{' '}
                        {inheritedValue}
                      </span>
                      {overridden && (
                        <button
                          type="button"
                          className="ghost small"
                          onClick={() => resetField?.(f.group, f.key)}
                          aria-label={`恢复继承 ${f.label}`}
                        >
                          恢复继承
                        </button>
                      )}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        </section>
      ))}
    </>
  )
}
