import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'

const mocks = vi.hoisted(() => ({
  getPolicy: vi.fn(), savePolicy: vi.fn(), list: vi.fn(), preview: vi.fn(), create: vi.fn(), cancel: vi.fn(), resume: vi.fn(), showError: vi.fn(), showSuccess: vi.fn(),
}))
vi.mock('@/api/admin/backup', () => ({ default: {}, getImageCleanupPolicy: mocks.getPolicy, updateImageCleanupPolicy: mocks.savePolicy }))
vi.mock('@/api/imageLibrary', () => ({ listCleanupJobs: mocks.list, previewCleanup: mocks.preview, createCleanupJob: mocks.create, cancelCleanupJob: mocks.cancel, resumeCleanupJob: mocks.resume }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError: mocks.showError, showSuccess: mocks.showSuccess }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run: (fn: () => unknown) => fn() }), isStepUpBlocked: () => false, isStepUpCancelled: () => false, stepUpBlockReason: () => '' }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ locale: { value: 'zh' }, t: (key: string, params?: Record<string, unknown>) => params ? `${key}:${JSON.stringify(params)}` : key }) }))
import ImageCleanupCard from '../ImageCleanupCard.vue'

const policy = { enabled: true, retention_value: 90, retention_unit: 'days', schedule_mode: 'interval', interval_minutes: 15, daily_time: '03:00', timezone: 'Asia/Shanghai', batch_size: 50, requests_per_second: 2, concurrency: 1, configured: false, revision: 3 }
const fixedPreview = { before: '2026-11-11T12:00:00Z', preview_token: 'fixed-token', matched_items: 8, matched_bytes: 2048, protected_count: 2, scanned_count: 10, truncated: false }
let wrapper: VueWrapper
async function render() {
  wrapper = mount(ImageCleanupCard, { global: { stubs: { TotpStepUpDialog: true } } })
  await flushPromises()
  return wrapper
}

