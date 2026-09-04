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
    client.get.mockResolvedValue({ data: { items: [], total: 0, page: 1, page_size: 20, pages: 0, stats: { active: 0, completed: 0, failed: 0, success_rate: 0, average_duration_ms: null } } })

    await asyncImageTasksAPI.user.list({ page: 1, page_size: 20 })
    expect(client.get).toHaveBeenLastCalledWith('/user/async-image-tasks', expect.objectContaining({
      params: { page: 1, page_size: 20 },
    }))

    await asyncImageTasksAPI.admin.list({ page: 2, page_size: 50, q: 'final-account' })
    expect(client.get).toHaveBeenLastCalledWith('/admin/async-image-tasks', expect.objectContaining({
      params: { page: 2, page_size: 50, q: 'final-account' },
    }))
  })

  it('keeps an empty completed-duration average as null', async () => {
    client.get.mockResolvedValue({ data: { items: [], total: 0, page: 1, page_size: 20, pages: 0, stats: { active: 1, completed: 0, failed: 0, success_rate: 0, average_duration_ms: null } } })

    const response = await asyncImageTasksAPI.user.list({ page: 1, page_size: 20 })

    expect(response.stats?.average_duration_ms).toBeNull()
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

  it('keeps upstream image_url fetch retry events visible in the timeline', async () => {
    client.get.mockResolvedValue({
      data: {
        task: {
          task_id: 'asyncimg_retry',
          platform: 'openai',
          request_type: 'image_to_image',
          model: 'gpt-image-2',
          status: 'queued',
          retry_count: 1,
          error_code: 'upstream_image_url_fetch_timeout',
          error_message: '参考图 URL 拉取超时，已安排自动重试（1/1）：image_url fetch failed',
          created_at: '2026-07-20T00:00:00Z',
        },
        results: [],
        events: [{
          id: 2,
          event_type: 'upstream_image_url_fetch_retry',
          to_status: 'queued',
          payload: { message: '参考图 URL 拉取超时，已安排自动重试（1/1）' },
          created_at: '2026-07-20T00:01:00Z',
        }],
      },
    })

    const task = await asyncImageTasksAPI.user.get('asyncimg_retry')
    expect(task.events?.[0]).toMatchObject({
      status: 'upstream_image_url_fetch_retry',
      message: '参考图 URL 拉取超时，已安排自动重试（1/1）',
    })
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
        stats: { active: 2, completed: 13, failed: 7, success_rate: 65, average_duration_ms: 1234.5 },
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
    expect(response.stats).toEqual({ active: 2, completed: 13, failed: 7, success_rate: 65, average_duration_ms: 1234.5 })
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

  it('uses a dedicated admin terminate action', async () => {
    client.post.mockResolvedValue({
      data: {
        task_id: 'imgtask_hung',
        platform: 'gemini',
        request_type: 'text_to_image',
        model: 'gemini-3-pro-image-preview',
        status: 'failed',
        created_at: '2026-07-20T00:00:00Z',
      },
    })

    await asyncImageTasksAPI.admin.terminate('imgtask_hung')
    expect(client.post).toHaveBeenCalledWith('/admin/async-image-tasks/imgtask_hung/terminate')
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
