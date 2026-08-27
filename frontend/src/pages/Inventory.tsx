import { useState } from 'react'
import { api, InventoryRow } from '../api/client'
import { Pager, usePagedList } from '../lib/paged'
import { useDebounced } from '../lib/ui'

const statusBadge: Record<string, string> = {
  in_stock: 'ok', listed: '', leased: 'warn', locked: 'bad', sold: 'bad',
}

export default function Inventory() {
  const [channel, setChannel] = useState('')
  const [status, setStatus] = useState('')
  const [search, setSearch] = useState('')
  const searchDebounced = useDebounced(search)
  const { data, page, setPage, err, setErr, reload } = usePagedList<InventoryRow>(
    '/inventory', { channel, status, search: searchDebounced },
  )

  const saveCost = async (row: InventoryRow, cost: number) => {
    try {
      await api.put(`/inventory/${row.channel}/${row.asset_id}/cost`, { cost })
      reload()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div>
      <h2>库存状态</h2>
      {err && <div className="error">{err}</div>}
      <div className="toolbar">
        <select value={channel} onChange={(e) => { setPage(1); setChannel(e.target.value) }}>
          <option value="">全部渠道</option><option value="uu">UU</option><option value="eco">ECO</option>
        </select>
        <select value={status} onChange={(e) => { setPage(1); setStatus(e.target.value) }}>
          <option value="">全部状态</option>
          <option value="in_stock">在库</option><option value="listed">已上架</option>
          <option value="leased">已租出</option><option value="locked">锁定</option>
        </select>
        <input placeholder="搜索名称…" value={search} onChange={(e) => { setPage(1); setSearch(e.target.value) }} />
        <span className="muted">共 {data?.total ?? 0} 件</span>
      </div>

      <table className="grid">
        <thead>
          <tr><th>渠道</th><th>名称</th><th>状态</th><th>参考价</th><th>成本价</th><th>账面收益率</th><th>录入成本</th></tr>
        </thead>
        <tbody>
          {(data?.items ?? []).map((r) => {
            const y = r.cost_basis > 0 ? (r.mark_price - r.cost_basis) / r.cost_basis : null
            return (
              <tr key={`${r.channel}-${r.asset_id}`}>
                <td>{r.channel.toUpperCase()}</td>
                <td title={r.hash_name}>{r.market_hash_name || r.hash_name}</td>
                <td><span className={`badge ${statusBadge[r.status] ?? ''}`}>{r.status}</span></td>
                <td>¥{r.mark_price.toFixed(2)}</td>
                <td>{r.cost_basis > 0 ? `¥${r.cost_basis.toFixed(2)}` : '—'}</td>
                <td>{y === null ? '—' : `${(y * 100).toFixed(2)}%`}</td>
                <td>
                  <CostEditor initial={r.cost_basis} onSave={(c) => saveCost(r, c)} />
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>

      <Pager page={page} total={data?.total} pageSize={50} onPage={setPage} />
    </div>
  )
}

function CostEditor({ initial, onSave }: { initial: number; onSave: (c: number) => void }) {
  const [v, setV] = useState(initial > 0 ? String(initial) : '')
  const n = parseFloat(v)
  const valid = !isNaN(n) && n > 0
  return (
    <span className="row">
      <input style={{ width: 80 }} value={v} onChange={(e) => setV(e.target.value)} placeholder="成本"
        aria-label="录入成本价" />
      <button className="small" disabled={!valid}
        title={valid ? '' : '请输入正数金额'}
        onClick={() => { if (valid) onSave(n) }}>存</button>
    </span>
  )
}
