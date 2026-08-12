import { beforeEach, describe, expect, it, vi } from 'vitest'

const post = vi.hoisted(() => vi.fn())
vi.mock('../client', () => ({ apiClient: { post } }))

import { archiveAsyncTask } from '../imageLibrary'

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
})
