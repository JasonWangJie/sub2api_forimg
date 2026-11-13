import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import type { Account } from '@/types'
import type { ImageResolutionCooldown } from '@/api/admin/imageResolutionGuard'
import AccountImageResolutionDialog from '../AccountImageResolutionDialog.vue'
import AccountActionMenu from '../AccountActionMenu.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), clear: vi.fn() }))
vi.mock('@/api/admin/imageResolutionGuard', () => ({ getImageResolutionCooldowns: mocks.get, clearImageResolutionCooldown: mocks.clear }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))
const account = (id = 12) => ({ id, name: `Image account ${id}`, platform: 'openai', type: 'apikey', schedulable: false, status: 'active' }) as Account
const cooldown = (overrides: Partial<ImageResolutionCooldown> = {}): ImageResolutionCooldown => ({
  account_id: 12, model: 'gpt-image-2', size_tier: '4K', cycle_id: 'cycle-1', cooldown_until: '2099-01-01T00:00:00Z', reason: 'image_resolution_downgrade',
  requested_width: 2880, requested_height: 2880, actual_width: 1024, actual_height: 1024, area_percent: 12.64, ...overrides
})
let wrapper: VueWrapper
const render = () => {
  wrapper = mount(AccountImageResolutionDialog, {
    props: { show: true, account: account() },
    global: { stubs: { BaseDialog: { name: 'BaseDialog', props: ['show', 'closeOnEscape', 'showCloseButton'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' } } }
  })
  return wrapper
}

describe('AccountImageResolutionDialog', () => {
  beforeEach(() => { vi.resetAllMocks(); mocks.get.mockResolvedValue([cooldown()]); mocks.clear.mockResolvedValue(undefined) })
  afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })

  it('loads actual dimensions and requires an explicit second confirmation', async () => {
    render(); await flushPromises()
    expect(mocks.get).toHaveBeenCalledWith(12)
    expect(wrapper.text()).toContain('2880 × 2880')
    expect(wrapper.text()).toContain('1024 × 1024 · 12.64%')
    expect(wrapper.text()).toContain('admin.accounts.imageResolutionGuard.defaultReason')
    expect(wrapper.text()).not.toContain('image_resolution_downgrade')
    await wrapper.get('[data-testid="resolution-request-clear"]').trigger('click')
    expect(wrapper.find('[data-testid="resolution-clear-confirmation"]').exists()).toBe(true)
    expect(mocks.clear).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="resolution-cancel-clear"]').trigger('click')
    expect(wrapper.find('[data-testid="resolution-clear-confirmation"]').exists()).toBe(false)
    expect(mocks.clear).not.toHaveBeenCalled()
  })

  it('clears only the selected cycle once, blocks close, and refreshes', async () => {
    let resolve!: () => void
    mocks.clear.mockReturnValue(new Promise<void>(done => { resolve = done }))
    render(); await flushPromises()
    await wrapper.get('[data-testid="resolution-request-clear"]').trigger('click')
    await wrapper.get('[data-testid="resolution-confirm-clear"]').trigger('click')
    await wrapper.get('[data-testid="resolution-confirm-clear"]').trigger('click')
    await wrapper.get('[data-testid="resolution-close"]').trigger('click')
    expect(mocks.clear).toHaveBeenCalledOnce()
    expect(mocks.clear).toHaveBeenCalledWith(12, 'cycle-1')
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.findComponent({ name: 'BaseDialog' }).props('closeOnEscape')).toBe(false)
    mocks.get.mockResolvedValue([])
    resolve(); await flushPromises()
    expect(wrapper.emitted('cleared')).toHaveLength(1)
    expect(mocks.get).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-testid="resolution-no-cooldowns"]').exists()).toBe(true)
  })

  it('keeps errors visible and supports a deliberate retry', async () => {
    mocks.clear.mockRejectedValueOnce(new Error('clear failed'))
    render(); await flushPromises()
    await wrapper.get('[data-testid="resolution-request-clear"]').trigger('click')
    await wrapper.get('[data-testid="resolution-confirm-clear"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('clear failed')
    expect(wrapper.emitted('cleared')).toBeUndefined()
    mocks.get.mockResolvedValue([])
    await wrapper.get('[data-testid="resolution-confirm-clear"]').trigger('click'); await flushPromises()
    expect(mocks.clear).toHaveBeenCalledTimes(2)
    expect(wrapper.emitted('cleared')).toHaveLength(1)
  })

  it('omits expired or invalid deadlines and stops offering a clear when time passes', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-11-13T12:00:00Z'))
    mocks.get.mockResolvedValue([cooldown({ cooldown_until: '2026-11-13T12:00:02Z' }), cooldown({ cycle_id: 'old', cooldown_until: '2000-01-01T00:00:00Z' }), cooldown({ cycle_id: 'bad', cooldown_until: 'invalid' })])
    render(); await flushPromises()
    expect(wrapper.findAll('[data-testid="resolution-cooldown"]')).toHaveLength(1)
    await wrapper.get('[data-testid="resolution-request-clear"]').trigger('click')
    await vi.advanceTimersByTimeAsync(3000)
    expect(wrapper.find('[data-testid="resolution-no-cooldowns"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="resolution-confirm-clear"]').exists()).toBe(false)
    expect(mocks.clear).not.toHaveBeenCalled()
  })

  it('does not show a delayed response for another account', async () => {
    let resolve!: (value: ImageResolutionCooldown[]) => void
    mocks.get.mockReturnValueOnce(new Promise<ImageResolutionCooldown[]>(done => { resolve = done })).mockResolvedValueOnce([])
    render()
    await wrapper.setProps({ account: account(13) }); await flushPromises()
    resolve([cooldown()]); await flushPromises()
    expect(wrapper.text()).toContain('Image account 13')
    expect(wrapper.find('[data-testid="resolution-no-cooldowns"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('gpt-image-2')
  })

  it('reports a load failure without offering a clear and refresh recovers', async () => {
    mocks.get.mockRejectedValueOnce(new Error('read failed'))
    render(); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('read failed')
    expect(wrapper.find('[data-testid="resolution-request-clear"]').exists()).toBe(false)
    await wrapper.get('[data-testid="resolution-refresh"]').trigger('click'); await flushPromises()
    expect(wrapper.find('[data-testid="resolution-request-clear"]').exists()).toBe(true)
  })

  it('offers an independent image cooldown action even without ordinary rate limits', async () => {
    wrapper = mount(AccountActionMenu, { props: { show: true, account: account(), position: { top: 20, left: 20 } }, global: { stubs: { Teleport: true, Icon: true } } })
    await wrapper.get('[data-testid="resolution-cooldowns-action"]').trigger('click')
    expect(wrapper.emitted('image-resolution-cooldowns')).toEqual([[account()]])
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
})
