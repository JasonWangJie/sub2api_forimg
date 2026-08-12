import { apiClient } from './client'
import type {
  CursorPage,
  ImageCleanupJob,
  ImageLibraryItem,
  ImageLibraryMigrationState,
  ImageLibraryStats,
  ImagePlazaSubmissionRequest,
  ImagePublicationRecord,
  ImageReportRecord,
} from '@/features/image-workflow/types'

export interface ImageLibraryListParams {
  cursor?: string
  limit?: number
  q?: string
  platform?: string
  source?: string
  visibility?: string
  publication_status?: string
}

function normalizePage<T>(data: any): CursorPage<T> {
  return {
    items: Array.isArray(data?.items) ? data.items : [],
    next_cursor: data?.next_cursor || null,
    total: data?.total == null ? undefined : Number(data.total),
  }
}

function normalizeLibraryItem(item: any): ImageLibraryItem {
  const publication = item?.publication || null
  return {
    ...item,
    id: String(item?.id || item?.asset_id || ''),
    asset_id: String(item?.id || item?.asset_id || ''),
    source: item?.source || item?.source_type || '',
    execution_mode: item?.execution_mode || item?.generation_mode || 'realtime',
    prompt: item?.prompt ?? item?.private_prompt ?? null,
    task_id: item?.task_id ?? item?.source_task_id ?? null,
    result_index: item?.result_index ?? item?.source_result_index ?? null,
    publication_status: item?.publication_status ?? publication?.status ?? null,
    publication_id: item?.publication_id ?? publication?.id ?? null,
    share_prompt: item?.share_prompt ?? publication?.share_prompt ?? false,
    view_url: item?.view_url || item?.image_url || `/api/v1/user/image-library/${encodeURIComponent(String(item?.id || ''))}/view`,
    preview_url: item?.preview_url || item?.image_url || null,
    error_message: item?.error_message || item?.archive_error || null,
  }
}

export async function listImageLibrary(
  params: ImageLibraryListParams = {},
  signal?: AbortSignal,
): Promise<CursorPage<ImageLibraryItem>> {
  const { publication_status, source, ...rest } = params
  const { data } = await apiClient.get('/user/image-library', {
    params: {
      ...rest,
      status: publication_status,
      source_type: source,
    },
    signal,
  })
  const page = normalizePage<any>(data)
  return { ...page, items: page.items.map(normalizeLibraryItem) }
}

export async function importImageFile(
  file: File,
  metadata: Record<string, unknown>,
  idempotencyKey: string,
): Promise<ImageLibraryItem> {
  const form = new FormData()
  form.append('file', file, file.name)
  const fields = {
    api_key_id: metadata.api_key_id,
    group_id: metadata.group_id,
    platform: metadata.platform,
    generation_mode: metadata.generation_mode ?? metadata.execution_mode,
    source_type: metadata.source_type ?? metadata.source,
    model: metadata.model,
    requested_size: metadata.requested_size,
    actual_size: metadata.actual_size,
    aspect_ratio: metadata.aspect_ratio,
    quality: metadata.quality,
    title: metadata.title,
    prompt: metadata.prompt,
  }
  Object.entries(fields).forEach(([key, value]) => {
    if (value != null && value !== '') form.append(key, typeof value === 'string' ? value : JSON.stringify(value))
  })
  const { data } = await apiClient.post<ImageLibraryItem>('/user/image-library/import', form, {
    headers: { 'Content-Type': 'multipart/form-data', 'Idempotency-Key': idempotencyKey },
  })
  return normalizeLibraryItem((data as any)?.item || data)
}

export async function importImageURL(
  url: string,
  metadata: Record<string, unknown>,
  idempotencyKey: string,
): Promise<ImageLibraryItem> {
  const { data } = await apiClient.post<ImageLibraryItem>(
    '/user/image-library/import-url',
    {
      image_url: url,
      api_key_id: metadata.api_key_id,
      group_id: metadata.group_id,
      platform: metadata.platform,
      generation_mode: metadata.generation_mode ?? metadata.execution_mode,
      source_type: metadata.source_type ?? metadata.source,
      model: metadata.model,
      requested_size: metadata.requested_size,
      actual_size: metadata.actual_size,
      aspect_ratio: metadata.aspect_ratio,
      quality: metadata.quality,
      title: metadata.title,
      prompt: metadata.prompt,
    },
    { headers: { 'Idempotency-Key': idempotencyKey } },
  )
  return normalizeLibraryItem((data as any)?.item || data)
}

