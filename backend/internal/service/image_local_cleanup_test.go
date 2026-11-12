package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestLocalCleanupIdentityAndIndependentChecksum(t *testing.T) {
	root := t.TempDir()
	storage, err := NewLocalImageStorage(root)
	require.NoError(t, err)
	ref, err := storage.SaveObject(context.Background(), "images/results/a.png", "image/png", []byte("first"))
	require.NoError(t, err)
	require.Len(t, ref.StorageIdentity, 64)
	require.Equal(t, ImageStorageIdentityFingerprint(&config.ImageStorageConfig{Backend: "local", Provider: "local", Local: config.ImageStorageLocalConfig{DataDir: root}}), ref.StorageIdentity)
	knownMeta, err := storage.Head(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, int64(5), knownMeta.SizeBytes)
	require.Empty(t, knownMeta.ChecksumSHA256) // Known identities need only a lightweight existence/size check.
	legacy := ref
	legacy.StorageIdentity = ""
	calls := 0
	ctx := WithImageCleanupRequest(context.Background(), func(context.Context) error { calls++; return nil })
	meta, err := storage.Head(ctx, legacy)
	require.NoError(t, err)
	require.Equal(t, ref.ChecksumSHA256, meta.ChecksumSHA256)
	require.NoError(t, os.WriteFile(filepath.Join(root, "images/results/a.png"), []byte("other"), 0600))
	meta, err = storage.Head(ctx, legacy)
	require.NoError(t, err)
	require.NotEmpty(t, meta.ChecksumSHA256)
	require.NotEqual(t, ref.ChecksumSHA256, meta.ChecksumSHA256)
	require.Equal(t, 2, calls)
	wrong := ref
	wrong.StorageIdentity = "other-directory"
	require.Error(t, storage.Delete(ctx, wrong))
	_, err = os.Stat(filepath.Join(root, "images/results/a.png"))
	require.NoError(t, err)
}

func TestLocalCleanupHonorsPauseAndHashSizeBudget(t *testing.T) {
	storage, err := NewLocalImageStorage(t.TempDir())
	require.NoError(t, err)
	ref, err := storage.SaveObject(context.Background(), "a.png", "image/png", []byte("bytes"))
	require.NoError(t, err)
	paused := errors.New("paused")
	ctx := WithImageCleanupRequest(context.Background(), func(context.Context) error { return paused })
	_, err = storage.Head(ctx, ref)
	require.ErrorIs(t, err, paused)
	require.ErrorIs(t, storage.Delete(ctx, ref), paused)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = storage.Head(cancelled, ref)
	require.ErrorIs(t, err, context.Canceled)
	ref.SizeBytes++
	ref.StorageIdentity = ""
	meta, err := storage.Head(context.Background(), ref)
	require.NoError(t, err)
	require.Empty(t, meta.ChecksumSHA256)
	file, err := os.OpenFile(filepath.Join(storage.RootDir(), "a.png"), os.O_WRONLY, 0600)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(65<<20))
	require.NoError(t, file.Close())
	ref.SizeBytes = 65 << 20
	meta, err = storage.Head(context.Background(), ref)
	require.NoError(t, err)
	require.Empty(t, meta.ChecksumSHA256)
}
