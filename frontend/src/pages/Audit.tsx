import { useCallback, useEffect, useState } from 'react'
import { api, AuditRow } from '../api/client'

export default function Audit() {
  const [data, setData] = useState<{ items: AuditRow[]; total: number } | null>(null)
  const [action, setAction] = useState('')
  const [channel, setChannel] = useState('')
  const [err, setErr] = useState('')

  const load = useCallback(() => {
    const q = new URLSearchParams({ page_size: '100' })
    if (action) q.set('action', action)
    if (channel) q.set('channel', channel)
    api.get<{ items: AuditRow[]; total: number }>(`/audit?${q}`).then(setData).catch((e) => setErr(e.message))
  }, [action, channel])

  useEffect(load, [load])

  return (
    <div>
      <h2>审计日志</h2>
      {err && <div className="error">{err}</div>}
      <div className="toolbar">
        <input placeholder="动作过滤，如 reprice" value={action} onChange={(e) => setAction(e.target.value)} />
        <select value={channel} onChange={(e) => setChannel(e.target.value)}>
          <option value="">全部渠道</option><option value="uu">UU</option><option value="eco">ECO</option>
        </select>
        <span className="muted">共 {data?.total ?? 0} 条（显示最近 100 条）</span>
      </div>

      <table className="grid">
        <thead><tr><th>时间</th><th>操作者</th><th>动作</th><th>渠道</th><th>目标</th><th>详情</th></tr></thead>
        <tbody>
          {(data?.items ?? []).map((a, i) => (
            <tr key={i}>
              <td className="muted">{new Date(a.ts).toLocaleString('zh-CN', { hour12: false })}</td>
              <td>{a.actor}</td>
              <td>{a.action}</td>
              <td>{a.channel || '—'}</td>
              <td>{a.target || '—'}</td>
              <td className="muted" style={{ maxWidth: 380, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {a.detail ? JSON.stringify(a.detail) : '—'}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
