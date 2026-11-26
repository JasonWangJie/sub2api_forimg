import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AsyncImageRetryRulesView from '../AsyncImageRetryRulesView.vue'
import { buildRuleFromError, duplicateRule, newRule, previewPage } from '../draft'

const mocks = vi.hoisted(() => ({ getConfig: vi.fn(), save: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin/asyncImageRetryRules', () => ({ asyncImageRetryRulesAPI: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

const configuration = () => ({
  revision: 4,
  pause_generation_retries: false,
  rules: [
    { ...newRule(), id: 'rule-x', name: 'Timeout recovery' },
    { ...newRule(), id: 'rule-y', name: 'Capacity recovery' },
    { ...newRule(), id: 'rule-z', name: 'Fallback' },
  ],
})

async function mountRules() {
  const view = mount(AsyncImageRetryRulesView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        ConfirmDialog: true,
        RetryRuleEditor: true,
        RetrySamples: true,
        RetryTextPreview: true,
        RetryVersions: true,
      },
    },
  })
  await flushPromises()
  return view
}

describe('Async image retry rule drafts', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    mocks.getConfig.mockImplementation(async () => configuration())
  })

  it('moves the searched rule in the complete priority list and sends it only on save', async () => {
    const view = await mountRules()
    await view.get('[data-test="retry-search"]').setValue('  RULE-Y ')
    expect(view.findAll('tbody tr')).toHaveLength(1)
    expect(view.get('[data-test="retry-priority"]').text()).toBe('2')
    await view.get('[data-test="retry-move-up"]').trigger('click')
    expect(view.get('[data-test="retry-priority"]').text()).toBe('1')
    expect(mocks.save).not.toHaveBeenCalled()
    mocks.save.mockImplementation(async value => ({ ...value, revision: 5 }))
    await view.get('[data-test="save-retry-rules"]').trigger('click')
    await flushPromises()
    expect(mocks.save.mock.calls[0][0].rules.map((rule: { id: string }) => rule.id)).toEqual(['rule-y', 'rule-x', 'rule-z'])
    expect(mocks.save.mock.calls[0][0].revision).toBe(4)
    expect(view.find('[data-test="retry-unsaved"]').exists()).toBe(false)
    view.unmount()
  })

  it('keeps the draft and disables retrying a conflicted revision', async () => {
    const view = await mountRules()
    const pause = view.get<HTMLInputElement>('[data-test="retry-pause"]')
    await pause.setValue(true)
    mocks.save.mockRejectedValue({ status: 409 })
    await view.get('[data-test="save-retry-rules"]').trigger('click')
    await flushPromises()
    expect(view.get('[role="alert"]').text()).toContain('retryRules.conflict')
    expect(pause.element.checked).toBe(true)
    expect(view.get('[data-test="save-retry-rules"]').attributes('disabled')).toBeDefined()
    view.unmount()
  })

  it('prefills supported historical fields and gives duplicates independent identities', () => {
    const rule = buildRuleFromError({ platform: 'openai', model: 'gpt-image-2', stage: 'submit', account_id: 15, http_status: 454, error_code: 'asset_download_timeout' })
    expect(rule.scope).toMatchObject({ platforms: ['openai'], models: ['gpt-image-2'], stages: ['submit'], source_account_ids: [15] })
    expect(rule.match).toMatchObject({ http_statuses: [454], error_codes: ['asset_download_timeout'] })
    expect(rule.allowed_account_ids).toEqual([])
    const copy = duplicateRule(rule, 'copy')
    expect(copy.id).not.toBe(rule.id)
    expect(copy.steps[0].id).not.toBe(rule.steps[0].id)
  })

  it('starts at most four preview calls and stops scheduling after cancellation', async () => {
    const controller = new AbortController()
    const finish: (() => void)[] = []
    const run = vi.fn(() => new Promise<void>(resolve => finish.push(resolve)))
    const batch = previewPage([1, 2, 3, 4, 5, 6], run, controller.signal)
    expect(run.mock.calls).toHaveLength(4)
    controller.abort()
    finish.forEach(resolve => resolve())
    await batch
    expect(run.mock.calls).toHaveLength(4)
  })
})
