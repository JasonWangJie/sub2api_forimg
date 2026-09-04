import type { AsyncImageRuntimeConfig } from '@/api/admin/backup'

export const ASYNC_IMAGE_DOWNLOAD_PIXEL_LIMITS = {
  min: 1_000_000,
  max: 80_000_000,
} as const

export const ASYNC_IMAGE_REFERENCE_RETRY_DEFAULTS = {
  auto_archive_to_library: false,
  openai_reference_transport_mode: 'passthrough_fallback_local',
  gemini_reference_transport_mode: 'passthrough',
  gemini_async_max_account_switches: 3,
  image_circuit_breaker_enabled: false,
  image_circuit_breaker_failure_threshold: 5,
  image_circuit_breaker_cooldown_seconds: 300,
  account_attempt_timeout_seconds: 300,
  reference_fetch_concurrency: 8,
  reference_cache_ttl_seconds: 60,
  reference_cache_max_bytes: 128 * 1024 * 1024,
  reference_fetch_max_retries: 2,
  reference_fetch_retry_base_seconds: 15,
  reference_fetch_retry_max_seconds: 60,
  upstream_transient_max_retries: 3,
  upstream_transient_retry_base_seconds: 15,
  upstream_transient_retry_max_seconds: 60,
  capacity_max_retries: 5,
  capacity_retry_base_seconds: 30,
  capacity_retry_max_seconds: 300,
  total_max_retries: 16,
  retry_jitter_percent: 20,
  retry_after_max_seconds: 900,
  download_max_pixels: ASYNC_IMAGE_DOWNLOAD_PIXEL_LIMITS.max,
} satisfies Pick<
  AsyncImageRuntimeConfig,
  | 'auto_archive_to_library'
  | 'openai_reference_transport_mode'
  | 'gemini_reference_transport_mode'
  | 'gemini_async_max_account_switches'
  | 'image_circuit_breaker_enabled'
  | 'image_circuit_breaker_failure_threshold'
  | 'image_circuit_breaker_cooldown_seconds'
  | 'account_attempt_timeout_seconds'
  | 'reference_fetch_concurrency'
  | 'reference_cache_ttl_seconds'
  | 'reference_cache_max_bytes'
  | 'reference_fetch_max_retries'
  | 'reference_fetch_retry_base_seconds'
  | 'reference_fetch_retry_max_seconds'
  | 'upstream_transient_max_retries'
  | 'upstream_transient_retry_base_seconds'
  | 'upstream_transient_retry_max_seconds'
  | 'capacity_max_retries'
  | 'capacity_retry_base_seconds'
  | 'capacity_retry_max_seconds'
  | 'total_max_retries'
  | 'retry_jitter_percent'
  | 'retry_after_max_seconds'
  | 'download_max_pixels'
>

export function mergeAsyncImageReferenceRetryDefaults(
  config: Partial<AsyncImageRuntimeConfig> = {},
) {
  return { ...ASYNC_IMAGE_REFERENCE_RETRY_DEFAULTS, ...config }
}
