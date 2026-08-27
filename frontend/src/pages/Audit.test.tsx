import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import Audit from './Audit'

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }))

vi.mock('../api/client', () => ({
  api: { get: getMock, post: vi.fn(), put: vi.fn(), del: vi.fn() },
}))

const entry = {
  ts: '2026-08-24T12:00:00Z', actor: 'user:admin', action: 'strategy.update',
  channel: '', target: 'global', detail: { route: 'both' },
}

beforeEach(() => {
  getMock.mockReset()
  getMock.mockResolvedValue({ items: [entry], total: 75 })
})

describe('Audit page', () => {
  it('renders entries with detail JSON and page meta', async () => {
    render(<Audit />)
    expect(await screen.findByText('strategy.update')).toBeDefined()
    expect(screen.getByText('user:admin')).toBeDefined()
    expect(screen.getByText(/共 75 条 · 第 1\/2 页/)).toBeDefined()
  })

  it('applies the action filter on input (debounced)', async () => {
    render(<Audit />)
    await screen.findByText('strategy.update')
    fireEvent.change(screen.getByPlaceholderText('动作过滤，如 reprice'), { target: { value: 'reprice' } })
    await vi.waitFor(() => {
      const last = getMock.mock.calls.at(-1)?.[0] as string
      expect(last).toContain('action=reprice')
    })
    // filter change must reset the page back to 1
    expect(getMock.mock.calls.at(-1)?.[0]).toContain('page=1')
  })

  it('encodes the since filter as ISO timestamp', async () => {
    render(<Audit />)
    await screen.findByText('strategy.update')
    fireEvent.change(screen.getByLabelText('起始时间'), { target: { value: '2026-08-01T08:00' } })
    await vi.waitFor(() => {
      const last = getMock.mock.calls.at(-1)?.[0] as string
      expect(last).toContain('since=2026-08-01T')
    })
  })
})
