import { useEffect, useState } from 'react'
import {
  createHashRouter,
  RouterProvider,
  Routes,
  Route,
  NavLink,
  Navigate,
  useNavigate,
  useLocation,
} from 'react-router-dom'
import { api, AUTH_EVENT, clearToken, getToken } from './api/client'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import Inventory from './pages/Inventory'
import Listings from './pages/Listings'
import Orders from './pages/Orders'
import Strategies from './pages/Strategies'
import Channels from './pages/Channels'
import Audit from './pages/Audit'
import PasswordDialog from './components/PasswordDialog'
import { ExecutionMode } from './components/Panel'

function Shell({ children }: { children: React.ReactNode }) {
  const nav = useNavigate()
  const [changingPassword, setChangingPassword] = useState(false)
  const [expanded, setExpanded] = useState(false)
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
      <a
        className="skip-link"
        href="#main-content"
        onClick={(e) => {
          e.preventDefault()
          document.getElementById('main-content')?.focus()
        }}
      >
        跳到页面内容
      </a>
      <aside className={expanded ? 'nav-open' : ''}>
        <div className="page-heading">
          <h1>rent-auto</h1>
          <button
            className="ghost nav-toggle"
            aria-expanded={expanded}
            aria-controls="main-nav"
            onClick={() => setExpanded((v) => !v)}
          >
            导航
          </button>
        </div>
        <nav
          id="main-nav"
          aria-label="主导航"
          onClick={() => setExpanded(false)}
        >
          <NavLink to="/">仪表盘</NavLink>
          <NavLink to="/inventory">库存状态</NavLink>
          <NavLink to="/listings">上架状态</NavLink>
          <NavLink to="/orders">租赁订单</NavLink>
          <NavLink to="/strategies">策略配置</NavLink>
          <NavLink to="/channels">渠道账号</NavLink>
          <NavLink to="/audit">审计日志</NavLink>
        </nav>
        <button className="ghost" onClick={() => setChangingPassword(true)}>
          修改密码
        </button>
        <button className="ghost" onClick={logout}>
          退出登录
        </button>
      </aside>
      <main id="main-content" tabIndex={-1}>
        <div className="global-status">
          <ExecutionMode />
        </div>
        {children}
      </main>
      {changingPassword && (
        <PasswordDialog
          onClose={() => setChangingPassword(false)}
          onChanged={() => {
            setChangingPassword(false)
            clearToken()
            nav('/login')
          }}
        />
      )}
    </div>
  )
}

function AuthRoot() {
  const location = useLocation()
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
    <Routes>
      <Route
        path="/login"
        element={<Login onLogin={() => setAuthed(true)} />}
      />
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
            <Navigate
              to={`/login?returnTo=${encodeURIComponent(location.pathname + location.search)}`}
              replace
            />
          )
        }
      />
    </Routes>
  )
}

export default function App() {
  const [router] = useState(() =>
    createHashRouter([{ path: '*', element: <AuthRoot /> }]),
  )
  return <RouterProvider router={router} />
}
