import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import type { Account } from '@/types'
import AccountRateLimitDialog from '../AccountRateLimitDialog.vue'
import AccountActionMenu from '../AccountActionMenu.vue'

const { clearRateLimit } = vi.hoisted(() => ({ clearRateLimit: vi.fn() }))
vi.mock('@/api/admin/accounts', () => ({ clearRateLimit }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

const future = '2099-01-01T00:00:00Z'
const past = '2000-01-01T00:00:00Z'
const account = (overrides: Partial<Account> = {}): Account => ({
  id: 12, name: 'Gemini 12', platform: 'gemini', type: 'apikey',
  status: 'active', schedulable: false,
  rate_limit_reset_at: future, overload_until: null, temp_unschedulable_until: null,
  ...overrides
} as Account)
let wrapper: VueWrapper
const render = (overrides: Partial<Account> = {}) => {
  wrapper = mount(AccountRateLimitDialog, {
    props: { show: true, account: account(overrides) },
    global: { stubs: {
      BaseDialog: {
        name: 'BaseDialog',
        props: ['show', 'closeOnEscape', 'showCloseButton'],
        template: '<div v-if="show"><slot /><slot name="footer" /></div>'
      }
    } }
  })
  return wrapper
}

describe('AccountRateLimitDialog', () => {
  beforeEach(() => { vi.clearAllMocks() })
  afterEach(() => { wrapper?.unmount() })

  it('shows all local restrictions and the disabled scheduling warning before any request', async () => {
    render({
      overload_until: future, temp_unschedulable_until: future,
      extra: { model_rate_limits: { 'gemini-image': { rate_limited_at: past, rate_limit_reset_at: future } } }
    })
    expect(wrapper.text()).toContain('Gemini 12')
    for (const kind of ['account', 'model', 'overload', 'temporary']) {
      expect(wrapper.text()).toContain(`admin.accounts.rateLimit.kinds.${kind}`)
    }
    expect(wrapper.text()).toContain('gemini-image')
    expect(wrapper.get('[data-testid="scheduling-paused"]').text()).toContain('schedulingPaused')
    expect(wrapper.text()).toContain('admin.accounts.rateLimit.settingsPath')
    expect(clearRateLimit).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="cancel"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(clearRateLimit).not.toHaveBeenCalled()
  })

  it('uses only the clear endpoint, prevents duplicate submits, and returns the unchanged scheduling switch', async () => {
    const updated = account({ rate_limit_reset_at: null })
    let resolve!: (value: Account) => void
    clearRateLimit.mockReturnValue(new Promise<Account>(done => { resolve = done }))
    render()
    await wrapper.get('[data-testid="clear-rate-limit"]').trigger('click')
    await wrapper.get('[data-testid="clear-rate-limit"]').trigger('click')
    expect(clearRateLimit).toHaveBeenCalledTimes(1)
    expect(clearRateLimit).toHaveBeenCalledWith(12)
    expect(wrapper.get('[data-testid="cancel"]').attributes('disabled')).toBeDefined()
    expect(wrapper.findComponent({ name: 'BaseDialog' }).props('closeOnEscape')).toBe(false)
    resolve(updated)
    await flushPromises()
    expect(wrapper.emitted('cleared')).toEqual([[updated]])
    expect((wrapper.emitted('cleared')![0][0] as Account).schedulable).toBe(false)
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('keeps a failure visible and allows a deliberate retry', async () => {
    clearRateLimit.mockRejectedValueOnce({ message: 'Service unavailable' }).mockResolvedValueOnce(account({ rate_limit_reset_at: null }))
    render()
    await wrapper.get('[data-testid="clear-rate-limit"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Service unavailable')
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.emitted('cleared')).toBeUndefined()
    expect(wrapper.get('[data-testid="clear-rate-limit"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-testid="clear-rate-limit"]').trigger('click')
    await flushPromises()
    expect(clearRateLimit).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.emitted('cleared')).toHaveLength(1)
  })

  it('does not submit expired or absent limits and clears errors when switching accounts', async () => {
    clearRateLimit.mockRejectedValueOnce(new Error('failed'))
    render()
    await wrapper.get('[data-testid="clear-rate-limit"]').trigger('click')
    await flushPromises()
    await wrapper.setProps({ account: account({ id: 39, rate_limit_reset_at: past, schedulable: true }) })
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="scheduling-paused"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="clear-rate-limit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="clear-rate-limit"]').trigger('click')
    expect(clearRateLimit).toHaveBeenCalledTimes(1)
  })
})

describe('AccountActionMenu rate-limit entry', () => {
  afterEach(() => { wrapper?.unmount() })
  const renderMenu = (overrides: Partial<Account>) => {
    wrapper = mount(AccountActionMenu, {
      props: { show: true, position: { top: 100, left: 20 }, account: account(overrides) },
      global: { stubs: { teleport: true } }
    })
    return wrapper
  }

  it.each([
    { rate_limit_reset_at: future },
    { rate_limit_reset_at: null, overload_until: future },
    { rate_limit_reset_at: null, temp_unschedulable_until: future },
    { rate_limit_reset_at: null, extra: { model_rate_limits: { image: { rate_limited_at: past, rate_limit_reset_at: future } } } },
    { rate_limit_reset_at: null, extra: { antigravity_quota_scopes: { gemini: true } } }
  ])('offers early clearing for a local restriction: %j', async (overrides) => {
    renderMenu(overrides)
    const button = wrapper.findAll('button').find(item => item.text().includes('admin.accounts.rateLimit.action'))!
    expect(button).toBeDefined()
    await button.trigger('click')
    expect(wrapper.emitted('clear-rate-limit')![0][0]).toMatchObject({ id: 12 })
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it.each([
    { rate_limit_reset_at: null },
    { rate_limit_reset_at: past },
    {
      rate_limit_reset_at: 'invalid',
      // Deliberately malformed API data: the UI must skip these entries safely.
      extra: { model_rate_limits: { bad: null, array: [], invalid: { rate_limit_reset_at: 'bad' }, expired: { rate_limited_at: past, rate_limit_reset_at: past } } } as unknown as Account['extra']
    },
    { rate_limit_reset_at: null, status: 'error' as const }
  ])('does not offer rate-limit clearing for scheduling disabled alone or expired/malformed records: %j', overrides => {
    renderMenu(overrides)
    expect(wrapper.text()).not.toContain('admin.accounts.rateLimit.action')
  })
})
