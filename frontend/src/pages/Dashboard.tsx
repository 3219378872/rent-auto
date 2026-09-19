import { useState } from 'react'
import { Link } from 'react-router-dom'
import { channelIssues, scaleY } from './Dashboard.helpers'
import type { DashboardData, ChannelHealth } from '../api/client'
import { useResource } from '../lib/data'
import { money, percent, time } from '../lib/format'
import { JobList, LoadState } from '../components/Panel'

function Card({
  label,
  value,
  sub,
}: {
  label: string
  value: string
  sub?: string
}) {
  return (
    <div className="card">
      <div className="label">{label}</div>
      <div className="value">{value}</div>
      {sub && <div className="sub">{sub}</div>}
    </div>
  )
}

function Trend({
  series,
  metric,
}: {
  series: DashboardData['series_30d']
  metric: 'income' | 'orders'
}) {
  const [selected, setSelected] = useState<number | null>(null)
  const labels = { income: '入账收入', orders: '入账订单数' },
    points = series.map((p) => p[metric] ?? 0)
  const width = 600,
    height = 150,
    min = Math.min(...points, 0),
    max = Math.max(...points, 0)
  const x = (i: number) =>
    20 + (i / Math.max(1, series.length - 1)) * (width - 40)
  const y = (v: number) => scaleY(v, min, max, height - 24) + 10
  const fmt = (v: number) => (metric === 'income' ? money(v) : `${v} 单`)
  const point = selected !== null ? series[selected] : undefined
  return (
    <section className="section">
      <h3>近 30 天{labels[metric]}</h3>
      <p className="hint">
        按 UTC 结算日显示有入账记录的日期；未列日期无已入账记录。
      </p>
      {series.length ? (
        <>
          <svg
            viewBox={`0 0 ${width} ${height}`}
            width="100%"
            height="170"
            role="group"
            aria-label={`${labels[metric]}趋势，Tab 键可逐点读取`}
          >
            <path
              className="chart-line"
              d={points
                .map((v, i) => `${i ? 'L' : 'M'}${x(i)},${y(v)}`)
                .join(' ')}
            />
            {series.map((p, i) => (
              <circle
                key={p.date}
                cx={x(i)}
                cy={y(points[i])}
                r={selected === i ? 6 : 4}
                tabIndex={0}
                role="img"
                aria-label={`${p.date}：${fmt(points[i])}`}
                onFocus={() => setSelected(i)}
                onMouseEnter={() => setSelected(i)}
              >
                <title>
                  {p.date}：{fmt(points[i])}
                </title>
              </circle>
            ))}
          </svg>
          <p className="muted" role="status">
            {point
              ? `${point.date}：${fmt(point[metric] ?? 0)}`
              : `${series[0].date} → ${series.at(-1)?.date} · 聚焦数据点查看金额`}
          </p>
          <details>
            <summary>查看日期与数值</summary>
            <div className="table-scroll" tabIndex={0}>
              <table className="grid">
                <thead>
                  <tr>
                    <th>结算日期（UTC）</th>
                    <th className="numeric">{labels[metric]}</th>
                  </tr>
                </thead>
                <tbody>
                  {series.map((p) => (
                    <tr key={p.date}>
                      <td>{p.date}</td>
                      <td className="numeric">{fmt(p[metric] ?? 0)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </details>
        </>
      ) : (
        <p className="empty-state">暂无已入账记录</p>
      )}
    </section>
  )
}

export default function Dashboard() {
  const dashboard = useResource<DashboardData>('/dashboard', 30000),
    health = useResource<ChannelHealth>('/channels', 30000)
  const data = dashboard.data,
    issues = channelIssues(health.data ?? {})
  return (
    <div>
      <div className="page-heading">
        <h2>仪表盘</h2>
        <button
          className="ghost"
          disabled={dashboard.loading || health.loading}
          onClick={() => {
            dashboard.reload()
            health.reload()
          }}
        >
          刷新概览
        </button>
      </div>
      <section className="section">
        <div className="page-heading">
          <h3>渠道健康</h3>
          <Link to="/channels">检查渠道账号</Link>
        </div>
        <LoadState {...health} />
        {health.err ? (
          <p className="badge warn">渠道健康未知，无法确认账号可用</p>
        ) : (
          health.data && (
            <>
              {issues.length > 0 ? (
                <div className="error" role="alert">
                  渠道异常：{issues.join('；')}
                </div>
              ) : Object.keys(health.data).length ? (
                <span className="badge ok">已配置渠道状态正常</span>
              ) : (
                <span className="badge warn">暂无渠道健康信息</span>
              )}
              <span className="muted"> 更新于 {time(health.updatedAt)}</span>
            </>
          )
        )}
      </section>
      <LoadState {...dashboard} />
      {data && (
        <>
          <p className="muted">
            财务数据更新于 {time(dashboard.updatedAt)} · 页面可见时每 30 秒刷新
          </p>
          <div className="cards">
            <Card
              label="总资产"
              value={money(data.assets.total)}
              sub={`库存 ${money(data.assets.inventory)} + 在外押金 + 钱包余额`}
            />
            <Card
              label="累计净收入"
              value={money(data.income.total)}
              sub={`累计入账减已售出成本 ${money(data.sold_cost ?? 0)}`}
            />
            <Card
              label="今日入账收入"
              value={money(data.income.today)}
              sub="UTC 当日已入账，未扣售出成本"
            />
            <Card
              label="年化收益率（估算）"
              value={
                data.roi_available ? percent(data.annualized_roi) : '证据不足'
              }
              sub={
                data.roi_available
                  ? `观察 ${(data.observation_days ?? 0).toFixed(1)} 天，短期外推波动较大`
                  : '需录入成本并建立观察期'
              }
            />
            <Card
              label="在租订单数"
              value={String(data.leased_out)}
              sub="状态为租赁中的订单数量"
            />
          </div>
          <section className="section">
            <h3>成本覆盖与统计口径</h3>
            <p>
              已录入 {data.costed_items ?? 0} / {data.inventory_items ?? 0}{' '}
              件，累计成本 {money(data.cost_total ?? 0)}。观察起点：
              {time(data.observation_started_at)}。
            </p>
            <p className="hint">
              累计净收入 = 分渠道入账收入合计 −
              已售出成本。今日、趋势及渠道分项展示入账收入；分类表展示净收入。成本缺失会影响收益率估算。
            </p>
            <Link to="/inventory?cost_missing=true">补全缺失成本</Link>
            <details>
              <summary>查看资产构成</summary>
              <div className="cards">
                <Card label="在库估值" value={money(data.assets.inventory)} />
                {Object.entries(data.assets.deposits).map(([ch, v]) => (
                  <Card
                    key={`d${ch}`}
                    label={`在外押金 · ${ch.toUpperCase()}`}
                    value={money(v)}
                  />
                ))}
                {Object.entries(data.assets.wallets).map(([ch, v]) => (
                  <Card
                    key={`w${ch}`}
                    label={`钱包余额 · ${ch.toUpperCase()}`}
                    value={money(v)}
                  />
                ))}
              </div>
            </details>
          </section>
          <div className="chart-grid">
            <Trend series={data.series_30d ?? []} metric="income" />
            <Trend series={data.series_30d ?? []} metric="orders" />
          </div>
          <section className="section">
            <h3>分渠道入账收入</h3>
            <div className="table-scroll" tabIndex={0}>
              <table className="grid">
                <thead>
                  <tr>
                    <th>渠道</th>
                    <th className="numeric">入账收入（未扣售出成本）</th>
                    <th className="numeric">入账订单数</th>
                  </tr>
                </thead>
                <tbody>
                  {(data.income.by_channel ?? []).map((c) => (
                    <tr key={c.channel}>
                      <td>{c.channel.toUpperCase()}</td>
                      <td className="numeric">{money(c.income)}</td>
                      <td className="numeric">{c.orders}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {!data.income.by_channel?.length && (
              <p className="empty-state">暂无已入账订单</p>
            )}
          </section>
          <section className="section">
            <h3>分类成本与收益</h3>
            <div className="table-scroll" tabIndex={0}>
              <table className="grid">
                <thead>
                  <tr>
                    <th>品类</th>
                    <th className="numeric">累计成本</th>
                    <th className="numeric">净收入</th>
                    <th className="numeric">累计收益率</th>
                  </tr>
                </thead>
                <tbody>
                  {(data.categories ?? []).map((c) => (
                    <tr key={c.category}>
                      <td>{c.category || '未分类'}</td>
                      <td className="numeric">{money(c.cost)}</td>
                      <td className="numeric">{money(c.income)}</td>
                      <td className="numeric">
                        {c.cost > 0 ? percent(c.yield) : '成本不足'}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {!data.categories?.length && (
              <p className="empty-state">暂无品类数据</p>
            )}
          </section>
        </>
      )}
      <JobList />
    </div>
  )
}
