package service

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestLocalImageStorageSignedURLRoundTrip(t *testing.T) {
	root := t.TempDir()
	signKey := []byte("test-sign-key")
	storage, err := NewLocalImageStorageWithURLOptions(root, "", "", "https://api.example.com", signKey)
	require.NoError(t, err)

	payload := []byte("hello-local-image")
	ctx := context.Background()
	ref, err := storage.SaveObject(ctx, "images/results/a.png", "image/png", payload)
	require.NoError(t, err)
	require.Equal(t, config.ImageStorageProviderLocal, ref.Provider)

	access, err := storage.SignURL(ctx, ref, time.Hour)
	require.NoError(t, err)
	require.Contains(t, access.URL, "https://api.example.com/v1/images/local/images/results/a.png")
	require.Contains(t, access.URL, "exp=")
	require.Contains(t, access.URL, "sig=")

	u, err := url.Parse(access.URL)
	require.NoError(t, err)
	require.NoError(t, VerifyLocalImageURLSignature(ref.ObjectKey, u.Query().Get("exp"), u.Query().Get("sig"), signKey, time.Now()))

	body, err := storage.Read(ctx, ref)
	require.NoError(t, err)
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	require.Equal(t, payload, got)

	require.NoError(t, storage.Delete(ctx, ref))
	_, err = os.Stat(filepath.Join(root, "images", "results", "a.png"))
	require.True(t, os.IsNotExist(err))
}

func TestLocalImageStoragePublicBaseURL(t *testing.T) {
	root := t.TempDir()
	storage, err := NewLocalImageStorageWithURLOptions(root, "https://cdn.example/img", "", "", nil)
	require.NoError(t, err)
	ref, err := storage.SaveObject(context.Background(), "a/b.png", "image/png", []byte("x"))
	require.NoError(t, err)
	access, err := storage.SignURL(context.Background(), ref, 0)
	require.NoError(t, err)
	require.Equal(t, "https://cdn.example/img/a/b.png", access.URL)
}

func TestLocalImageStorageRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	storage, err := NewLocalImageStorage(root)
	require.NoError(t, err)
	_, err = storage.ObjectIntent("../escape.png", "image/png", 1, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	require.Error(t, err)
}

func TestLocalImageStorageLocalURLTakesPrecedence(t *testing.T) {
	root := t.TempDir()
	storage, err := NewLocalImageStorageWithURLOptions(root, "https://img.local", "https://cdn.legacy", "", nil)
	require.NoError(t, err)
	ref, err := storage.SaveObject(context.Background(), "a/b.png", "image/png", []byte("x"))
	require.NoError(t, err)
	access, err := storage.SignURL(context.Background(), ref, 0)
	require.NoError(t, err)
	require.Equal(t, "https://img.local/a/b.png", access.URL)
}

func TestJoinLocalObjectURL(t *testing.T) {
	got, err := JoinLocalObjectURL("https://img.example.com/", "images/results/a.png")
	require.NoError(t, err)
	require.Equal(t, "https://img.example.com/images/results/a.png", got)

	got, err = JoinLocalObjectURL("https://img.example.com", "https://cdn.superbed.cn/item/xyz.png")
	require.NoError(t, err)
	require.Equal(t, "https://img.example.com/item/xyz.png", got)
}

func TestVerifyLocalImageURLSignatureExpired(t *testing.T) {
	key := []byte("k")
	exp := time.Now().Add(-time.Minute).Unix()
	sig := LocalImageURLSignature("a.png", exp, key)
	err := VerifyLocalImageURLSignature("a.png", strconv.FormatInt(exp, 10), sig, key, time.Now())
	require.Error(t, err)
}
