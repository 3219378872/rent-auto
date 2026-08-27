import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import Inventory from './Inventory'

const { getMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn() }))

vi.mock('../api/client', () => ({
  api: { get: getMock, put: putMock, post: vi.fn(), del: vi.fn() },
}))

const oneItem = {
  id: 1, channel: 'uu', asset_id: 'A1', hash_name: 'AK-47 | Redline (FT)',
  market_hash_name: 'AK-47 | Redline (Field-Tested)', template_id: 9,
  mark_price: 100, tradable: true, status: 'in_stock', cost_basis: 80,
}

beforeEach(() => {
  getMock.mockReset()
  putMock.mockReset()
  putMock.mockResolvedValue({ status: 'ok' })
})

describe('Inventory page', () => {
  it('renders rows with book yield computed from cost', async () => {
    getMock.mockResolvedValue({ items: [oneItem], total: 1 })
    render(<Inventory />)
    expect(await screen.findByText('AK-47 | Redline (Field-Tested)')).toBeDefined()
    expect(screen.getByText('25.00%')).toBeDefined()
    expect(screen.getByText('共 1 件')).toBeDefined()
  })

  it('rejects non-positive cost input and saves valid cost with reload', async () => {
    getMock.mockResolvedValue({ items: [oneItem], total: 1 })
    render(<Inventory />)
    const input = await screen.findByLabelText('录入成本价')
    // invalid: zero/negative stays disabled
    fireEvent.change(input, { target: { value: '-5' } })
    expect((screen.getByRole('button', { name: '存' }) as HTMLButtonElement).disabled).toBe(true)
    // valid input → PUT + reload
    fireEvent.change(input, { target: { value: '90.5' } })
    fireEvent.click(screen.getByRole('button', { name: '存' }))
    await vi.waitFor(() => expect(putMock).toHaveBeenCalledTimes(1))
    expect(putMock).toHaveBeenCalledWith('/inventory/uu/A1/cost', { cost: 90.5 })
    await vi.waitFor(() => expect(getMock).toHaveBeenCalledTimes(2))
  })
})
