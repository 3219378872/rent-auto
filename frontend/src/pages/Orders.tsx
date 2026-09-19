import { api, type Paged, type OrderRow } from '../api/client'
import { Pager, usePagedList } from '../lib/paged'
import { csvCell } from '../lib/ui'
import { useAction, useFilters } from '../lib/data'
import {
  money,
  orderTypes,
  relativeDue,
  statusLabel,
  time,
  timezone,
  toISO,
} from '../lib/format'
import {
  CopyButton,
  Feedback,
  ItemLinks,
  LoadState,
  StatusBadge,
} from '../components/Panel'
import { ChannelFilter, TimeRange, dateError } from '../components/Filters'

const PAGE_SIZE = 50
export default function Orders() {
  const filters = useFilters({
    channel: '',
    status: '',
    search: '',
    order_type: '',
    view: '',
    sort: '',
    since: '',
    until: '',
    hash_name: '',
    asset_id: '',
  })
  const f = filters.values,
    invalid = dateError(f.since, f.until)
  const query = { ...f, since: toISO(f.since), until: toISO(f.until) }
  const list = usePagedList<OrderRow>('/orders', query, PAGE_SIZE, 0, !invalid),
    exporting = useAction()
  const exportCsv = () =>
    exporting.run(async () => {
      const rows: OrderRow[] = []
      for (let p = 1; ; p++) {
        const q = new URLSearchParams({
          ...query,
          page: String(p),
          page_size: String(PAGE_SIZE),
        })
        const d = await api.get<Paged<OrderRow>>(`/orders?${q}`)
        rows.push(...d.items)
        if (d.items.length < PAGE_SIZE || rows.length >= d.total) break
      }
      const head = [
        '渠道',
        '订单号',
        '名称',
        '资产 ID',
        '类型',
        '状态',
        '租期(天)',
        '租金/天',
        '订单金额',
        '押金',
        '开始(ISO 8601)',
        '到期(ISO 8601)',
      ]
      const lines = rows.map((r) =>
        [
          r.channel,
          r.order_ref,
          r.hash_name,
          r.asset_id || '',
          orderTypes[r.order_type] || r.order_type,
          statusLabel(r.status),
          String(r.rent_days),
          r.rent_price.toFixed(2),
          r.order_amount.toFixed(2),
          r.deposits.toFixed(2),
          r.started_at || '',
          r.due_at || '',
        ]
          .map(csvCell)
          .join(','),
      )
      const blob = new Blob(
        ['\uFEFF' + head.map(csvCell).join(',') + '\n' + lines.join('\n')],
        { type: 'text/csv;charset=utf-8' },
      )
      const a = document.createElement('a')
      a.href = URL.createObjectURL(blob)
      a.download = `lease-orders-${new Date().toISOString().slice(0, 10)}.csv`
      a.click()
      URL.revokeObjectURL(a.href)
    }, '导出完成，已包含所有匹配页面')
  return (
    <div>
      <div className="page-heading">
        <h2>租赁订单</h2>
        <button
          className="ghost"
          onClick={exportCsv}
          disabled={exporting.busy || !!invalid || list.loading || !list.data}
        >
          {exporting.busy ? '导出中…' : '导出 CSV'}
        </button>
      </div>
      <Feedback {...exporting} />
      <div className="tabs" aria-label="订单视图">
        {[
          ['', '全部'],
          ['attention', '待处理'],
          ['active', '在租'],
          ['history', '历史'],
        ].map(([value, label]) => (
          <button
            className={f.view === value ? 'active' : 'ghost'}
            aria-pressed={f.view === value}
            key={value}
            onClick={() => filters.set({ view: value, status: '' })}
          >
            {label}
          </button>
        ))}
      </div>
      <div className="toolbar filters">
        <ChannelFilter
          value={f.channel}
          onChange={(channel) => filters.set({ channel })}
        />
        <label className="field">
          订单状态
          <select
            value={f.status}
            onChange={(e) => filters.set({ status: e.target.value, view: '' })}
          >
            <option value="">全部状态</option>
            {[
              'pending_payment',
              'delivering',
              'leasing',
              'returning',
              'done',
              'bought_out',
              'cancelled',
              'breach',
              'arbitrating',
              'unknown',
            ].map((s) => (
              <option key={s} value={s}>
                {statusLabel(s)}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          订单类型
          <select
            value={f.order_type}
            onChange={(e) => filters.set({ order_type: e.target.value })}
          >
            <option value="">全部类型</option>
            {Object.entries(orderTypes).map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          名称 / 订单号
          <input
            value={f.search}
            onChange={(e) => filters.set({ search: e.target.value })}
          />
        </label>
        <label className="field">
          排序
          <select
            value={f.sort}
            onChange={(e) => filters.set({ sort: e.target.value })}
          >
            <option value="">最近同步</option>
            <option value="due_asc">最早到期</option>
            <option value="started_desc">最近开始</option>
            <option value="amount_desc">金额从高到低</option>
          </select>
        </label>
        <button className="ghost" onClick={filters.reset}>
          清除筛选
        </button>
        <button className="ghost" disabled={list.loading} onClick={list.reload}>
          刷新订单
        </button>
      </div>
      <p className="hint">
        按订单开始时间筛选。待处理包括待支付、待发货、归还、仲裁、违约及未知状态。
      </p>
      <TimeRange since={f.since} until={f.until} onChange={filters.set} />
      {(f.hash_name || f.asset_id) && (
        <p className="hint">
          关联筛选：{f.hash_name} · 资产 {f.asset_id || '全部'}
        </p>
      )}
      <LoadState
        {...list}
        empty={list.data?.total === 0}
        filtered={Object.values(f).some(Boolean)}
        reset={filters.reset}
      />
      {list.data && (
        <>
          <p className="muted">
            共 {list.data.total} 单 · 时间按 {timezone()} 显示 ·
            导出包含全部匹配结果
          </p>
          <div className="table-scroll" tabIndex={0} aria-label="订单列表">
            <table className="grid">
              <thead>
                <tr>
                  <th>渠道 / 订单</th>
                  <th>商品 / 资产</th>
                  <th>类型 / 状态</th>
                  <th className="numeric">租金 / 天</th>
                  <th className="numeric">金额 / 押金</th>
                  <th>时间 / 租期</th>
                </tr>
              </thead>
              <tbody>
                {list.data.items.map((r) => (
                  <tr key={`${r.channel}-${r.order_ref}`}>
                    <td>
                      {r.channel.toUpperCase()}
                      <div className="record-id">{r.order_ref}</div>
                      <CopyButton value={r.order_ref} label="复制订单号" />
                    </td>
                    <td className="item-cell">
                      <strong>{r.hash_name}</strong>
                      <div className="muted">资产 {r.asset_id || '未记录'}</div>
                      <ItemLinks hash={r.hash_name} asset={r.asset_id} />
                    </td>
                    <td>
                      {orderTypes[r.order_type] || `未知（${r.order_type}）`}
                      <div>
                        <StatusBadge value={r.status} />
                      </div>
                    </td>
                    <td className="numeric">{money(r.rent_price)}</td>
                    <td className="numeric">
                      {money(r.order_amount)}
                      <div className="muted">押金 {money(r.deposits)}</div>
                    </td>
                    <td>
                      <div>开始：{time(r.started_at)}</div>
                      <div>到期：{time(r.due_at)}</div>
                      <span className="badge warn">
                        {relativeDue(r.due_at, r.status)}
                      </span>
                      <span className="muted"> {r.rent_days} 天</span>
                      {r.finished_at && (
                        <div className="muted">结束：{time(r.finished_at)}</div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
      <Pager
        page={list.page}
        total={list.data?.total}
        pageSize={PAGE_SIZE}
        onPage={list.setPage}
        loading={list.loading}
      />
    </div>
  )
}
