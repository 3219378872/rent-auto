import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { renderPage } from '../test-render'
import type { StrategyRow } from '../api/client'
import Strategies from './Strategies'

const { getMock, putMock, postMock } = vi.hoisted(() => ({
  getMock: vi.fn(),
  putMock: vi.fn(),
  postMock: vi.fn(),
}))
vi.mock('../api/client', () => ({
  api: { get: getMock, put: putMock, post: postMock, del: vi.fn() },
}))
const makeGlobal = (params: Record<string, unknown> = {}): StrategyRow => ({
  id: 1,
  name: 'default',
  scope: 'global',
  channel_route: 'both',
  params,
  real_execution_enabled: false,
  priority: 0,
  updated_at: '2026-09-19T00:00:00Z',
})
let rows: StrategyRow[]
beforeEach(() => {
  rows = [makeGlobal()]
  getMock.mockReset()
  putMock.mockReset()
  postMock.mockReset()
  getMock.mockImplementation((path: string) =>
    Promise.resolve(
      path === '/strategies'
        ? structuredClone(rows)
        : path.startsWith('/templates/catalog')
          ? {
              items: [
                { hash_name: 'T', display_name: '模板 T', blacklisted: false },
              ],
              total: 1,
            }
          : { dry_run: true, reasons: ['environment_dry_run'] },
    ),
  )
  putMock.mockImplementation((_path, body) => {
    rows[0] = {
      ...rows[0],
      params: body.params,
      channel_route: body.channel_route,
      real_execution_enabled: body.real_execution_enabled,
    }
    return Promise.resolve({ status: 'ok' })
  })
  postMock.mockImplementation((_path, body) => {
    rows = [
      rows[0],
      {
        id: 7,
        name: `tpl:${body.hash_name}`,
        scope: 'template',
        updated_at: '',
        ...body,
      },
    ]
    return Promise.resolve({ id: 7 })
  })
  vi.spyOn(window, 'confirm').mockReturnValue(true)
})

