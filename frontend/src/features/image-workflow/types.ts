export type ImageWorkbenchPlatform = 'openai' | 'gemini' | 'grok'
export type ImageExecutionMode = 'realtime' | 'async'
export type ImageAsyncProtocol = 'bb' | 'sc'

export interface ImageModelCapability {
  id: string
  label: string
}

export interface ImageWorkbenchCapabilities {
  api_key_id: number
  group_id: number | null
  capability_version: string
  platform: ImageWorkbenchPlatform
  execution_mode: ImageExecutionMode
  async_protocol: ImageAsyncProtocol | null
  models: ImageModelCapability[]
  sizes: string[]
  resolutions: string[]
  aspect_ratios: string[]
  qualities: string[]
  output_formats: string[]
  backgrounds: string[]
  styles: string[]
  max_images: number
  max_reference_images: number
  supports_reference_images: boolean
}

export interface WorkbenchAccess {
  supported: boolean
  platform: ImageWorkbenchPlatform | null
  mode: ImageExecutionMode | null
  protocol: ImageAsyncProtocol | null
  reason?: 'inactive' | 'image_disabled' | 'unsupported_platform'
}

export type ImageLibraryVisibility = 'private' | 'public'
export type ImageLibraryArchiveStatus = 'ready' | 'pending' | 'failed' | string
export type ImagePublicationStatus =
  | 'pending_review'
  | 'published'
  | 'rejected'
  | 'withdrawn'
  | 'admin_hidden'
  | 'expired'

export type ImagePlazaSubmissionStatus =
  | 'pending_review'
  | 'approved_pending_sync'
  | 'rejected'
  | 'withdrawn'
  | 'synced'

export interface ImagePlazaSubmissionRequest {
  id: string
  user_id?: number
  status: ImagePlazaSubmissionStatus | string
  title: string
  private_prompt?: string
  public_title: string
  public_prompt?: string | null
  share_prompt: boolean
  platform: string
  generation_mode: string
  source_type: string
  model: string
  requested_size?: string
  aspect_ratio?: string
  quality?: string
  content_type: string
  byte_size: number
  checksum_sha256: string
  client_blob_key: string
  library_item_id?: string | null
  publication_id?: string | null
  review_reason?: string | null
  reviewed_at?: string | null
  expires_at: string
  created_at: string
  updated_at: string
  image_held_client_side?: boolean
}

export interface ImageLibraryItem {
  id: string | number
  asset_id?: string | number
  source: 'realtime_import' | 'async_task' | 'legacy_plaza' | string
  platform: string
  execution_mode: ImageExecutionMode | string
  model: string
  title: string
  prompt?: string | null
  requested_size?: string | null
  actual_size?: string | null
  aspect_ratio?: string | null
  quality?: string | null
  output_format?: string | null
  width?: number | null
  height?: number | null
  byte_size?: number | null
  visibility: ImageLibraryVisibility | string
  archive_status?: ImageLibraryArchiveStatus
  publication_status?: ImagePublicationStatus | null
  publication_id?: string | number | null
  share_prompt?: boolean
  task_id?: string | null
  result_index?: number | null
  view_url: string
  preview_url?: string | null
  created_at: string
  expires_at?: string | null
  error_message?: string | null
}

export interface CursorPage<T> {
  items: T[]
  next_cursor: string | null
  total?: number
}

export interface ImagePlazaItem {
  id: string | number
  publication_id?: string | number
  asset_id?: string | number | null
  title: string
  prompt?: string | null
  share_prompt: boolean
  platform: string
  model: string
  size?: string | null
  aspect_ratio?: string | null
  width?: number | null
  height?: number | null
  public_identity: string
  is_owner: boolean
  image_url: string
  view_url?: string | null
  published_at: string
  created_at?: string
}

export interface ImagePublicationRecord extends ImagePlazaItem {
  status: ImagePublicationStatus
  user_id?: number
  user_label?: string
  submitted_at?: string
  reviewed_at?: string | null
  review_reason?: string | null
}

export interface ImageReportRecord {
  id: string | number
  publication_id: string | number
  reason: string
  detail?: string | null
  status: 'pending' | 'resolved' | 'dismissed' | string
  reporter_label?: string | null
  created_at: string
  resolved_at?: string | null
}

export interface ImageLibraryStats {
  item_count?: number
  object_count: number
  total_bytes: number
  private_count: number
  pending_review_count: number
  published_count: number
  failed_count?: number
  pending_review?: number
  published?: number
  open_reports?: number
}

export interface ImageCleanupJob {
  id: string | number
  scope: string
  filters?: Record<string, unknown> | string | null
  status: string
  matched_items?: number
  matched_bytes?: number
  deleted_items?: number
  scanned_count?: number
  deleted_count?: number
  deleted_bytes?: number
  created_at: string
  finished_at?: string | null
  error_message?: string | null
}

export interface ImageLibraryMigrationState {
  migration_key: string
  status: 'pending' | 'running' | 'succeeded' | 'failed' | string
  last_legacy_id: number
  migrated_count: number
  quarantined_count: number
  last_error?: string | null
  started_at?: string | null
  finished_at?: string | null
  updated_at: string
}
