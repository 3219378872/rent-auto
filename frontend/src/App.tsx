import { useEffect, useState } from 'react'
import { HashRouter, Routes, Route, NavLink, Navigate, useNavigate } from 'react-router-dom'
import { api, AUTH_EVENT, clearToken, getToken } from './api/client'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import Inventory from './pages/Inventory'
import Listings from './pages/Listings'
import Orders from './pages/Orders'
import Strategies from './pages/Strategies'
import Channels from './pages/Channels'
import Audit from './pages/Audit'

function Shell({ children }: { children: React.ReactNode }) {
  const nav = useNavigate()
  const logout = () => {
    // Revoke server-side first (epoch bump kills every token), then discard
    // the local copy; navigation happens either way.
    api
      .post('/auth/logout', {})
      .catch(() => undefined)
      .finally(() => {
        clearToken()
        nav('/login')
      })
  }
  return (
    <div className="shell">
      <aside>
        <h1>rent-auto</h1>
        <nav>
          <NavLink to="/">仪表盘</NavLink>
          <NavLink to="/inventory">库存状态</NavLink>
          <NavLink to="/listings">上架状态</NavLink>
          <NavLink to="/orders">租赁订单</NavLink>
          <NavLink to="/strategies">策略配置</NavLink>
          <NavLink to="/channels">渠道账号</NavLink>
          <NavLink to="/audit">审计日志</NavLink>
        </nav>
        <button className="ghost" onClick={logout}>退出登录</button>
      </aside>
      <main>{children}</main>
    </div>
  )
}

export default function App() {
  const [authed, setAuthed] = useState(!!getToken())
  useEffect(() => {
    // Event-driven auth gate: token changes fire in-tab (custom event from
    // setToken/clearToken) or cross-tab (storage). No polling.
    const sync = () => setAuthed(!!getToken())
    window.addEventListener(AUTH_EVENT, sync)
    window.addEventListener('storage', sync)
    return () => {
      window.removeEventListener(AUTH_EVENT, sync)
      window.removeEventListener('storage', sync)
    }
  }, [])
  return (
    <HashRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <Routes>
        <Route path="/login" element={<Login onLogin={() => setAuthed(true)} />} />
        <Route
          path="/*"
          element={
            authed ? (
              <Shell>
                <Routes>
                  <Route path="/" element={<Dashboard />} />
                  <Route path="/inventory" element={<Inventory />} />
                  <Route path="/listings" element={<Listings />} />
                  <Route path="/orders" element={<Orders />} />
                  <Route path="/strategies" element={<Strategies />} />
                  <Route path="/channels" element={<Channels />} />
                  <Route path="/audit" element={<Audit />} />
                  <Route path="*" element={<Navigate to="/" replace />} />
                </Routes>
              </Shell>
            ) : (
              <Navigate to="/login" replace />
            )
          }
        />
      </Routes>
    </HashRouter>
  )
}
