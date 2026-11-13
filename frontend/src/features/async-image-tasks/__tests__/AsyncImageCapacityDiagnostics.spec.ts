import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AsyncImageCapacityDiagnostics from '../AsyncImageCapacityDiagnostics.vue'
import type { AsyncImageTaskEvent } from '../types'
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key, te: (key: string) => !key.endsWith('new_reason') }) }))
const events: AsyncImageTaskEvent[] = [{
  created_at: '2026-11-13T09:00:00Z', status: 'capacity_diagnostic', capacity_diagnostics: {
    observed_at: '2026-11-13T09:00:00Z', group_id: 2, model: 'gemini-image', size_tier: '2K', observations: [
      { source: 'pool_snapshot', pool_mode: 'resolution', pool_size_tier: '2K', candidate_count: 2, candidates_truncated: false, reasons: [{ code: 'scheduling_disabled', count: 1, account_ids: [12] }, { code: 'rate_limited', count: 1, account_ids: [39], earliest_recovery_at: '2026-11-13T10:00:00Z' }] },
      { source: 'concurrency', candidate_count: 1, candidates_truncated: true, reasons: [{ code: 'concurrency_full', count: 1, account_ids: [25] }, { code: 'new_reason', count: 1 }] }
    ]
  }
}]
describe('AsyncImageCapacityDiagnostics', () => {
  it('separates snapshots and actual admission observations without combining counts', () => {
    const wrapper = mount(AsyncImageCapacityDiagnostics, { props: { admin: true, events } })
    expect(wrapper.text()).toContain('sources.pool_snapshot')
    expect(wrapper.text()).toContain('snapshotNote')
    expect(wrapper.text()).toContain('sources.concurrency')
    expect(wrapper.text()).toContain('reasons.scheduling_disabled')
    expect(wrapper.text()).toContain('reasons.rate_limited')
    expect(wrapper.text()).toContain('recovery')
    expect(wrapper.text()).toContain('#39')
    expect(wrapper.text()).toContain('reasons.unknown')
    expect(wrapper.text()).toContain('truncated')
    expect(wrapper.findAll('details')).toHaveLength(1)
    wrapper.unmount()
  })
  it('does not render account diagnostics for ordinary users even if mistakenly present in data', () => {
    const wrapper = mount(AsyncImageCapacityDiagnostics, { props: { admin: false, events } })
    expect(wrapper.find('[data-testid="capacity-diagnostics"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('#39')
    wrapper.unmount()
  })
  it('keeps older tasks without diagnostics unchanged', () => {
    const wrapper = mount(AsyncImageCapacityDiagnostics, { props: { admin: true, events: [{ status: 'failed', created_at: '2026-11-13T09:00:00Z' }] } })
    expect(wrapper.find('section').exists()).toBe(false)
    wrapper.unmount()
  })
})
