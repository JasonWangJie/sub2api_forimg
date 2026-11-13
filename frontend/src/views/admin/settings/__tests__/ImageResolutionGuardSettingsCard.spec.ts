import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import ImageResolutionGuardSettingsCard from '../ImageResolutionGuardSettingsCard.vue'
import type { ImageResolutionGuardSettingsView } from '@/api/admin/imageResolutionGuard'

const mocks = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn(), success: vi.fn() }))
vi.mock('@/api/admin/imageResolutionGuard', async () => ({
  ...await vi.importActual<typeof import('@/api/admin/imageResolutionGuard')>('@/api/admin/imageResolutionGuard'),
  getImageResolutionGuardSettings: mocks.get,
  updateImageResolutionGuardSettings: mocks.update
}))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess: mocks.success }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))
const view = (overrides: Partial<ImageResolutionGuardSettingsView> = {}): ImageResolutionGuardSettingsView => ({
  enabled: false, min_area_percent: 75, failure_threshold: 1, cooldown_seconds: 600, email_enabled: true, recipients: [],
  effective_recipients: ['admin@example.com'], recipient_source: 'admin', smtp_configured: true, warnings: [], ...overrides
})
let wrapper: VueWrapper
const render = () => { wrapper = mount(ImageResolutionGuardSettingsCard); return wrapper }

describe('ImageResolutionGuardSettingsCard', () => {
  beforeEach(() => { vi.resetAllMocks(); mocks.get.mockResolvedValue(view()); mocks.update.mockResolvedValue(view()) })
  afterEach(() => wrapper?.unmount())

  it('loads defaults and shows effective administrator recipients without writing', async () => {
    render()
    expect(wrapper.get('[data-testid="guard-save"]').attributes('disabled')).toBeDefined()
    await flushPromises()
    expect((wrapper.get('[data-testid="guard-percent"]').element as HTMLInputElement).value).toBe('75')
    expect((wrapper.get('[data-testid="guard-threshold"]').element as HTMLInputElement).value).toBe('1')
    expect((wrapper.get('[data-testid="guard-cooldown"]').element as HTMLInputElement).value).toBe('600')
    expect(wrapper.get('[data-testid="guard-enabled"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[data-testid="guard-delivery-status"]').text()).toContain('admin@example.com')
    expect(mocks.update).not.toHaveBeenCalled()
  })

  it.each([
    ['guard-percent', '0'], ['guard-percent', '101'], ['guard-percent', '74.5'],
    ['guard-threshold', '0'], ['guard-threshold', '101'], ['guard-cooldown', '9'], ['guard-cooldown', '86401']
  ])('blocks invalid %s = %s before sending', async (field, value) => {
    render(); await flushPromises()
    await wrapper.get(`[data-testid="${field}"]`).setValue(value)
    await wrapper.get('[data-testid="guard-save"]').trigger('click')
    expect(wrapper.get('[role="alert"]').text()).toContain('invalidNumbers')
    expect(mocks.update).not.toHaveBeenCalled()
  })

  it('validates addresses and saves parsed recipients and independent switches', async () => {
    render(); await flushPromises()
    await wrapper.get('[data-testid="guard-recipients"]').setValue('not-an-email')
    await wrapper.get('[data-testid="guard-save"]').trigger('click')
    expect(wrapper.get('[role="alert"]').text()).toContain('invalidRecipients')
    expect(mocks.update).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="guard-recipients"]').setValue('ops@example.com, admin@example.com\nops@example.com')
    await wrapper.get('[data-testid="guard-enabled"]').trigger('click')
    await wrapper.get('[data-testid="guard-email"]').trigger('click')
    await wrapper.get('[data-testid="guard-save"]').trigger('click'); await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({ enabled: true, email_enabled: false, recipients: ['ops@example.com', 'admin@example.com'] }))
    expect(mocks.success).toHaveBeenCalledOnce()
  })

  it('fails closed after a load error and allows retry without overwriting unknown settings', async () => {
    mocks.get.mockRejectedValueOnce(new Error('read failed'))
    render(); await flushPromises()
    expect(wrapper.get('[data-testid="guard-save"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[role="alert"]').text()).toContain('read failed')
    await wrapper.get('[data-testid="guard-reload"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-testid="guard-save"]').attributes('disabled')).toBeUndefined()
    expect(mocks.update).not.toHaveBeenCalled()
  })

  it('shows SMTP and recipient problems while keeping protection settings usable', async () => {
    mocks.get.mockResolvedValue(view({ enabled: true, smtp_configured: false, effective_recipients: [], recipient_source: 'none' }))
    render(); await flushPromises()
    expect(wrapper.get('[data-testid="guard-mail-warning"]').text()).toContain('smtpMissing')
    expect(wrapper.get('[data-testid="guard-mail-warning"]').text()).toContain('recipientsMissing')
    expect(wrapper.get('[data-testid="guard-mail-warning"]').text()).toContain('cooldownStillWorks')
    expect(wrapper.get('[data-testid="guard-save"]').attributes('disabled')).toBeUndefined()
  })

  it('localizes delivery failures without duplicating SMTP and recipient warning codes', async () => {
    mocks.get.mockResolvedValue(view({ smtp_configured: false, effective_recipients: [], warnings: ['smtp_not_configured', 'no_effective_recipients', 'mail_delivery_failed'] }))
    render(); await flushPromises()
    const text = wrapper.get('[data-testid="guard-mail-warning"]').text()
    expect(text).toContain('deliveryFailed')
    expect(text).not.toContain('smtp_not_configured')
    expect(text).not.toContain('no_effective_recipients')
    expect(text).not.toContain('mail_delivery_failed')
  })

  it('prevents duplicate writes and preserves input after a failed save', async () => {
    let reject!: (reason: Error) => void
    mocks.update.mockReturnValue(new Promise((_, fail) => { reject = fail }))
    render(); await flushPromises()
    await wrapper.get('[data-testid="guard-cooldown"]').setValue('900')
    await wrapper.get('[data-testid="guard-save"]').trigger('click')
    await wrapper.get('[data-testid="guard-save"]').trigger('click')
    expect(mocks.update).toHaveBeenCalledOnce()
    reject(new Error('write failed')); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('write failed')
    expect((wrapper.get('[data-testid="guard-cooldown"]').element as HTMLInputElement).value).toBe('900')
    expect(wrapper.get('[data-testid="guard-save"]').attributes('disabled')).toBeUndefined()
  })
})
