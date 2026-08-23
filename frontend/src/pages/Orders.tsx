import { useCallback, useEffect, useState } from 'react'
import { api, Paged, OrderRow } from '../api/client'
import { csvCell } from '../lib/ui'

const statusBadge: Record<string, string> = {
  leasing: 'ok', done: '', bought_out: 'warn', pending_payment: '',
  delivering: '', returning: 'warn', cancelled: 'bad', breach: 'bad', arbitrating: 'bad',
}

const PAGE_SIZE = 50

export default function Orders() {
  const [data, setData] = useState<Paged<OrderRow> | null>(null)
  const [channel, setChannel] = useState('')
  const [status, setStatus] = useState('')
  const [page, setPage] = useState(1)
  const [err, setErr] = useState('')
  const [exporting, setExporting] = useState(false)

  const load = useCallback(() => {
    const q = new URLSearchParams({ page: String(page), page_size: String(PAGE_SIZE) })
    if (channel) q.set('channel', channel)
    if (status) q.set('status', status)
    let alive = true
    api.get<Paged<OrderRow>>(`/orders?${q}`)
      .then((d) => { if (alive) { setData(d); setErr('') } })
      .catch((e) => { if (alive) setErr(e.message) })
    return () => { alive = false }
  }, [channel, status, page])

  useEffect(load, [load])

  // exportCsv fetches EVERY matching page (not just the visible one) so the
  // download always matches the "共 N 单" counter.
  const exportCsv = async () => {
    setErr('')
    setExporting(true)
    try {
      const rows: OrderRow[] = []
      for (let p = 1; ; p++) {
        const q = new URLSearchParams({ page: String(p), page_size: String(PAGE_SIZE) })
        if (channel) q.set('channel', channel)
        if (status) q.set('status', status)
        const d = await api.get<Paged<OrderRow>>(`/orders?${q}`)
        rows.push(...d.items)
        if (d.items.length < PAGE_SIZE || (data !== null && rows.length >= data.total)) break
      }
      const head = ['渠道', '订单号', '名称', '类型', '状态', '租期(天)', '租金/天', '订单金额', '押金', '开始', '到期']
      const lines = rows.map((r) => [
        r.channel, r.order_ref, r.hash_name, r.order_type, r.status,
        String(r.rent_days), r.rent_price.toFixed(2), r.order_amount.toFixed(2),
        r.deposits.toFixed(2),
        r.started_at ?? '', r.due_at ?? '',
      ].map(csvCell).join(','))
      const csv = '\uFEFF' + head.map(csvCell).join(',') + '\n' + lines.join('\n')
      const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' })
      const a = document.createElement('a')
      a.href = URL.createObjectURL(blob)
      a.download = `lease-orders-${new Date().toISOString().slice(0, 10)}.csv`
      a.click()
      URL.revokeObjectURL(a.href)
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setExporting(false)
    }
  }

  return (
    <div>
      <h2>租赁订单</h2>
      {err && <div className="error">{err}</div>}
      <div className="toolbar">
        <select value={channel} onChange={(e) => { setPage(1); setChannel(e.target.value) }}>
          <option value="">全部渠道</option><option value="uu">UU</option><option value="eco">ECO</option>
        </select>
        <select value={status} onChange={(e) => { setPage(1); setStatus(e.target.value) }}>
          <option value="">全部状态</option>
          <option value="leasing">租赁中</option><option value="done">已完成</option>
          <option value="bought_out">已买断</option><option value="cancelled">已取消</option>
        </select>
        <span className="muted">共 {data?.total ?? 0} 单</span>
        <div className="grow" />
        <button className="ghost" onClick={exportCsv} disabled={exporting}>
          {exporting ? '导出中…' : '导出 CSV'}
        </button>
      </div>

      <table className="grid">
        <thead>
          <tr>
            <th>渠道</th><th>订单号</th><th>名称</th><th>类型</th><th>状态</th>
            <th>租期</th><th>租金/天</th><th>订单金额</th><th>押金</th><th>到期</th>
          </tr>
        </thead>
        <tbody>
          {(data?.items ?? []).map((r) => (
            <tr key={`${r.channel}-${r.order_ref}`}>
              <td>{r.channel.toUpperCase()}</td>
              <td className="muted">{r.order_ref}</td>
              <td title={r.hash_name}>{r.hash_name}</td>
              <td>{r.order_type === 'long' ? '长租' : '短租'}</td>
              <td><span className={`badge ${statusBadge[r.status] ?? ''}`}>{r.status}</span></td>
              <td>{r.rent_days}天</td>
              <td>¥{r.rent_price.toFixed(2)}</td>
              <td>¥{r.order_amount.toFixed(2)}</td>
              <td>¥{r.deposits.toFixed(2)}</td>
              <td className="muted">{fmtDay(r.due_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>

      <div className="toolbar" style={{ marginTop: 12 }}>
        <button className="ghost small" disabled={page <= 1} onClick={() => setPage(page - 1)}>上一页</button>
        <span className="muted">第 {page} 页</span>
        <button className="ghost small"
          disabled={data ? page * PAGE_SIZE >= data.total : true}
          onClick={() => setPage(page + 1)}>下一页</button>
      </div>
    </div>
  )
}

function fmtDay(s: string | null): string {
  if (!s) return '—'
  return new Date(s).toLocaleDateString('zh-CN')
}