export async function archiveAsyncTask(
  taskID: string,
  resultIndexes?: number[],
): Promise<ImageLibraryItem[]> {
  const indexes = resultIndexes?.length ? resultIndexes : [0]
  return Promise.all(indexes.map(async (imageIndex) => {
    const { data } = await apiClient.post<ImageLibraryItem | { item: ImageLibraryItem; reused?: boolean }>(
      '/user/image-library/from-task',
      { task_id: taskID, image_index: imageIndex },
    )
    return normalizeLibraryItem('item' in data ? data.item : data)
  }))
}

export async function getImageLibraryItem(id: string | number): Promise<ImageLibraryItem> {
  const { data } = await apiClient.get<ImageLibraryItem>(`/user/image-library/${encodeURIComponent(String(id))}`)
  return normalizeLibraryItem(data)
}

export async function updateImageLibraryItem(
  id: string | number,
  patch: { title?: string; prompt?: string },
): Promise<ImageLibraryItem> {
  const { data } = await apiClient.patch<ImageLibraryItem>(
    `/user/image-library/${encodeURIComponent(String(id))}`,
    { title: patch.title, private_prompt: patch.prompt },
  )
  return normalizeLibraryItem(data)
}

export async function deleteImageLibraryItem(id: string | number): Promise<void> {
  await apiClient.delete(`/user/image-library/${encodeURIComponent(String(id))}`)
}

export async function publishImageLibraryItem(
  id: string | number,
  payload: { title?: string; share_prompt: boolean; public_prompt?: string },
): Promise<ImageLibraryItem> {
  const { data } = await apiClient.post<ImageLibraryItem>(
    `/user/image-library/${encodeURIComponent(String(id))}/publications`,
    { public_title: payload.title, share_prompt: payload.share_prompt, public_prompt: payload.public_prompt },
  )
  return normalizeLibraryItem({ ...data, id, publication: data })
}

export async function createPlazaSubmissionRequest(
  payload: Record<string, unknown>,
  idempotencyKey: string,
): Promise<ImagePlazaSubmissionRequest> {
  const { data } = await apiClient.post<{ item: ImagePlazaSubmissionRequest; reused?: boolean }>(
    '/user/image-library/submission-requests',
    payload,
    { headers: { 'Idempotency-Key': idempotencyKey } },
  )
  return (data as any)?.item || data
}

export async function listMyPlazaSubmissionRequests(params: {
  cursor?: string
  limit?: number
  status?: string
} = {}): Promise<CursorPage<ImagePlazaSubmissionRequest>> {
  const { data } = await apiClient.get('/user/image-library/submission-requests', { params })
  return normalizePage<ImagePlazaSubmissionRequest>(data)
}

export async function withdrawPlazaSubmissionRequest(requestId: string): Promise<void> {
  await apiClient.delete(`/user/image-library/submission-requests/${encodeURIComponent(requestId)}`)
}

export async function syncPlazaSubmissionRequest(
  requestId: string,
  file: File | Blob,
  fileName = 'sync-image.png',
): Promise<{ item: ImagePlazaSubmissionRequest; library_item: ImageLibraryItem }> {
  const form = new FormData()
  form.append('file', file, fileName)
  const { data } = await apiClient.post<{ item: ImagePlazaSubmissionRequest; library_item: ImageLibraryItem }>(
    `/user/image-library/submission-requests/${encodeURIComponent(requestId)}/sync`,
    form,
    { headers: { 'Content-Type': 'multipart/form-data' } },
  )
  return {
    item: data.item,
    library_item: normalizeLibraryItem(data.library_item),
  }
}

export async function listAdminPlazaSubmissionRequests(params: {
  cursor?: string
  limit?: number
  status?: string
} = {}): Promise<CursorPage<ImagePlazaSubmissionRequest>> {
  const { data } = await apiClient.get('/admin/image-plaza/submission-requests', { params })
  return normalizePage<ImagePlazaSubmissionRequest>(data)
}

export async function reviewPlazaSubmissionRequest(
  requestId: string,
  action: 'approve' | 'reject',
  reason?: string,
): Promise<ImagePlazaSubmissionRequest> {
  const { data } = await apiClient.post<ImagePlazaSubmissionRequest>(
    `/admin/image-plaza/submission-requests/${encodeURIComponent(requestId)}/${action}`,
    reason ? { reason } : {},
  )
  return data
}

export async function withdrawImageLibraryItem(id: string | number): Promise<void> {
  await apiClient.delete(
    `/user/image-library/${encodeURIComponent(String(id))}/publication`,
  )
}

export function imageLibraryViewURL(item: Pick<ImageLibraryItem, 'id' | 'view_url' | 'preview_url'>): string {
  return item.view_url || item.preview_url || `/api/v1/user/image-library/${encodeURIComponent(String(item.id))}/view`
}

