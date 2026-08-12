/**
 * Image workbench clients. Gateway calls use the selected downstream API key;
 * capability and library calls use the signed-in dashboard session.
 */

import { apiClient } from './client'
import { buildGatewayUrl } from './url'
import { normalizeCapabilities } from '@/features/image-workflow/policy'
import type { ApiKey } from '@/types'
import type { ImageWorkbenchCapabilities } from '@/features/image-workflow/types'

export interface ImageGenerateParams {
  model: string
  prompt: string
  n?: number
  size?: string
  resolution?: string
  aspect_ratio?: string
  quality?: string
  response_format?: 'url' | 'b64_json'
  output_format?: string
  background?: string
  style?: string
}

export interface ImageGenerateResultItem {
  b64_json?: string
  url?: string
  revised_prompt?: string
  output_format?: string
  mime_type?: string
}

export interface ImageGenerateResponse {
  created?: number
  data: ImageGenerateResultItem[]
  output_format?: string
  error?: { message?: string; code?: string }
}

export interface AsyncImageSubmission {
  task_id: string
  query_url?: string
  status: string
  protocol: 'bb' | 'sc'
}

export interface PreparedAsyncSubmission {
  idempotency_key: string
  protocol: 'bb' | 'sc'
  send: (signal?: AbortSignal) => Promise<AsyncImageSubmission>
}

export interface AsyncImagePollResult {
  task_id: string
  status: 'queued' | 'processing' | 'succeeded' | 'failed'
  progress: number
  images: Array<{ url: string; expires_at?: number | null }>
  fail_reason?: string
}

export interface GeminiImageParams {
  model: string
  prompt: string
  resolution?: string
  /** Preferred SC field for aspect ratio (e.g. "3:2"). */
  size?: string
  /** Alias mapped to size when size is empty. */
  aspect_ratio?: string
  references?: Array<{ mimeType: string; base64: string }>
}

function createGatewayError(message: string, status?: number, code?: string | number): Error {
  const error = new Error(message) as Error & { status?: number; code?: string | number }
  error.status = status
  error.code = code
  return error
}

async function parseGatewayError(response: Response): Promise<Error> {
  try {
    const body = await response.json()
    return createGatewayError(
      body?.error?.message || body?.message || response.statusText || `HTTP ${response.status}`,
      response.status,
      body?.error?.code || body?.code || response.status,
    )
  } catch {
    return createGatewayError(response.statusText || `HTTP ${response.status}`, response.status, response.status)
  }
}

function authHeaders(apiKey: string, extra?: HeadersInit): HeadersInit {
  return { Authorization: `Bearer ${apiKey}`, ...extra }
}

function readBlobBytes(blob: Blob): Promise<ArrayBuffer> {
  if (typeof blob.arrayBuffer === 'function') return blob.arrayBuffer()
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(reader.result as ArrayBuffer)
    reader.onerror = () => reject(reader.error || new Error('Failed to read image bytes'))
    reader.readAsArrayBuffer(blob)
  })
}

async function gatewayJSON<T>(
  path: string,
  apiKey: string,
  init: RequestInit,
): Promise<T> {
  const response = await fetch(buildGatewayUrl(path), {
    ...init,
    headers: authHeaders(apiKey, init.headers),
  })
  if (!response.ok) throw await parseGatewayError(response)
  return response.json() as Promise<T>
}

function normalizeOutputFormat(value: unknown): string {
  const format = String(value || '').trim().toLowerCase()
  if (format === 'jpg') return 'jpeg'
  return format === 'png' || format === 'jpeg' || format === 'webp' ? format : ''
}

function normalizeImageResponse(
  response: ImageGenerateResponse,
  requestedFormat?: string,
): ImageGenerateResponse {
  const responseFormat = normalizeOutputFormat(response.output_format)
    || normalizeOutputFormat(requestedFormat)
  return {
    ...response,
    data: (response.data || []).map((item) => ({
      ...item,
      ...(normalizeOutputFormat(item.output_format) || responseFormat
        ? { output_format: normalizeOutputFormat(item.output_format) || responseFormat }
        : {}),
    })),
  }
}

