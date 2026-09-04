import { apiClient } from '../client'

export interface BackupS3Config {
  endpoint: string
  region: string
  bucket: string
  access_key_id: string
  secret_access_key?: string
  prefix: string
  force_path_style: boolean
}

export interface BackupScheduleConfig {
  enabled: boolean
  cron_expr: string
  retain_days: number
  retain_count: number
}

export interface BackupRecord {
  id: string
  status: 'pending' | 'running' | 'completed' | 'failed'
  backup_type: string
  file_name: string
  s3_key: string
  parts?: BackupPart[]
  size_bytes: number
  triggered_by: string
  error_message?: string
  started_at: string
  finished_at?: string
  expires_at?: string
  progress?: string
  restore_status?: string
  restore_error?: string
  restored_at?: string
}

export interface BackupPart {
  index: number
  s3_key: string
  size_bytes: number
  sha256?: string
}

export interface BackupDownloadPart {
  index: number
  size_bytes: number
  url: string
}

export interface BackupDownloadResponse {
  url?: string
  parts?: BackupDownloadPart[]
}

export interface CreateBackupRequest {
  expire_days?: number
}

export interface TestS3Response {
  ok: boolean
  message: string
}

// S3 Config
export async function getS3Config(): Promise<BackupS3Config> {
  const { data } = await apiClient.get<BackupS3Config>('/admin/backups/s3-config')
  return data
}

export async function updateS3Config(config: BackupS3Config): Promise<BackupS3Config> {
  const { data } = await apiClient.put<BackupS3Config>('/admin/backups/s3-config', config)
  return data
}

export async function testS3Connection(config: BackupS3Config): Promise<TestS3Response> {
  const { data } = await apiClient.post<TestS3Response>('/admin/backups/s3-config/test', config)
  return data
}

// Async image object storage
//
// Shares the S3 client with backups when backend=oss and reuse_backup_s3 is set.
export interface ImageStorageConfig {
  enabled: boolean
  backend: 'oss' | 'superbed' | 'local'
  provider: 'custom_s3' | 'qiniu' | 'aliyun' | 'tencent' | 'local' | 'superbed'
  reuse_backup_s3: boolean
  bucket: string
  prefix: string
  public_base_url: string
  presign_expiry_hours: number
  max_download_bytes: number
  endpoint: string
  region: string
  access_key_id: string
  secret_access_key?: string
  force_path_style: boolean
  superbed: ImageStorageSuperbedConfig
  local: ImageStorageLocalConfig
  async_image: AsyncImageRuntimeConfig
  image_library: ImageLibraryRuntimeConfig
}

export interface ImageStorageSuperbedConfig {
  token?: string
  categories: string
  upload_url: string
  local_url: string
}

export interface ImageStorageLocalConfig {
  data_dir: string
  local_url: string
}

export interface ImageLibraryRuntimeConfig {
  retention_days: number
  max_items_per_user: number
  max_bytes_per_user: number
  max_image_bytes: number
  max_image_pixels: number
  signed_url_expiry_seconds: number
  import_per_minute: number
  publish_per_minute: number
}

export interface AsyncImageRuntimeConfig {
  public_base_url: string
  auto_archive_to_library: boolean
  worker_concurrency: number
  worker_lease_seconds: number
  recovery_interval_seconds: number
  execution_timeout_seconds: number
  account_attempt_timeout_seconds: number
  storage_retry_attempts: number
  billing_retry_attempts: number
  retry_backoff_seconds: number
  openai_reference_transport_mode: 'passthrough' | 'local' | 'passthrough_fallback_local'
  gemini_reference_transport_mode: 'passthrough' | 'local' | 'passthrough_fallback_local'
  gemini_async_max_account_switches: number
  image_circuit_breaker_enabled: boolean
  image_circuit_breaker_failure_threshold: number
  image_circuit_breaker_cooldown_seconds: number
  reference_fetch_max_retries: number
  reference_fetch_retry_base_seconds: number
  reference_fetch_retry_max_seconds: number
  upstream_transient_max_retries: number
  upstream_transient_retry_base_seconds: number
  upstream_transient_retry_max_seconds: number
  capacity_max_retries: number
  capacity_retry_base_seconds: number
  capacity_retry_max_seconds: number
  total_max_retries: number
  retry_jitter_percent: number
  retry_after_max_seconds: number
  download_max_bytes: number
  download_max_pixels: number
  max_reference_images: number
  max_reference_total_bytes: number
  max_reference_total_pixels: number
  download_timeout_seconds: number
  reference_fetch_concurrency: number
  reference_cache_ttl_seconds: number
  reference_cache_max_bytes: number
  download_max_redirects: number
  signed_url_expiry_seconds: number
  input_retention_hours: number
  upload_per_minute: number
  max_input_bytes_per_key: number
  upload_timeout_seconds: number
  task_retention_days: number
  result_retention_days: number
	gemini_half_k_models: string[]
	prompt_preview_enabled: boolean
	prompt_preview_max_chars: number
}

export interface ImageStorageConfigResponse {
  config: ImageStorageConfig
  secret_configured: boolean
  superbed_token_configured?: boolean
}

export async function getImageStorageConfig(): Promise<ImageStorageConfigResponse> {
  const { data } = await apiClient.get<ImageStorageConfigResponse>('/admin/backups/image-storage')
  return data
}

export async function updateImageStorageConfig(
  config: ImageStorageConfig,
): Promise<ImageStorageConfig> {
  const { data } = await apiClient.put<ImageStorageConfig>('/admin/backups/image-storage', config)
  return data
}

export async function testImageStorageConnection(
  config: ImageStorageConfig,
): Promise<TestS3Response> {
  const { data } = await apiClient.post<TestS3Response>(
    '/admin/backups/image-storage/test',
    config,
  )
  return data
}

// Schedule
export async function getSchedule(): Promise<BackupScheduleConfig> {
  const { data } = await apiClient.get<BackupScheduleConfig>('/admin/backups/schedule')
  return data
}

export async function updateSchedule(config: BackupScheduleConfig): Promise<BackupScheduleConfig> {
  const { data } = await apiClient.put<BackupScheduleConfig>('/admin/backups/schedule', config)
  return data
}

// Backup operations
export async function createBackup(req?: CreateBackupRequest): Promise<BackupRecord> {
  const { data } = await apiClient.post<BackupRecord>('/admin/backups', req || {})
  return data
}

export async function listBackups(): Promise<{ items: BackupRecord[] }> {
  const { data } = await apiClient.get<{ items: BackupRecord[] }>('/admin/backups')
  return data
}

export async function getBackup(id: string): Promise<BackupRecord> {
  const { data } = await apiClient.get<BackupRecord>(`/admin/backups/${id}`)
  return data
}

export async function deleteBackup(id: string): Promise<void> {
  await apiClient.delete(`/admin/backups/${id}`)
}

export async function getDownloadURL(id: string): Promise<BackupDownloadResponse> {
  const { data } = await apiClient.get<BackupDownloadResponse>(`/admin/backups/${id}/download-url`)
  return data
}

// Restore
export async function restoreBackup(id: string, password: string): Promise<BackupRecord> {
  const { data } = await apiClient.post<BackupRecord>(`/admin/backups/${id}/restore`, { password })
  return data
}

export const backupAPI = {
  getS3Config,
  updateS3Config,
  testS3Connection,
  getImageStorageConfig,
  updateImageStorageConfig,
  testImageStorageConnection,
  getSchedule,
  updateSchedule,
  createBackup,
  listBackups,
  getBackup,
  deleteBackup,
  getDownloadURL,
  restoreBackup,
}

export default backupAPI