describe('global strategy drafts', () => {
  it('normalizes legacy flat fields and renders native route controls', async () => {
    rows = [makeGlobal({ topn: 20 })]
    renderPage(<Strategies />)
    expect(await screen.findByLabelText('topn 行情取样条数数值')).toHaveValue(
      20,
    )
    for (const name of ['基线定价', '反馈控制器', '护栏', '租期'])
      expect(screen.getByRole('heading', { name })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: '双渠道' })).toBeChecked()
    expect(screen.getByText('关闭')).toBeInTheDocument()
  })
  it('saves nested values and reads back the saved form', async () => {
    renderPage(<Strategies />)
    await screen.findByLabelText('k1 短租基线系数数值')
    fireEvent.click(screen.getByRole('radio', { name: '仅 UU' }))
    fireEvent.change(screen.getByRole('slider', { name: 'k1 短租基线系数' }), {
      target: { value: '1.02' },
    })
    fireEvent.click(screen.getByRole('button', { name: '保存全局策略' }))
    await screen.findByText('策略已保存并回读确认')
    expect(putMock).toHaveBeenCalledWith(
      '/strategies/global',
      expect.objectContaining({
        channel_route: 'uu_only',
        real_execution_enabled: false,
        params: expect.objectContaining({
          baseline: expect.objectContaining({ k1: 1.02, topn: 15 }),
        }),
      }),
    )
    expect(screen.getByRole('button', { name: '保存全局策略' })).toBeDisabled()
  })
  it('allows incomplete numeric text without changing it and blocks invalid submit', async () => {
    renderPage(<Strategies />)
    const input = await screen.findByLabelText('k1 短租基线系数数值')
    fireEvent.input(input, { target: { value: '' } })
    fireEvent.blur(input)
    expect(input).toHaveValue(null)
    expect(await screen.findByRole('alert')).toHaveTextContent('请输入')
    fireEvent.click(screen.getByRole('button', { name: '保存全局策略' }))
    expect(putMock).not.toHaveBeenCalled()
    fireEvent.input(input, { target: { value: '0.95' } })
    fireEvent.click(screen.getByRole('button', { name: '保存全局策略' }))
    await screen.findByText('策略已保存并回读确认')
    expect(rows[0].params.baseline).toMatchObject({ k1: 0.95 })
  })
  it('blocks inverted factor bounds and restores defaults', async () => {
    rows = [makeGlobal({ topn: 42 })]
    renderPage(<Strategies />)
    await screen.findByLabelText('topn 行情取样条数数值')
    fireEvent.change(
      screen.getByRole('slider', { name: 'factor.min 因子下限' }),
      { target: { value: '1.2' } },
    )
    fireEvent.change(
      screen.getByRole('slider', { name: 'factor.max 因子上限' }),
      { target: { value: '1.05' } },
    )
    fireEvent.click(screen.getByRole('button', { name: '保存全局策略' }))
    await screen.findByText('反馈因子下限不可大于上限')
    expect(putMock).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: '恢复默认值' }))
    expect(screen.getByLabelText('topn 行情取样条数数值')).toHaveValue(15)
  })
  it('preserves zero change cap and equal bounds', async () => {
    rows = [
      makeGlobal({
        factor: { min: 1, max: 1 },
        guardrails: { max_change_ratio: 0 },
      }),
    ]
    renderPage(<Strategies />)
    await screen.findByLabelText('topn 行情取样条数数值')
    fireEvent.click(screen.getByRole('radio', { name: '仅 ECO' }))
    fireEvent.click(screen.getByRole('button', { name: '保存全局策略' }))
    await screen.findByText('策略已保存并回读确认')
    expect(rows[0].params).toMatchObject({
      factor: { min: 1, max: 1 },
      guardrails: { max_change_ratio: 0 },
    })
  })
  it('has no editable defaults before load and retries a failed load', async () => {
    getMock.mockRejectedValueOnce(new Error('网络连接失败'))
    renderPage(<Strategies />)
    expect(screen.queryByRole('button', { name: '保存全局策略' })).toBeNull()
    fireEvent.click(await screen.findByRole('button', { name: '重试' }))
    expect(await screen.findByLabelText('k1 短租基线系数数值')).toHaveValue(
      0.97,
    )
  })
  it('prevents duplicate saves and retains the draft after failure', async () => {
    let reject!: (error: Error) => void
    putMock.mockImplementation(
      () =>
        new Promise((_resolve, no) => {
          reject = no
        }),
    )
    renderPage(<Strategies />)
    await screen.findByLabelText('k1 短租基线系数数值')
    fireEvent.click(screen.getByRole('radio', { name: '仅 ECO' }))
    const button = screen.getByRole('button', { name: '保存全局策略' })
    fireEvent.click(button)
    fireEvent.click(button)
    expect(putMock).toHaveBeenCalledTimes(1)
    reject(new Error('保存失败'))
    await screen.findByText('保存失败')
    expect(screen.getByRole('radio', { name: '仅 ECO' })).toBeChecked()
    expect(
      screen.getByRole('button', { name: '保存全局策略' }),
    ).not.toBeDisabled()
  })
  it('does not wipe an unsaved global draft when another tab refreshes', async () => {
    renderPage(<Strategies />)
    const input = await screen.findByLabelText('k1 短租基线系数数值')
    fireEvent.input(input, { target: { value: '1.02' } })
    fireEvent.click(screen.getByRole('button', { name: '模板黑名单' }))
    fireEvent.click(screen.getByRole('button', { name: '加入黑名单' }))
    await screen.findByText(/已拉黑：模板 T/)
    fireEvent.click(screen.getByRole('button', { name: /全局策略 · 未保存/ }))
    expect(input).toHaveValue(1.02)
  })
  it('refreshes clean saved fields but protects edits until explicitly discarded', async () => {
    renderPage(<Strategies />)
    await screen.findByLabelText('k1 短租基线系数数值')
    rows[0] = makeGlobal({ baseline: { k1: 1.01 } })
    fireEvent.click(screen.getByRole('button', { name: '刷新已保存策略' }))
    await waitFor(() =>
      expect(screen.getByLabelText('k1 短租基线系数数值')).toHaveValue(1.01),
    )
    fireEvent.input(screen.getByLabelText('k1 短租基线系数数值'), {
      target: { value: '0.95' },
    })
    rows[0] = makeGlobal({ baseline: { k1: 0.99 } })
    fireEvent.click(screen.getByRole('button', { name: '刷新已保存策略' }))
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: '刷新已保存策略' }),
      ).not.toBeDisabled(),
    )
    expect(screen.getByLabelText('k1 短租基线系数数值')).toHaveValue(0.95)
    fireEvent.click(screen.getByRole('button', { name: '放弃更改' }))
    expect(screen.getByLabelText('k1 短租基线系数数值')).toHaveValue(0.99)
  })
})

