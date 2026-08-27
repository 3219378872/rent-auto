import { useState } from 'react'
import type { AuditRow } from '../api/client'
import { Pager, usePagedList } from '../lib/paged'
import { useDebounced } from '../lib/ui'

const PAGE_SIZE = 50

export default function Audit() {
  const [action, setAction] = useState('')
  const [channel, setChannel] = useState('')
  const [since, setSince] = useState('')
  const [until, setUntil] = useState('')
  const actionDebounced = useDebounced(action)
  const { data, page, setPage, err } = usePagedList<AuditRow>(
    '/audit',
    {
      action: actionDebounced, channel,
      since: since ? new Date(since).toISOString() : '',
      until: until ? new Date(until).toISOString() : '',
    },
    PAGE_SIZE,
  )
  const total = data?.total ?? 0

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

      <Pager
        page={page} total={total} pageSize={PAGE_SIZE} onPage={setPage}
        meta={`共 ${total} 条 · 第 ${page}/${Math.max(1, Math.ceil(total / PAGE_SIZE))} 页`}
      />
    </div>
  )
}
