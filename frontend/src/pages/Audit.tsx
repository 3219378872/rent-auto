import { useCallback, useEffect, useState } from 'react'
import { api, AuditRow } from '../api/client'
import { useDebounced } from '../lib/ui'

const PAGE_SIZE = 50

export default function Audit() {
  const [data, setData] = useState<{ items: AuditRow[]; total: number } | null>(null)
  const [action, setAction] = useState('')
  const [channel, setChannel] = useState('')
  const [since, setSince] = useState('')
  const [until, setUntil] = useState('')
  const [page, setPage] = useState(1)
  const [err, setErr] = useState('')
  const actionDebounced = useDebounced(action)

  const load = useCallback(() => {
    const q = new URLSearchParams({ page_size: String(PAGE_SIZE), page: String(page) })
    if (actionDebounced) q.set('action', actionDebounced)
    if (channel) q.set('channel', channel)
    if (since) q.set('since', new Date(since).toISOString())
    if (until) q.set('until', new Date(until).toISOString())
    let alive = true
    api.get<{ items: AuditRow[]; total: number }>(`/audit?${q}`)
      .then((d) => { if (alive) { setData(d); setErr('') } })
      .catch((e) => { if (alive) setErr(e.message) })
    return () => { alive = false }
  }, [actionDebounced, channel, since, until, page])

  useEffect(load, [load])

  return (
    <div>
      <h2>审计日志</h2>
      {err && <div className="error">{err}</div>}
      <div className="toolbar">
        <input placeholder="动作过滤，如 reprice" value={action} onChange={(e) => { setPage(1); setAction(e.target.value) }} />
        <select value={channel} onChange={(e) => { setPage(1); setChannel(e.target.value) }}>
          <option value="">全部渠道</option><option value="uu">UU</option><option value="eco">ECO</option>
        </select>
        <input type="datetime-local" aria-label="起始时间" value={since}
          onChange={(e) => { setPage(1); setSince(e.target.value) }} />
        <span className="muted">至</span>
        <input type="datetime-local" aria-label="结束时间" value={until}
          onChange={(e) => { setPage(1); setUntil(e.target.value) }} />
      </div>

      <table className="grid">
        <thead><tr><th>时间</th><th>操作者</th><th>动作</th><th>渠道</th><th>目标</th><th>详情</th></tr></thead>
        <tbody>
          {(data?.items ?? []).map((a) => (
            <tr key={`${a.ts}-${a.action}-${a.target ?? ''}`}>
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

      <div className="toolbar" style={{ marginTop: 12 }}>
        <button className="ghost small" disabled={page <= 1} onClick={() => setPage(page - 1)}>上一页</button>
        <span className="muted">
          共 {data?.total ?? 0} 条 · 第 {page}/{Math.max(1, Math.ceil((data?.total ?? 0) / PAGE_SIZE))} 页
        </span>
        <button className="ghost small"
          disabled={data ? page * PAGE_SIZE >= data.total : true}
          onClick={() => setPage(page + 1)}>下一页</button>
      </div>
    </div>
  )
}
