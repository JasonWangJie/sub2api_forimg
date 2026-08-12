//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestResolveImageStorageProviderPresets(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		region       string
		endpoint     string
		wantProvider string
		wantEndpoint string
	}{
		{
			name:         "legacy empty provider",
			provider:     "",
			region:       "auto",
			endpoint:     "https://r2.example.com/",
			wantProvider: ImageStorageProviderCustomS3,
			wantEndpoint: "https://r2.example.com",
		},
		{
			name:         "qiniu preset",
			provider:     ImageStorageProviderQiniu,
			region:       "cn-east-1",
			wantProvider: ImageStorageProviderQiniu,
			wantEndpoint: "https://s3-cn-east-1.qiniucs.com",
		},
		{
			name:         "aliyun preset",
			provider:     ImageStorageProviderAliyun,
			region:       "cn-hangzhou",
			wantProvider: ImageStorageProviderAliyun,
			wantEndpoint: "https://oss-cn-hangzhou.aliyuncs.com",
		},
		{
			name:         "tencent preset",
			provider:     ImageStorageProviderTencent,
			region:       "ap-guangzhou",
			wantProvider: ImageStorageProviderTencent,
			wantEndpoint: "https://cos.ap-guangzhou.myqcloud.com",
		},
		{
			name:         "explicit endpoint overrides preset",
			provider:     ImageStorageProviderAliyun,
			region:       "cn-hangzhou",
			endpoint:     "https://oss-internal.example.com/",
			wantProvider: ImageStorageProviderAliyun,
			wantEndpoint: "https://oss-internal.example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.ImageStorageConfig{
				Provider: tt.provider,
				Region:   tt.region,
				Endpoint: tt.endpoint,
			}
			require.NoError(t, ResolveImageStorageProvider(&cfg))
			require.Equal(t, tt.wantProvider, cfg.Provider)
			require.Equal(t, tt.wantEndpoint, cfg.Endpoint)
		})
	}
}

func TestResolveImageStorageProviderRejectsInvalidValues(t *testing.T) {
	cfg := config.ImageStorageConfig{Provider: "unknown", Region: "auto"}
	require.ErrorContains(t, ResolveImageStorageProvider(&cfg), "unsupported image storage provider")

	cfg = config.ImageStorageConfig{Provider: ImageStorageProviderQiniu}
	require.ErrorContains(t, ResolveImageStorageProvider(&cfg), "region is required")

	cfg = config.ImageStorageConfig{Provider: ImageStorageProviderTencent, Region: "../bad"}
	require.ErrorContains(t, ResolveImageStorageProvider(&cfg), "invalid image storage region")
}

func TestImageStorageRuntimeConfigHotOverride(t *testing.T) {
	svc, _, _ := newImageStorageFixture(t, config.ImageStorageConfig{})
	svc.asyncFallback = config.AsyncImageConfig{
		PublicBaseURL:     "https://fallback.example.com/",
		WorkerConcurrency: 8,
		TaskRetentionDays: 120,
	}

	got, err := svc.RuntimeConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, "https://fallback.example.com", got.PublicBaseURL)
	require.Equal(t, 8, got.WorkerConcurrency)
	require.Equal(t, 120, got.TaskRetentionDays)
	require.Equal(t, 120, got.WorkerLeaseSeconds, "zero fallback fields receive safe defaults")

	_, err = svc.Update(context.Background(), ImageStorageSettings{
		AsyncImage: AsyncImageRuntimeConfig{
			PublicBaseURL:       "https://hot.example.com/",
			WorkerConcurrency:   2,
			ResultRetentionDays: 30,
		},
	})
	require.NoError(t, err)

	got, err = svc.RuntimeConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, "https://hot.example.com", got.PublicBaseURL)
	require.Equal(t, 2, got.WorkerConcurrency)
	require.Equal(t, 30, got.ResultRetentionDays)
	require.Equal(t, 90, got.TaskRetentionDays)
}

type probeDurableStorage struct {
	steps   []string
	key     string
	payload []byte
	ref     ObjectRef
}

func (s *probeDurableStorage) Save(ctx context.Context, key, contentType string, data []byte) (string, error) {
	ref, err := s.SaveObject(ctx, key, contentType, data)
	if err != nil {
		return "", err
	}
	access, err := s.SignURL(ctx, ref, time.Hour)
	return access.URL, err
}

func (s *probeDurableStorage) SaveObject(_ context.Context, key, contentType string, data []byte) (ObjectRef, error) {
	s.steps = append(s.steps, "save")
	s.key = key
	s.payload = append([]byte(nil), data...)
	s.ref = ObjectRef{
		Provider:    ImageStorageProviderCustomS3,
		Bucket:      "images",
		ObjectKey:   key,
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
	}
	return s.ref, nil
}

func (s *probeDurableStorage) SignURL(_ context.Context, _ ObjectRef, _ time.Duration) (ObjectAccess, error) {
	return ObjectAccess{URL: "https://example.com/probe"}, nil
}

func (s *probeDurableStorage) Read(_ context.Context, _ ObjectRef) (io.ReadCloser, error) {
	s.steps = append(s.steps, "read")
	return io.NopCloser(bytes.NewReader(s.payload)), nil
}

func (s *probeDurableStorage) Head(_ context.Context, ref ObjectRef) (ObjectMetadata, error) {
	s.steps = append(s.steps, "head")
	return ObjectMetadata{ObjectRef: ref}, nil
}

func (s *probeDurableStorage) Delete(_ context.Context, _ ObjectRef) error {
	s.steps = append(s.steps, "delete")
	return nil
}

func TestImageStorageTestConnectionPerformsFullProbe(t *testing.T) {
	repo := newStubSettingRepo()
	backup := NewBackupService(repo, &config.Config{}, reversibleEncryptor{}, nil, nil)
	storage := &probeDurableStorage{}
	factory := func(context.Context, *config.ImageStorageConfig) (ImageStorage, error) {
		return storage, nil
	}
	svc := NewImageStorageSettingService(repo, reversibleEncryptor{}, backup, factory, config.ImageStorageConfig{})

	err := svc.TestConnection(context.Background(), ImageStorageSettings{
		Provider:        ImageStorageProviderCustomS3,
		Bucket:          "images",
		Prefix:          "probe/",
		Region:          "auto",
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"save", "head", "read", "delete"}, storage.steps)
	require.Contains(t, storage.key, "probe/.sub2api-probe/")
	require.Equal(t, []byte("sub2api image storage connection probe"), storage.payload)
}