export async function getCapabilities(
  apiKeyId: number,
  key: ApiKey,
  signal?: AbortSignal,
): Promise<ImageWorkbenchCapabilities> {
  const { data } = await apiClient.get(
    `/user/image-workbench/capabilities/${encodeURIComponent(String(apiKeyId))}`,
    { signal },
  )
  return normalizeCapabilities(data, key)
}

export async function generateImage(
  apiKey: string,
  params: ImageGenerateParams,
  signal?: AbortSignal,
): Promise<ImageGenerateResponse> {
  const payload = {
    model: params.model,
    prompt: params.prompt,
    n: params.n ?? 1,
    ...(params.resolution ? { resolution: params.resolution } : {}),
    ...(params.aspect_ratio ? { aspect_ratio: params.aspect_ratio } : {}),
    ...(params.size && !params.resolution ? { size: params.size } : {}),
    ...(params.quality ? { quality: params.quality } : {}),
    response_format: params.response_format || 'b64_json',
    ...(params.output_format ? { output_format: params.output_format } : {}),
    ...(params.background && params.background !== 'auto' ? { background: params.background } : {}),
    ...(params.style && params.style !== 'auto' ? { style: params.style } : {}),
  }

  const response = await gatewayJSON<ImageGenerateResponse>('/v1/images/generations', apiKey, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
    signal,
  })
  return normalizeImageResponse(response, params.output_format)
}

export async function editImage(
  apiKey: string,
  params: ImageGenerateParams & { imageFile?: File; imageFiles?: File[] },
  signal?: AbortSignal,
): Promise<ImageGenerateResponse> {
  const files = (params.imageFiles?.length ? params.imageFiles : params.imageFile ? [params.imageFile] : []).filter(Boolean)
  if (!files.length) throw new Error('image file is required')

  const form = new FormData()
  form.append('model', params.model)
  form.append('prompt', params.prompt)
  files.forEach((file) => form.append('image', file, file.name))
  form.append('n', String(params.n ?? 1))
  if (params.resolution) form.append('resolution', params.resolution)
  if (params.aspect_ratio) form.append('aspect_ratio', params.aspect_ratio)
  if (params.size && !params.resolution) form.append('size', params.size)
  if (params.quality) form.append('quality', params.quality)
  if (params.output_format) form.append('output_format', params.output_format)
  if (params.background && params.background !== 'auto') form.append('background', params.background)
  if (params.style && params.style !== 'auto') form.append('style', params.style)
  form.append('response_format', params.response_format || 'b64_json')

  const response = await gatewayJSON<ImageGenerateResponse>('/v1/images/edits', apiKey, {
    method: 'POST', body: form, signal,
  })
  return normalizeImageResponse(response, params.output_format)
}

export async function generateGeminiImage(
  apiKey: string,
  params: GeminiImageParams,
  signal?: AbortSignal,
): Promise<ImageGenerateResponse> {
  const parts: Array<Record<string, unknown>> = (params.references || []).map((reference) => ({
    inlineData: { mimeType: reference.mimeType, data: reference.base64 },
  }))
  parts.push({ text: params.prompt })

  const imageConfig: Record<string, string> = {}
  if (params.resolution) imageConfig.imageSize = params.resolution
  if (params.aspect_ratio && params.aspect_ratio !== 'auto') imageConfig.aspectRatio = params.aspect_ratio
  const payload = {
    contents: [{ role: 'user', parts }],
    generationConfig: {
      responseModalities: ['TEXT', 'IMAGE'],
      ...(Object.keys(imageConfig).length ? { imageConfig } : {}),
    },
  }
  const response = await gatewayJSON<any>(
    `/v1beta/models/${encodeURIComponent(params.model)}:generateContent`,
    apiKey,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
      signal,
    },
  )

  const data: ImageGenerateResultItem[] = []
  for (const candidate of response?.candidates || []) {
    for (const part of candidate?.content?.parts || []) {
      const inline = part?.inlineData || part?.inline_data
      if (inline?.data) data.push({
        b64_json: inline.data,
        mime_type: String(inline.mimeType || inline.mime_type || ''),
      })
    }
  }
  return { data }
}

