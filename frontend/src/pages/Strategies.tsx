import { useEffect, useId, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type StrategyRow, type TemplateRow } from '../api/client'
import { ParamGroupsEditor } from './strategies/fields'
import { HELP_ROWS } from './strategies/help'
import {
  cloneDefaults,
  normalizeParams,
  ROUTES,
  validateParams,
  type GroupKey,
  type StrategyParams,
} from './strategies/params'
import { useAction, useFilters, useResource } from '../lib/data'
import { Pager, usePagedList } from '../lib/paged'
import {
  DraftGuard,
  ExecutionMode,
  Feedback,
  LoadState,
} from '../components/Panel'
import { money, time } from '../lib/format'

const changedMode = () =>
  window.dispatchEvent(new Event('ra-execution-changed'))
function RouteField({
  value,
  onChange,
  template = false,
}: {
  value: string
  onChange: (value: string) => void
  template?: boolean
}) {
  const name = useId()
  return (
    <fieldset className="route-field">
      <legend>{template ? '模板渠道路由' : '渠道路由'}</legend>
      <div
        className="seg"
        role="radiogroup"
        aria-label={template ? '模板渠道路由' : '渠道路由'}
      >
        {ROUTES.map((r) => (
          <label key={r.value} className={r.value === value ? 'active' : ''}>
            <input
              type="radio"
              name={name}
              value={r.value}
              checked={value === r.value}
              onChange={() => onChange(r.value)}
            />
            {r.label}
          </label>
        ))}
      </div>
    </fieldset>
  )
}
function confirmReal(was: boolean, next: boolean, label: string) {
  return (
    was ||
    !next ||
    window.confirm(
      `开启${label}真实执行许可？保存后，符合环境、全局及模板许可的任务可能向平台上架、改价或下架。`,
    )
  )
}
function diffParams(before: StrategyParams, after: StrategyParams) {
  const flatten = (f: StrategyParams) =>
    Object.fromEntries(
      Object.entries(f).flatMap(([k, v]) =>
        typeof v === 'object'
          ? Object.entries(v).map(([sub, n]) => [`${k}.${sub}`, n])
          : [[k, v]],
      ),
    )
  const old = flatten(before)
  return Object.entries(flatten(after))
    .filter(([key, value]) => value !== old[key])
    .map(([key, value]) => `${key}: ${old[key]} → ${value}`)
}

