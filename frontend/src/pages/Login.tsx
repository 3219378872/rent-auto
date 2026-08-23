import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, setToken } from '../api/client'

export default function Login({ onLogin }: { onLogin: () => void }) {
  const nav = useNavigate()
  const [username, setUser] = useState('admin')
  const [password, setPass] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setErr('')
    try {
      const r = await api.post<{ token: string }>('/auth/login', { username, password })
      setToken(r.token)
      onLogin()
      nav('/')
    } catch (ex) {
      setErr(ex instanceof Error ? ex.message : String(ex))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={{ display: 'grid', placeItems: 'center', minHeight: '100vh' }}>
      <form onSubmit={submit} className="section" style={{ width: 320 }}>
        <h2>rent-auto 登录</h2>
        {err && <div className="error">{err}</div>}
        <div className="row" style={{ marginBottom: 10 }}>
          <input className="grow" value={username} onChange={(e) => setUser(e.target.value)} placeholder="用户名" />
        </div>
        <div className="row" style={{ marginBottom: 14 }}>
          <input className="grow" type="password" value={password} onChange={(e) => setPass(e.target.value)} placeholder="密码" />
        </div>
        <button disabled={busy} style={{ width: '100%' }}>{busy ? '登录中…' : '登录'}</button>
      </form>
    </div>
  )
}
