import { describe, expect, it } from 'vitest'

import {
  listPendingImageArchives,
  removePendingImageArchive,
  savePendingImageArchive,
} from '../archiveRecovery'

describe('image archive recovery queue', () => {
  it('scopes pending records by user and removes expired records', async () => {
    const suffix = `${Date.now()}-${Math.random()}`
    const activeID = `active-${suffix}`
    const otherUserID = `other-${suffix}`
    const expiredID = `expired-${suffix}`

    await savePendingImageArchive({
      id: activeID,
      userId: 41,
      kind: 'task',
      title: 'Active',
      taskId: 'asyncimg_active',
    })
    await savePendingImageArchive({
      id: otherUserID,
      userId: 42,
      kind: 'task',
      title: 'Other user',
      taskId: 'asyncimg_other',
    })
    await savePendingImageArchive({
      id: expiredID,
      userId: 41,
      kind: 'task',
      title: 'Expired',
      taskId: 'asyncimg_expired',
      createdAt: Date.now() - 2_000,
      expiresAt: Date.now() - 1_000,
    })

    const records = await listPendingImageArchives(41)

    expect(records.map((record) => record.id)).toContain(activeID)
    expect(records.map((record) => record.id)).not.toContain(otherUserID)
    expect(records.map((record) => record.id)).not.toContain(expiredID)

    await Promise.all([
      removePendingImageArchive(activeID),
      removePendingImageArchive(otherUserID),
      removePendingImageArchive(expiredID),
    ])
  })
})
