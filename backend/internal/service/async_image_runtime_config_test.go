package service

import (
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
