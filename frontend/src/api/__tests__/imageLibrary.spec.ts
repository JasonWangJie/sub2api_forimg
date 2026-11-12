import { beforeEach, describe, expect, it, vi } from 'vitest'

const post = vi.hoisted(() => vi.fn())
vi.mock('../client', () => ({ apiClient: { post } }))

import { archiveAsyncTask, cancelCleanupJob, createCleanupJob, importImageFile, previewCleanup, resumeCleanupJob, syncPlazaSubmissionRequest } from '../imageLibrary'

describe('image library async archive', () => {
  beforeEach(() => post.mockReset())

  it('archives only through from-task with zero-based result indexes', async () => {
    post
      .mockResolvedValueOnce({ data: { item: { id: 'img_1' }, reused: false } })
      .mockResolvedValueOnce({ data: { item: { id: 'img_2' }, reused: true } })

    const items = await archiveAsyncTask('asyncimg_1', [0, 1])

    expect(post).toHaveBeenNthCalledWith(1, '/user/image-library/from-task', {
      task_id: 'asyncimg_1', image_index: 0,
    })
    expect(post).toHaveBeenNthCalledWith(2, '/user/image-library/from-task', {
      task_id: 'asyncimg_1', image_index: 1,
    })
    expect(items.map((item) => item.id)).toEqual(['img_1', 'img_2'])
  })

  it('lets Axios set the multipart boundary for plaza sync uploads', async () => {
    post.mockResolvedValueOnce({
      data: {
        item: { request_id: 'imgsub_1', status: 'synced' },
        library_item: { id: 'img_1' },
      },
    })

    const file = new File(['png-bytes'], 'result.png', { type: 'image/png' })
    const result = await syncPlazaSubmissionRequest('imgsub_1', file, file.name)

    const [url, body, config] = post.mock.calls[0]
    expect(url).toBe('/user/image-library/submission-requests/imgsub_1/sync')
    expect(body).toBeInstanceOf(FormData)
    const uploaded = (body as FormData).get('file') as File
    expect(uploaded).toMatchObject({ name: file.name, type: file.type, size: file.size })
    expect(config).toBeUndefined()
    expect(result.library_item.id).toBe('img_1')
  })

  it('does not override multipart headers for direct library imports', async () => {
    post.mockResolvedValueOnce({ data: { id: 'img_1' } })
    const file = new File(['png-bytes'], 'result.png', { type: 'image/png' })

    await importImageFile(file, { platform: 'openai' }, 'idem-1')

    const config = post.mock.calls[0][2]
    expect(config).toEqual({ headers: { 'Idempotency-Key': 'idem-1' } })
  })

  it('keeps result cleanup preview and execution filters separate', async () => {
    const preview = { matched_items: 3, matched_bytes: 20, before: '2026-11-11T00:00:00Z', preview_token: 'preview-1' }
    post.mockResolvedValueOnce({ data: preview }).mockResolvedValueOnce({ data: { id: 5 } })
    expect(await previewCleanup({ scope: 'async_results', older_than_value: 6, older_than_unit: 'hours' })).toEqual(preview)
    await createCleanupJob({ scope: 'async_results', before: preview.before, preview_token: preview.preview_token })
    expect(post).toHaveBeenNthCalledWith(1, '/admin/image-library/cleanup-jobs/preview', { scope: 'async_results', filters: { older_than_value: 6, older_than_unit: 'hours' } })
    expect(post).toHaveBeenNthCalledWith(2, '/admin/image-library/cleanup-jobs', { scope: 'async_results', filters: { before: preview.before, preview_token: 'preview-1' } })
  })

  it('uses the cancel and resume endpoints for the selected job only', async () => {
    post.mockResolvedValue({})
    await cancelCleanupJob(5)
    await resumeCleanupJob(6)
    expect(post).toHaveBeenNthCalledWith(1, '/admin/image-library/cleanup-jobs/5/cancel')
    expect(post).toHaveBeenNthCalledWith(2, '/admin/image-library/cleanup-jobs/6/resume')
  })
})
