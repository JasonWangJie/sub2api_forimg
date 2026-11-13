import { describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import type { Account } from '@/types'
import TempUnschedStatusModal from '../TempUnschedStatusModal.vue'
const { getStatus, recoverState } = vi.hoisted(() => ({ getStatus: vi.fn(), recoverState: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getTempUnschedulableStatus: getStatus, recoverState } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
describe('TempUnschedStatusModal preview handoff', () => {
  it('routes clearing to versioned preview instead of performing an unguarded recovery', async () => {
    getStatus.mockResolvedValue({ active: true, state: { until_unix: 4102444800, triggered_at_unix: 1770000000, rule_index: 0, matched_keyword: 'test' } })
    const account = { id: 12, name: 'Test account' } as Account
    const wrapper = mount(TempUnschedStatusModal, { props: { show: false, account }, global: { stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' } } } })
    await wrapper.setProps({ show: true }); await flushPromises()
    const button = wrapper.findAll('button').find(item => item.text() === 'admin.accounts.rateLimit.action')!
    expect(button).toBeDefined()
    await button.trigger('click')
    expect(wrapper.emitted('preview-clear')).toEqual([[account]])
    expect(recoverState).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
