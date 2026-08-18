package repository_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestSuperbedImageStorageUploadAndSign(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "tok-123", r.Header.Get("X-API-Key"))
		require.NoError(t, r.ParseMultipartForm(1<<20))
		require.Equal(t, "tok-123", r.FormValue("token"))
		require.Equal(t, "album-a", r.FormValue("categories"))
		file, header, err := r.FormFile("file")
		require.NoError(t, err)
		defer func() { _ = file.Close() }()
		require.Equal(t, "000.png", header.Filename)
		body, err := io.ReadAll(file)
		require.NoError(t, err)
		require.Equal(t, []byte("png-bytes"), body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://cdn.example/item/abc.png"}`))
	}))
	defer server.Close()

	storage, err := repository.NewSuperbedImageStorage(&config.ImageStorageConfig{
		Backend: config.ImageStorageBackendSuperbed,
		Superbed: config.ImageStorageSuperbedConfig{
			Token:      "tok-123",
			Categories: "album-a",
			UploadURL:  server.URL,
		},
	})
	require.NoError(t, err)

	ctx := context.Background()
	intent, err := storage.ObjectIntent("images/results/2026/01/01/t1/000.png", "image/png", 9, strings.Repeat("a", 64))
	require.NoError(t, err)
	require.Equal(t, config.ImageStorageProviderSuperbed, intent.Provider)
	require.Equal(t, "album-a", intent.Bucket)
	require.Equal(t, "images/results/2026/01/01/t1/000.png", intent.ObjectKey)

	ref, err := storage.SaveObject(ctx, "images/results/2026/01/01/t1/000.png", "image/png", []byte("png-bytes"))
	require.NoError(t, err)
	require.Equal(t, "https://cdn.example/item/abc.png", ref.ObjectKey)

	access, err := storage.SignURL(ctx, ref, 0)
	require.NoError(t, err)
	require.Equal(t, "https://cdn.example/item/abc.png", access.URL)
	require.True(t, access.ExpiresAt.IsZero())
}

func TestSuperbedImageStorageLocalURLJoin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://cdn.superbed.cn/item/abc.png"}`))
	}))
	defer server.Close()

	storage, err := repository.NewSuperbedImageStorage(&config.ImageStorageConfig{
		Backend: config.ImageStorageBackendSuperbed,
		Superbed: config.ImageStorageSuperbedConfig{
			Token:     "tok",
			UploadURL: server.URL,
			LocalURL:  "https://img.example.com",
		},
	})
	require.NoError(t, err)

	ctx := context.Background()
	key := "images/results/2026/01/01/t1/000.png"
	ref, err := storage.SaveObject(ctx, key, "image/png", []byte("png-bytes"))
	require.NoError(t, err)
	require.Equal(t, key, ref.ObjectKey)

	access, err := storage.SignURL(ctx, ref, 0)
	require.NoError(t, err)
	require.Equal(t, "https://img.example.com/images/results/2026/01/01/t1/000.png", access.URL)
}

func TestSuperbedImageStorageRejectsMissingToken(t *testing.T) {
	_, err := repository.NewSuperbedImageStorage(&config.ImageStorageConfig{
		Backend: config.ImageStorageBackendSuperbed,
	})
	require.Error(t, err)
}
