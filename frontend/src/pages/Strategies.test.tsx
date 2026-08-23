import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import Strategies from './Strategies'

const { getMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn() }))

vi.mock('../api/client', () => ({
  api: { get: getMock, put: putMock },
}))

function globalRow(params: Record<string, unknown>) {
  return {
    id: 1, name: 'default', scope: 'global', channel_route: 'both',
    params, real_execution_enabled: false, priority: 0,
    updated_at: '2026-08-23T00:00:00Z',
  }
}

beforeEach(() => {
  getMock.mockReset()
  putMock.mockReset()
  putMock.mockResolvedValue({ status: 'ok' })
})

describe('Strategies page', () => {
  it('renders form sections and normalizes legacy flat params', async () => {
    getMock.mockResolvedValue([globalRow({ topn: 20 })])
    render(<Strategies />)
    expect(await screen.findByRole('heading', { name: '基线定价' })).toBeDefined()
    expect(screen.getByDisplayValue(20)).toBeDefined()
    expect(screen.getByRole('heading', { name: '反馈控制器' })).toBeDefined()
    expect(screen.getByRole('heading', { name: '护栏' })).toBeDefined()
    expect(screen.getByRole('heading', { name: '租期' })).toBeDefined()
    expect((screen.getByRole('radio', { name: '双渠道' }).className)).toContain('active')
  })

  it('falls back to defaults for empty params and shows percent labels', async () => {
    getMock.mockResolvedValue([globalRow({})])
    render(<Strategies />)
    await screen.findByRole('heading', { name: '基线定价' })
    expect(screen.getByDisplayValue(15)).toBeDefined()
    expect(screen.getAllByText('97%').length).toBeGreaterThan(0)
    expect(screen.getByText('关闭')).toBeDefined()
  })

  it('saves nested params with route and execution flag', async () => {
    getMock.mockResolvedValue([globalRow({})])
    render(<Strategies />)
    await screen.findByRole('heading', { name: '基线定价' })
    fireEvent.click(screen.getByRole('radio', { name: '仅 UU' }))
    const k1 = screen.getByRole('slider', { name: 'k1 短租基线系数' }) as HTMLInputElement
    fireEvent.change(k1, { target: { value: '1.02' } })
    fireEvent.click(screen.getByRole('button', { name: '保存全局策略' }))
    await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1))
    expect(putMock).toHaveBeenCalledWith('/strategies/global', {
      params: expect.objectContaining({
        baseline: expect.objectContaining({ topn: 15, k1: 1.02 }),
        factor: expect.objectContaining({ min: 0.85, max: 1.25 }),
        guardrails: expect.objectContaining({ cooldown_minutes: 30 }),
        uu_max_days: 60,
        eco_max_days: 30,
      }),
      channel_route: 'uu_only',
      real_execution_enabled: false,
    })
  })

  it('blocks save when factor bounds are inverted', async () => {
    getMock.mockResolvedValue([globalRow({})])
    render(<Strategies />)
    await screen.findByRole('heading', { name: '基线定价' })
    const fmin = screen.getByRole('slider', { name: 'factor.min 因子下限' }) as HTMLInputElement
    fireEvent.change(fmin, { target: { value: '1.2' } })
    const fmax = screen.getByRole('slider', { name: 'factor.max 因子上限' }) as HTMLInputElement
    fireEvent.change(fmax, { target: { value: '1.05' } })
    fireEvent.click(screen.getByRole('button', { name: '保存全局策略' }))
    expect(await screen.findByText('反馈因子下限必须小于上限')).toBeDefined()
    expect(putMock).not.toHaveBeenCalled()
  })

  it('reset restores default values in the form', async () => {
    getMock.mockResolvedValue([globalRow({ topn: 42 })])
    render(<Strategies />)
    await screen.findByRole('heading', { name: '基线定价' })
    expect(screen.getByDisplayValue(42)).toBeDefined()
    fireEvent.click(screen.getByRole('button', { name: '恢复默认值' }))
    expect(screen.getByDisplayValue(15)).toBeDefined()
  })
})
