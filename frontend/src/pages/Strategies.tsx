import { useCallback, useEffect, useState } from 'react'
import { api, StrategyRow } from '../api/client'

export default function Strategies() {
  const [list, setList] = useState<StrategyRow[]>([])
  const [globalRow, setGlobalRow] = useState<StrategyRow | null>(null)
  const [paramsText, setParamsText] = useState('')
  const [route, setRoute] = useState('both')
  const [real, setReal] = useState(false)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const load = useCallback(() => {
    api.get<StrategyRow[]>('/strategies').then((rows) => {
      setList(rows)
      const g = rows.find((r) => r.scope === 'global')
      if (g) {
        setGlobalRow(g)
        setParamsText(JSON.stringify(g.params, null, 2))
        setRoute(g.channel_route)
        setReal(g.real_execution_enabled)
      }
    }).catch((e) => setErr(e.message))
  }, [])

  useEffect(load, [load])

  const save = async () => {
    setErr(''); setMsg('')
    let params: Record<string, unknown>
    try {
      params = paramsText.trim() ? JSON.parse(paramsText) : {}
    } catch {
      setErr('参数不是合法 JSON')
      return
    }
    try {
      await api.put('/strategies/global', { params, channel_route: route, real_execution_enabled: real })
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
        <div className="row" style={{ marginBottom: 12 }}>
          <label>渠道路由</label>
          <select value={route} onChange={(e) => setRoute(e.target.value)}>
            <option value="both">双渠道 (both)</option>
            <option value="uu_only">仅 UU</option>
            <option value="eco_only">仅 ECO</option>
            <option value="uu_primary_eco_fallback">UU 主渠道，失效切 ECO</option>
          </select>
          <label style={{ marginLeft: 16 }}>
            <input type="checkbox" checked={real} onChange={(e) => setReal(e.target.checked)} />
            {' '}允许真实执行（关闭 = 永远 dry-run）
          </label>
        </div>
        <div className="muted" style={{ marginBottom: 6 }}>
          参数结构见 docs/knowledge/spec/pricing-spec.md §5；模板级覆盖策略后续在
          「模板覆盖」中配置（当前版本仅全局层）
        </div>
        <textarea
          rows={18}
          style={{ width: '100%', fontFamily: 'monospace', fontSize: 12.5 }}
          value={paramsText}
          onChange={(e) => setParamsText(e.target.value)}
        />
        <div className="toolbar" style={{ marginTop: 10 }}>
          <button onClick={save}>保存全局策略</button>
          <span className="muted">
            最近更新：{globalRow ? new Date(globalRow.updated_at).toLocaleString('zh-CN') : '—'}
          </span>
        </div>
      </div>

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
    </div>
  )
}
