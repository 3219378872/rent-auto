import { afterEach, expect, it, vi } from 'vitest'
import { AUTH_EVENT, ApiError, api, getToken, setToken } from './client'

// CQ-03: a response from an earlier session cannot revoke a new one.
afterEach(() => {
  localStorage.clear()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

it.each([
  { name: 'same-tab login', original: 'old-token', crossTab: false },
  { name: 'cross-tab login', original: 'old-token', crossTab: true },
  { name: 'login during an anonymous request', original: '', crossTab: false },
])('preserves a new session after $name', async ({ original, crossTab }) => {
  localStorage.clear()
  window.location.hash = '#/dashboard'
  if (original) setToken(original)
  let finish!: (response: Response) => void
  const fetchMock = vi.fn(() => new Promise<Response>((resolve) => { finish = resolve }))
  vi.stubGlobal('fetch', fetchMock)
  const pending = expect(api.get('/dashboard')).rejects.toBeInstanceOf(ApiError)
  expect(fetchMock).toHaveBeenCalledWith('/api/v1/dashboard', expect.objectContaining({
    headers: original ? { Authorization: `Bearer ${original}` } : {},
  }))
  // A storage write by another tab does not call this tab's setToken helper.
  if (crossTab) localStorage.setItem('ra_token', 'new-token')
  else setToken('new-token')
  const dispatch = vi.spyOn(window, 'dispatchEvent')
  finish(new Response(JSON.stringify({ code: 'unauthorized' }), {
    status: 401, headers: { 'Content-Type': 'application/json' },
  }))
  await pending
  expect(getToken()).toBe('new-token')
  expect(window.location.hash).toBe('#/dashboard')
  expect(dispatch.mock.calls.some(([event]) => event.type === AUTH_EVENT)).toBe(false)
})

it('checks the current token after parsing a delayed error body', async () => {
  setToken('old-token')
  window.location.hash = '#/orders'
  let bodyReady!: (body: unknown) => void
  const body = new Promise((resolve) => { bodyReady = resolve })
  let parsing!: () => void
  const startedParsing = new Promise<void>((resolve) => { parsing = resolve })
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
    status: 401,
    ok: false,
    clone: () => ({ json: () => { parsing(); return body } }),
    json: async () => ({ code: 'unauthorized' }),
  }))
  const pending = expect(api.get('/orders')).rejects.toBeInstanceOf(ApiError)
  await startedParsing
  setToken('new-token')
  bodyReady({ code: 'unauthorized' })
  await pending
  expect(getToken()).toBe('new-token')
  expect(window.location.hash).toBe('#/orders')
})
