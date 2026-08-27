import { useCallback, useEffect, useState } from 'react'
import { api } from '../api/client'
import type { StrategyRow, TemplateRow } from '../api/client'
import { ParamGroupsEditor } from './strategies/fields'
import { HELP_ROWS } from './strategies/help'
import type { GroupKey, StrategyParams } from './strategies/params'
import { cloneDefaults, normalizeParams, ROUTES, validateParams } from './strategies/params'

export default function Strategies() {
  const [list, setList] = useState<StrategyRow[]>([])
  const [globalRow, setGlobalRow] = useState<StrategyRow | null>(null)
  const [form, setForm] = useState<StrategyParams>(cloneDefaults)
  const [route, setRoute] = useState('both')
  const [real, setReal] = useState(false)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')
  const [templates, setTemplates] = useState<TemplateRow[]>([])
  const [tplEditing, setTplEditing] = useState<{
    id?: number
    hashName: string
    route: string
    real: boolean
    form: StrategyParams
  } | null>(null)

  const tplPatchGroup = (group: GroupKey, key: string, value: number) =>
    setTplEditing((t) => (t ? { ...t, form: { ...t.form, [group]: { ...t.form[group], [key]: value } } as StrategyParams } : t))
  const tplPatchInt = (key: 'uu_max_days' | 'eco_max_days', value: number) =>
    setTplEditing((t) => (t ? { ...t, form: { ...t.form, [key]: Math.round(value) } } : t))

  const openNewTpl = () => {
    setErr(''); setMsg('')
    const usable = templates.filter((x) => !x.blacklisted)
    if (usable.length === 0) {
      setErr('暂无可用模板（待库存同步产出后再创建模板级策略）')
      return
    }
    setTplEditing({ hashName: usable[0].hash_name, route: 'both', real: false, form: cloneDefaults() })
  }

  const saveTpl = async () => {
    if (!tplEditing) return
    setErr(''); setMsg('')
    const invalid = validateParams(tplEditing.form)
    if (invalid) { setErr(invalid); return }
    try {
      await api.post('/strategies/template', {
        hash_name: tplEditing.hashName,
        channel_route: tplEditing.route,
        params: tplEditing.form,
        ...(tplEditing.id ? {} : { real_execution_enabled: tplEditing.real }),
      })
      setMsg(`模板策略已保存：${tplEditing.hashName}`)
      setTplEditing(null)
      load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  const deleteTpl = async (row: StrategyRow) => {
    if (!window.confirm(`删除模板「${row.name}」的覆盖策略？该模板将立即回落全局策略。`)) return
    setErr(''); setMsg('')
    try {
      await api.del(`/strategies/template/${row.id}`)
      setMsg(`已删除覆盖策略：${row.name}（回落全局）`)
      load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  const loadTemplates = useCallback(() => {
    api.get<TemplateRow[]>('/templates').then(setTemplates).catch(() => undefined)
  }, [])

  const toggleBlacklist = async (t: TemplateRow) => {
    setErr(''); setMsg('')
    try {
      await api.put('/templates/blacklist', { hash_name: t.hash_name, blacklisted: !t.blacklisted })
      setMsg(t.blacklisted ? `已解除拉黑：${t.display_name || t.hash_name}` : `已拉黑：${t.display_name || t.hash_name}（将退出上架路由与锚点合成）`)
      loadTemplates()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  useEffect(loadTemplates, [loadTemplates])

  const load = useCallback(() => {
    api.get<StrategyRow[]>('/strategies').then((rows) => {
      setList(rows)
      const g = rows.find((r) => r.scope === 'global')
      if (g) {
        setGlobalRow(g)
        setForm(normalizeParams(g.params ?? {}))
        setRoute(g.channel_route)
        setReal(g.real_execution_enabled)
      }
    }).catch((e) => setErr(e.message))
  }, [])

  useEffect(load, [load])

  const patchGroup = (group: GroupKey, key: string, value: number) =>
    setForm((f) => ({ ...f, [group]: { ...f[group], [key]: value } }) as StrategyParams)

  const patchInt = (key: 'uu_max_days' | 'eco_max_days', value: number) =>
    setForm((f) => ({ ...f, [key]: Math.round(value) }))

  const save = async () => {
    setErr(''); setMsg('')
    const invalid = validateParams(form)
    if (invalid) { setErr(invalid); return }
    try {
      await api.put('/strategies/global', {
        params: form,
        channel_route: route,
        real_execution_enabled: real,
      })
      setMsg('策略已保存')
      load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div>
      <h2>上架 / 改价策略</h2>
      {err && <div className="error">{err}</div>}
      {msg && <div className="ok-msg">{msg}</div>}

      <div className="section">
        <h3 style={{ marginTop: 0 }}>渠道与执行</h3>
        <div className="row" style={{ flexWrap: 'wrap' }}>
          <span className="field-label">渠道路由</span>
          <div className="seg" role="radiogroup" aria-label="渠道路由">
            {ROUTES.map((r) => (
              <button
                key={r.value} type="button" role="radio" aria-checked={route === r.value}
                className={route === r.value ? 'active' : ''}
                onClick={() => setRoute(r.value)}
              >
                {r.label}
              </button>
            ))}
          </div>
          <label className="switch">
            <input type="checkbox" checked={real} onChange={(e) => setReal(e.target.checked)} />
            <span className="track" />
            允许真实执行（关闭 = 永远 dry-run）
          </label>
        </div>
        <div className="hint" style={{ marginTop: 8 }}>
          新策略首次执行必须保持 dry-run：完整走决策链但只写模拟记录、不调平台接口；
          在「审计」页核对模拟决策无误后再开启真实执行。
        </div>
      </div>

      <ParamGroupsEditor form={form} patchGroup={patchGroup} patchInt={patchInt} />

      <div className="toolbar">
        <button onClick={save}>保存全局策略</button>
        <button className="ghost" onClick={() => setForm(cloneDefaults())}>恢复默认值</button>
        <span className="muted">
          最近更新：{globalRow ? new Date(globalRow.updated_at).toLocaleString('zh-CN') : '—'}
        </span>
      </div>

      <details className="help section">
        <summary>字段详细说明（作用 · 默认值 · 调整影响）</summary>
        <table className="grid">
          <thead><tr><th>分组</th><th>字段</th><th>默认值</th><th>说明</th></tr></thead>
          <tbody>
            {HELP_ROWS.map((r, i) => (
              <tr key={i}>
                <td>{r.group}</td><td>{r.name}</td>
                <td className="muted">{r.def}</td>
                <td style={{ whiteSpace: 'normal' }}>{r.desc}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <div className="hint" style={{ marginTop: 10 }}>
          合并规则：模板级策略深覆盖全局策略，未设字段回落全局 → 内置默认值。
          完整公式见 docs/knowledge/spec/pricing-spec.md。
        </div>
      </details>

      <div className="section">
        <h3 style={{ marginTop: 0 }}>全部策略行</h3>
        <table className="grid">
          <thead><tr><th>ID</th><th>名称</th><th>范围</th><th>路由</th><th>真实执行</th><th>优先级</th></tr></thead>
          <tbody>
            {list.map((s) => (
              <tr key={s.id}>
                <td>{s.id}</td><td>{s.name}</td><td>{s.scope}</td>
                <td>{s.channel_route}</td>
                <td>{s.real_execution_enabled ? '是' : '否'}</td>
                <td>{s.priority}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="section">
        <div className="toolbar" style={{ marginBottom: 8 }}>
          <h3 style={{ margin: 0 }}>模板级覆盖策略（US-STRAT-02）</h3>
          <div className="grow" />
          <button onClick={openNewTpl} disabled={tplEditing !== null}>新建模板策略</button>
        </div>
        {tplEditing && (
          <div className="section" style={{ border: '1px solid var(--border, #ddd)', borderRadius: 8, padding: 12, marginTop: 0 }}>
            <div className="row" style={{ flexWrap: 'wrap' }}>
              <span className="field-label">目标模板</span>
              {tplEditing.id ? (
                <strong>{tplEditing.hashName}</strong>
              ) : (
                <select
                  value={tplEditing.hashName}
                  onChange={(e) => setTplEditing({ ...tplEditing, hashName: e.target.value })}
                >
                  {templates.filter((t) => !t.blacklisted).map((t) => (
                    <option key={t.hash_name} value={t.hash_name}>
                      {t.display_name || t.hash_name}
                    </option>
                  ))}
                </select>
              )}
              <div className="seg" role="radiogroup" aria-label="模板渠道路由">
                {ROUTES.map((rt) => (
                  <button
                    key={rt.value} type="button" role="radio" aria-checked={tplEditing.route === rt.value}
                    className={tplEditing.route === rt.value ? 'active' : ''}
                    onClick={() => setTplEditing({ ...tplEditing, route: rt.value })}
                  >
                    {rt.label}
                  </button>
                ))}
              </div>
              {!tplEditing.id && (
                <label className="switch">
                  <input type="checkbox" checked={tplEditing.real}
                    onChange={(e) => setTplEditing({ ...tplEditing, real: e.target.checked })} />
                  <span className="track" />
                  允许真实执行（关闭 = 永远 dry-run）
                </label>
              )}
            </div>
            {!tplEditing.id && (
              <div className="hint">已有覆盖的模板再次保存会整行替换；更新时留空真实执行开关则保留原值。</div>
            )}
            <ParamGroupsEditor form={tplEditing.form} patchGroup={tplPatchGroup} patchInt={tplPatchInt} />
            <div className="toolbar">
              <button onClick={saveTpl}>保存模板策略</button>
              <button className="ghost" onClick={() => setTplEditing(null)}>取消</button>
            </div>
          </div>
        )}
        <table className="grid">
          <thead><tr><th>模板</th><th>路由</th><th>真实执行</th><th>优先级</th><th>更新时间</th><th>操作</th></tr></thead>
          <tbody>
            {list.filter((s) => s.scope === 'template').map((s) => (
              <tr key={s.id}>
                <td title={s.name}>{s.name.replace(/^tpl:/, '')}</td>
                <td>{s.channel_route}</td>
                <td>{s.real_execution_enabled ? '是' : '否（dry-run）'}</td>
                <td>{s.priority}</td>
                <td className="muted">{new Date(s.updated_at).toLocaleString('zh-CN')}</td>
                <td>
                  <button className="ghost small"
                    onClick={() => {
                      const tpl = templates.find((t) => t.hash_name === s.name.replace(/^tpl:/, ''))
                      setTplEditing({
                        id: s.id,
                        hashName: tpl?.hash_name ?? s.name.replace(/^tpl:/, ''),
                        route: s.channel_route,
                        real: s.real_execution_enabled,
                        form: normalizeParams(s.params ?? {}),
                      })
                    }}
                    disabled={tplEditing !== null}>
                    编辑
                  </button>
                  {' '}
                  <button className="ghost small" onClick={() => deleteTpl(s)} disabled={tplEditing !== null}>删除</button>
                </td>
              </tr>
            ))}
            {list.filter((s) => s.scope === 'template').length === 0 && (
              <tr><td colSpan={6} className="muted">暂无模板级覆盖 —— 所有商品按全局策略执行</td></tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="section">
        <h3 style={{ marginTop: 0 }}>模板黑名单（US-STRAT-05）</h3>
        <p className="hint" style={{ marginTop: 0 }}>
          拉黑后的模板立即退出可路由库存与价值锚点合成；已在架商品由 reconcile 在宽限期后下架。
        </p>
        <table className="grid">
          <thead><tr><th>名称</th><th>品类</th><th>锚点价</th><th>UU 商品类目</th><th>状态</th><th>操作</th></tr></thead>
          <tbody>
            {templates.map((t) => (
              <tr key={t.hash_name}>
                <td title={t.hash_name}>{t.display_name || t.hash_name}</td>
                <td>{t.category || '—'}</td>
                <td>{t.value_anchor != null ? `¥${t.value_anchor.toFixed(2)}` : '—'}</td>
                <td>{t.uu_template_id ?? '—'}</td>
                <td>{t.blacklisted ? <span className="badge bad">已拉黑</span> : <span className="badge ok">正常</span>}</td>
                <td>
                  <button className="ghost small" onClick={() => toggleBlacklist(t)}>
                    {t.blacklisted ? '解除拉黑' : '拉黑'}
                  </button>
                </td>
              </tr>
            ))}
            {templates.length === 0 && (
              <tr><td colSpan={6} className="muted">暂无模板数据（待库存/订单同步产出）</td></tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
