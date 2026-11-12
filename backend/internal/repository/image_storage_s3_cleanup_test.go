package repository

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestS3CleanupDisablesUnbudgetedSDKRetries(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	storage, err := NewS3ImageStorage(context.Background(), &config.ImageStorageConfig{Backend: "oss", Provider: "custom_s3", Endpoint: server.URL, Region: "auto", Bucket: "images", AccessKeyID: "test", SecretAccessKey: "test", ForcePathStyle: true})
	require.NoError(t, err)
	ref := service.ObjectRef{Provider: "custom_s3", Bucket: "images", ObjectKey: "images/results/a.png"}
	tokens := 0
	ctx := service.WithImageCleanupRequest(context.Background(), func(context.Context) error { tokens++; return nil })
	_, err = storage.Head(ctx, ref)
	require.Error(t, err)
	err = storage.Delete(ctx, ref)
	require.Error(t, err)
	require.EqualValues(t, 2, requests.Load())
	require.Equal(t, 2, tokens)
	denied := errors.New("cleanup paused")
	ctx = service.WithImageCleanupRequest(context.Background(), func(context.Context) error { return denied })
	err = storage.Delete(ctx, ref)
	require.ErrorIs(t, err, denied)
	require.EqualValues(t, 2, requests.Load())
}