export async function prepareOpenAIAsyncSubmission(
  apiKey: string,
  params: ImageGenerateParams & { imageFiles?: File[] },
  idempotencyKey: string,
): Promise<PreparedAsyncSubmission> {
  const editing = Boolean(params.imageFiles?.length)
  // Unified async OpenAI entry: generations_oa auto-routes to image-to-image
  // when multipart image files (or image_urls) are present.
  const path = '/v1/images/generations_oa'
  let body: BodyInit
  let headers: HeadersInit = { 'Idempotency-Key': idempotencyKey }

  if (editing) {
    // Serialize multipart once. Retrying a FormData object would generate a new
    // boundary and therefore a different server request hash.
    const boundary = `----sub2api-${idempotencyKey.replace(/[^a-zA-Z0-9]/g, '').slice(-48)}`
    const chunks: BlobPart[] = []
    const field = (name: string, value: string) => {
      chunks.push(`--${boundary}\r\nContent-Disposition: form-data; name="${name}"\r\n\r\n${value}\r\n`)
    }
    field('model', params.model)
    field('prompt', params.prompt)
    field('n', String(params.n ?? 1))
    if (params.resolution) field('resolution', params.resolution)
    if (params.aspect_ratio) field('aspect_ratio', params.aspect_ratio)
    if (params.size && !params.resolution) field('size', params.size)
    if (params.quality) field('quality', params.quality)
    if (params.output_format) field('output_format', params.output_format)
    if (params.background && params.background !== 'auto') field('background', params.background)
    if (params.style && params.style !== 'auto') field('style', params.style)
    for (const file of params.imageFiles!) {
      const filename = file.name.replace(/["\r\n]/g, '_') || 'image.png'
      chunks.push(`--${boundary}\r\nContent-Disposition: form-data; name="image"; filename="${filename}"\r\nContent-Type: ${file.type || 'application/octet-stream'}\r\n\r\n`)
      chunks.push(await readBlobBytes(file))
      chunks.push('\r\n')
    }
    chunks.push(`--${boundary}--\r\n`)
    body = new Blob(chunks)
    headers = { ...headers, 'Content-Type': `multipart/form-data; boundary=${boundary}` }
  } else {
    headers = { ...headers, 'Content-Type': 'application/json' }
    body = JSON.stringify({
      model: params.model,
      prompt: params.prompt,
      n: params.n ?? 1,
      ...(params.resolution ? { resolution: params.resolution } : {}),
      ...(params.aspect_ratio ? { aspect_ratio: params.aspect_ratio } : {}),
      ...(params.size && !params.resolution ? { size: params.size } : {}),
      quality: params.quality,
      output_format: params.output_format,
      background: params.background,
      style: params.style,
      stream: false,
    })
  }

  return {
    idempotency_key: idempotencyKey,
    protocol: 'bb',
    send: async (signal?: AbortSignal) => {
      const result = await gatewayJSON<any>(path, apiKey, { method: 'POST', headers, body, signal })
      const taskID = String(result?.task_id || result?.id || '')
      if (!taskID) throw new Error('Task submission did not return a task ID')
      return { task_id: taskID, query_url: result.query_url, status: result.status || 'queued', protocol: 'bb' }
    },
  }
}

export async function submitOpenAIAsync(
  apiKey: string,
  params: ImageGenerateParams & { imageFiles?: File[] },
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<AsyncImageSubmission> {
  return (await prepareOpenAIAsyncSubmission(apiKey, params, idempotencyKey)).send(signal)
}

export async function uploadGeminiReference(
  apiKey: string,
  file: File,
  signal?: AbortSignal,
): Promise<string> {
  const form = new FormData()
  form.append('file', file, file.name)
  const result = await gatewayJSON<any>('/v1/uploads/images_sc', apiKey, {
    method: 'POST', body: form, signal,
  })
  const url = String(result?.url || result?.data?.url || '')
  if (!url) throw new Error('Reference upload did not return a URL')
  return url
}

export function prepareGeminiAsyncSubmission(
  apiKey: string,
  params: GeminiImageParams & { image_urls?: string[] },
  idempotencyKey: string,
): PreparedAsyncSubmission {
  // SC dialect prefers size as the aspect-ratio field (e.g. "3:2"); aspect_ratio remains accepted by the API.
  const size = params.size || params.aspect_ratio
  const body = JSON.stringify({
    model: params.model,
    prompt: params.prompt,
    ...(params.image_urls?.length ? { image_urls: params.image_urls } : {}),
    ...(params.resolution ? { resolution: params.resolution } : {}),
    ...(size ? { size } : {}),
  })
  return {
    idempotency_key: idempotencyKey,
    protocol: 'sc',
    send: async (signal?: AbortSignal) => {
      const result = await gatewayJSON<any>('/v1/images/generations_sc', apiKey, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey },
        body,
        signal,
      })
      const taskID = String(result?.task_id || result?.id || '')
      if (!taskID) throw new Error('Task submission did not return a task ID')
      return { task_id: taskID, query_url: result.query_url, status: result.status || 'queued', protocol: 'sc' }
    },
  }
}

export async function submitGeminiAsync(
  apiKey: string,
  params: GeminiImageParams & { image_urls?: string[] },
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<AsyncImageSubmission> {
  return prepareGeminiAsyncSubmission(apiKey, params, idempotencyKey).send(signal)
}

export async function pollAsyncImage(
  apiKey: string,
  taskID: string,
  protocol: 'bb' | 'sc',
  signal?: AbortSignal,
): Promise<AsyncImagePollResult> {
  // OpenAI and Gemini async share GET /v1/images/tasks_async/{task_id}.
  void protocol
  const path = `/v1/images/tasks_async/${encodeURIComponent(taskID)}`
  const result = await gatewayJSON<any>(path, apiKey, { method: 'GET', signal })

  const status = result?.status === 'succeeded'
    ? 'succeeded'
    : result?.status === 'failed'
      ? 'failed'
      : result?.status === 'queued'
        ? 'queued'
        : 'processing'
  return {
    task_id: String(result?.task_id || taskID),
    status,
    progress: Number(result?.progress ?? (status === 'succeeded' ? 100 : 0)),
    images: (result?.data || []).map((item: any) => ({ url: String(item?.url || '') })).filter((item: any) => item.url),
    fail_reason: result?.fail_reason,
  }
}

function detectBase64ImageFormat(value: string): string {
  try {
    const normalized = value.replace(/\s+/g, '')
    const prefixLength = Math.min(normalized.length, 24)
    const prefix = atob(normalized.slice(0, prefixLength - (prefixLength % 4)))
    const byte = (index: number) => prefix.charCodeAt(index)
    if (byte(0) === 0x89 && prefix.slice(1, 4) === 'PNG') return 'png'
    if (byte(0) === 0xff && byte(1) === 0xd8 && byte(2) === 0xff) return 'jpeg'
    if (prefix.slice(0, 4) === 'RIFF' && prefix.slice(8, 12) === 'WEBP') return 'webp'
  } catch {
    // Let the caller use trusted response metadata when the payload is malformed.
  }
  return ''
}

function imageMIMEType(format: string): string {
  return format === 'jpeg' ? 'image/jpeg' : format === 'webp' ? 'image/webp' : 'image/png'
}

export function resultToDataUrl(item: ImageGenerateResultItem, format = 'png'): string | null {
  if (item.b64_json) {
    const actualFormat = detectBase64ImageFormat(item.b64_json)
    const metadataFormat = normalizeOutputFormat(item.output_format)
      || normalizeOutputFormat(String(item.mime_type || '').replace(/^image\//i, ''))
    const mime = imageMIMEType(actualFormat || metadataFormat || normalizeOutputFormat(format) || 'png')
    return `data:${mime};base64,${item.b64_json}`
  }
  return item.url || null
}

export function createImageIdempotencyKey(): string {
  const random = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
  return `image-workbench-${random}`
}
