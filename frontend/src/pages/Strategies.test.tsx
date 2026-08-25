import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import Strategies from './Strategies'

const { getMock, putMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn(), postMock: vi.fn() }))

vi.mock('../api/client', () => ({
  api: { get: getMock, put: putMock, post: postMock, del: vi.fn() },
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
  postMock.mockReset()
  putMock.mockResolvedValue({ status: 'ok' })
  postMock.mockResolvedValue({ id: 99 })
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

// 模板级覆盖策略（US-STRAT-02）：列表渲染 + 新建表单 + 保存载荷。
describe('template strategy editor', () => {
  it('lists template rows and opens the create form with template picker', async () => {
    getMock.mockImplementation((path: string) => {
      if (path === '/templates') {
        return Promise.resolve([
          { hash_name: 'AK-47 | Redline (FT)', display_name: 'AK 红线', category: 'rifle', blacklisted: false },
          { hash_name: 'Ghost', display_name: 'Ghost', category: 'knife', blacklisted: true },
        ])
      }
      return Promise.resolve([
        globalRow({}),
        {
          id: 7, name: 'tpl:AK-47 | Redline (FT)', scope: 'template',
          channel_route: 'eco_only', params: { baseline: { k1: 0.9 } },
          real_execution_enabled: true, priority: 0, updated_at: '2026-08-24T00:00:00Z',
        },
      ])
    })
    render(<Strategies />)
    expect(await screen.findByText('AK-47 | Redline (FT)')).toBeDefined()
    fireEvent.click(screen.getByRole('button', { name: '新建模板策略' }))
    // 黑名单模板不可选
    const options = screen.getAllByRole('option').map((o) => o.textContent)
    if (options.includes('Ghost')) throw new Error('blacklisted template must not be selectable')
    fireEvent.click(within(screen.getByRole('radiogroup', { name: '模板渠道路由' })).getByRole('radio', { name: '仅 ECO' }))
    fireEvent.click(screen.getByRole('button', { name: '保存模板策略' }))
    await waitFor(() => expect(postMock).toHaveBeenCalled())
    const [path, body] = postMock.mock.calls[0]
    if (path !== '/strategies/template') throw new Error(`post path ${path}`)
    if (body.channel_route !== 'eco_only') throw new Error('route payload')
    if (body.real_execution_enabled !== false) throw new Error('new row must default dry-run flag explicitly')
    if ((body.params as { baseline?: { k1?: number } }).baseline?.k1 === undefined) {
      throw new Error('params payload should carry grouped structure')
    }
  })
})
