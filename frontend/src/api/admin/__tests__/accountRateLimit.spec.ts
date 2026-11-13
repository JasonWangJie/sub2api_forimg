import { beforeEach, describe, expect, it, vi } from 'vitest'
const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../../client', () => ({ apiClient: client }))
import { getRateLimitState, clearRateLimit } from '../accounts'
describe('guarded account rate-limit API', () => {
  beforeEach(() => vi.clearAllMocks())
  it('loads the server version and sends exactly that version when clearing', async () => {
    const preview = { account_id: 12, version: 'revision-token', restrictions: [], active_count: 0, status: 'active', schedulable: false }
    client.get.mockResolvedValue({ data: preview })
    client.post.mockResolvedValue({ data: { account: null, before: preview, after: preview, cache_sync: { status: 'warning', failed_components: ['account_refresh'] } } })
    expect(await getRateLimitState(12)).toEqual(preview)
    expect(client.get).toHaveBeenCalledWith('/admin/accounts/12/rate-limit-state')
    const result = await clearRateLimit(12, preview.version)
    expect(client.post).toHaveBeenCalledWith('/admin/accounts/12/clear-rate-limit', { expected_version: 'revision-token' })
    expect(result.cache_sync.status).toBe('warning')
    expect(result.account).toBeNull()
  })
})
