import { beforeEach, describe, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('../../client', () => ({ apiClient: mocks }))
import { getImageCleanupPolicy, updateImageCleanupPolicy, type ImageCleanupPolicy } from '../backup'

describe('image cleanup policy API', () => {
  beforeEach(() => vi.clearAllMocks())
  it('uses an independent policy endpoint and excludes server schedule metadata from updates', async () => {
    const policy: ImageCleanupPolicy = { enabled: true, retention_value: 7, retention_unit: 'days', schedule_mode: 'daily', interval_minutes: 15, daily_time: '03:00', timezone: 'Asia/Shanghai', batch_size: 50, requests_per_second: 2, revision: 4, configured: true, next_run_at: '2026-11-12T19:00:00Z' }
    mocks.get.mockResolvedValue({ data: policy })
    mocks.put.mockResolvedValue({ data: { ...policy, revision: 5 } })
    expect(await getImageCleanupPolicy()).toEqual(policy)
    const saved = await updateImageCleanupPolicy(policy)
    expect(mocks.get).toHaveBeenCalledWith('/admin/backups/image-storage/cleanup-policy')
    expect(mocks.put).toHaveBeenCalledWith('/admin/backups/image-storage/cleanup-policy', expect.objectContaining({ revision: 4, daily_time: '03:00', timezone: 'Asia/Shanghai' }))
    expect(mocks.put.mock.calls[0][1]).not.toHaveProperty('configured')
    expect(mocks.put.mock.calls[0][1]).not.toHaveProperty('next_run_at')
    expect(saved.revision).toBe(5)
  })
})
