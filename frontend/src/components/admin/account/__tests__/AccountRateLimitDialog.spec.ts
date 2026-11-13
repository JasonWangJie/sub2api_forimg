import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import type { Account } from '@/types'
import type { AccountRateLimitState, AccountRateLimitClearResult } from '@/api/admin/accounts'
import AccountRateLimitDialog from '../AccountRateLimitDialog.vue'
import AccountActionMenu from '../AccountActionMenu.vue'
import { getAccountRateLimitEntries } from '../accountRateLimits'

const { clearRateLimit, getRateLimitState } = vi.hoisted(() => ({ clearRateLimit: vi.fn(), getRateLimitState: vi.fn() }))
vi.mock('@/api/admin/accounts', () => ({ clearRateLimit, getRateLimitState }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))
const future = '2099-01-01T00:00:00Z'
const past = '2000-01-01T00:00:00Z'
const account = (overrides: Partial<Account> = {}): Account => ({
  id: 12, name: 'Gemini 12', platform: 'gemini', type: 'apikey', status: 'active', schedulable: false,
  rate_limit_reset_at: future, overload_until: null, temp_unschedulable_until: null, ...overrides
} as Account)
const state = (value = account(), version = 'version-1'): AccountRateLimitState => ({
  account_id: value.id, version, status: value.status, schedulable: value.schedulable,
  restrictions: getAccountRateLimitEntries(value).map(item => ({ ...item, until: item.until ?? undefined })),
  active_count: getAccountRateLimitEntries(value).length
})
const result = (warning = false): AccountRateLimitClearResult => ({
  account: account({ rate_limit_reset_at: null }), before: state(), after: state(account({ rate_limit_reset_at: null }), 'version-2'),
  cache_sync: { status: warning ? 'warning' : 'synced', failed_components: warning ? ['temp_unsched'] : [] }
})
let wrapper: VueWrapper
const render = (overrides: Partial<Account> = {}) => {
  wrapper = mount(AccountRateLimitDialog, {
    props: { show: true, account: account(overrides) },
    global: { stubs: { BaseDialog: { name: 'BaseDialog', props: ['show', 'closeOnEscape', 'showCloseButton'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' } } }
  })
  return wrapper
}

describe('AccountRateLimitDialog', () => {
  beforeEach(() => { vi.resetAllMocks(); getRateLimitState.mockResolvedValue(state()) })
  afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })

  it('loads a server preview before enabling confirmation and cancel never writes', async () => {
    getRateLimitState.mockResolvedValue(state(account({ overload_until: future, temp_unschedulable_until: future, extra: { model_rate_limits: { image: { rate_limited_at: past, rate_limit_reset_at: future } } } })))
    render()
    expect(wrapper.get('[data-testid="clear-rate-limit"]').attributes('disabled')).toBeDefined()
    await flushPromises()
    expect(getRateLimitState).toHaveBeenCalledWith(12)
    expect(wrapper.text()).not.toContain('admin.accounts.rateLimit.noActiveLimit')
    for (const kind of ['account', 'model', 'overload', 'temporary']) expect(wrapper.text()).toContain(`admin.accounts.rateLimit.kinds.${kind}`)
    expect(wrapper.find('[data-testid="scheduling-paused"]').exists()).toBe(true)
    await wrapper.get('[data-testid="cancel"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(clearRateLimit).not.toHaveBeenCalled()
  })

  it('sends the preview version once, blocks duplicate submission and keeps the result summary', async () => {
    let resolve!: (value: AccountRateLimitClearResult) => void
    clearRateLimit.mockReturnValue(new Promise<AccountRateLimitClearResult>(done => { resolve = done }))
    render(); await flushPromises()
    await wrapper.get('[data-testid="clear-rate-limit"]').trigger('click')
    await wrapper.get('[data-testid="clear-rate-limit"]').trigger('click')
    expect(clearRateLimit).toHaveBeenCalledTimes(1)
    expect(clearRateLimit).toHaveBeenCalledWith(12, 'version-1')
    expect(wrapper.get('[data-testid="cancel"]').attributes('disabled')).toBeDefined()
    expect(wrapper.findComponent({ name: 'BaseDialog' }).props('closeOnEscape')).toBe(false)
    resolve(result()); await flushPromises()
    expect(wrapper.emitted('cleared')).toEqual([[result()]])
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.get('[data-testid="clear-result"]').text()).toContain('cacheSynced')
    expect(wrapper.find('[data-testid="clear-rate-limit"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="scheduling-paused"]').exists()).toBe(true)
    await wrapper.setProps({ account: result().account })
    expect(wrapper.get('[data-testid="clear-result"]').text()).toContain('cacheSynced')
    expect(getRateLimitState).toHaveBeenCalledTimes(1)
  })

  it('requires reload and a new deliberate confirmation after a changed-state conflict', async () => {
    clearRateLimit.mockRejectedValueOnce({ status: 409, code: 'RATE_LIMIT_STATE_CHANGED' }).mockResolvedValueOnce(result())
    render(); await flushPromises()
    await wrapper.get('[data-testid="clear-rate-limit"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('changed')
    expect(wrapper.find('[data-testid="clear-rate-limit"]').exists()).toBe(false)
    getRateLimitState.mockResolvedValueOnce(state(account(), 'fresh-version'))
    await wrapper.get('[data-testid="reload-preview"]').trigger('click'); await flushPromises()
    expect(clearRateLimit).toHaveBeenCalledTimes(1)
    await wrapper.get('[data-testid="clear-rate-limit"]').trigger('click'); await flushPromises()
    expect(clearRateLimit).toHaveBeenLastCalledWith(12, 'fresh-version')
  })

  it('distinguishes committed database changes from failed cache synchronization without retrying clear', async () => {
    clearRateLimit.mockResolvedValue({ ...result(true), account: null, cache_sync: { status: 'warning', failed_components: ['account_refresh', 'temp_unsched', 'runtime_block'] } })
    render(); await flushPromises()
    await wrapper.get('[data-testid="clear-rate-limit"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-testid="clear-result"]').text()).toContain('cacheWarning')
    expect(wrapper.text()).toContain('cache.account_refresh')
    expect(wrapper.text()).toContain('cache.runtime_block')
    expect(wrapper.text()).not.toContain('admin.accounts.rateLimit.noActiveLimit')
    expect(wrapper.find('[data-testid="clear-rate-limit"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="reload-preview"]').exists()).toBe(false)
    expect(wrapper.emitted('cleared')).toHaveLength(1)
  })

  it('fails closed when preview loading fails and allows reloading', async () => {
    getRateLimitState.mockRejectedValueOnce(new Error('load failed'))
    render(); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('load failed')
    expect(wrapper.find('[data-testid="clear-rate-limit"]').exists()).toBe(false)
    await wrapper.get('[data-testid="reload-preview"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-testid="clear-rate-limit"]').attributes('disabled')).toBeUndefined()
  })

  it('keeps ordinary write errors visible for a deliberate retry', async () => {
    clearRateLimit.mockRejectedValueOnce(new Error('write failed')).mockResolvedValueOnce(result())
    render(); await flushPromises()
    await wrapper.get('[data-testid="clear-rate-limit"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('write failed')
    expect(wrapper.emitted('close')).toBeUndefined()
    await wrapper.get('[data-testid="clear-rate-limit"]').trigger('click'); await flushPromises()
    expect(clearRateLimit).toHaveBeenCalledTimes(2)
  })

  it('ignores a stale preview response when switching accounts', async () => {
    let resolve!: (value: AccountRateLimitState) => void
    getRateLimitState.mockReturnValueOnce(new Promise<AccountRateLimitState>(done => { resolve = done }))
    render()
    getRateLimitState.mockResolvedValueOnce(state(account({ id: 39, schedulable: true, rate_limit_reset_at: null })))
    await wrapper.setProps({ account: account({ id: 39 }) }); await flushPromises()
    resolve(state()); await flushPromises()
    expect(wrapper.get('[data-testid="clear-rate-limit"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-testid="scheduling-paused"]').exists()).toBe(false)
  })

  it('does not display a prior account clear result after the selected account changes', async () => {
    let resolve!: (value: AccountRateLimitClearResult) => void
    clearRateLimit.mockReturnValue(new Promise<AccountRateLimitClearResult>(done => { resolve = done }))
    render(); await flushPromises()
    await wrapper.get('[data-testid="clear-rate-limit"]').trigger('click')
    await wrapper.setProps({ account: account({ id: 39 }) })
    resolve(result()); await flushPromises()
    expect(wrapper.find('[data-testid="clear-result"]').exists()).toBe(false)
    expect(wrapper.emitted('cleared')).toEqual([[result()]])
  })

  it('updates expiry while open, guards submission between ticks and stops its timer when closed', async () => {
    vi.useFakeTimers(); vi.setSystemTime(new Date('2026-11-13T09:00:00Z'))
    getRateLimitState.mockResolvedValueOnce(state(account({ rate_limit_reset_at: new Date(Date.now() + 500).toISOString() })))
    render(); await flushPromises()
    vi.setSystemTime(new Date('2026-11-13T09:00:00.600Z'))
    await wrapper.get('[data-testid="clear-rate-limit"]').trigger('click')
    expect(clearRateLimit).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="clear-rate-limit"]').attributes('disabled')).toBeDefined()
    await wrapper.setProps({ show: false }); expect(vi.getTimerCount()).toBe(0)
  })
})

describe('AccountActionMenu rate-limit entry', () => {
  afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })
  const renderMenu = (overrides: Partial<Account>) => {
    wrapper = mount(AccountActionMenu, {
      props: { show: true, position: { top: 100, left: 20 }, account: account(overrides) },
      global: { stubs: { teleport: true } }
    })
    return wrapper
  }

  it('removes an expired action while open and stops the clock when closed', async () => {
    vi.useFakeTimers()
    const currentTime = new Date('2026-11-13T09:00:00Z')
    vi.setSystemTime(currentTime)
    renderMenu({ rate_limit_reset_at: new Date(currentTime.getTime() + 1000).toISOString() })
    expect(wrapper.text()).toContain('admin.accounts.rateLimit.action')
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.text()).not.toContain('admin.accounts.rateLimit.action')
    await wrapper.setProps({ show: false })
    expect(vi.getTimerCount()).toBe(0)
  })

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
