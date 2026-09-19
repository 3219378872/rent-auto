import { useState } from 'react'
import type { AuditRow, PriceActionRow } from '../api/client'
import { Pager, usePagedList } from '../lib/paged'
import { useFilters } from '../lib/data'
import { money, reasonLabel, time, toISO } from '../lib/format'
import {
  DecisionBadge,
  ItemLinks,
  LoadState,
  Modal,
  RecordDetail,
} from '../components/Panel'
import { ChannelFilter, TimeRange, dateError } from '../components/Filters'

export default function Audit() {
  const filters = useFilters({
    tab: 'audit',
    action: '',
    channel: '',
    since: '',
    until: '',
    mode: '',
    result: '',
    hash_name: '',
    asset_id: '',
    id: '',
    listing_id: '',
  })
  const f = filters.values,
    pricing = f.tab === 'pricing',
    [selected, setSelected] = useState<AuditRow | PriceActionRow | null>(null)
  const query = pricing
    ? { ...f, since: toISO(f.since), until: toISO(f.until) }
    : {
        action: f.action,
        channel: f.channel,
        since: toISO(f.since),
        until: toISO(f.until),
      }
  const list = usePagedList<AuditRow | PriceActionRow>(
    pricing ? '/price-actions' : '/audit',
    query,
    50,
    0,
    !dateError(f.since, f.until),
  )
  return (
    <div>
      <div className="page-heading">
        <h2>审计日志</h2>
        <button className="ghost" onClick={list.reload} disabled={list.loading}>
          刷新记录
        </button>
      </div>
      <div className="tabs" aria-label="记录类型">
        <button
          aria-pressed={!pricing}
          className={!pricing ? 'active' : 'ghost'}
          onClick={() => {
            filters.set({ tab: 'audit', action: '' })
            setSelected(null)
          }}
        >
          操作审计
        </button>
        <button
          aria-pressed={pricing}
          className={pricing ? 'active' : 'ghost'}
          onClick={() => {
            filters.set({ tab: 'pricing', action: '' })
            setSelected(null)
          }}
        >
          定价记录
        </button>
      </div>
      <div className="toolbar filters">
        <ChannelFilter
          value={f.channel}
          onChange={(channel) => filters.set({ channel })}
        />
        <label className="field">
          动作
          <input
            placeholder={
              pricing
                ? '如 reprice / skip / publish'
                : '如 strategy.global.update'
            }
            value={f.action}
            onChange={(e) => filters.set({ action: e.target.value })}
          />
        </label>
        {pricing && (
          <>
            <label className="field">
              执行模式
              <select
                value={f.mode}
                onChange={(e) => filters.set({ mode: e.target.value })}
              >
                <option value="">全部模式</option>
                <option value="dry_run">模拟执行</option>
                <option value="real">真实执行</option>
              </select>
            </label>
            <label className="field">
              结果
              <select
                value={f.result}
                onChange={(e) => filters.set({ result: e.target.value })}
              >
                <option value="">全部结果</option>
                <option value="success">成功 / 模拟建议</option>
                <option value="failed">失败</option>
                <option value="skip">跳过</option>
              </select>
            </label>
            <label className="field">
              模板名称（精确）
              <input
                value={f.hash_name}
                onChange={(e) => filters.set({ hash_name: e.target.value })}
              />
            </label>
          </>
        )}
        <button
          className="ghost"
          onClick={() => {
            const tab = f.tab
            filters.reset()
            filters.set({
              tab,
              action: '',
              channel: '',
              since: '',
              until: '',
              mode: '',
              result: '',
              hash_name: '',
              asset_id: '',
              id: '',
              listing_id: '',
            })
          }}
        >
          清除筛选
        </button>
      </div>
      <TimeRange since={f.since} until={f.until} onChange={filters.set} />
      {pricing && (
        <p className="hint">
          记录 ID：{f.id || '全部'} · 货架：{f.listing_id || '全部'} · 资产：
          {f.asset_id || '全部'}。历史策略版本与行情快照未存储时显示“未记录”。
        </p>
      )}
      <LoadState
        {...list}
        empty={list.data?.total === 0}
        filtered={Object.entries(f).some(([k, v]) => k !== 'tab' && !!v)}
        reset={filters.reset}
        emptyText="暂无记录。任务运行或保存配置后可在此查看。"
      />
      {list.data && (
        <>
          <p className="muted">
            共 {list.data.total} 条 · 更新于 {time(list.updatedAt)}
          </p>
          <div className="table-scroll" tabIndex={0} aria-label="审计记录">
            <table className="grid">
              <thead>
                <tr>
                  <th>时间 / 渠道</th>
                  <th>{pricing ? '商品 / 动作' : '操作者 / 动作'}</th>
                  <th>{pricing ? '模式 / 结果' : '目标'}</th>
                  <th>{pricing ? '租金变化' : '详情摘要'}</th>
                  <th>查看</th>
                </tr>
              </thead>
              <tbody>
                {list.data.items.map((r, i) =>
                  'dry_run' in r ? (
                    <tr key={r.id}>
                      <td>
                        {time(r.ts)}
                        <div>
                          {r.channel.toUpperCase()} · #{r.id}
                        </div>
                      </td>
                      <td className="item-cell">
                        <strong>{r.hash_name}</strong>
                        <div>
                          {r.action} · 资产 {r.asset_id || '未记录'}
                        </div>
                      </td>
                      <td>
                        <span className="muted">
                          {r.dry_run ? '模拟执行' : '真实执行'}
                        </span>
                        <div>
                          <DecisionBadge decision={r} />
                        </div>
                        {typeof r.decision?.skip === 'string' && (
                          <small>{reasonLabel(r.decision.skip)}</small>
                        )}
                      </td>
                      <td className="numeric">
                        {r.old_rent == null ? '未记录' : money(r.old_rent)} →{' '}
                        {r.new_rent == null ? '未记录' : money(r.new_rent)}
                      </td>
                      <td>
                        <button
                          className="ghost small"
                          onClick={() => setSelected(r)}
                        >
                          查看详情
                        </button>
                      </td>
                    </tr>
                  ) : (
                    <tr key={`${r.ts}-${i}`}>
                      <td>
                        {time(r.ts)}
                        <div>{r.channel || '系统'}</div>
                      </td>
                      <td>
                        {r.actor}
                        <div>{r.action}</div>
                      </td>
                      <td className="item-cell">{r.target || '未记录'}</td>
                      <td className="item-cell">
                        <div className="clamp-text">
                          {JSON.stringify(r.detail ?? {})}
                        </div>
                      </td>
                      <td>
                        <button
                          className="ghost small"
                          onClick={() => setSelected(r)}
                        >
                          查看详情
                        </button>
                      </td>
                    </tr>
                  ),
                )}
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
      {selected && (
        <Modal
          title={
            'dry_run' in selected ? `定价记录 #${selected.id}` : '审计详情'
          }
          onClose={() => setSelected(null)}
        >
          {'dry_run' in selected && (
            <>
              <p>
                {selected.hash_name} ·{' '}
                {selected.dry_run ? '模拟执行' : '真实执行'}{' '}
                <DecisionBadge decision={selected} />
              </p>
              <table className="grid">
                <thead>
                  <tr>
                    <th>字段</th>
                    <th>旧值</th>
                    <th>新值</th>
                  </tr>
                </thead>
                <tbody>
                  {[
                    ['短租', selected.old_rent, selected.new_rent],
                    ['长租', selected.old_long, selected.new_long],
                    ['押金', selected.old_deposit, selected.new_deposit],
                    ['租期（天）', selected.old_days, selected.new_days],
                  ].map(([label, old, newValue]) => (
                    <tr key={String(label)}>
                      <td>{label}</td>
                      <td>
                        {old == null
                          ? '未记录'
                          : label === '租期（天）'
                            ? String(old)
                            : money(Number(old))}
                      </td>
                      <td>
                        {newValue == null
                          ? '未记录'
                          : label === '租期（天）'
                            ? String(newValue)
                            : money(Number(newValue))}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <p className="hint">策略版本：未记录 · 行情快照：未记录</p>
              <ItemLinks hash={selected.hash_name} asset={selected.asset_id} />
            </>
          )}
          <RecordDetail detail={selected} />
        </Modal>
      )}
    </div>
  )
}
