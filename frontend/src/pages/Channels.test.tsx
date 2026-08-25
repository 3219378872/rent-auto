import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import Channels from './Channels'

const { getMock, postMock, solveCaptchaMock } = vi.hoisted(() => ({
  getMock: vi.fn(),
  postMock: vi.fn(),
  solveCaptchaMock: vi.fn(),
}))

vi.mock('../api/client', () => ({
  api: { get: getMock, post: postMock, put: vi.fn() },
}))

vi.mock('../lib/tcaptcha', () => ({
  UU_CAPTCHA_APP_ID: 'aid-test',
  solveCaptcha: solveCaptchaMock,
}))

beforeEach(() => {
  getMock.mockReset()
  postMock.mockReset()
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
})
