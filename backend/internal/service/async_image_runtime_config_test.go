package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAsyncImageGeminiModelSupportsHalfK(t *testing.T) {
	cfg := AsyncImageRuntimeConfig{GeminiHalfKModels: []string{"nano-banana-2", "gemini-image-*"}}
	normalizeAsyncImageRuntimeConfig(&cfg)
	require.True(t, AsyncImageGeminiModelSupportsHalfK(cfg, "nano-banana-2"))
	require.True(t, AsyncImageGeminiModelSupportsHalfK(cfg, "gemini-image-preview"))
	require.False(t, AsyncImageGeminiModelSupportsHalfK(cfg, "other-model"))
}

func TestAsyncImageRuntimeConfigOldJSONGetsRetryDefaults(t *testing.T) {
	var cfg AsyncImageRuntimeConfig
	require.NoError(t, json.Unmarshal([]byte(`{"download_max_pixels":40000000}`), &cfg))
	normalizeAsyncImageRuntimeConfig(&cfg)

	require.Equal(t, int64(40_000_000), cfg.DownloadMaxPixels, "explicit legacy pixel limit must be preserved")
	require.Equal(t, AsyncImageReferenceTransportPassthroughFallbackLocal, cfg.OpenAIReferenceTransportMode)
	require.Equal(t, AsyncImageReferenceTransportPassthrough, cfg.GeminiReferenceTransportMode)
	require.Equal(t, 3, cfg.GeminiAsyncMaxAccountSwitches)
	require.Equal(t, 2, cfg.ReferenceFetchMaxRetries)
	require.Equal(t, 3, cfg.UpstreamTransientMaxRetries)
	require.Equal(t, 5, cfg.CapacityMaxRetries)
	require.Equal(t, 16, cfg.TotalMaxRetries)
	require.Equal(t, 20, cfg.RetryJitterPercent)
	require.Equal(t, 900, cfg.RetryAfterMaxSeconds)
	require.False(t, cfg.AutoArchiveToLibrary)
}

func TestAsyncImageRuntimeConfigPreservesExplicitAutoArchivePolicy(t *testing.T) {
	var cfg AsyncImageRuntimeConfig
	require.NoError(t, json.Unmarshal([]byte(`{"auto_archive_to_library":true}`), &cfg))
	normalizeAsyncImageRuntimeConfig(&cfg)
	require.True(t, cfg.AutoArchiveToLibrary)
}

func TestAsyncImageRuntimeConfigPreservesExplicitZeroRetryLimits(t *testing.T) {
	var cfg AsyncImageRuntimeConfig
	require.NoError(t, json.Unmarshal([]byte(`{
      "reference_fetch_max_retries":0,
      "upstream_transient_max_retries":0,
      "capacity_max_retries":0,
      "total_max_retries":0,
      "retry_jitter_percent":0
    }`), &cfg))
	normalizeAsyncImageRuntimeConfig(&cfg)
	require.Zero(t, cfg.ReferenceFetchMaxRetries)
	require.Zero(t, cfg.UpstreamTransientMaxRetries)
	require.Zero(t, cfg.CapacityMaxRetries)
	require.Zero(t, cfg.TotalMaxRetries)
	require.Zero(t, cfg.RetryJitterPercent)
}

func TestAsyncImageRuntimeConfigNormalizesRetryBoundsAndModes(t *testing.T) {
	cfg := defaultAsyncImageRuntimeConfig()
	cfg.OpenAIReferenceTransportMode = "invalid"
	cfg.GeminiReferenceTransportMode = "LOCAL"
	cfg.ReferenceFetchMaxRetries = 99
	cfg.UpstreamTransientMaxRetries = 99
	cfg.CapacityMaxRetries = 99
	cfg.TotalMaxRetries = 99
	cfg.RetryJitterPercent = 99
	cfg.RetryAfterMaxSeconds = 9999
	cfg.DownloadMaxPixels = 1
	normalizeAsyncImageRuntimeConfig(&cfg)

	require.Equal(t, AsyncImageReferenceTransportPassthroughFallbackLocal, cfg.OpenAIReferenceTransportMode)
	require.Equal(t, AsyncImageReferenceTransportLocal, cfg.GeminiReferenceTransportMode)
	require.Equal(t, maxAsyncImageReferenceFetchRetries, cfg.ReferenceFetchMaxRetries)
	require.Equal(t, maxAsyncImageUpstreamTransientRetries, cfg.UpstreamTransientMaxRetries)
	require.Equal(t, maxAsyncImageCapacityRetries, cfg.CapacityMaxRetries)
	require.Equal(t, maxAsyncImageTotalRetries, cfg.TotalMaxRetries)
	require.Equal(t, maxAsyncImageRetryJitterPercent, cfg.RetryJitterPercent)
	require.Equal(t, maxAsyncImageRetryAfterSeconds, cfg.RetryAfterMaxSeconds)
	require.Equal(t, minAsyncImageDownloadPixels, cfg.DownloadMaxPixels)
}

func TestNormalizeAsyncImageRuntimeConfigCapsWorkerAndReferenceResources(t *testing.T) {
	cfg := AsyncImageRuntimeConfig{
		WorkerConcurrency:       10000,
		WorkerLeaseSeconds:      1,
		DownloadMaxPixels:       100_000_000,
		MaxReferenceImages:      100,
		MaxReferenceTotalBytes:  1 << 40,
		MaxReferenceTotalPixels: 1_000_000_000,
	}
	normalizeAsyncImageRuntimeConfig(&cfg)
	require.Equal(t, maxAsyncImageWorkerConcurrency, cfg.WorkerConcurrency)
	require.Equal(t, minAsyncImageWorkerLease, cfg.WorkerLeaseSeconds)
	require.Equal(t, maxAsyncImageDownloadPixels, cfg.DownloadMaxPixels)
	require.Equal(t, maxAsyncImageReferenceImages, cfg.MaxReferenceImages)
	require.Equal(t, maxAsyncImageReferenceBytes, cfg.MaxReferenceTotalBytes)
	require.Equal(t, maxAsyncImageReferencePixels, cfg.MaxReferenceTotalPixels)
}
