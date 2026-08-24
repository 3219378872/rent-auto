import { describe, expect, it } from 'vitest'
import * as H from './Dashboard.helpers'

describe('sparkline scaling', () => {
  it('maps values into the viewBox', () => {
    const ys = [0, 5, 10].map((v) => H.scaleY(v, 0, 10, 120))
    expect(ys[0]).toBeCloseTo(116)
    expect(ys[2]).toBeCloseTo(4)
  })
  it('handles flat series without division blowup', () => {
    const y = H.scaleY(7, 7, 7, 120)
    expect(Number.isFinite(y)).toBe(true)
  })
})

import { channelIssues } from './Dashboard.helpers'
import { render, screen } from '@testing-library/react'
import Dashboard from './Dashboard'
import { vi } from 'vitest'

describe('channelIssues', () => {
  it('lists only non-ok channels with their status', () => {
    expect(channelIssues({ uu: 'ok', eco: 'error: expired token', steam: 'not_configured' }))
      .toEqual(['ECO：error: expired token', 'STEAM：not_configured'])
  })
  it('returns empty when everything is ok', () => {
    expect(channelIssues({ uu: 'ok' })).toEqual([])
  })
})

// 告警条（US-DASH-03）：任一渠道非 ok 时仪表盘顶部出现显著告警。
describe('Dashboard channel health banner', () => {
  it('renders an alert when a channel is unhealthy', async () => {
    vi.mock('../api/client', () => ({
      api: {
        get: (_path: string) =>
          _path === '/channels'
            ? Promise.resolve({ uu: 'ok', eco: 'error: sign failed' })
            : Promise.resolve({
                assets: { total: 0, inventory: 0, deposits: {}, wallets: {} },
                income: { total: 0, today: 0, by_channel: [] },
                leased_out: 0,
                annualized_roi: 0,
                categories: [],
                series_30d: [],
              }),
      },
    }))
    render(<Dashboard />)
    const alert = await screen.findByRole('alert')
    if (!alert.textContent.includes('ECO：error: sign failed')) {
      throw new Error(`banner missing status: ${alert.textContent}`)
    }
  })
})