describe('sparse template overrides', () => {
  const sparse = {
    id: 7,
    name: 'tpl:T',
    scope: 'template',
    channel_route: 'eco_only',
    params: { baseline: { k1: 0.9 } },
    real_execution_enabled: true,
    priority: 5,
    updated_at: '',
  }
  async function edit(real = true) {
    rows = [
      {
        ...makeGlobal({
          guardrails: { min_rent: 20, cooldown_minutes: 120 },
          eco_max_days: 60,
        }),
        real_execution_enabled: true,
      },
      { ...sparse, real_execution_enabled: real },
    ]
    renderPage(<Strategies />, '/strategies?tab=templates')
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    await waitFor(() => expect(screen.queryByText('执行模式加载中')).toBeNull())
  }
  it('creates a new template with a safe mode and no implicit parameter overrides', async () => {
    renderPage(<Strategies />, '/strategies?tab=templates')
    fireEvent.click(await screen.findByRole('button', { name: '新建模板策略' }))
    await screen.findByRole('option', { name: '模板 T' })
    fireEvent.change(screen.getByLabelText('目标模板'), {
      target: { value: 'T' },
    })
    fireEvent.click(
      within(
        screen.getByRole('radiogroup', { name: '模板渠道路由' }),
      ).getByRole('radio', { name: '仅 ECO' }),
    )
    fireEvent.click(screen.getByRole('button', { name: '保存模板策略' }))
    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith('/strategies/template', {
        hash_name: 'T',
        channel_route: 'eco_only',
        params: {},
        real_execution_enabled: false,
        priority: 0,
      }),
    )
    await screen.findByText('模板策略已保存')
  })
  it('keeps inherited fields sparse when changing only route', async () => {
    await edit()
    expect(screen.getAllByText(/继承全局/).length).toBeGreaterThan(10)
    fireEvent.click(
      within(
        screen.getByRole('radiogroup', { name: '模板渠道路由' }),
      ).getByRole('radio', { name: '仅 UU' }),
    )
    fireEvent.click(screen.getByRole('button', { name: '保存模板策略' }))
    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        '/strategies/template',
        expect.objectContaining({
          params: sparse.params,
          priority: 5,
          channel_route: 'uu_only',
        }),
      ),
    )
    await screen.findByText('模板策略已保存')
  })
  it.each([true, false])(
    'explicitly persists the changed real flag starting at %s',
    async (real) => {
      await edit(real)
      fireEvent.click(screen.getByLabelText('该模板真实执行'))
      fireEvent.click(screen.getByRole('button', { name: '保存模板策略' }))
      await waitFor(() =>
        expect(postMock).toHaveBeenCalledWith(
          '/strategies/template',
          expect.objectContaining({
            params: sparse.params,
            real_execution_enabled: !real,
            priority: 5,
          }),
        ),
      )
      await screen.findByText('模板策略已保存')
    },
  )
  it.each(['清除参数覆盖', '恢复继承 k1 短租基线系数'])(
    'removes parameter overrides with %s without changing independent settings',
    async (label) => {
      await edit()
      fireEvent.click(screen.getByRole('button', { name: label }))
      expect(
        screen.getAllByLabelText('k1 短租基线系数数值').at(-1),
      ).toHaveValue(0.97)
      fireEvent.click(screen.getByRole('button', { name: '保存模板策略' }))
      await waitFor(() =>
        expect(postMock).toHaveBeenCalledWith('/strategies/template', {
          hash_name: 'T',
          channel_route: 'eco_only',
          params: {},
          priority: 5,
          real_execution_enabled: true,
        }),
      )
      await screen.findByText('模板策略已保存')
    },
  )
})
