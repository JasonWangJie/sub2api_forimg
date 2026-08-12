/** Public, approved Image Plaza APIs (dashboard JWT session). */

import { apiClient } from './client'
import type { CursorPage, ImagePlazaItem } from '@/features/image-workflow/types'

export type { ImagePlazaItem }

export const IMAGE_PLAZA_REPORT_REASONS = [
  'spam',
  'sexual',
  'violence',
  'copyright',
  'privacy',
  'other',
] as const

export type ImagePlazaReportReason = (typeof IMAGE_PLAZA_REPORT_REASONS)[number]

export interface ImagePlazaListParams {
  q?: string
  cursor?: string
  limit?: number
  platform?: string
  model?: string
  aspect_ratio?: string
  sort?: 'newest' | 'oldest'
}

function normalizeItem(item: any): ImagePlazaItem {
  const id = item?.publication_id ?? item?.id
  const prompt = item?.share_prompt === false ? null : (item?.prompt ?? null)
  return {
    ...item,
    id,
    publication_id: item?.publication_id ?? id,
    asset_id: item?.asset_id ?? null,
    title: String(item?.title || prompt || ''),
    prompt,
    share_prompt: item?.share_prompt !== false && Boolean(prompt),
    platform: String(item?.platform || ''),
    model: String(item?.model || ''),
    public_identity: String(item?.public_identity || item?.publisher_name || item?.user_label || 'Member'),
    is_owner: Boolean(item?.is_owner),
    image_url: String(item?.image_url || item?.view_url || ''),
    published_at: String(item?.published_at || item?.created_at || ''),
  }
}

export async function listImagePlaza(
  params: ImagePlazaListParams = {},
  signal?: AbortSignal,
): Promise<CursorPage<ImagePlazaItem>> {
  const { data } = await apiClient.get('/image-plaza', { params, signal })
  return {
    items: Array.isArray(data?.items) ? data.items.map(normalizeItem) : [],
    next_cursor: data?.next_cursor || null,
    total: data?.total == null ? undefined : Number(data.total),
  }
}

export async function reportImagePlaza(
  publicationID: string | number,
  payload: { reason: ImagePlazaReportReason; detail?: string },
): Promise<void> {
  await apiClient.post(`/image-plaza/${encodeURIComponent(String(publicationID))}/reports`, {
    reason: payload.reason,
    details: payload.detail,
  })
}

export function resolvePlazaImageUrl(item: Pick<ImagePlazaItem, 'id' | 'image_url' | 'view_url'>): string {
  return item.image_url || item.view_url || `/api/v1/image-plaza/${encodeURIComponent(String(item.id))}/content`
}
