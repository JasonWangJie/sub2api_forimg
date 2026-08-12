import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))

import asyncImageTasksAPI from '../api'

describe('async image task API', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.post.mockReset()
  })

  it('keeps user and admin list namespaces separate', async () => {
    client.get.mockResolvedValue({ data: { items: [], total: 0, page: 1, page_size: 20, pages: 0 } })

    await asyncImageTasksAPI.user.list({ page: 1, page_size: 20 })
    expect(client.get).toHaveBeenLastCalledWith('/user/async-image-tasks', expect.objectContaining({
      params: { page: 1, page_size: 20 },
    }))

    await asyncImageTasksAPI.admin.list({ page: 2, page_size: 50 })
    expect(client.get).toHaveBeenLastCalledWith('/admin/async-image-tasks', expect.objectContaining({
      params: { page: 2, page_size: 50 },
    }))
  })

  it('normalizes the durable task detail envelope for the shared task view', async () => {
    client.get.mockResolvedValue({
      data: {
        task: {
          task_id: 'imgtask_abc',
          platform: 'gemini',
          request_type: 'text_to_image',
          model: 'gemini-3-pro-image-preview',
          status: 'succeeded',
          requested_image_size: '2K',
          actual_image_size: '2048x2048',
          prompt_preview: 'A product photo',
          created_at: '2026-07-20T00:00:00Z',
        },
        results: [{ image_index: 1, byte_size: 1024, provider: 'qiniu' }],
        events: [{ id: 1, event_type: 'transition', to_status: 'succeeded', created_at: '2026-07-20T00:01:00Z' }],
      },
    })

    const task = await asyncImageTasksAPI.user.get('imgtask_abc')
    expect(task).toMatchObject({
      id: 'imgtask_abc',
      requested_size: '2K',
      actual_size: '2048x2048',
      prompt_summary: 'A product photo',
    })
    expect(task.results?.[0]).toMatchObject({ id: 'imgtask_abc:1', index: 1, size_bytes: 1024 })
    expect(task.events?.[0]).toMatchObject({ status: 'succeeded' })
  })

  it('keeps list result summaries and stable view links from the backend contract', async () => {
    client.get.mockResolvedValue({
      data: {
        items: [{
          task_id: 'asyncimg_list',
          platform: 'openai',
          request_type: 'image_to_image',
          model: 'gpt-image-1',
          status: 'succeeded',
          image_count: 2,
          result_count: 2,
          storage_provider: 'aliyun',
          preview_url: 'https://signed.example/result-1.png',
          view_url: '/api/v1/user/async-image-tasks/asyncimg_list/results/1/view',
          submitted_at: '2026-07-20T00:00:00Z',
        }],
        total: 1,
        page: 1,
        page_size: 20,
        pages: 1,
      },
    })

    const response = await asyncImageTasksAPI.user.list({
      page: 1,
      page_size: 20,
      status: 'succeeded',
      platform: 'openai',
    })

    expect(response.items[0]).toMatchObject({
      id: 'asyncimg_list',
      result_count: 2,
      storage_provider: 'aliyun',
      preview_url: 'https://signed.example/result-1.png',
      view_url: '/api/v1/user/async-image-tasks/asyncimg_list/results/1/view',
    })
    expect(client.get).toHaveBeenLastCalledWith('/user/async-image-tasks', expect.objectContaining({
      params: { page: 1, page_size: 20, status: 'succeeded', platform: 'openai' },
    }))
  })

  it('uses a dedicated admin resume action', async () => {
    client.post.mockResolvedValue({
      data: {
        task_id: 'imgtask_retry',
        platform: 'openai',
        request_type: 'text_to_image',
        model: 'gpt-image-1',
        status: 'uploading',
        created_at: '2026-07-20T00:00:00Z',
      },
    })

    await asyncImageTasksAPI.admin.resume('imgtask_retry')
    expect(client.post).toHaveBeenCalledWith('/admin/async-image-tasks/imgtask_retry/resume')
  })

  it('resolves a stable result view through the authenticated API client', async () => {
    client.get.mockResolvedValue({
      data: {
        url: 'https://oss.example/signed.png',
        expires_at: '2026-07-20T01:00:00Z',
      },
    })

    const access = await asyncImageTasksAPI.resolveView(
      '/api/v1/user/async-image-tasks/asyncimg_list/results/1/view',
    )

    expect(client.get).toHaveBeenCalledWith(
      '/api/v1/user/async-image-tasks/asyncimg_list/results/1/view',
      expect.objectContaining({ headers: { Accept: 'application/json' } }),
    )
    expect(access.url).toBe('https://oss.example/signed.png')
  })
})
