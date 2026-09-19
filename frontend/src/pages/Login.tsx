import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api, setToken } from '../api/client'
import { useAction } from '../lib/data'
import { Feedback } from '../components/Panel'

export default function Login({ onLogin }: { onLogin: () => void }) {
  const nav = useNavigate()
  const [params] = useSearchParams()
  const [username, setUser] = useState('admin')
  const [password, setPass] = useState('')
  const action = useAction()

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    await action.run(async () => {
      const r = await api.post<{ token: string }>('/auth/login', {
        username,
        password,
      })
      setToken(r.token)
      onLogin()
      const target = params.get('returnTo') || '/'
      nav(
        /^\/(?:inventory|listings|orders|strategies|channels|audit)(?:\?[^#\\]*)?$/.test(
          target,
        )
          ? target
          : '/',
        { replace: true },
      )
    }, '')
  }

  return (
    <div style={{ display: 'grid', placeItems: 'center', minHeight: '100vh' }}>
      <form onSubmit={submit} className="section" style={{ width: 320 }}>
        <h2>rent-auto 登录</h2>
        <Feedback err={action.err} />
        <label className="field">
          用户名
          <input
            autoComplete="username"
            required
            value={username}
            onChange={(e) => setUser(e.target.value)}
            placeholder="用户名"
          />
        </label>
        <label className="field">
          密码
          <input
            autoComplete="current-password"
            required
            type="password"
            value={password}
            onChange={(e) => setPass(e.target.value)}
            placeholder="密码"
          />
        </label>
        <button disabled={action.busy} style={{ width: '100%', marginTop: 18 }}>
          {action.busy ? '登录中…' : '登录'}
        </button>
      </form>
    </div>
  )
}
