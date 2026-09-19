import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type ListingRow } from '../api/client'
import { Pager, usePagedList } from '../lib/paged'
import { useAction, useFilters } from '../lib/data'
import { money, reasonLabel, statusLabel, time } from '../lib/format'
import {
  DecisionBadge,
  ExecutionMode,
  Feedback,
  ItemLinks,
  JobList,
  LoadState,
  Modal,
  StatusBadge,
} from '../components/Panel'
import { ChannelFilter } from '../components/Filters'

export default function Listings() {
  const filters = useFilters({
    channel: '',
    state: 'active',
    search: '',
    mismatch: '',
    hash_name: '',
    asset_id: '',
  })
  const f = filters.values,
    list = usePagedList<ListingRow>('/listings', f, 50, 30000)
  const action = useAction(),
    [confirm, setConfirm] = useState(false),
    [jobKey, setJobKey] = useState(0)
  return (
    <div>
      <h2>上架状态（双渠道）</h2>
      <section className="section">
        <div className="page-heading">
          <div>
            <h3>全局定价任务</h3>
            <p className="hint">
              检查所有符合策略的 UU / ECO
              货架。下方筛选仅用于查看，不限制任务范围。
            </p>
          </div>
          <button onClick={() => setConfirm(true)} disabled={action.busy}>
            {action.busy ? '受理中…' : '运行全局重定价'}
          </button>
        </div>
        <Feedback {...action} />
      </section>
      <div className="toolbar filters">
        <ChannelFilter
          value={f.channel}
          onChange={(channel) => filters.set({ channel })}
        />
        <label className="field">
          实际状态
          <select
            value={f.state}
            onChange={(e) => filters.set({ state: e.target.value })}
          >
            <option value="">全部实际状态</option>
            {['active', 'leased', 'none', 'stale', 'unknown'].map((s) => (
              <option key={s} value={s}>
                {statusLabel(s)}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          商品 / 资产 ID
          <input
            value={f.search}
            onChange={(e) => filters.set({ search: e.target.value })}
          />
        </label>
        <label>
          <input
            type="checkbox"
            checked={f.mismatch === 'true'}
            onChange={(e) =>
              filters.set({ mismatch: e.target.checked ? 'true' : '' })
            }
          />{' '}
          仅状态不一致
        </label>
        <button className="ghost" onClick={filters.reset}>
          清除筛选
        </button>
        <button className="ghost" onClick={list.reload} disabled={list.loading}>
          刷新货架
        </button>
      </div>
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
            共 {list.data.total} 条 · 更新于 {time(list.updatedAt)}
          </p>
          <div className="table-scroll" tabIndex={0} aria-label="货架列表">
            <table className="grid">
              <thead>
                <tr>
                  <th>渠道 / 状态</th>
                  <th>商品 / 资产</th>
                  <th className="numeric">租金 / 天</th>
                  <th className="numeric">长租 / 天</th>
                  <th className="numeric">押金 / 租期</th>
                  <th>最近决策</th>
                </tr>
              </thead>
              <tbody>
                {list.data.items.map((r) => (
                  <tr key={r.id}>
                    <td>
                      {r.channel.toUpperCase()}
                      <div>
                        <StatusBadge value={r.actual_state} />
                      </div>
                      <small>期望：{statusLabel(r.desired_state)}</small>
                    </td>
                    <td className="item-cell">
                      <strong>{r.hash_name}</strong>
                      <div className="muted">
                        资产 {r.asset_id} · 货架 {r.goods_ref}
                      </div>
                      <ItemLinks hash={r.hash_name} asset={r.asset_id} />
                    </td>
                    <td className="numeric">{money(r.rent_price)}</td>
                    <td className="numeric">
                      {r.long_rent_price > 0
                        ? money(r.long_rent_price)
                        : '未设置'}
                    </td>
                    <td className="numeric">
                      {money(r.deposit)}
                      <div className="muted">最长 {r.max_days || '—'} 天</div>
                    </td>
                    <td className="decision-cell">
                      <div>因子 ×{(r.factor ?? 1).toFixed(2)}</div>
                      {r.last_decision ? (
                        <>
                          <DecisionBadge decision={r.last_decision} />
                          <div>
                            {r.last_decision.skip
                              ? reasonLabel(r.last_decision.skip)
                              : r.last_decision.new_rent != null
                                ? `建议 ${money(r.last_decision.new_rent)}`
                                : r.last_decision.action}
                          </div>
                          <Link
                            to={`/audit?tab=pricing&${r.last_decision.id ? `id=${r.last_decision.id}` : `listing_id=${r.id}`}`}
                          >
                            查看决策详情
                          </Link>
                          <div className="muted">
                            决策：{time(r.last_decision.at)}
                          </div>
                        </>
                      ) : (
                        <span className="muted">暂无决策记录</span>
                      )}
                      <div className="muted">
                        最近改价：{time(r.last_reprice_at)}
                      </div>
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
      <JobList key={jobKey} only="reprice" onRefresh={list.reload} />
      {confirm && (
        <Modal
          title="确认运行全局重定价"
          onClose={() => {
            if (!action.busy) setConfirm(false)
          }}
          actions={
            <>
              <button
                disabled={action.busy}
                onClick={async () => {
                  if (
                    await action.run(
                      () => api.post('/jobs/reprice/run'),
                      '任务已受理，请查看最近任务状态；受理不代表执行完成。',
                    )
                  ) {
                    setConfirm(false)
                    setJobKey((v) => v + 1)
                    list.reload()
                  }
                }}
              >
                确认全局运行
              </button>
              <button
                className="ghost"
                disabled={action.busy}
                onClick={() => setConfirm(false)}
              >
                取消
              </button>
            </>
          }
        >
          <p>
            任务范围：所有符合策略的 UU / ECO 货架，包含当前列表未显示的资产。
          </p>
          <p>
            当前筛选：{f.channel ? f.channel.toUpperCase() : '全部渠道'} ·{' '}
            {f.state ? statusLabel(f.state) : '全部状态'}。此筛选不会传给任务。
          </p>
          <ExecutionMode />
          <p className="hint">
            实际写入仍取决于每个模板的许可、渠道状态和定价护栏。
          </p>
          <Feedback err={action.err} />
        </Modal>
      )}
    </div>
  )
}