export async function resolveImageLibraryViewURL(
  id: string | number,
  admin = false,
): Promise<{ url: string; expires_at?: string | null }> {
  const scope = admin ? 'admin' : 'user'
  const { data } = await apiClient.get<{ url: string; expires_at?: string | null }>(
    `/${scope}/image-library/${encodeURIComponent(String(id))}/view`,
    { headers: { Accept: 'application/json' } },
  )
  if (!data?.url) throw new Error('Image view URL is unavailable')
  return data
}

export interface AdminPublicationParams {
  cursor?: string
  limit?: number
  q?: string
  status?: string
  platform?: string
  model?: string
  user_id?: number
}

export async function listAdminPublications(params: AdminPublicationParams = {}): Promise<CursorPage<ImagePublicationRecord>> {
  const { data } = await apiClient.get('/admin/image-plaza/publications', { params })
  const page = normalizePage<any>(data)
  return {
    ...page,
    items: page.items.map((item) => ({
      ...item,
      id: String(item.id || ''),
      publication_id: String(item.id || ''),
      image_url: String(item.preview_url || item.image_url || ''),
      public_identity: String(item.creator || 'Member'),
      user_label: String(item.creator || 'Member'),
      share_prompt: Boolean(item.prompt),
    })),
  }
}

export async function reviewPublication(
  id: string | number,
  action: 'approve' | 'reject' | 'hide' | 'restore',
  reason?: string,
): Promise<ImagePublicationRecord> {
  const { data } = await apiClient.post<ImagePublicationRecord>(
    `/admin/image-plaza/publications/${encodeURIComponent(String(id))}/${action}`,
    reason ? { reason } : {},
  )
  return data
}

export async function listAdminReports(params: { cursor?: string; limit?: number; status?: string } = {}): Promise<CursorPage<ImageReportRecord>> {
	const wireParams = { ...params, status: params.status === 'pending' ? 'open' : params.status }
	const { data } = await apiClient.get('/admin/image-plaza/reports', { params: wireParams })
	const page = normalizePage<any>(data)
	return {
		...page,
		items: page.items.map((item) => ({
			...item,
			detail: item.detail ?? item.details ?? null,
			status: item.status === 'open' ? 'pending' : item.status,
		})),
	}
}

export async function resolveAdminReport(
  id: string | number,
  payload: { status: 'resolved' | 'dismissed'; resolution?: string },
): Promise<ImageReportRecord> {
  const { data } = await apiClient.post<ImageReportRecord>(
    `/admin/image-plaza/reports/${encodeURIComponent(String(id))}/resolve`,
    payload,
  )
  return data
}

export async function listAdminImageLibrary(params: ImageLibraryListParams & { user_id?: number } = {}): Promise<CursorPage<ImageLibraryItem>> {
  const { publication_status, source, ...rest } = params
  const { data } = await apiClient.get('/admin/image-library', {
    params: { ...rest, status: publication_status, source_type: source },
  })
  const page = normalizePage<any>(data)
  return { ...page, items: page.items.map(normalizeLibraryItem) }
}

export async function getAdminImageLibraryStats(): Promise<ImageLibraryStats> {
  const { data } = await apiClient.get<ImageLibraryStats>('/admin/image-library/stats')
  return {
    ...data,
    private_count: data.private_count ?? Math.max(0, Number(data.item_count || 0) - Number(data.published || 0)),
    pending_review_count: data.pending_review_count ?? Number(data.pending_review || 0),
    published_count: data.published_count ?? Number(data.published || 0),
  }
}

export async function listCleanupJobs(params: { cursor?: string; limit?: number } = {}): Promise<CursorPage<ImageCleanupJob>> {
  const { data } = await apiClient.get('/admin/image-library/cleanup-jobs', { params })
  const page = normalizePage<any>(data)
  return {
    ...page,
    items: page.items.map((item) => ({
      ...item,
      matched_items: item.matched_items ?? item.scanned_count,
      deleted_items: item.deleted_items ?? item.deleted_count,
      error_message: item.error_message ?? item.last_error,
    })),
  }
}

export async function previewCleanup(payload: Record<string, unknown>): Promise<{ matched_items: number; matched_bytes: number }> {
  const { scope, ...filters } = payload
  const { data } = await apiClient.post('/admin/image-library/cleanup-jobs/preview', { scope, filters })
  return data
}

export async function createCleanupJob(payload: Record<string, unknown>): Promise<ImageCleanupJob> {
  const { scope, ...filters } = payload
  const { data } = await apiClient.post<ImageCleanupJob>('/admin/image-library/cleanup-jobs', { scope, filters })
  return data
}

export async function getImageLibraryMigrationState(): Promise<ImageLibraryMigrationState> {
  const { data } = await apiClient.get<ImageLibraryMigrationState>('/admin/image-library/migration')
  return data
}
