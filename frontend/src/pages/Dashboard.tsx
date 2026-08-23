import { useEffect, useState } from 'react'
import { scaleY } from './Dashboard.helpers'
import { api, DashboardData } from '../api/client'

function Sparkline({ points }: { points: number[] }) {
  if (points.length < 2) return <div className="muted">数据不足</div>
  const w = 560, h = 120, max = Math.max(...points, 0.0001), min = Math.min(...points, 0)
  const xs = (i: number) => (i / (points.length - 1)) * w
  const ys = (v: number) => scaleY(v, min, max, h)
  const line = points.map((v, i) => `${i === 0 ? 'M' : 'L'}${xs(i).toFixed(1)},${ys(v).toFixed(1)}`).join(' ')
  const area = `${line} L${w},${h} L0,${h} Z`
  return (
    <svg viewBox={`0 0 ${w} ${h}`} width="100%" height="140">
      <path className="chart-area" d={area} />
      <path className="chart-line" d={line} />
    </svg>
  )
}

function Card({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="card">
      <div className="label">{label}</div>
      <div className="value">{value}</div>
      {sub && <div className="sub">{sub}</div>}
    </div>
  )
}

const fmtMoney = (n: number) => `¥${n.toFixed(2)}`
const fmtPct = (n: number) => `${(n * 100).toFixed(2)}%`

export default function Dashboard() {
  const [data, setData] = useState<DashboardData | null>(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    api.get<DashboardData>('/dashboard').then(setData).catch((e) => setErr(e.message))
  }, [])

  if (err) return <div><h2>仪表盘</h2><div className="error">{err}</div></div>
  if (!data) return <div className="muted">加载中…</div>

  return (
    <div>
      <h2>仪表盘</h2>
      <div className="cards">
        <Card label="总资产" value={fmtMoney(data.assets.total)}
          sub={`库存 ${fmtMoney(data.assets.inventory)}`} />
        <Card label="总收入" value={fmtMoney(data.income.total)}
          sub={`今日 ${fmtMoney(data.income.today)}`} />
        <Card label="年化收益率" value={fmtPct(data.annualized_roi)} />
        <Card label="在租件数" value={String(data.leased_out)} />
        {Object.entries(data.assets.deposits).map(([ch, v]) => (
          <Card key={ch} label={`在外押金 · ${ch.toUpperCase()}`} value={fmtMoney(v)} />
        ))}
        {Object.entries(data.assets.wallets).map(([ch, v]) => (
          <Card key={ch} label={`钱包余额 · ${ch.toUpperCase()}`} value={fmtMoney(v)} />
        ))}
      </div>

      <div className="section">
        <h3 style={{ marginTop: 0 }}>近 30 天收益</h3>
        <Sparkline points={(data.series_30d ?? []).map((p) => p.income)} />
        <div className="muted">{(data.series_30d ?? [])[0]?.date ?? '—'} → {(data.series_30d ?? []).at(-1)?.date ?? '—'}</div>
      </div>

      <div className="section">
        <h3 style={{ marginTop: 0 }}>分渠道收入</h3>
        <table className="grid">
          <thead><tr><th>渠道</th><th>收入</th><th>订单数</th></tr></thead>
          <tbody>
            {(data.income.by_channel ?? []).map((c) => (
              <tr key={c.channel}><td>{c.channel}</td><td>{fmtMoney(c.income)}</td><td>{c.orders}</td></tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="section">
        <h3 style={{ marginTop: 0 }}>分类成本与收益</h3>
        <table className="grid">
          <thead><tr><th>品类</th><th>成本</th><th>收入</th><th>收益率</th></tr></thead>
          <tbody>
            {(data.categories ?? []).map((c) => (
              <tr key={c.category}>
                <td>{c.category}</td><td>{fmtMoney(c.cost)}</td>
                <td>{fmtMoney(c.income)}</td><td>{fmtPct(c.yield)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
