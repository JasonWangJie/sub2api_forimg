import { beforeEach, describe, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))
vi.mock('../../client', () => ({ apiClient: mocks }))
import { clearImageResolutionCooldown, defaultImageResolutionGuardSettings, getImageResolutionCooldowns, getImageResolutionGuardSettings, updateImageResolutionGuardSettings } from '../imageResolutionGuard'

describe('image resolution guard API', () => {
  beforeEach(() => vi.resetAllMocks())
  it('uses independent settings and excludes delivery diagnostics from writes', async () => {
    const view = { ...defaultImageResolutionGuardSettings(), effective_recipients: ['admin@example.com'], recipient_source: 'admin' as const, smtp_configured: true, warnings: [] }
    mocks.get.mockResolvedValue({ data: view })
    mocks.put.mockResolvedValue({ data: { ...view, enabled: true } })
    expect(await getImageResolutionGuardSettings()).toEqual(view)
    expect(mocks.get).toHaveBeenCalledWith('/admin/settings/image-resolution-guard')
    await updateImageResolutionGuardSettings({ ...view, enabled: true })
    expect(mocks.put).toHaveBeenCalledWith('/admin/settings/image-resolution-guard', { ...defaultImageResolutionGuardSettings(), enabled: true })
  })

  it('lists account-scoped cooldowns and clears only the selected encoded cycle', async () => {
    mocks.get.mockResolvedValue({ data: { items: [{ cycle_id: 'cycle/a', model: 'gpt-image-2' }] } })
    expect(await getImageResolutionCooldowns(12)).toEqual([{ cycle_id: 'cycle/a', model: 'gpt-image-2' }])
    expect(mocks.get).toHaveBeenCalledWith('/admin/accounts/12/image-resolution-cooldowns')
    mocks.post.mockResolvedValue({ data: {} })
    await clearImageResolutionCooldown(12, 'cycle/a')
    expect(mocks.post).toHaveBeenCalledWith('/admin/accounts/12/image-resolution-cooldowns/cycle%2Fa/clear')
  })
})
