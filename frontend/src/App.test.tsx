import { describe, expect, it, vi, beforeEach } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import App from './App'

const state = vi.hoisted(() => ({ token: '' }))
const { getMock, postMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn(), putMock: vi.fn() }))

vi.mock('./api/client', () => ({
  AUTH_EVENT: 'ra-auth',
  getToken: () => state.token,
  setToken: (t: string) => { state.token = t; window.dispatchEvent(new Event('ra-auth')) },
  clearToken: () => { state.token = ''; window.dispatchEvent(new Event('ra-auth')) },
  api: { get: getMock, post: postMock, put: putMock, del: vi.fn() },
}))

const dashboardPayload = {
  assets: { total: 0, inventory: 0, deposits: {}, wallets: {} },
  income: { total: 0, today: 0, by_channel: [] },
  leased_out: 0, annualized_roi: 0, categories: [], series_30d: [],
}

beforeEach(() => {
  // jsdom shares the URL hash across tests in a file; a leftover #/login from
  // a previous render makes the outer router pick the /login route (which
  // precedes /*) and mask the auth gate under test.
  window.history.replaceState(null, '', '/#/')
  getMock.mockReset()
  postMock.mockReset()
  putMock.mockReset()
  postMock.mockResolvedValue({})
  putMock.mockResolvedValue({ ok: true })
  getMock.mockImplementation((path: string) => {
    if (path === '/dashboard') return Promise.resolve(dashboardPayload)
    if (path === '/channels') return Promise.resolve({ uu: 'ok', eco: 'ok', steam: 'ok:76561198000000000' })
    if (path === '/strategies' || path === '/templates') return Promise.resolve([])
    return Promise.resolve({ items: [], total: 0 })
  })
})

describe('App auth gate', () => {
  it('redirects to the login form when no token exists', () => {
    state.token = ''
    render(<App />)
    expect(screen.getByRole('heading', { name: 'rent-auto 登录' })).toBeDefined()
  })

  it('renders the shell once a token exists and reacts to the auth event', async () => {
    state.token = 't0'
    render(<App />)
    expect(await screen.findByRole('heading', { name: '仪表盘' })).toBeDefined()
    // login elsewhere in-tab fires ra-auth → gate flips without a reload
    await act(async () => {
      state.token = ''
      window.dispatchEvent(new Event('ra-auth'))
    })
    await waitFor(() => expect(screen.getByRole('heading', { name: 'rent-auto 登录' })).toBeDefined())
  })

  it('logout clears the token and returns to login', async () => {
    state.token = 't1'
    render(<App />)
    await screen.findByRole('heading', { name: '仪表盘' })
    fireEvent.click(screen.getByRole('button', { name: '退出登录' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'rent-auto 登录' })).toBeDefined())
    expect(state.token).toBe('')
  })

  it('keeps every existing page reachable through hash navigation', async () => {
    state.token = 't1'
    render(<App />)
    await screen.findByRole('heading', { name: '仪表盘' })
    for (const [label, heading, hash] of [
      ['库存状态', '库存状态', '/inventory'],
      ['上架状态', '上架状态（双渠道）', '/listings'],
      ['租赁订单', '租赁订单', '/orders'],
      ['策略配置', '上架 / 改价策略', '/strategies'],
      ['渠道账号', '渠道账号', '/channels'],
      ['审计日志', '审计日志', '/audit'],
      ['仪表盘', '仪表盘', '/'],
    ]) {
      await act(async () => { fireEvent.click(screen.getByRole('link', { name: label })) })
      expect(await screen.findByRole('heading', { name: heading })).toBeDefined()
      expect(window.location.hash).toBe(`#${hash}`)
    }
  })

  it('logs in and navigates back to the dashboard', async () => {
    state.token = ''
    postMock.mockResolvedValue({ token: 'fresh-token' })
    render(<App />)
    fireEvent.change(screen.getByPlaceholderText('密码'), { target: { value: 'test-password' } })
    fireEvent.click(screen.getByRole('button', { name: '登录' }))
    expect(await screen.findByRole('heading', { name: '仪表盘' })).toBeDefined()
    expect(state.token).toBe('fresh-token')
  })

  it('changes the password and clears the revoked session', async () => {
    state.token = 'old-token'
    render(<App />)
    await screen.findByRole('heading', { name: '仪表盘' })
    fireEvent.click(screen.getByRole('button', { name: '修改密码' }))
    expect(screen.getByRole('dialog', { name: '修改密码' })).toBeDefined()
    fireEvent.change(screen.getByLabelText('当前密码'), { target: { value: 'current-password' } })
    fireEvent.change(screen.getByLabelText('新密码'), { target: { value: 'new-password-123' } })
    fireEvent.change(screen.getByLabelText('确认新密码'), { target: { value: 'new-password-123' } })
    fireEvent.click(screen.getByRole('button', { name: '保存新密码' }))
    await screen.findByRole('heading', { name: 'rent-auto 登录' })
    expect(putMock).toHaveBeenCalledWith('/auth/password', {
      current_password: 'current-password', new_password: 'new-password-123',
    })
    expect(state.token).toBe('')
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('shows password-management errors without clearing the current session', async () => {
    state.token = 'existing-token'
    putMock.mockRejectedValue(new Error('password_managed_externally'))
    render(<App />)
    await screen.findByRole('heading', { name: '仪表盘' })
    fireEvent.click(screen.getByRole('button', { name: '修改密码' }))
    fireEvent.change(screen.getByLabelText('当前密码'), { target: { value: 'current-password' } })
    fireEvent.change(screen.getByLabelText('新密码'), { target: { value: 'new-password-123' } })
    fireEvent.change(screen.getByLabelText('确认新密码'), { target: { value: 'new-password-123' } })
    fireEvent.click(screen.getByRole('button', { name: '保存新密码' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('password_managed_externally')
    expect(state.token).toBe('existing-token')
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('rejects mismatched or oversized multibyte passwords before requesting a change', async () => {
    state.token = 'existing-token'
    render(<App />)
    await screen.findByRole('heading', { name: '仪表盘' })
    fireEvent.click(screen.getByRole('button', { name: '修改密码' }))
    fireEvent.change(screen.getByLabelText('当前密码'), { target: { value: 'current-password' } })
    fireEvent.change(screen.getByLabelText('新密码'), { target: { value: 'new-password-123' } })
    fireEvent.change(screen.getByLabelText('确认新密码'), { target: { value: 'different-password' } })
    fireEvent.click(screen.getByRole('button', { name: '保存新密码' }))
    expect(screen.getByRole('alert')).toHaveTextContent('两次输入的新密码不一致')
    fireEvent.change(screen.getByLabelText('新密码'), { target: { value: '密'.repeat(25) } })
    fireEvent.click(screen.getByRole('button', { name: '保存新密码' }))
    expect(screen.getByRole('alert')).toHaveTextContent('12–72 字节')
    expect(putMock).not.toHaveBeenCalled()
  })
})
