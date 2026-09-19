import { useState } from 'react'
import { api, type InventoryRow } from '../api/client'
import { Pager, usePagedList } from '../lib/paged'
import { useAction, useFilters, useResource } from '../lib/data'
import { money, percent, statusLabel, time } from '../lib/format'
import {
  CopyButton,
  Feedback,
  ItemLinks,
  LoadState,
  StatusBadge,
} from '../components/Panel'
import { ChannelFilter } from '../components/Filters'

export default function Inventory() {
  const filters = useFilters({
    channel: '',
    status: '',
    search: '',
    category: '',
    sort: '',
    cost_missing: '',
    hash_name: '',
    asset_id: '',
  })
  const f = filters.values
  const list = usePagedList<InventoryRow>('/inventory', f)
  const categories = useResource<string[]>('/inventory/categories')
  return (
    <div>
      <div className="page-heading">
        <h2>库存状态</h2>
        <button className="ghost" disabled={list.loading} onClick={list.reload}>
          刷新库存
        </button>
      </div>
      <div className="toolbar filters">
        <ChannelFilter
          value={f.channel}
          onChange={(channel) => filters.set({ channel })}
        />
        <label className="field">
          状态
          <select
            value={f.status}
            onChange={(e) => filters.set({ status: e.target.value })}
          >
            <option value="">全部状态</option>
            {['in_stock', 'listed', 'leased', 'locked', 'sold', 'missing'].map(
              (s) => (
                <option key={s} value={s}>
                  {statusLabel(s)}
                </option>
              ),
            )}
          </select>
        </label>
        <label className="field">
          名称 / 资产 ID
          <input
            placeholder="搜索名称或资产 ID…"
            value={f.search}
            onChange={(e) => filters.set({ search: e.target.value })}
          />
        </label>
        <label className="field">
          品类
          <select
            value={f.category}
            onChange={(e) => filters.set({ category: e.target.value })}
          >
            <option value="">全部品类</option>
            {(categories.data ?? []).map((c) => (
              <option key={c}>{c}</option>
            ))}
          </select>
        </label>
        <label className="field">
          排序
          <select
            value={f.sort}
            onChange={(e) => filters.set({ sort: e.target.value })}
          >
            <option value="">入库顺序</option>
            <option value="name">名称</option>
            <option value="price_desc">参考价从高到低</option>
            <option value="price_asc">参考价从低到高</option>
            <option value="cost_desc">成本从高到低</option>
          </select>
        </label>
        <label>
          <input
            type="checkbox"
            checked={f.cost_missing === 'true'}
            onChange={(e) =>
              filters.set({ cost_missing: e.target.checked ? 'true' : '' })
            }
          />{' '}
          仅未录入成本
        </label>
        <button className="ghost" onClick={filters.reset}>
          清除筛选
        </button>
      </div>
      {categories.err && (
        <div className="error" role="alert">
          品类加载失败{' '}
          <button className="ghost small" onClick={categories.reload}>
            重试品类
          </button>
        </div>
      )}
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
            共 {list.data.total} 件 · 更新于 {time(list.updatedAt)} ·
            账面价差不含租金收入
          </p>
          <div className="table-scroll" tabIndex={0} aria-label="库存列表">
            <table className="grid">
              <thead>
                <tr>
                  <th>渠道 / 状态</th>
                  <th>商品 / 资产</th>
                  <th className="numeric">参考价</th>
                  <th className="numeric">成本价</th>
                  <th className="numeric">账面价差率</th>
                  <th>成本维护</th>
                </tr>
              </thead>
              <tbody>
                {list.data.items.map((r) => (
                  <tr key={`${r.channel}-${r.asset_id}`}>
                    <td>
                      {r.channel.toUpperCase()}
                      <div>
                        <StatusBadge value={r.status} />
                      </div>
                      <small>{r.tradable ? '可交易' : '不可交易'}</small>
                    </td>
                    <td className="item-cell">
                      <strong>{r.market_hash_name || r.hash_name}</strong>
                      <div className="muted">
                        {r.category || '未分类'} · 资产 {r.asset_id}{' '}
                        <CopyButton value={r.asset_id} label="复制资产 ID" />
                      </div>
                      <ItemLinks hash={r.hash_name} asset={r.asset_id} />
                    </td>
                    <td className="numeric">{money(r.mark_price)}</td>
                    <td className="numeric">
                      {r.cost_basis > 0 ? money(r.cost_basis) : '未录入'}
                    </td>
                    <td className="numeric">
                      {r.cost_basis > 0
                        ? percent((r.mark_price - r.cost_basis) / r.cost_basis)
                        : '成本不足'}
                    </td>
                    <td>
                      <CostEditor row={r} onSaved={list.reload} />
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
        pageSize={50}
        onPage={list.setPage}
        loading={list.loading}
      />
    </div>
  )
}

function CostEditor({
  row,
  onSaved,
}: {
  row: InventoryRow
  onSaved: () => void
}) {
  const [editing, setEditing] = useState(false),
    [value, setValue] = useState(
      row.cost_basis > 0 ? row.cost_basis.toFixed(2) : '',
    )
  const action = useAction()
  return (
    <div className="cost-editor">
      <span className="hint">
        {row.cost_source === 'manual'
          ? '手动录入'
          : row.cost_source || '来源未记录'}{' '}
        · {time(row.cost_modified_at)}
      </span>
      <Feedback err={action.err} msg={action.msg} />
      {editing ? (
        <form
          onSubmit={async (e) => {
            e.preventDefault()
            if (
              await action.run(
                () =>
                  api.put(
                    `/inventory/${row.channel}/${encodeURIComponent(row.asset_id)}/cost`,
                    { cost: Number(value) },
                  ),
                '成本已保存',
              )
            ) {
              setEditing(false)
              onSaved()
            }
          }}
        >
          <label className="field">
            成本金额（元）
            <input
              aria-label="录入成本价"
              autoFocus
              type="number"
              required
              min="0.01"
              step="0.01"
              value={value}
              onChange={(e) => setValue(e.target.value)}
            />
          </label>
          <div className="toolbar">
            <button className="small" disabled={action.busy || !value}>
              {action.busy ? '保存中…' : '保存成本'}
            </button>
            <button
              type="button"
              className="ghost small"
              disabled={action.busy}
              onClick={() => setEditing(false)}
            >
              取消
            </button>
          </div>
        </form>
      ) : (
        <button
          className="ghost small"
          onClick={() => {
            setValue(row.cost_basis > 0 ? row.cost_basis.toFixed(2) : '')
            setEditing(true)
          }}
        >
          {row.cost_basis > 0 ? '修改成本' : '录入成本'}
        </button>
      )}
    </div>
  )
}
