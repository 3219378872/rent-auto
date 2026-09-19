import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent, screen } from '@testing-library/react'
import { renderPage as render } from '../test-render'
import Audit from './Audit'

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }))

vi.mock('../api/client', () => ({
  api: { get: getMock, post: vi.fn(), put: vi.fn(), del: vi.fn() },
}))

const entry = {
  ts: '2026-08-24T12:00:00Z',
  actor: 'user:admin',
  action: 'strategy.update',
  channel: '',
  target: 'global',
  detail: { route: 'both' },
}

beforeEach(() => {
  getMock.mockReset()
  getMock.mockResolvedValue({ items: [entry], total: 75 })
})

afterEach(() => vi.useRealTimers())

describe('Audit page', () => {
  it('renders entries with detail JSON and page meta', async () => {
    render(<Audit />)
    expect(await screen.findByText('strategy.update')).toBeDefined()
    expect(screen.getByText('user:admin')).toBeDefined()
    expect(screen.getByText(/共 75 条/)).toBeDefined()
    expect(screen.getByText(/共 2 页/)).toBeDefined()
  })

  it('applies the action filter on input (debounced)', async () => {
    render(<Audit />)
    await screen.findByText('strategy.update')
    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('动作'), {
      target: { value: 'reprice' },
    })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300)
    })
    expect(getMock.mock.calls.at(-1)?.[0]).toContain('action=reprice')
    // filter change must reset the page back to 1
    expect(getMock.mock.calls.at(-1)?.[0]).toContain('page=1')
  })

  it('encodes the since filter as ISO timestamp', async () => {
    render(<Audit />)
    await screen.findByText('strategy.update')
    await act(async () => {
      fireEvent.change(screen.getByLabelText('起始时间'), {
        target: { value: '2026-08-01T08:00' },
      })
    })
    expect(getMock.mock.calls.at(-1)?.[0]).toContain('since=2026-08-01T')
  })
})
