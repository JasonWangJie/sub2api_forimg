import { describe, expect, it } from 'vitest'

import {
  normalizeUserUsageLatencyDivisor,
  scaleUsageLatency,
} from '@/utils/usageLatency'

describe('usage latency display scaling', () => {
  it('keeps the original value with the default divisor', () => {
    expect(scaleUsageLatency(345, 1)).toBe(345)
  })

  it('supports decimal divisors and rounds milliseconds to two decimals', () => {
    expect(scaleUsageLatency(345, 2)).toBe(172.5)
    expect(scaleUsageLatency(10, 3)).toBe(3.33)
    expect(scaleUsageLatency(201, 200)).toBe(1.01)
  })

  it('preserves missing latency values', () => {
    expect(scaleUsageLatency(null, 2)).toBeNull()
    expect(scaleUsageLatency(undefined, 2)).toBeNull()
  })

  it('falls back to one for invalid divisors', () => {
    for (const divisor of [0, 0.5, 1001, Number.NaN, Number.POSITIVE_INFINITY, 'invalid']) {
      expect(normalizeUserUsageLatencyDivisor(divisor)).toBe(1)
      expect(scaleUsageLatency(345, divisor)).toBe(345)
    }
  })
})
