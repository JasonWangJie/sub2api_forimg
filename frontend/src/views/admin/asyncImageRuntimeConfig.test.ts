import { describe, expect, it } from 'vitest'
import {
  ASYNC_IMAGE_DOWNLOAD_PIXEL_LIMITS,
  ASYNC_IMAGE_REFERENCE_RETRY_DEFAULTS,
  mergeAsyncImageReferenceRetryDefaults,
} from './asyncImageRuntimeConfig'

describe('async image reference and retry settings', () => {
  it('supplies defaults when loading a legacy config', () => {
    const merged = mergeAsyncImageReferenceRetryDefaults({ worker_concurrency: 8 })
    expect(merged.auto_archive_to_library).toBe(false)
    expect(merged.openai_reference_transport_mode).toBe('passthrough_fallback_local')
    expect(merged.gemini_reference_transport_mode).toBe('passthrough')
    expect(merged.reference_fetch_max_retries).toBe(2)
    expect(merged.upstream_transient_max_retries).toBe(3)
    expect(merged.capacity_max_retries).toBe(5)
    expect(merged.total_max_retries).toBe(16)
  })

  it('preserves explicitly saved legacy pixel limits and mode changes', () => {
    const merged = mergeAsyncImageReferenceRetryDefaults({
      download_max_pixels: 40_000_000,
      openai_reference_transport_mode: 'local',
      gemini_reference_transport_mode: 'passthrough_fallback_local',
    })
    expect(merged.download_max_pixels).toBe(40_000_000)
    expect(merged.openai_reference_transport_mode).toBe('local')
    expect(merged.gemini_reference_transport_mode).toBe('passthrough_fallback_local')
    expect({ ...merged }).toMatchObject({
      openai_reference_transport_mode: 'local',
      gemini_reference_transport_mode: 'passthrough_fallback_local',
    })
  })

  it('uses the configured pixel range and retry defaults', () => {
    expect(ASYNC_IMAGE_DOWNLOAD_PIXEL_LIMITS).toEqual({ min: 1_000_000, max: 80_000_000 })
    expect(ASYNC_IMAGE_REFERENCE_RETRY_DEFAULTS.retry_jitter_percent).toBe(20)
    expect(ASYNC_IMAGE_REFERENCE_RETRY_DEFAULTS.retry_after_max_seconds).toBe(900)
  })
})
