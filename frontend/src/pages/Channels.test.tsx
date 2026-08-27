import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import Channels from './Channels'

const { getMock, postMock, putMock, solveCaptchaMock } = vi.hoisted(() => ({
  getMock: vi.fn(),
  postMock: vi.fn(),
  putMock: vi.fn(),
  solveCaptchaMock: vi.fn(),
}))

vi.mock('../api/client', () => ({
  api: { get: getMock, post: postMock, put: putMock },
}))

vi.mock('../lib/tcaptcha', () => ({
  UU_CAPTCHA_APP_ID: 'aid-test',
  solveCaptcha: solveCaptchaMock,
}))

beforeEach(() => {
  getMock.mockReset()
  postMock.mockReset()
  putMock.mockReset()
  solveCaptchaMock.mockReset()
  getMock.mockResolvedValue({ uu: 'not_configured' })
})

describe('Channels page UU 短信登录', () => {
  // 回归：滑块重试曾因闭包读到过期 session 而漏带 session_id，
  // 后端静默换新 session → 平台 reqTicket 关联失败 → 图形校验死循环。
  it('图形校验后重发必须复用首次响应的 session_id', async () => {
    postMock
      .mockResolvedValueOnce({ session_id: 'sess-A', mode: 'captcha', req_ticket: 'rt-1' })
      .mockResolvedValueOnce({ session_id: 'sess-A', mode: 'down', msg: '验证码发送成功' })
    solveCaptchaMock.mockResolvedValue({ ret: 0, ticket: 'ck-ticket', randstr: 'ck-rand' })

    render(<Channels />)
    await screen.findByRole('heading', { name: '渠道账号' })

    fireEvent.change(screen.getByPlaceholderText('+86 手机号'), { target: { value: '13800000000' } })
    fireEvent.click(screen.getByRole('button', { name: '发送验证码' }))
    await screen.findByText('验证码已发送，请查收短信')

    expect(solveCaptchaMock).toHaveBeenCalledWith('aid-test')
    expect(postMock).toHaveBeenCalledTimes(2)
    expect(postMock).toHaveBeenNthCalledWith(1, '/channels/uu/sms', { phone: '13800000000' })
    expect(postMock).toHaveBeenNthCalledWith(2, '/channels/uu/sms', {
      phone: '13800000000',
      session_id: 'sess-A',
      captcha: { ticket: 'ck-ticket', randstr: 'ck-rand', req_ticket: 'rt-1' },
    })
  })

  it('首次发送不带 session_id，由后端下发并在后续复用', async () => {
    postMock.mockResolvedValueOnce({
      session_id: 'sess-B', mode: 'down', msg: '验证码发送成功', secs: 60,
    })

    render(<Channels />)
    await screen.findByRole('heading', { name: '渠道账号' })

    fireEvent.change(screen.getByPlaceholderText('+86 手机号'), { target: { value: '13800000001' } })
    fireEvent.click(screen.getByRole('button', { name: '发送验证码' }))
    await screen.findByText(/60s/)

    expect(postMock).toHaveBeenCalledTimes(1)
    expect(postMock).toHaveBeenCalledWith('/channels/uu/sms', { phone: '13800000001' })
    expect(screen.getByText('session: sess-B…')).toBeDefined()
  })

  // 2026-08-27 审计：解滑块后 +5s/+10s 即重发，落在平台 secs 冷却窗内再次被拦。
  // 通过滑块后必须等满 secs 再自动重发。
  it('图形校验通过后遵守 secs 冷却才自动重发', async () => {
    postMock
      .mockResolvedValueOnce({ session_id: 'sess-C', mode: 'captcha', req_ticket: 'rt-2', secs: 1 })
      .mockResolvedValueOnce({ session_id: 'sess-C', mode: 'down', msg: '验证码发送成功' })
    solveCaptchaMock.mockResolvedValue({ ret: 0, ticket: 'ck-ticket', randstr: 'ck-rand' })

    render(<Channels />)
    await screen.findByRole('heading', { name: '渠道账号' })

    fireEvent.change(screen.getByPlaceholderText('+86 手机号'), { target: { value: '13800000002' } })
    fireEvent.click(screen.getByRole('button', { name: '发送验证码' }))

    // 冷却期内不重发
    await screen.findByText(/平台冷却中/)
    expect(postMock).toHaveBeenCalledTimes(1)

    // 冷却结束后自动重发
    await screen.findByText('验证码已发送，请查收短信', {}, { timeout: 3000 })
    expect(postMock).toHaveBeenCalledTimes(2)
    expect(postMock).toHaveBeenNthCalledWith(2, '/channels/uu/sms', {
      phone: '13800000002',
      session_id: 'sess-C',
      captcha: { ticket: 'ck-ticket', randstr: 'ck-rand', req_ticket: 'rt-2' },
    })
  })

  // 5050 门禁下短信登录不可用，手动粘贴 token 是主登录路径：
  // 导入按钮必须把 token 原样 PUT 到 /channels/uu。
  it('手动导入 UU Token：PUT 原样 token 并清空输入', async () => {
    putMock.mockResolvedValue({ status: 'ok' })

    render(<Channels />)
    await screen.findByRole('heading', { name: '渠道账号' })

    fireEvent.change(screen.getByPlaceholderText('eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.…'), {
      target: { value: '  jwt-token-pasted  ' },
    })
    fireEvent.click(screen.getByRole('button', { name: '验证并保存 Token' }))
    await screen.findByText('UU Token 已验证并加密入库')

    expect(putMock).toHaveBeenCalledWith('/channels/uu', { token: 'jwt-token-pasted' })
    expect((screen.getByPlaceholderText('eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.…') as HTMLTextAreaElement).value).toBe('')
  })
})
