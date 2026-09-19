import { timezone } from '../lib/format'

export function ChannelFilter({
  value,
  onChange,
}: {
  value: string
  onChange: (value: string) => void
}) {
  return (
    <label className="field">
      渠道
      <select
        aria-label="渠道"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value="">全部渠道</option>
        <option value="uu">UU</option>
        <option value="eco">ECO</option>
      </select>
    </label>
  )
}

export const dateError = (since: string, until: string) =>
  (since && !Number.isFinite(Date.parse(since))) ||
  (until && !Number.isFinite(Date.parse(until)))
    ? '请输入有效时间'
    : since && until && Date.parse(since) >= Date.parse(until)
      ? '结束时间必须晚于起始时间'
      : ''

export function TimeRange({
  since,
  until,
  onChange,
}: {
  since: string
  until: string
  onChange: (values: { since?: string; until?: string }) => void
}) {
  const err = dateError(since, until)
  return (
    <div className="time-range">
      <label className="field">
        起始时间（含）
        <input
          type="datetime-local"
          aria-label="起始时间"
          value={since}
          onChange={(e) => onChange({ since: e.target.value })}
        />
      </label>
      <label className="field">
        结束时间（不含）
        <input
          type="datetime-local"
          aria-label="结束时间"
          aria-invalid={!!err}
          value={until}
          onChange={(e) => onChange({ until: e.target.value })}
        />
      </label>
      <span className="hint">时区：{timezone()}</span>
      {err && (
        <span className="error" role="alert">
          {err}
        </span>
      )}
    </div>
  )
}
