import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
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

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
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

  it('exports every matching order while a changed filter is still loading', async () => {
    let filteredCalls = 0
    const rows = (start: number, count: number) => Array.from({ length: count }, (_, i) => ({
      ...oneOrder, channel: 'eco', order_ref: `ECO-${start + i}`,
    }))
    getMock.mockImplementation((path: string) => {
      if (!path.includes('channel=eco')) return Promise.resolve(orderPage([oneOrder], 1))
      const page = Number(new URLSearchParams(path.split('?')[1]).get('page'))
      filteredCalls++
      if (filteredCalls === 1) return new Promise(() => undefined)
      return Promise.resolve(orderPage(rows((page - 1) * 50, page < 3 ? 50 : 20), 120))
    })
    const createObjectURL = vi.fn<(blob: Blob) => string>(() => 'blob:orders')
    class ExportURL extends URL {
      static createObjectURL = createObjectURL
      static revokeObjectURL = vi.fn()
    }
    vi.stubGlobal('URL', ExportURL)
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)

    render(<Orders />)
    await screen.findByText('共 1 单')
    fireEvent.change(screen.getAllByRole('combobox')[0], { target: { value: 'eco' } })
    await waitFor(() => expect(filteredCalls).toBe(1))
    fireEvent.click(screen.getByRole('button', { name: '导出 CSV' }))
    await waitFor(() => expect(createObjectURL).toHaveBeenCalledTimes(1))
    expect(getMock.mock.calls.some(([path]) => path.includes('page=3'))).toBe(true)

    const csv = await new Promise<string>((resolve) => {
      const reader = new FileReader()
      reader.onload = () => resolve(String(reader.result))
      reader.readAsText(createObjectURL.mock.calls[0][0])
    })
    expect(csv.split('\n')).toHaveLength(121)
    expect(csv).toContain('"ECO-119"')
    expect(csv).not.toContain('"UU-1"')
  })
})
