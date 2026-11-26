// SPDX-License-Identifier: MIT
import { describe, expect, it } from 'vitest'
import { devVersionCode, stableVersionCode } from '../scripts/version-code.mjs'

describe('stableVersionCode', () => {
  it('encodes MAJOR.MINOR.PATCH with the stable suffix 99', () => {
    expect(stableVersionCode('5.0.0')).toBe(500000099)
    expect(stableVersionCode('4.2.1')).toBe(400200199)
    expect(stableVersionCode('20.999.999')).toBe(2099999999)
  })

  it('keeps the release order', () => {
    const order = ['4.2.1', '4.2.2', '4.3.0', '5.0.0'].map(stableVersionCode)
    for (let i = 1; i < order.length; i++) expect(order[i]).toBeGreaterThan(order[i - 1])
  })

  it('refuses a prerelease, an out-of-range result and garbage', () => {
    expect(() => stableVersionCode('4.3.0-rc.1')).toThrow()
    expect(() => stableVersionCode('21.0.0')).toThrow()
    expect(() => stableVersionCode('x')).toThrow()
  })
})

describe('devVersionCode', () => {
  it('counts whole minutes since 2026-01-01T00:00:00Z, never below 1', () => {
    expect(devVersionCode(new Date('2026-01-01T00:00:00Z'))).toBe(1)
    expect(devVersionCode(new Date('2026-01-01T00:00:59Z'))).toBe(1)
    expect(devVersionCode(new Date('2026-01-01T00:02:00Z'))).toBe(2)
    expect(devVersionCode(new Date('2026-01-01T01:30:59Z'))).toBe(90)
  })

  it('rises with time', () => {
    const a = devVersionCode(new Date('2026-11-25T10:00:00Z'))
    const b = devVersionCode(new Date('2026-11-25T10:05:00Z'))
    expect(b).toBeGreaterThan(a)
  })
})
