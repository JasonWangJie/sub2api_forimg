import { beforeEach, describe, expect, it, vi } from 'vitest'

const post = vi.hoisted(() => vi.fn())
vi.mock('../client', () => ({ apiClient: { post } }))

import { archiveAsyncTask, importImageFile, syncPlazaSubmissionRequest } from '../imageLibrary'

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
})
