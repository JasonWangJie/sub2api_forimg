import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AsyncImageReliabilitySummary from '../AsyncImageReliabilitySummary.vue'
import type { AsyncImageReliabilityStats } from '../types'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

const stats: AsyncImageReliabilityStats = {
  total: 10, unknown: 1, expired: 3, admin_terminated: 2,
  instrumented_tasks: 0, first_attempt_succeeded: 0,
  http_invocations: 0, account_selections: 20,
  multi_account_tasks: 4, multi_account_succeeded: 3,
  local_fallback_tasks: 2, local_fallback_succeeded: 1,
  result_fetch_retried_tasks: 2, result_fetch_recovered_tasks: 2, unknown_events: 5,
  p95_duration_ms: 1250, p99_duration_ms: 2000, p95_queue_ms: null,
  p95_generation_ms: null, p95_postprocess_ms: null, oldest_queued_age_ms: null,
  actual_cost: 0,
}

describe('Async image reliability summary', () => {
  it('does not invent reliability data for an old API response', () => {
    expect(mount(AsyncImageReliabilitySummary).find('section').exists()).toBe(false)
  })

  it('separates unknown historical measurements, selections, and recovery outcomes', () => {
    const wrapper = mount(AsyncImageReliabilitySummary, { props: { stats } })
    const values = wrapper.findAll('dd').map(cell => cell.text())
    expect(values[0]).toBe('—')
    expect(values[1]).toBe('3 / 4 · 75.0%')
    expect(values[2]).toBe('1 / 2 · 50.0%')
    expect(values[3]).toBe('2 / 2 · 100.0%')
    expect(values[4]).toBe('1 / 10 · 10.00%')
    expect(values[5]).toBe('0 / 20')
    expect(values[6]).toBe('1.3 s / 2.0 s')
    expect(values[7]).toBe('—')
    expect(values[11]).toBe('5 / 2 / 3')
  })
})
