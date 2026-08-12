//go:build unit

package repository

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestS3ImageStorageDurableObjectLifecycle(t *testing.T) {
	var mu sync.Mutex
	var stored []byte
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		require.Contains(t, r.URL.Path, "/images/tasks/task-1/result 1.png")
		switch r.Method {
		case http.MethodPut:
			var err error
			stored, err = io.ReadAll(r.Body)
			require.NoError(t, err)
			w.WriteHeader(http.StatusOK)
		case http.MethodHead:
			w.Header().Set("Content-Length", "7")
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("ETag", `"etag-value"`)
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.Header().Set("x-amz-meta-sha256", "server-sha")
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(stored)
		case http.MethodDelete:
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	storage, err := NewS3ImageStorage(context.Background(), &config.ImageStorageConfig{
		Provider:        config.ImageStorageProviderCustomS3,
		Endpoint:        server.URL,
		Region:          "us-east-1",
		Bucket:          "images",
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		ForcePathStyle:  true,
		PresignExpiry:   1,
	})
	require.NoError(t, err)

	ref, err := storage.SaveObject(context.Background(), "tasks/task-1/result 1.png", "image/png", []byte("payload"))
	require.NoError(t, err)
	require.Equal(t, service.ObjectRef{
		Provider:       service.ImageStorageProviderCustomS3,
		Bucket:         "images",
		ObjectKey:      "tasks/task-1/result 1.png",
		ContentType:    "image/png",
		SizeBytes:      7,
		ChecksumSHA256: "239f59ed55e737c77147cf55ad0c1b030b6d7ee748a7426952f9b852d5a935e5",
	}, ref)

	metadata, err := storage.Head(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, int64(7), metadata.SizeBytes)
	require.Equal(t, "image/png", metadata.ContentType)
	require.Equal(t, "server-sha", metadata.ChecksumSHA256)
	require.Equal(t, "etag-value", metadata.ETag)

	body, err := storage.Read(context.Background(), ref)
	require.NoError(t, err)
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	require.Equal(t, []byte("payload"), got)

	access, err := storage.SignURL(context.Background(), ref, 15*time.Minute)
	require.NoError(t, err)
	require.Contains(t, access.URL, server.URL)
	require.Contains(t, access.URL, "X-Amz-Signature=")
	require.WithinDuration(t, time.Now().Add(15*time.Minute), access.ExpiresAt, 5*time.Second)

	require.NoError(t, storage.Delete(context.Background(), ref))
	mu.Lock()
	require.True(t, deleted)
	mu.Unlock()
}

func TestS3ImageStoragePublicURLAndReferenceValidation(t *testing.T) {
	storage, err := NewS3ImageStorage(context.Background(), &config.ImageStorageConfig{
		Provider:        config.ImageStorageProviderAliyun,
		Endpoint:        "https://oss.example.com",
		Region:          "cn-hangzhou",
		Bucket:          "images",
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		PublicBaseURL:   "https://cdn.example.com/",
	})
	require.NoError(t, err)
	ref := service.ObjectRef{
		Provider:  service.ImageStorageProviderAliyun,
		Bucket:    "images",
		ObjectKey: "tasks/a result.png",
	}

	access, err := storage.SignURL(context.Background(), ref, time.Hour)
	require.NoError(t, err)
	require.Equal(t, "https://cdn.example.com/tasks/a%20result.png", access.URL)
	require.True(t, access.ExpiresAt.IsZero())

	bad := ref
	bad.Bucket = "other"
	_, err = storage.SignURL(context.Background(), bad, time.Hour)
	require.ErrorContains(t, err, "does not match configured bucket")

	bad = ref
	bad.Provider = service.ImageStorageProviderTencent
	_, err = storage.SignURL(context.Background(), bad, time.Hour)
	require.ErrorContains(t, err, "does not match configured provider")
	require.False(t, strings.Contains(access.URL, " "))
}
