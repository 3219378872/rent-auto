import { useState } from 'react'
import { api, ListingRow } from '../api/client'
import { Pager, usePagedList } from '../lib/paged'

const stateBadge: Record<string, string> = {
  active: 'ok', leased: 'warn', none: '', stale: 'bad', unknown: '',
}

export default function Listings() {
  const [channel, setChannel] = useState('')
  const [state, setState] = useState('active')
  const [msg, setMsg] = useState('')
  const [repricing, setRepricing] = useState(false)
  const { data, page, setPage, err, setErr } = usePagedList<ListingRow>('/listings', { channel, state })

  const triggerReprice = async () => {
    setErr(''); setMsg('')
    setRepricing(true)
    try {
      await api.post('/jobs/reprice/run')
      setMsg('已触发立即重定价')
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setRepricing(false)
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
        <button onClick={triggerReprice} disabled={repricing}>
          {repricing ? '触发中…' : '立即重定价'}
        </button>
      </div>

      <table className="grid">
        <thead>
          <tr>
            <th>渠道</th><th>名称</th><th>实际状态</th><th>期望状态</th>
            <th>租金/天</th><th>长租租金</th><th>押金</th><th>最大天数</th>
            <th title="反馈控制器因子 × 最近一次定价动作">决策依据</th><th>最近改价</th>
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
              <td className="muted" title={decisionTitle(r)}>{fmtDecision(r)}</td>
              <td className="muted">{fmtTime(r.last_reprice_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>

      <Pager page={page} total={data?.total} pageSize={50} onPage={setPage} />
    </div>
  )
}

function fmtTime(s: string | null): string {
  if (!s) return '—'
  return new Date(s).toLocaleString('zh-CN', { hour12: false })
}

// 决策依据摘要：因子 × 最近动作（US-LIST-02）。
function fmtDecision(r: ListingRow): string {
  const parts: string[] = [`×${(r.factor ?? 1).toFixed(2)}`]
  const d = r.last_decision
  if (d) {
    if (d.action === 'skip' && d.skip) parts.push(`跳过:${d.skip}`)
    else if (d.action === 'reprice' && d.new_rent != null) parts.push(`→¥${d.new_rent.toFixed(2)}`)
    else parts.push(d.action)
  }
  return parts.join(' ')
}

function decisionTitle(r: ListingRow): string {
  const d = r.last_decision
  if (!d?.at) return '暂无定价动作记录'
  return `${d.action} @ ${fmtTime(d.at)}`
}
