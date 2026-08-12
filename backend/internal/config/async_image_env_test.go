//go:build unit

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadAsyncImageRuntimeFromEnv(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("IMAGE_STORAGE_PROVIDER", ImageStorageProviderTencent)
	t.Setenv("ASYNC_IMAGE_PUBLIC_BASE_URL", "https://api.example.com")
	t.Setenv("ASYNC_IMAGE_WORKER_CONCURRENCY", "7")
	t.Setenv("ASYNC_IMAGE_WORKER_LEASE_SECONDS", "180")
	t.Setenv("ASYNC_IMAGE_RECOVERY_INTERVAL_SECONDS", "45")
	t.Setenv("ASYNC_IMAGE_EXECUTION_TIMEOUT_SECONDS", "1200")
	t.Setenv("ASYNC_IMAGE_STORAGE_RETRY_ATTEMPTS", "6")
	t.Setenv("ASYNC_IMAGE_BILLING_RETRY_ATTEMPTS", "11")
	t.Setenv("ASYNC_IMAGE_RETRY_BACKOFF_SECONDS", "20")
	t.Setenv("ASYNC_IMAGE_DOWNLOAD_MAX_BYTES", "1048576")
	t.Setenv("ASYNC_IMAGE_DOWNLOAD_TIMEOUT_SECONDS", "40")
	t.Setenv("ASYNC_IMAGE_DOWNLOAD_MAX_REDIRECTS", "2")
	t.Setenv("ASYNC_IMAGE_SIGNED_URL_EXPIRY_SECONDS", "7200")
	t.Setenv("ASYNC_IMAGE_INPUT_RETENTION_HOURS", "48")
	t.Setenv("ASYNC_IMAGE_TASK_RETENTION_DAYS", "120")
	t.Setenv("ASYNC_IMAGE_RESULT_RETENTION_DAYS", "60")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, ImageStorageProviderTencent, cfg.ImageStorage.Provider)
	require.Equal(t, "https://api.example.com", cfg.AsyncImage.PublicBaseURL)
	require.Equal(t, 7, cfg.AsyncImage.WorkerConcurrency)
	require.Equal(t, 180, cfg.AsyncImage.WorkerLeaseSeconds)
	require.Equal(t, 45, cfg.AsyncImage.RecoveryIntervalSeconds)
	require.Equal(t, 1200, cfg.AsyncImage.ExecutionTimeoutSeconds)
	require.Equal(t, 6, cfg.AsyncImage.StorageRetryAttempts)
	require.Equal(t, 11, cfg.AsyncImage.BillingRetryAttempts)
	require.Equal(t, 20, cfg.AsyncImage.RetryBackoffSeconds)
	require.Equal(t, int64(1048576), cfg.AsyncImage.DownloadMaxBytes)
	require.Equal(t, 40, cfg.AsyncImage.DownloadTimeoutSeconds)
	require.Equal(t, 2, cfg.AsyncImage.DownloadMaxRedirects)
	require.Equal(t, 7200, cfg.AsyncImage.SignedURLExpirySeconds)
	require.Equal(t, 48, cfg.AsyncImage.InputRetentionHours)
	require.Equal(t, 120, cfg.AsyncImage.TaskRetentionDays)
	require.Equal(t, 60, cfg.AsyncImage.ResultRetentionDays)
}
