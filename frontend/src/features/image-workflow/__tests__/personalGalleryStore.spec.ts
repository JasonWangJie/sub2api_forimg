import { beforeEach, describe, expect, it } from 'vitest'
import {
  PERSONAL_GALLERY_MAX_BYTES,
  PERSONAL_GALLERY_MAX_RECORDS,
  PERSONAL_GALLERY_TTL_MS,
  __resetPersonalGalleryMemoryForTests,
  savePersonalGalleryItem,
  selectPersonalGalleryOverflowIDs,
} from '../personalGalleryStore'

describe('selectPersonalGalleryOverflowIDs', () => {
  beforeEach(() => {
    __resetPersonalGalleryMemoryForTests()
  })

  it('removes expired records first', () => {
    const now = 1_000_000
    const ids = selectPersonalGalleryOverflowIDs([
      { id: 'old', userId: 1, byteSize: 10, createdAt: now - PERSONAL_GALLERY_TTL_MS - 1, expiresAt: now - 1 },
      { id: 'fresh', userId: 1, byteSize: 10, createdAt: now - 10, expiresAt: now + PERSONAL_GALLERY_TTL_MS },
    ], 1, now, 100)
    expect(ids).toEqual(['old'])
  })

  it('trims oldest alive records when over the count cap', () => {
    const now = Date.now()
    const records = Array.from({ length: 5 }, (_, index) => ({
      id: `r${index}`,
      userId: 1,
      byteSize: 10,
      createdAt: now - index * 1000,
      expiresAt: now + PERSONAL_GALLERY_TTL_MS,
    }))
    const ids = selectPersonalGalleryOverflowIDs(records, 1, now, 3)
    expect(ids).toEqual(['r4', 'r3'])
  })

  it('trims the oldest records when over the byte budget', () => {
    const now = Date.now()
    const records = [
      { id: 'new', userId: 1, byteSize: 60, createdAt: now, expiresAt: now + 1000 },
      { id: 'middle', userId: 1, byteSize: 50, createdAt: now - 1, expiresAt: now + 1000 },
      { id: 'old', userId: 1, byteSize: 40, createdAt: now - 2, expiresAt: now + 1000 },
    ]

    expect(selectPersonalGalleryOverflowIDs(records, 1, now, 100, 100)).toEqual(['old', 'middle'])
  })

  it('does not evict another user records', () => {
    const now = Date.now()
    const records = [
      { id: 'user-1-new', userId: 1, byteSize: 10, createdAt: now, expiresAt: now + 1000 },
      { id: 'user-1-old', userId: 1, byteSize: 10, createdAt: now - 1, expiresAt: now + 1000 },
      { id: 'user-2', userId: 2, byteSize: 10, createdAt: now - 2, expiresAt: now + 1000 },
    ]

    expect(selectPersonalGalleryOverflowIDs(records, 1, now, 1)).toEqual(['user-1-old'])
  })

  it('uses the 100-record and 200 MiB defaults', () => {
    expect(PERSONAL_GALLERY_MAX_RECORDS).toBe(100)
    expect(PERSONAL_GALLERY_MAX_BYTES).toBe(200 * 1024 * 1024)
  })

  it('uses the Blob size instead of caller-provided byte metadata', async () => {
    const file = new Blob(['actual image bytes'], { type: 'image/png' })
    const saved = await savePersonalGalleryItem({
      id: 'trusted-size',
      userId: 1,
      title: 'Trusted size',
      file,
      byteSize: 1,
      checksumSha256: 'a'.repeat(64),
    })

    expect(saved.byteSize).toBe(file.size)
  })
})
