package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestImageStorageConfigIsConfiguredByBackend(t *testing.T) {
	oss := config.ImageStorageConfig{Backend: "oss", Bucket: "b", AccessKeyID: "ak", SecretAccessKey: "sk"}
	require.True(t, oss.IsConfigured())
	require.True(t, oss.Active() == false) // enabled false

	oss.Enabled = true
	require.True(t, oss.Active())

	incomplete := config.ImageStorageConfig{Enabled: true, Backend: "oss", Bucket: "b"}
	require.False(t, incomplete.IsConfigured())
	require.Contains(t, incomplete.MissingCredentialKeys(), "image_storage.access_key_id")

	superbed := config.ImageStorageConfig{Enabled: true, Backend: "superbed"}
	require.False(t, superbed.IsConfigured())
	require.Contains(t, superbed.MissingCredentialKeys(), "image_storage.superbed.token")
	superbed.Superbed.Token = "tok"
	require.True(t, superbed.IsConfigured())
	require.True(t, superbed.Active())

	local := config.ImageStorageConfig{Enabled: true, Backend: "local"}
	require.True(t, local.IsConfigured())
	require.True(t, local.Active())
	require.Empty(t, local.MissingCredentialKeys())

	emptyBackend := config.ImageStorageConfig{Enabled: true, Bucket: "b", AccessKeyID: "a", SecretAccessKey: "s"}
	require.Equal(t, config.ImageStorageBackendOSS, emptyBackend.NormalizedBackend())
	require.True(t, emptyBackend.IsConfigured())
}

func TestNormalizeImageStorageSettingsBackend(t *testing.T) {
	in := &ImageStorageSettings{Backend: "SUPERBED", Superbed: ImageStorageSuperbedSettings{UploadURL: ""}}
	normalizeImageStorageSettings(in)
	require.Equal(t, ImageStorageBackendSuperbed, in.Backend)
	require.Equal(t, ImageStorageProviderSuperbed, in.Provider)
	require.Equal(t, "https://api.superbed.cn/upload", in.Superbed.UploadURL)
	require.False(t, in.ReuseBackupS3)

	local := &ImageStorageSettings{Backend: "local"}
	normalizeImageStorageSettings(local)
	require.Equal(t, ImageStorageProviderLocal, local.Provider)
}
