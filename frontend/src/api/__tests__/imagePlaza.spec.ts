import { beforeEach, describe, expect, it, vi } from 'vitest'
import en from '@/i18n/locales/en/imageWorkflow'
import zh from '@/i18n/locales/zh/imageWorkflow'

const post = vi.hoisted(() => vi.fn())
vi.mock('../client', () => ({ apiClient: { post } }))

import { IMAGE_PLAZA_REPORT_REASONS, reportImagePlaza } from '../imagePlaza'

describe('image plaza reports', () => {
  beforeEach(() => post.mockReset())

  it('keeps the frontend reasons aligned with the public API contract and locales', () => {
    const expected = ['spam', 'sexual', 'violence', 'copyright', 'privacy', 'other']

    expect(IMAGE_PLAZA_REPORT_REASONS).toEqual(expected)
    expect(Object.keys(en.imageWorkflow.plaza.reasons)).toEqual(expected)
    expect(Object.keys(zh.imageWorkflow.plaza.reasons)).toEqual(expected)
  })

  it('submits a supported reason using the backend field names', async () => {
    post.mockResolvedValueOnce({ data: {} })

    await reportImagePlaza('imgpub_1', { reason: 'sexual', detail: 'report details' })

    expect(post).toHaveBeenCalledWith('/image-plaza/imgpub_1/reports', {
      reason: 'sexual',
      details: 'report details',
    })
  })
})
