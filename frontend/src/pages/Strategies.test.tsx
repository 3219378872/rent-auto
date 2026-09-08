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

function mockGlobal(params: Record<string, unknown>) {
  getMock.mockImplementation((path: string) => Promise.resolve(path === '/templates' ? [] : [globalRow(params)]))
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
    mockGlobal({ topn: 20 })
    render(<Strategies />)
    await screen.findByText('default')
    expect(screen.getByRole('heading', { name: '基线定价' })).toBeDefined()
    expect(screen.getByDisplayValue(20)).toBeDefined()
    expect(screen.getByRole('heading', { name: '反馈控制器' })).toBeDefined()
    expect(screen.getByRole('heading', { name: '护栏' })).toBeDefined()
    expect(screen.getByRole('heading', { name: '租期' })).toBeDefined()
    expect((screen.getByRole('radio', { name: '双渠道' }).className)).toContain('active')
  })

  it('falls back to defaults for empty params and shows percent labels', async () => {
    mockGlobal({})
    render(<Strategies />)
    await screen.findByText('default')
    expect(screen.getByDisplayValue(15)).toBeDefined()
    expect(screen.getAllByText('97%').length).toBeGreaterThan(0)
    expect(screen.getByText('关闭')).toBeDefined()
  })

  it('saves nested params with route and execution flag', async () => {
    mockGlobal({})
    render(<Strategies />)
    await screen.findByText('default')
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
    mockGlobal({})
    render(<Strategies />)
    await screen.findByText('default')
    const fmin = screen.getByRole('slider', { name: 'factor.min 因子下限' }) as HTMLInputElement
    fireEvent.change(fmin, { target: { value: '1.2' } })
    const fmax = screen.getByRole('slider', { name: 'factor.max 因子上限' }) as HTMLInputElement
    fireEvent.change(fmax, { target: { value: '1.05' } })
    fireEvent.click(screen.getByRole('button', { name: '保存全局策略' }))
    expect(await screen.findByText('反馈因子下限不可大于上限')).toBeDefined()
    expect(putMock).not.toHaveBeenCalled()
  })

  it('reset restores default values in the form', async () => {
    mockGlobal({ topn: 42 })
    render(<Strategies />)
    await screen.findByText('default')
    expect(screen.getByDisplayValue(42)).toBeDefined()
    fireEvent.click(screen.getByRole('button', { name: '恢复默认值' }))
    expect(screen.getByDisplayValue(15)).toBeDefined()
  })

  it('preserves a zero change cap and equal factor bounds when editing unrelated fields', async () => {
    mockGlobal({ factor: { min: 1, max: 1 }, guardrails: { max_change_ratio: 0 } })
    render(<Strategies />)
    await screen.findByText('default')
    fireEvent.click(screen.getByRole('radio', { name: '仅 ECO' }))
    fireEvent.click(screen.getByRole('button', { name: '保存全局策略' }))
    await waitFor(() => expect(putMock).toHaveBeenCalledWith('/strategies/global', expect.objectContaining({
      channel_route: 'eco_only', params: expect.objectContaining({
        factor: expect.objectContaining({ min: 1, max: 1 }),
        guardrails: expect.objectContaining({ max_change_ratio: 0 }),
      }),
    })))
    await screen.findByText('策略已保存')
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
    expect(body.params).toEqual({})
    expect(body.priority).toBe(0)
  })

  const sparseTemplate = {
    id: 7, name: 'tpl:T', scope: 'template', channel_route: 'eco_only',
    params: { baseline: { k1: 0.9 } }, real_execution_enabled: true,
    priority: 5, updated_at: '2026-09-08T00:00:00Z',
  }

  function mockSparseTemplate(real = true) {
    getMock.mockImplementation((path: string) => Promise.resolve(path === '/templates'
      ? [{ hash_name: 'T', display_name: 'T', blacklisted: false }]
      : [
        { ...globalRow({ guardrails: { min_rent: 20, cooldown_minutes: 120 }, eco_max_days: 60 }), real_execution_enabled: true },
        { ...sparseTemplate, real_execution_enabled: real },
      ]))
  }

  it('preserves sparse inheritance and priority when only the route changes', async () => {
    mockSparseTemplate()
    render(<Strategies />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    expect(screen.getAllByLabelText('min_rent 租金下限数值')[1]).toHaveValue(20)
    expect(screen.getAllByLabelText('cooldown_minutes 改价冷却数值')[1]).toHaveValue(120)
    expect(screen.getAllByLabelText('eco_max_days ECO 最长租期数值')[1]).toHaveValue(60)
    fireEvent.click(within(screen.getByRole('radiogroup', { name: '模板渠道路由' }))
      .getByRole('radio', { name: '仅 UU' }))
    fireEvent.click(screen.getByRole('button', { name: '保存模板策略' }))
    await waitFor(() => expect(postMock).toHaveBeenCalledWith('/strategies/template', {
      hash_name: 'T', channel_route: 'uu_only', params: sparseTemplate.params,
      real_execution_enabled: true, priority: 5,
    }))
    await screen.findByText('模板策略已保存：T')
  })

  it('writes only changed parameter overrides while retaining untouched fields', async () => {
    mockSparseTemplate()
    render(<Strategies />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    fireEvent.change(screen.getAllByLabelText('cooldown_minutes 改价冷却数值')[1], { target: { value: '180' } })
    fireEvent.change(screen.getByLabelText('优先级'), { target: { value: '8' } })
    fireEvent.click(screen.getByRole('button', { name: '保存模板策略' }))
    await waitFor(() => expect(postMock).toHaveBeenCalledWith('/strategies/template', expect.objectContaining({
      params: { baseline: { k1: 0.9 }, guardrails: { cooldown_minutes: 180 } }, priority: 8,
    })))
    await screen.findByText('模板策略已保存：T')
  })

  it.each([false, true])('allows an existing template real flag to change from %s', async (real) => {
    mockSparseTemplate(real)
    render(<Strategies />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    fireEvent.click(screen.getByRole('checkbox', { name: '模板真实执行' }))
    fireEvent.click(screen.getByRole('button', { name: '保存模板策略' }))
    await waitFor(() => expect(postMock).toHaveBeenCalledWith('/strategies/template', expect.objectContaining({
      real_execution_enabled: !real, params: sparseTemplate.params, priority: 5,
    })))
    await screen.findByText('模板策略已保存：T')
  })

  it('can remove all parameter overrides without changing route, priority, or execution', async () => {
    mockSparseTemplate()
    render(<Strategies />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    fireEvent.click(screen.getByRole('button', { name: '清除参数覆盖' }))
    expect(screen.getAllByLabelText('k1 短租基线系数数值')[1]).toHaveValue(0.97)
    fireEvent.click(screen.getByRole('button', { name: '保存模板策略' }))
    await waitFor(() => expect(postMock).toHaveBeenCalledWith('/strategies/template', {
      hash_name: 'T', channel_route: 'eco_only', params: {}, priority: 5, real_execution_enabled: true,
    }))
    await screen.findByText('模板策略已保存：T')
  })
})
