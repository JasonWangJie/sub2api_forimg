import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import type { Account, ImageResolutionCooldown } from '@/types'
import AccountImageResolutionSummary from '../AccountImageResolutionSummary.vue'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string, args?: Record<string, unknown>) => key + (args ? ` ${JSON.stringify(args)}` : '') })
}))
const now = Date.parse('2026-11-13T12:00:00Z')
const item = (overrides: Partial<ImageResolutionCooldown> = {}): ImageResolutionCooldown => ({
  account_id: 12, model: 'gpt-image-2', size_tier: '4K', cycle_id: 'cycle-1', cooldown_until: '2026-11-13T12:10:00Z', reason: 'image_resolution_downgrade', ...overrides
})
const account = (overrides: Partial<Account> = {}): Account => ({
  id: 12, name: 'Image account 12', platform: 'openai', image_resolution_guard: { enabled: true, cooldowns: [item()] }, ...overrides
} as Account)
let wrapper: VueWrapper
const render = (value = account()) => { wrapper = mount(AccountImageResolutionSummary, { props: { account: value, now } }); return wrapper }
afterEach(() => wrapper?.unmount())

describe('AccountImageResolutionSummary', () => {
  it('shows each active model, tier and deadline and opens the existing account details', async () => {
    const value = account({ image_resolution_guard: { enabled: true, cooldowns: [item(), item({ cycle_id: 'cycle-2', model: 'gemini-3-pro-image-preview', size_tier: '2K' })] } })
    render(value)
    expect(wrapper.text()).toContain('gpt-image-2')
    expect(wrapper.text()).toContain('gemini-3-pro-image-preview')
    expect(wrapper.text()).toContain('4K')
    expect(wrapper.text()).toContain('2K')
    expect(wrapper.text()).toContain('rowUntil')
    expect(wrapper.get('[data-testid="resolution-summary"]').classes()).toContain('border-amber-200')
    await wrapper.get('[data-testid="resolution-summary"]').trigger('click')
    expect(wrapper.emitted('open')).toEqual([[value]])
  })

  it('clearly separates retained records while protection is disabled from a scheduling block', () => {
    render(account({ image_resolution_guard: { enabled: false, cooldowns: [item()] } }))
    expect(wrapper.text()).toContain('rowRecords')
    expect(wrapper.text()).not.toContain('rowCooling')
    expect(wrapper.find('[data-testid="resolution-summary-disabled"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="resolution-summary"]').classes()).toContain('border-gray-200')
    expect(wrapper.text()).toContain('gpt-image-2')
  })

  it('shows unavailable state when the server omitted runtime metadata instead of claiming recovery', async () => {
    render(account({ image_resolution_guard: undefined }))
    expect(wrapper.get('[data-testid="resolution-summary-unknown"]').text()).toContain('stateUnavailable')
    expect(wrapper.text()).not.toContain('noCooldowns')
    await wrapper.get('[data-testid="resolution-summary"]').trigger('click')
    expect(wrapper.emitted('open')).toHaveLength(1)
  })

  it('omits expired, malformed and absent active cooldowns and updates with the shared clock', async () => {
    render(account({ image_resolution_guard: { enabled: true, cooldowns: [item({ cooldown_until: 'invalid' }), item({ cycle_id: 'old', cooldown_until: '2000-01-01T00:00:00Z' }), item({ cycle_id: 'soon', cooldown_until: '2026-11-13T12:00:01Z' })] } }))
    expect(wrapper.findAll('[data-testid="resolution-summary-item"]')).toHaveLength(1)
    await wrapper.setProps({ now: now + 2000 })
    expect(wrapper.find('[data-testid="resolution-summary"]').exists()).toBe(false)
  })

  it('keeps large model lists compact with an explicit view-all action', () => {
    render(account({ image_resolution_guard: { enabled: true, cooldowns: Array.from({ length: 5 }, (_, index) => item({ cycle_id: `cycle-${index}`, model: `image-model-${index}` })) } }))
    expect(wrapper.findAll('[data-testid="resolution-summary-item"]')).toHaveLength(3)
    expect(wrapper.text()).toContain('moreCooldowns {"count":2}')
    expect(wrapper.text()).toContain('viewAndRestore')
  })

  it('does not add unknown-state clutter to non-image platforms', () => {
    render(account({ platform: 'anthropic', image_resolution_guard: undefined }))
    expect(wrapper.find('[data-testid="resolution-summary"]').exists()).toBe(false)
  })
})
