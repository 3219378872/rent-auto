import { useCallback, useEffect, useState } from 'react'
import { api, Paged, ListingRow } from '../api/client'

const stateBadge: Record<string, string> = {
  active: 'ok', leased: 'warn', none: '', stale: 'bad', unknown: '',
}

export default function Listings() {
  const [data, setData] = useState<Paged<ListingRow> | null>(null)
  const [channel, setChannel] = useState('')
  const [state, setState] = useState('active')
  const [page, setPage] = useState(1)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const load = useCallback(() => {
    const q = new URLSearchParams({ page: String(page), page_size: '50' })
    if (channel) q.set('channel', channel)
    if (state) q.set('state', state)
    api.get<Paged<ListingRow>>(`/listings?${q}`).then(setData).catch((e) => setErr(e.message))
  }, [channel, state, page])

  useEffect(load, [load])

  const triggerReprice = async () => {
    setErr(''); setMsg('')
    try {
      await api.post('/jobs/reprice/run')
      setMsg('已触发立即重定价')
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div>
      <h2>上架状态（双渠道）</h2>
      {err && <div className="error">{err}</div>}
      {msg && <div className="ok-msg">{msg}</div>}
      <div className="toolbar">
        <select value={channel} onChange={(e) => { setPage(1); setChannel(e.target.value) }}>
          <option value="">全部渠道</option><option value="uu">UU</option><option value="eco">ECO</option>
        </select>
        <select value={state} onChange={(e) => { setPage(1); setState(e.target.value) }}>
          <option value="">全部实际状态</option>
          <option value="active">在架</option><option value="leased">出租中</option>
          <option value="none">已消失</option>
        </select>
        <span className="muted">共 {data?.total ?? 0} 条</span>
        <div className="grow" />
        <button onClick={triggerReprice}>立即重定价</button>
      </div>

      <table className="grid">
        <thead>
          <tr>
            <th>渠道</th><th>名称</th><th>实际状态</th><th>期望状态</th>
            <th>租金/天</th><th>长租租金</th><th>押金</th><th>最大天数</th><th>最近改价</th>
          </tr>
        </thead>
        <tbody>
          {(data?.items ?? []).map((r) => (
            <tr key={`${r.channel}-${r.goods_ref}`}>
              <td>{r.channel.toUpperCase()}</td>
              <td title={r.hash_name}>{r.hash_name}</td>
              <td><span className={`badge ${stateBadge[r.actual_state] ?? ''}`}>{r.actual_state}</span></td>
              <td className="muted">{r.desired_state}</td>
              <td>¥{r.rent_price.toFixed(2)}</td>
              <td>{r.long_rent_price > 0 ? `¥${r.long_rent_price.toFixed(2)}` : '—'}</td>
              <td>¥{r.deposit.toFixed(2)}</td>
              <td>{r.max_days || '—'}天</td>
              <td className="muted">{fmtTime(r.last_reprice_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>

      <div className="toolbar" style={{ marginTop: 12 }}>
        <button className="ghost small" disabled={page <= 1} onClick={() => setPage(page - 1)}>上一页</button>
        <span className="muted">第 {page} 页</span>
        <button className="ghost small" disabled={(data?.items.length ?? 0) < 50} onClick={() => setPage(page + 1)}>下一页</button>
      </div>
    </div>
  )
}

function fmtTime(s: string | null): string {
  if (!s) return '—'
  return new Date(s).toLocaleString('zh-CN', { hour12: false })
}
