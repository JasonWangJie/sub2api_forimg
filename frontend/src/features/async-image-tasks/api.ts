import { apiClient } from '@/api/client'

import type {
  AsyncImageTask,
  AsyncImageTaskEvent,
  AsyncImageTaskListParams,
  AsyncImageTaskListResponse,
  AsyncImageTaskResult,
  AsyncImageResultAccess,
} from './types'

type Scope = 'user' | 'admin'

const scopePath = (scope: Scope) => `/${scope}/async-image-tasks`

type TaskWire = Partial<AsyncImageTask> & {
  task_id?: string
  requested_image_size?: string | null
  actual_image_size?: string | null
  prompt_preview?: string | null
}

type ResultWire = Partial<AsyncImageTaskResult> & {
  image_index?: number
  byte_size?: number
}

type EventWire = Partial<AsyncImageTaskEvent> & {
  event_type?: string
  to_status?: string | null
  payload?: { message?: string; error?: string } | null
}

type DetailsWire = {
  task?: TaskWire
  results?: ResultWire[]
  events?: EventWire[]
}

function normalizeTask(task: TaskWire): AsyncImageTask {
  const taskID = String(task.task_id || task.id || '')
  return {
    ...task,
    id: taskID,
    task_id: taskID,
    platform: task.platform || '',
    request_type: task.request_type || '',
    status: task.status || 'queued',
    model: task.model || '',
    requested_size: task.requested_size || task.requested_image_size || null,
    actual_size: task.actual_size || task.actual_image_size || null,
    prompt_summary: task.prompt_summary || task.prompt_preview || null,
    created_at: task.created_at || task.submitted_at || '',
  }
}

function normalizeResult(result: ResultWire, taskID: string, arrayIndex: number): AsyncImageTaskResult {
  const imageIndex = result.index ?? result.image_index ?? arrayIndex + 1
  return {
    ...result,
    id: result.id ?? `${taskID}:${imageIndex}`,
    index: imageIndex,
    size_bytes: result.size_bytes ?? result.byte_size ?? null,
  }
}

function normalizeEvent(event: EventWire): AsyncImageTaskEvent {
  return {
    ...event,
    status: event.status || event.to_status || event.event_type || 'queued',
    message: event.message || event.payload?.message || event.payload?.error || null,
    created_at: event.created_at || '',
  }
}

function normalizeDetails(data: AsyncImageTask | DetailsWire): AsyncImageTask {
  const wire = data as DetailsWire
  const task = normalizeTask(wire.task || (data as TaskWire))
  const rawResults = wire.task ? wire.results : (data as AsyncImageTask).results
  const rawEvents = wire.task ? wire.events : (data as AsyncImageTask).events
  return {
    ...task,
    results: (rawResults || []).map((result, index) => normalizeResult(result as ResultWire, String(task.task_id), index)),
    events: (rawEvents || []).map((event) => normalizeEvent(event as EventWire)),
  }
}

function normalizeListResponse(
  data: AsyncImageTaskListResponse | null | undefined,
  params: AsyncImageTaskListParams,
): AsyncImageTaskListResponse {
  const items = Array.isArray(data?.items) ? data.items.map((task) => normalizeTask(task)) : []
  const pageSize = Number(data?.page_size) || Number(params.page_size) || 20
  const total = Number(data?.total) || 0
  return {
    items,
    total,
    page: Number(data?.page) || Number(params.page) || 1,
    page_size: pageSize,
    pages: Number(data?.pages) || Math.ceil(total / pageSize),
  }
}

async function list(
  scope: Scope,
  params: AsyncImageTaskListParams,
  signal?: AbortSignal,
): Promise<AsyncImageTaskListResponse> {
  const { data } = await apiClient.get<AsyncImageTaskListResponse>(scopePath(scope), {
    params,
    signal,
  })
  return normalizeListResponse(data, params)
}

async function get(scope: Scope, id: string | number, signal?: AbortSignal): Promise<AsyncImageTask> {
  const { data } = await apiClient.get<AsyncImageTask | DetailsWire>(
    `${scopePath(scope)}/${encodeURIComponent(String(id))}`,
    { signal },
  )
  return normalizeDetails(data)
}

async function resume(id: string | number): Promise<AsyncImageTask> {
  const { data } = await apiClient.post<AsyncImageTask | DetailsWire>(
    `/admin/async-image-tasks/${encodeURIComponent(String(id))}/resume`,
  )
  return normalizeDetails(data)
}

async function resolveView(viewUrl: string, signal?: AbortSignal): Promise<AsyncImageResultAccess> {
  const { data } = await apiClient.get<AsyncImageResultAccess>(viewUrl, {
    headers: { Accept: 'application/json' },
    signal,
  })
  return data
}

export const asyncImageTasksAPI = {
  user: {
    list: (params: AsyncImageTaskListParams, signal?: AbortSignal) => list('user', params, signal),
    get: (id: string | number, signal?: AbortSignal) => get('user', id, signal),
  },
  admin: {
    list: (params: AsyncImageTaskListParams, signal?: AbortSignal) => list('admin', params, signal),
    get: (id: string | number, signal?: AbortSignal) => get('admin', id, signal),
    resume,
  },
  resolveView,
}

export default asyncImageTasksAPI