describe('ImageCleanupCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.getPolicy.mockResolvedValue({ ...policy })
    mocks.savePolicy.mockImplementation(async payload => ({ ...payload, configured: true }))
    mocks.list.mockResolvedValue({ items: [] })
    mocks.preview.mockResolvedValue({ ...fixedPreview })
    mocks.create.mockResolvedValue({ id: 1 })
    mocks.cancel.mockResolvedValue(undefined)
    mocks.resume.mockResolvedValue(undefined)
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })
  afterEach(() => { wrapper?.unmount(); vi.restoreAllMocks(); vi.useRealTimers() })

  it('loads legacy values and saves an independent daily policy with the revision', async () => {
    await render()
    expect(wrapper.text()).toContain('imageCleanup.legacyHint')
    await wrapper.get('[data-testid="schedule-mode"]').setValue('daily')
    await wrapper.get('[data-testid="daily-time"]').setValue('04:30')
    await wrapper.get('[data-testid="retention-value"]').setValue('12')
    await wrapper.get('[data-testid="retention-unit"]').setValue('hours')
    await wrapper.get('[data-testid="cleanup-policy"]').trigger('submit')
    await flushPromises()
    expect(mocks.savePolicy).toHaveBeenCalledWith(expect.objectContaining({ schedule_mode: 'daily', daily_time: '04:30', timezone: 'Asia/Shanghai', retention_value: 12, retention_unit: 'hours', revision: 3, batch_size: 50, requests_per_second: 2, concurrency: 1 }))
    expect(wrapper.text()).not.toContain('imageCleanup.legacyHint')
  })

  it('submits the server preview cutoff and token instead of recomputing an age', async () => {
    await render()
    await wrapper.get('[data-testid="manual-value"]').setValue('6')
    await wrapper.get('[data-testid="manual-unit"]').setValue('hours')
    await wrapper.get('[data-testid="cleanup-manual"]').trigger('submit')
    await flushPromises()
    expect(mocks.preview).toHaveBeenCalledWith({ scope: 'async_results', older_than_value: 6, older_than_unit: 'hours' })
    expect(wrapper.text()).toContain('imageCleanup.protected:{"count":2}')
    await wrapper.get('[data-testid="manual-start"]').trigger('click')
    await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith({ scope: 'async_results', before: fixedPreview.before, preview_token: fixedPreview.preview_token })
    expect(wrapper.find('[data-testid="manual-start"]').exists()).toBe(false)
  })

  it('keeps the inactive schedule valid after switching away from an incomplete interval', async () => {
    await render()
    await wrapper.get('[data-testid="interval-minutes"]').setValue('0')
    await wrapper.get('[data-testid="schedule-mode"]').setValue('daily')
    await wrapper.get('[data-testid="cleanup-policy"]').trigger('submit')
    await flushPromises()
    expect(mocks.savePolicy).toHaveBeenCalledWith(expect.objectContaining({ schedule_mode: 'daily', interval_minutes: 15, daily_time: '03:00' }))
  })

  it('invalidates a preview when the manual age changes', async () => {
    await render()
    await wrapper.get('[data-testid="cleanup-manual"]').trigger('submit')
    await flushPromises()
    await wrapper.get('[data-testid="manual-unit"]').setValue('hours')
    expect(wrapper.find('[data-testid="manual-start"]').exists()).toBe(false)
    expect(mocks.create).not.toHaveBeenCalled()
  })

  it('does not submit malformed limits or zero retention', async () => {
    await render()
    for (const [selector, value] of [['batch-size', '101'], ['retention-value', '0']]) {
      await wrapper.get(`[data-testid="${selector}"]`).setValue(value)
      await wrapper.get('[data-testid="cleanup-policy"]').trigger('submit')
    }
    expect(mocks.savePolicy).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="manual-value"]').setValue('0')
    await wrapper.get('[data-testid="cleanup-manual"]').trigger('submit')
    expect(mocks.preview).not.toHaveBeenCalled()
  })

  it.each([1, 50, 100, 500, 1000])('saves a storage request limit of %i with the existing policy revision', async rate => {
    await render()
    const input = wrapper.get<HTMLInputElement>('[data-testid="delete-rate"]')
    await input.setValue(String(rate))
    expect(input.element.checkValidity()).toBe(true)
    expect(wrapper.get('[data-testid="cleanup-policy"] button[type="submit"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-testid="cleanup-policy"]').trigger('submit')
    await flushPromises()
    expect(mocks.savePolicy).toHaveBeenCalledOnce()
    expect(mocks.savePolicy).toHaveBeenCalledWith(expect.objectContaining({ requests_per_second: rate, revision: 3 }))
  })

  it.each([0, 1.5, 1001])('rejects an invalid storage request limit of %s before saving', async rate => {
    await render()
    const input = wrapper.get<HTMLInputElement>('[data-testid="delete-rate"]')
    await input.setValue(String(rate))
    expect(input.element.checkValidity()).toBe(false)
    expect(wrapper.get('[data-testid="cleanup-policy"] button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="cleanup-policy"]').trigger('submit')
    await flushPromises()
    expect(mocks.savePolicy).not.toHaveBeenCalled()
  })

  it.each([4, 16])('saves cleanup concurrency %i with the shared rate and revision', async concurrency => {
    await render()
    const input = wrapper.get<HTMLInputElement>('[data-testid="cleanup-concurrency"]')
    await input.setValue(String(concurrency))
    expect(input.element.checkValidity()).toBe(true)
    expect(wrapper.get('[data-testid="cleanup-policy"] button[type="submit"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-testid="cleanup-policy"]').trigger('submit')
    await flushPromises()
    expect(mocks.savePolicy).toHaveBeenCalledOnce()
    expect(mocks.savePolicy).toHaveBeenCalledWith(expect.objectContaining({ concurrency, requests_per_second: 2, revision: 3 }))
  })

  it.each([0, 1.5, 17])('rejects invalid cleanup concurrency %s before saving', async concurrency => {
    await render()
    const input = wrapper.get<HTMLInputElement>('[data-testid="cleanup-concurrency"]')
    await input.setValue(String(concurrency))
    expect(input.element.checkValidity()).toBe(false)
    expect(wrapper.get('[data-testid="cleanup-policy"] button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="cleanup-policy"]').trigger('submit')
    await flushPromises()
    expect(mocks.savePolicy).not.toHaveBeenCalled()
  })

  it('restores concurrency one when loading a legacy response without the field', async () => {
    mocks.getPolicy.mockResolvedValue({ ...policy, concurrency: undefined })
    await render()
    const input = wrapper.get<HTMLInputElement>('[data-testid="cleanup-concurrency"]')
    expect(input.element.value).toBe('1')
    await input.setValue('4')
    await wrapper.get('[data-testid="cleanup-policy"] button[type="button"]').trigger('click')
    await flushPromises()
    expect(input.element.value).toBe('1')
    await wrapper.get('[data-testid="cleanup-policy"]').trigger('submit')
    await flushPromises()
    expect(mocks.savePolicy).toHaveBeenCalledWith(expect.objectContaining({ concurrency: 1, revision: 3 }))
  })

  it('prevents execution without a preview token and explains truncated counts', async () => {
    mocks.preview.mockResolvedValue({ ...fixedPreview, preview_token: undefined, truncated: true })
    await render()
    await wrapper.get('[data-testid="cleanup-manual"]').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-testid="manual-start"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('imageCleanup.truncated:{"count":10}')
  })

  it('allows tracked candidates protected by unresolved history to start with runtime protection checks', async () => {
    mocks.preview.mockResolvedValue({ ...fixedPreview, matched_items: 0, matched_bytes: 0, protected_count: 10, scanned_count: 10 })
    await render()
    await wrapper.get('[data-testid="cleanup-manual"]').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-testid="manual-start"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).toContain('imageCleanup.protectionRecheck')
    await wrapper.get('[data-testid="manual-start"]').trigger('click')
    await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith({ scope: 'async_results', before: fixedPreview.before, preview_token: fixedPreview.preview_token })
  })

  it('clears expired previews after a create failure', async () => {
    mocks.create.mockRejectedValue({ message: 'preview expired' })
    await render()
    await wrapper.get('[data-testid="cleanup-manual"]').trigger('submit')
    await flushPromises()
    await wrapper.get('[data-testid="manual-start"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="manual-start"]').exists()).toBe(false)
    expect(mocks.showError).toHaveBeenCalledWith('preview expired')
  })

  it('shows real object counts separately and cancels or resumes the selected result job', async () => {
    mocks.list.mockResolvedValue({ items: [
      { id: 7, scope: 'async_results', status: 'running', source: 'scheduled', before: fixedPreview.before, created_at: fixedPreview.before, scanned_count: 9, deleted_count: 3, detached_count: 4, protected_count: 2, skipped_count: 1, failed_count: 0, deleted_bytes: 2048 },
      { id: 8, scope: 'async_results', status: 'cancelled', created_at: fixedPreview.before },
      { id: 9, scope: 'expired', status: 'running', created_at: fixedPreview.before },
    ] })
    await render()
    expect(wrapper.find('[data-testid="cleanup-job-9"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="cleanup-job-7"]').text()).toContain('"deleted":3,"detached":4')
    await wrapper.get('[data-testid="cleanup-job-7"] button').trigger('click')
    await flushPromises()
    expect(mocks.cancel).toHaveBeenCalledWith(7)
    await wrapper.get('[data-testid="cleanup-job-8"] button').trigger('click')
    await flushPromises()
    expect(mocks.resume).toHaveBeenCalledWith(8)
  })

  it('stops polling after unmount', async () => {
    vi.useFakeTimers()
    await render()
    const initialCalls = mocks.list.mock.calls.length
    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(30000)
    expect(mocks.list).toHaveBeenCalledTimes(initialCalls)
  })
})
