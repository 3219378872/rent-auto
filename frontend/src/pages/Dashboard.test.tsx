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
