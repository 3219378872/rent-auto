import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import App from './App'

const state = vi.hoisted(() => ({ token: '' }))
const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }))

vi.mock('./api/client', () => ({
  AUTH_EVENT: 'ra-auth',
  getToken: () => state.token,
  setToken: (t: string) => { state.token = t; window.dispatchEvent(new Event('ra-auth')) },
  clearToken: () => { state.token = ''; window.dispatchEvent(new Event('ra-auth')) },
  api: { get: getMock, post: vi.fn(() => Promise.resolve({})), put: vi.fn(), del: vi.fn() },
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
  window.location.hash = ''
})

describe('App auth gate', () => {
  it('redirects to the login form when no token exists', () => {
    state.token = ''
    render(<App />)
    expect(screen.getByRole('heading', { name: 'rent-auto 登录' })).toBeDefined()
  })

  it('renders the shell once a token exists and reacts to the auth event', async () => {
    state.token = 't0'
    getMock.mockResolvedValue(dashboardPayload)
    render(<App />)
    expect(await screen.findByText('仪表盘', { selector: 'nav a' })).toBeDefined()
    // login elsewhere in-tab fires ra-auth → gate flips without a reload
    state.token = ''
    window.dispatchEvent(new Event('ra-auth'))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'rent-auto 登录' })).toBeDefined())
  })

  it('logout clears the token and returns to login', async () => {
    state.token = 't1'
    getMock.mockResolvedValue(dashboardPayload)
    render(<App />)
    fireEvent.click(await screen.findByRole('button', { name: '退出登录' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'rent-auto 登录' })).toBeDefined())
    expect(state.token).toBe('')
  })
})
