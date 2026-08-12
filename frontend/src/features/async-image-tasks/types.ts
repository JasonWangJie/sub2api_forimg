export type AsyncImageTaskPlatform = 'gemini' | 'openai'
export type AsyncImageTaskProtocol = 'bb' | 'sc'
export type AsyncImageTaskRequestType = 'text_to_image' | 'image_to_image'
export type AsyncImageStorageProvider = 'qiniu' | 'aliyun' | 'tencent' | 'custom_s3' | string

export type AsyncImageTaskStatus =
  | 'queued'
  | 'invoking'
  | 'upstream_succeeded'
  | 'uploading'
  | 'billing_pending'
  | 'succeeded'
  | 'failed'
  | 'execution_unknown'
  | 'storage_failed'
  | 'billing_failed'
  | 'expired'

export interface AsyncImageTaskResult {
  id: string | number
  index?: number
  provider?: AsyncImageStorageProvider | null
  bucket?: string | null
  object_key?: string | null
  content_type?: string | null
  size_bytes?: number | null
  checksum?: string | null
  width?: number | null
  height?: number | null
  url?: string | null
  view_url?: string | null
  preview_url?: string | null
  expires_at?: string | null
  created_at?: string | null
}

export interface AsyncImageTaskEvent {
  id?: string | number
  status: AsyncImageTaskStatus | string
  message?: string | null
  created_at: string
}

export interface AsyncImageTask {
  id: string | number
  task_id?: string
  protocol?: AsyncImageTaskProtocol | string | null
  platform: AsyncImageTaskPlatform | string
  request_type: AsyncImageTaskRequestType | string
  status: AsyncImageTaskStatus | string
  progress?: number | null
  model: string
  requested_size?: string | null
  actual_size?: string | null
  image_size?: string | null
  aspect_ratio?: string | null
  image_count?: number | null
  result_count?: number | null
  prompt_summary?: string | null

  user_id?: number | null
  user_email?: string | null
  api_key_id?: number | null
  api_key_name?: string | null
  group_id?: number | null
  group_name?: string | null
  account_id?: number | null
  account_name?: string | null

  actual_cost?: number | null
  currency?: string | null
  billing_type?: number | string | null
  billing_mode?: string | null
  billing_status?: string | null
  storage_provider?: AsyncImageStorageProvider | null
  preview_url?: string | null
  view_url?: string | null
  retry_count?: number | null
  upstream_request_id?: string | null
  error_code?: string | null
  error_message?: string | null
  can_resume?: boolean

  submitted_at?: string | null
  created_at: string
  started_at?: string | null
  finished_at?: string | null
  updated_at?: string | null
  expires_at?: string | null
  duration_ms?: number | null
  results?: AsyncImageTaskResult[]
  events?: AsyncImageTaskEvent[]
}

export interface AsyncImageTaskListParams {
  page?: number
  page_size?: number
  q?: string
  status?: string
  platform?: string
  request_type?: string
  model?: string
  api_key_id?: number
  user_id?: number
  group_id?: number
  storage_provider?: string
  start_date?: string
  end_date?: string
  sort_by?: string
  sort_order?: 'asc' | 'desc'
}

export interface AsyncImageTaskListResponse {
  items: AsyncImageTask[]
  total: number
  page: number
  page_size: number
  pages: number
}

export interface AsyncImageResultAccess {
  url: string
  expires_at?: string | null
}