export default function Strategies() {
  const resource = useResource<StrategyRow[]>('/strategies'),
    filters = useFilters({
      tab: 'global',
      search: '',
      blacklisted: '',
      strategy_search: '',
    })
  const f = filters.values,
    rows = Array.isArray(resource.data) ? resource.data : [],
    global = rows.find((r) => r.scope === 'global')
  const [globalDirty, setGlobalDirty] = useState(false),
    [tplDirty, setTplDirty] = useState(false)
  const [editing, setEditing] = useState<StrategyRow | 'new' | null>(null)
  const catalog = usePagedList<TemplateRow>(
    '/templates/catalog',
    {
      search: f.search,
      blacklisted:
        editing === 'new' && f.tab === 'templates' ? 'false' : f.blacklisted,
    },
    50,
  )
  const actions = useAction()
  const open = (row: StrategyRow | 'new') => {
    if (tplDirty && !window.confirm('放弃当前未保存的模板更改？')) return
    setEditing(row)
    setTplDirty(false)
  }
  return (
    <div>
      <DraftGuard dirty={globalDirty || tplDirty} />
      <div className="page-heading">
        <h2>上架 / 改价策略</h2>
        <button
          className="ghost"
          disabled={resource.loading}
          onClick={resource.reload}
        >
          刷新已保存策略
        </button>
      </div>
      <div className="tabs" aria-label="策略分类">
        {[
          ['global', '全局策略'],
          ['templates', '模板覆盖'],
          ['blacklist', '模板黑名单'],
        ].map(([tab, label]) => (
          <button
            key={tab}
            className={f.tab === tab ? 'active' : 'ghost'}
            aria-pressed={f.tab === tab}
            onClick={() => filters.set({ tab })}
          >
            {label}
            {(tab === 'global' && globalDirty) ||
            (tab === 'templates' && tplDirty)
              ? ' · 未保存'
              : ''}
          </button>
        ))}
      </div>
      <LoadState
        {...resource}
        empty={resource.data !== undefined && !global}
        emptyText="暂无全局策略，请等待服务完成初始化后重试。"
      />
      <Feedback {...actions} />
      {global && (
        <>
          <div hidden={f.tab !== 'global'}>
            <GlobalEditor
              row={global}
              onDirty={setGlobalDirty}
              onSaved={resource.reload}
            />
          </div>
          <div hidden={f.tab !== 'templates'}>
            <div className="page-heading">
              <h3>模板级覆盖策略</h3>
              <button onClick={() => open('new')}>新建模板策略</button>
            </div>
            {editing && (
              <TemplateEditor
                key={editing === 'new' ? 'new' : editing.id}
                row={editing === 'new' ? undefined : editing}
                global={global}
                templates={catalog.data?.items ?? []}
                picker={
                  <>
                    <label className="field">
                      搜索可用模板
                      <input
                        value={f.search}
                        onChange={(e) =>
                          filters.set({ search: e.target.value })
                        }
                      />
                    </label>
                    <LoadState
                      {...catalog}
                      empty={catalog.data?.total === 0}
                      emptyText="没有可选模板，请调整搜索或等待库存同步。"
                    />
                    <Pager
                      page={catalog.page}
                      total={catalog.data?.total}
                      pageSize={50}
                      onPage={catalog.setPage}
                      loading={catalog.loading}
                    />
                  </>
                }
                onDirty={setTplDirty}
                onClose={() => {
                  if (tplDirty && !window.confirm('放弃当前未保存的模板更改？'))
                    return
                  setEditing(null)
                  setTplDirty(false)
                }}
                onSaved={() => {
                  resource.reload()
                  changedMode()
                  setTplDirty(false)
                  setEditing(null)
                  actions.setMsg('模板策略已保存')
                }}
              />
            )}
            <label className="field">
              搜索已有覆盖
              <input
                value={f.strategy_search}
                onChange={(e) =>
                  filters.set({ strategy_search: e.target.value })
                }
              />
            </label>
            <div className="table-scroll" tabIndex={0} aria-label="模板策略">
              <table className="grid">
                <thead>
                  <tr>
                    <th>模板</th>
                    <th>路由</th>
                    <th>真实执行许可</th>
                    <th>覆盖字段 / 优先级</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {rows
                    .filter(
                      (r) =>
                        r.scope === 'template' &&
                        r.name
                          .toLowerCase()
                          .includes(f.strategy_search.toLowerCase()),
                    )
                    .map((r) => (
                      <tr key={r.id}>
                        <td className="item-cell">
                          {r.name.replace(/^tpl:/, '')}
                        </td>
                        <td>
                          {ROUTES.find((x) => x.value === r.channel_route)
                            ?.label || r.channel_route}
                        </td>
                        <td>
                          {r.real_execution_enabled
                            ? '已开启（仍受全局约束）'
                            : '模拟执行'}
                        </td>
                        <td>
                          {Object.keys(r.params).join('、') || '全部参数继承'} ·{' '}
                          {r.priority}
                        </td>
                        <td>
                          <div className="toolbar">
                            <button
                              className="ghost small"
                              onClick={() => open(r)}
                            >
                              编辑
                            </button>
                            <button
                              className="ghost small"
                              disabled={actions.busy}
                              onClick={async () => {
                                if (
                                  !window.confirm(
                                    `删除「${r.name}」覆盖？参数和执行许可将回落全局${global.real_execution_enabled ? '，全局当前允许真实执行' : ''}。`,
                                  )
                                )
                                  return
                                if (
                                  await actions.run(
                                    () =>
                                      api.del(`/strategies/template/${r.id}`),
                                    '已删除覆盖并回落全局',
                                  )
                                ) {
                                  resource.reload()
                                  changedMode()
                                  if (
                                    editing !== 'new' &&
                                    editing?.id === r.id
                                  ) {
                                    setEditing(null)
                                    setTplDirty(false)
                                  }
                                }
                              }}
                            >
                              删除覆盖
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                </tbody>
              </table>
            </div>
            {!rows.some((r) => r.scope === 'template') && (
              <p className="empty-state">
                暂无模板覆盖，所有模板使用全局参数。
              </p>
            )}
          </div>
        </>
      )}
      {f.tab === 'blacklist' && (
        <section className="section">
          <h3>模板黑名单</h3>
          <p className="hint">拉黑后退出上架路由与锚点合成。修改即时保存。</p>
          <div className="toolbar filters">
            <label className="field">
              搜索模板
              <input
                value={f.search}
                onChange={(e) => filters.set({ search: e.target.value })}
              />
            </label>
            <label className="field">
              黑名单状态
              <select
                value={f.blacklisted}
                onChange={(e) => filters.set({ blacklisted: e.target.value })}
              >
                <option value="">全部</option>
                <option value="true">已拉黑</option>
                <option value="false">未拉黑</option>
              </select>
            </label>
          </div>
          <LoadState
            {...catalog}
            empty={catalog.data?.total === 0}
            filtered={!!f.search || !!f.blacklisted}
            reset={() => filters.set({ search: '', blacklisted: '' })}
          />
          {catalog.data && (
            <>
              <p className="muted">共 {catalog.data.total} 个模板</p>
              <div className="table-scroll" tabIndex={0}>
                <table className="grid">
                  <thead>
                    <tr>
                      <th>模板</th>
                      <th>品类</th>
                      <th className="numeric">价值锚点</th>
                      <th>状态 / 操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {catalog.data.items.map((t) => (
                      <tr key={t.hash_name}>
                        <td className="item-cell">
                          {t.display_name || t.hash_name}
                          <div className="muted">{t.hash_name}</div>
                        </td>
                        <td>{t.category || '未分类'}</td>
                        <td className="numeric">
                          {t.value_anchor == null
                            ? '未记录'
                            : money(t.value_anchor)}
                        </td>
                        <td>
                          <button
                            className="ghost small"
                            disabled={actions.busy}
                            onClick={async () => {
                              if (
                                await actions.run(
                                  () =>
                                    api.put('/templates/blacklist', {
                                      hash_name: t.hash_name,
                                      blacklisted: !t.blacklisted,
                                    }),
                                  `${t.blacklisted ? '已解除拉黑' : '已拉黑'}：${t.display_name || t.hash_name}`,
                                )
                              )
                                catalog.reload()
                            }}
                          >
                            {t.blacklisted ? '解除拉黑' : '加入黑名单'}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          )}
          <Pager
            page={catalog.page}
            total={catalog.data?.total}
            pageSize={50}
            onPage={catalog.setPage}
            loading={catalog.loading}
          />
        </section>
      )}
    </div>
  )
}

function GlobalEditor({
  row,
  onDirty,
  onSaved,
}: {
  row: StrategyRow
  onDirty: (v: boolean) => void
  onSaved: () => void
}) {
  const initial = () => ({
    form: normalizeParams(row.params),
    route: row.channel_route,
    real: row.real_execution_enabled,
    updatedAt: row.updated_at,
  })
  const [saved, setSaved] = useState(initial),
    [draft, setDraft] = useState(initial),
    [dirty, setDirty] = useState(false),
    [revision, setRevision] = useState(0)
  const action = useAction(),
    mark = () => {
      setDirty(true)
      onDirty(true)
    }
  const previousRow = useRef(row)
  useEffect(() => {
    if (previousRow.current === row) return
    previousRow.current = row
    if (!dirty) {
      const latest = {
        form: normalizeParams(row.params),
        route: row.channel_route,
        real: row.real_execution_enabled,
        updatedAt: row.updated_at,
      }
      setSaved(latest)
      setDraft(latest)
      setRevision((v) => v + 1)
    }
  }, [row, dirty])
  const restore = () => {
    const latest = initial()
    setSaved(latest)
    setDraft(latest)
    setDirty(false)
    onDirty(false)
    setRevision((v) => v + 1)
    action.setErr('')
  }
  const changes = diffParams(saved.form, draft.form)
  return (
    <form
      onInput={mark}
      onSubmit={async (e) => {
        e.preventDefault()
        const invalid = validateParams(draft.form)
        if (invalid) {
          action.setErr(invalid)
          return
        }
        if (!confirmReal(saved.real, draft.real, '全局')) return
        if (
          await action.run(async () => {
            await api.put('/strategies/global', {
              params: draft.form,
              channel_route: draft.route,
              real_execution_enabled: draft.real,
            })
            const rows = await api.get<StrategyRow[]>('/strategies')
            const updated = rows.find((r) => r.scope === 'global')
            if (!updated) throw new Error('已提交，但未能回读策略，请刷新核对')
            const next = {
              form: normalizeParams(updated.params),
              route: updated.channel_route,
              real: updated.real_execution_enabled,
              updatedAt: updated.updated_at,
            }
            setSaved(next)
            setDraft(next)
            setDirty(false)
            onDirty(false)
            setRevision((v) => v + 1)
            changedMode()
            onSaved()
          }, '策略已保存并回读确认')
        )
          action.setErr('')
      }}
    >
      <fieldset disabled={action.busy} className="form-body">
        <section className="section">
          <h3>渠道与执行</h3>
          <RouteField
            value={draft.route}
            onChange={(route) => {
              mark()
              setDraft({ ...draft, route })
            }}
          />
          <label className="switch">
            <input
              type="checkbox"
              checked={draft.real}
              onChange={(e) => {
                mark()
                setDraft({ ...draft, real: e.target.checked })
              }}
            />
            <span className="track" />
            全局真实执行
          </label>
          <p className="hint">
            开启只代表授予许可，保存时会单独确认。实际执行还取决于环境设置及模板许可。
          </p>
          <ExecutionMode />
          <p>
            <Link to="/audit?tab=pricing&mode=dry_run">查看模拟定价记录</Link>
          </p>
        </section>
        <ParamGroupsEditor
          key={revision}
          form={draft.form}
          patchGroup={(group, key, value) => {
            mark()
            setDraft((d) => ({
              ...d,
              form: { ...d.form, [group]: { ...d.form[group], [key]: value } },
            }))
          }}
          patchInt={(key, value) => {
            mark()
            setDraft((d) => ({ ...d, form: { ...d.form, [key]: value } }))
          }}
        />
        <details className="section">
          <summary>字段详细说明</summary>
          <div className="table-scroll" tabIndex={0}>
            <table className="grid">
              <thead>
                <tr>
                  <th>分组</th>
                  <th>字段</th>
                  <th>默认值</th>
                  <th>说明</th>
                </tr>
              </thead>
              <tbody>
                {HELP_ROWS.map((r, i) => (
                  <tr key={i}>
                    <td>{r.group}</td>
                    <td>{r.name}</td>
                    <td>{r.def}</td>
                    <td className="item-cell">{r.desc}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </details>
      </fieldset>
      <div className="save-bar">
        <Feedback {...action} />
        <div className="toolbar">
          <button disabled={action.busy || !dirty}>
            {action.busy ? '保存中…' : '保存全局策略'}
          </button>
          <button
            type="button"
            className="ghost"
            disabled={action.busy || !dirty}
            onClick={restore}
          >
            放弃更改
          </button>
          <button
            type="button"
            className="ghost"
            disabled={action.busy}
            onClick={() => {
              mark()
              setDraft((d) => ({ ...d, form: cloneDefaults() }))
              setRevision((v) => v + 1)
            }}
          >
            恢复默认值
          </button>
          <span className="muted">
            {dirty ? '未保存更改' : '已保存'} · {row.name} · 更新{' '}
            {time(saved.updatedAt)}
          </span>
        </div>
        {dirty && (
          <details>
            <summary>查看变更摘要（{changes.length} 项参数）</summary>
            <p className="hint">
              {[
                ...changes,
                ...(draft.route !== saved.route
                  ? [`渠道：${saved.route} → ${draft.route}`]
                  : []),
                ...(draft.real !== saved.real
                  ? [
                      `真实执行许可：${saved.real ? '开启' : '关闭'} → ${draft.real ? '开启' : '关闭'}`,
                    ]
                  : []),
              ].join('；') || '输入尚未完成，请检查各字段'}
            </p>
          </details>
        )}
      </div>
    </form>
  )
}

function TemplateEditor({
  row,
  global,
  templates,
  picker,
  onDirty,
  onClose,
  onSaved,
}: {
  row?: StrategyRow
  global: StrategyRow
  templates: TemplateRow[]
  picker: React.ReactNode
  onDirty: (v: boolean) => void
  onClose: () => void
  onSaved: () => void
}) {
  const [hash, setHash] = useState(row?.name.replace(/^tpl:/, '') ?? ''),
    [route, setRoute] = useState(row?.channel_route ?? global.channel_route),
    [real, setReal] = useState(row?.real_execution_enabled ?? false),
    [priority, setPriority] = useState(String(row?.priority ?? 0)),
    [params, setParams] = useState<Record<string, unknown>>(row?.params ?? {}),
    [revision, setRevision] = useState(0)
  const action = useAction(),
    inherited = normalizeParams(global.params),
    form = normalizeParams(params, inherited),
    mark = () => onDirty(true)
  const patch = (group: GroupKey, key: string, value: number) => {
    mark()
    setParams((p) => ({
      ...p,
      [group]: { ...((p[group] as object) || {}), [key]: value },
    }))
  }
  return (
    <form
      className="template-editor"
      onInput={mark}
      onSubmit={async (e) => {
        e.preventDefault()
        const invalid = validateParams(form)
        if (invalid) {
          action.setErr(invalid)
          return
        }
        if (!confirmReal(row?.real_execution_enabled ?? false, real, '模板'))
          return
        if (
          await action.run(async () => {
            await api.post('/strategies/template', {
              hash_name: hash,
              channel_route: route,
              params,
              real_execution_enabled: real,
              priority: Number(priority),
            })
            const rows = await api.get<StrategyRow[]>('/strategies')
            if (
              !rows.some(
                (r) => r.scope === 'template' && r.name === `tpl:${hash}`,
              )
            )
              throw new Error('已提交，但未能回读模板策略，请刷新核对')
          }, `模板策略已保存：${hash}`)
        )
          onSaved()
      }}
    >
      <fieldset disabled={action.busy} className="form-body">
        <section className="section">
          <h3>{row ? '编辑模板覆盖' : '新建模板覆盖'}</h3>
          {row ? (
            <p>{hash}</p>
          ) : (
            <>
              {picker}
              <label className="field">
                目标模板
                <select
                  required
                  value={hash}
                  onChange={(e) => {
                    mark()
                    setHash(e.target.value)
                  }}
                >
                  <option value="">请选择模板</option>
                  {hash && !templates.some((t) => t.hash_name === hash) && (
                    <option value={hash}>{hash}</option>
                  )}
                  {templates
                    .filter((t) => !t.blacklisted)
                    .map((t) => (
                      <option value={t.hash_name} key={t.hash_name}>
                        {t.display_name || t.hash_name}
                      </option>
                    ))}
                </select>
              </label>
            </>
          )}
          <RouteField
            template
            value={route}
            onChange={(v) => {
              mark()
              setRoute(v)
            }}
          />
          <div className="toolbar">
            <label className="switch">
              <input
                type="checkbox"
                checked={real}
                onChange={(e) => {
                  mark()
                  setReal(e.target.checked)
                }}
              />
              <span className="track" />
              该模板真实执行
            </label>
            <label className="field">
              优先级
              <input
                type="number"
                required
                step="1"
                min="-100000"
                max="100000"
                value={priority}
                onChange={(e) => setPriority(e.target.value)}
              />
            </label>
          </div>
          <p className="hint">
            未覆盖的数值继承已保存全局策略 →
            内置默认值。路由、优先级和执行许可为模板独立设置。
          </p>
          {hash && <ExecutionMode hash={hash} />}
          <button
            type="button"
            className="ghost"
            onClick={() => {
              mark()
              setParams({})
              setRevision((v) => v + 1)
            }}
          >
            清除参数覆盖
          </button>
        </section>
        <ParamGroupsEditor
          key={revision}
          form={form}
          inherited={inherited}
          overrides={params}
          patchGroup={patch}
          patchInt={(key, value) => {
            mark()
            setParams((p) => ({ ...p, [key]: value }))
          }}
          resetField={(group, key) => {
            mark()
            setParams((p) => {
              const next = { ...p }
              if (group) {
                const g = { ...(p[group] as Record<string, unknown>) }
                delete g[key]
                if (Object.keys(g).length) next[group] = g
                else delete next[group]
              } else delete next[key]
              return next
            })
            setRevision((v) => v + 1)
          }}
        />
      </fieldset>
      <div className="save-bar">
        <Feedback {...action} />
        <div className="toolbar">
          <button disabled={action.busy || !hash}>
            {action.busy ? '保存中…' : '保存模板策略'}
          </button>
          <button
            type="button"
            className="ghost"
            disabled={action.busy}
            onClick={onClose}
          >
            关闭编辑
          </button>
          <span className="hint">保存稀疏覆盖，未设置字段继续继承</span>
        </div>
      </div>
    </form>
  )
}
