import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api, clearToken, getToken, setToken } from './client'

// ---- fetch stubbing helpers ----

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

beforeEach(() => {
  localStorage.clear()
  vi.stubGlobal('location', { ...window.location, hash: '' })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('api client 401 semantics', () => {
  it('panel-session 401 (code=unauthorized) clears the token and redirects to login', async () => {
    setToken('stale-token')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      jsonResponse(401, { code: 'unauthorized', message: 'invalid token' })))

    await expect(api.get('/dashboard')).rejects.toBeInstanceOf(ApiError)
    expect(getToken()).toBe('')
    expect(window.location.hash).toBe('#/login')
  })

  it('channel-level 401 keeps the panel session alive', async () => {
    setToken('good-token')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      jsonResponse(401, { code: 'sms_failed', message: '上游平台登录过期' })))

    await expect(api.post('/channels/uu/sms', {})).rejects.toMatchObject({ code: 'sms_failed' })
    expect(getToken()).toBe('good-token')
  })

  it('login endpoint never triggers session teardown', async () => {
    setToken('pre-existing')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      jsonResponse(401, { code: 'unauthorized', message: 'bad credentials' })))

    await expect(api.post('/auth/login', {})).rejects.toBeInstanceOf(ApiError)
    expect(getToken()).toBe('pre-existing')
  })
})

describe('api client timeout', () => {
  it('aborts hung requests and raises a timeout error', async () => {
    vi.stubGlobal('fetch', vi.fn((_url: string, init?: RequestInit) =>
      new Promise((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () =>
          reject(new DOMException('Aborted', 'AbortError')))
      })))

    vi.useFakeTimers()
    try {
      const p = api.get('/orders')
      const expectation = expect(p).rejects.toMatchObject({ code: 'timeout' })
      await vi.advanceTimersByTimeAsync(15000)
      await expectation
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('clearToken', () => {
  it('removes only our own key', () => {
    setToken('t1')
    localStorage.setItem('other', 'keep')
    clearToken()
    expect(getToken()).toBe('')
    expect(localStorage.getItem('other')).toBe('keep')
  })
})
