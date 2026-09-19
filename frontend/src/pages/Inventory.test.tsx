import { describe, expect, it, vi, beforeEach } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { renderPage as render } from '../test-render'
import Inventory from './Inventory'

const { getMock, putMock } = vi.hoisted(() => ({
  getMock: vi.fn(),
  putMock: vi.fn(),
}))

vi.mock('../api/client', () => ({
  api: { get: getMock, put: putMock, post: vi.fn(), del: vi.fn() },
}))

const oneItem = {
  id: 1,
  channel: 'uu',
  asset_id: 'A1',
  hash_name: 'AK-47 | Redline (FT)',
  market_hash_name: 'AK-47 | Redline (Field-Tested)',
  template_id: 9,
  mark_price: 100,
  tradable: true,
  status: 'in_stock',
  cost_basis: 80,
}

beforeEach(() => {
  getMock.mockReset()
  putMock.mockReset()
  putMock.mockResolvedValue({ status: 'ok' })
  getMock.mockImplementation((path: string) =>
    Promise.resolve(
      path === '/inventory/categories'
        ? ['rifle']
        : { items: [oneItem], total: 1 },
    ),
  )
})

describe('Inventory page', () => {
  it('labels and filters inventory missing from a complete snapshot without marking it sold', async () => {
    getMock.mockImplementation((path: string) =>
      Promise.resolve(
        path === '/inventory/categories'
          ? []
          : { items: [{ ...oneItem, status: 'missing' }], total: 1 },
      ),
    )
    render(<Inventory />)
    expect(
      await screen.findByText('同步未见', { selector: 'span.badge' }),
    ).toBeDefined()
    await act(async () => {
      fireEvent.change(screen.getAllByRole('combobox')[1], {
        target: { value: 'missing' },
      })
    })
    expect(getMock.mock.calls.at(-1)?.[0]).toContain('status=missing')
  })

  it('renders rows with book yield computed from cost', async () => {
    render(<Inventory />)
    expect(
      await screen.findByText('AK-47 | Redline (Field-Tested)'),
    ).toBeDefined()
    expect(screen.getByText('25.00%')).toBeDefined()
    expect(screen.getByText(/共 1 件/)).toBeDefined()
  })

  it('rejects non-positive cost input and saves valid cost with reload', async () => {
    render(<Inventory />)
    fireEvent.click(await screen.findByRole('button', { name: '修改成本' }))
    const input = screen.getByLabelText('录入成本价')
    // Invalid amounts are rejected by the form before any request.
    fireEvent.change(input, { target: { value: '-5' } })
    fireEvent.click(screen.getByRole('button', { name: '保存成本' }))
    expect(putMock).not.toHaveBeenCalled()
    // valid input → PUT + reload
    fireEvent.change(input, { target: { value: '90.5' } })
    fireEvent.click(screen.getByRole('button', { name: '保存成本' }))
    await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1))
    expect(putMock).toHaveBeenCalledWith('/inventory/uu/A1/cost', {
      cost: 90.5,
    })
    await screen.findByText('成本已保存')
    await waitFor(() =>
      expect(
        getMock.mock.calls.filter(([path]) => path.startsWith('/inventory?')),
      ).toHaveLength(2),
    )
  })
})
