import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import Orders from './Orders'

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }))

vi.mock('../api/client', () => ({
  api: { get: getMock, post: vi.fn(), put: vi.fn(), del: vi.fn() },
}))

function orderPage(items: unknown[], total = items.length) {
  return { items, total }
}

const oneOrder = {
  id: 1, channel: 'uu', order_ref: 'UU-1', hash_name: 'AK-47 | Redline (FT)',
  order_type: 'short', status: 'leasing', rent_days: 7, rent_price: 1.2,
  order_amount: 8.4, deposits: 120, started_at: '2026-08-01T00:00:00Z',
  due_at: '2026-08-08T00:00:00Z', finished_at: null,
}

beforeEach(() => {
  getMock.mockReset()
})

describe('Orders page', () => {
  it('renders order rows and the total counter', async () => {
    getMock.mockResolvedValue(orderPage([oneOrder], 1))
    render(<Orders />)
    expect(await screen.findByText('UU-1')).toBeDefined()
    expect(screen.getByText('共 1 单')).toBeDefined()
    expect(screen.getByText('leasing')).toBeDefined()
  })

  it('disables paging on a single page and advances on multi-page results', async () => {
    getMock.mockResolvedValue(orderPage([oneOrder], 120))
    render(<Orders />)
    await screen.findByText('UU-1')
    const next = screen.getByRole('button', { name: '下一页' }) as HTMLButtonElement
    const prev = screen.getByRole('button', { name: '上一页' }) as HTMLButtonElement
    expect(prev.disabled).toBe(true)
    expect(next.disabled).toBe(false)
    fireEvent.click(next)
    expect(await screen.findByText('第 2 页')).toBeDefined()
    expect(prev.disabled).toBe(false)
    // page state moved: the hook re-requests with page=2
    const last = getMock.mock.calls.at(-1)?.[0] as string
    expect(last).toContain('page=2')
  })

  it('last page disables 下一页', async () => {
    getMock.mockResolvedValue(orderPage([oneOrder], 50))
    render(<Orders />)
    await screen.findByText('UU-1')
    expect((screen.getByRole('button', { name: '下一页' }) as HTMLButtonElement).disabled).toBe(true)
  })
})
