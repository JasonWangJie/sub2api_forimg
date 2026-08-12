package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// S3ImageStorage 用 S3 兼容对象存储实现 service.ImageStorage。
type S3ImageStorage struct {
	client        *s3.Client
	provider      string
	bucket        string
	publicBaseURL string
	presignExpiry time.Duration
}

var _ service.ImageStorage = (*S3ImageStorage)(nil)
var _ service.DurableImageStorage = (*S3ImageStorage)(nil)

// NewS3ImageStorage 依据配置构造 S3 图片存储（调用方应先确认 cfg.Active()）。
func NewS3ImageStorage(ctx context.Context, cfg *config.ImageStorageConfig) (*S3ImageStorage, error) {
	if cfg == nil {
		return nil, fmt.Errorf("image storage config is nil")
	}
	resolved := *cfg
	if err := service.ResolveImageStorageProvider(&resolved); err != nil {
		return nil, err
	}
	client, err := newS3Client(ctx, s3ClientParams{
		Endpoint:        resolved.Endpoint,
		Region:          resolved.Region,
		AccessKeyID:     resolved.AccessKeyID,
		SecretAccessKey: resolved.SecretAccessKey,
		ForcePathStyle:  resolved.ForcePathStyle,
	})
	if err != nil {
		return nil, err
	}

	expiry := time.Duration(resolved.PresignExpiry) * time.Hour
	if expiry <= 0 {
		expiry = 24 * time.Hour
	}

	return &S3ImageStorage{
		client:        client,
		provider:      resolved.Provider,
		bucket:        resolved.Bucket,
		publicBaseURL: strings.TrimRight(resolved.PublicBaseURL, "/"),
		presignExpiry: expiry,
	}, nil
}

// Save 上传图片字节，返回可访问 URL：配了 public_base_url 则返回公开直链，否则返回 presigned 临时链接。
func (s *S3ImageStorage) Save(ctx context.Context, key, contentType string, data []byte) (string, error) {
	ref, err := s.SaveObject(ctx, key, contentType, data)
	if err != nil {
		return "", err
	}
	access, err := s.SignURL(ctx, ref, s.presignExpiry)
	if err != nil {
		return "", err
	}
	return access.URL, nil
}

// SaveObject uploads bytes and returns a durable identity rather than an
// expiring URL. The SHA-256 is stored as object metadata for later validation.
func (s *S3ImageStorage) SaveObject(ctx context.Context, key, contentType string, data []byte) (service.ObjectRef, error) {
	checksum := fmt.Sprintf("%x", sha256.Sum256(data))
	intent, err := s.ObjectIntent(key, contentType, int64(len(data)), checksum)
	if err != nil {
		return service.ObjectRef{}, err
	}
	finish := servertiming.ObserveDependency(ctx, "s3")
	defer finish()
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &intent.ObjectKey,
		Body:        bytes.NewReader(data),
		ContentType: &intent.ContentType,
		Metadata:    map[string]string{"sha256": intent.ChecksumSHA256},
	})
	if err != nil {
		return service.ObjectRef{}, fmt.Errorf("S3 PutObject: %w", err)
	}
	return intent, nil
}

func (s *S3ImageStorage) ObjectIntent(key, contentType string, sizeBytes int64, checksumSHA256 string) (service.ObjectRef, error) {
	if s == nil || s.client == nil {
		return service.ObjectRef{}, fmt.Errorf("image storage client is nil")
	}
	key = strings.TrimLeft(strings.TrimSpace(key), "/")
	if key == "" {
		return service.ObjectRef{}, fmt.Errorf("object key is required")
	}
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	checksumSHA256 = strings.ToLower(strings.TrimSpace(checksumSHA256))
	if sizeBytes <= 0 || len(checksumSHA256) != 64 {
		return service.ObjectRef{}, fmt.Errorf("object intent size and SHA-256 are required")
	}
	return service.ObjectRef{
		Provider:       s.provider,
		Bucket:         s.bucket,
		ObjectKey:      key,
		ContentType:    contentType,
		SizeBytes:      sizeBytes,
		ChecksumSHA256: checksumSHA256,
	}, nil
}

// SignURL resolves a durable reference to either a permanent CDN URL or a new
// presigned URL. The reported expiry exactly matches the signing duration.
func (s *S3ImageStorage) SignURL(ctx context.Context, ref service.ObjectRef, expiry time.Duration) (service.ObjectAccess, error) {
	if err := s.validateRef(ref); err != nil {
		return service.ObjectAccess{}, err
	}
	if s.publicBaseURL != "" {
		return service.ObjectAccess{URL: s.publicBaseURL + "/" + escapeObjectKey(ref.ObjectKey)}, nil
	}
	if expiry <= 0 {
		expiry = s.presignExpiry
	}

	presignClient := s3.NewPresignClient(s.client)
	result, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &ref.ObjectKey,
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return service.ObjectAccess{}, fmt.Errorf("presign url: %w", err)
	}
	return service.ObjectAccess{URL: result.URL, ExpiresAt: time.Now().Add(expiry)}, nil
}

func (s *S3ImageStorage) Read(ctx context.Context, ref service.ObjectRef) (io.ReadCloser, error) {
	if err := s.validateRef(ref); err != nil {
		return nil, err
	}
	finish := servertiming.ObserveDependency(ctx, "s3")
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &ref.ObjectKey})
	finish()
	if err != nil {
		return nil, fmt.Errorf("S3 GetObject: %w", err)
	}
	return result.Body, nil
}

func (s *S3ImageStorage) Head(ctx context.Context, ref service.ObjectRef) (service.ObjectMetadata, error) {
	if err := s.validateRef(ref); err != nil {
		return service.ObjectMetadata{}, err
	}
	finish := servertiming.ObserveDependency(ctx, "s3")
	result, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &ref.ObjectKey})
	finish()
	if err != nil {
		return service.ObjectMetadata{}, fmt.Errorf("S3 HeadObject: %w", err)
	}
	checksum := ""
	for key, value := range result.Metadata {
		if strings.EqualFold(key, "sha256") {
			checksum = value
			break
		}
	}
	return service.ObjectMetadata{
		ObjectRef: service.ObjectRef{
			Provider:       s.provider,
			Bucket:         s.bucket,
			ObjectKey:      ref.ObjectKey,
			ContentType:    aws.ToString(result.ContentType),
			SizeBytes:      aws.ToInt64(result.ContentLength),
			ChecksumSHA256: checksum,
		},
		ETag:         strings.Trim(aws.ToString(result.ETag), "\""),
		LastModified: aws.ToTime(result.LastModified),
	}, nil
}

func (s *S3ImageStorage) Delete(ctx context.Context, ref service.ObjectRef) error {
	if err := s.validateRef(ref); err != nil {
		return err
	}
	finish := servertiming.ObserveDependency(ctx, "s3")
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &ref.ObjectKey})
	finish()
	if err != nil {
		return fmt.Errorf("S3 DeleteObject: %w", err)
	}
	return nil
}

func (s *S3ImageStorage) validateRef(ref service.ObjectRef) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("image storage client is nil")
	}
	provider := strings.ToLower(strings.TrimSpace(ref.Provider))
	if provider == "" || provider == "s3" {
		provider = service.ImageStorageProviderCustomS3
	}
	if provider != s.provider {
		return fmt.Errorf("object provider %q does not match configured provider %q", provider, s.provider)
	}
	if ref.Bucket != s.bucket {
		return fmt.Errorf("object bucket %q does not match configured bucket", ref.Bucket)
	}
	if strings.TrimSpace(ref.ObjectKey) == "" {
		return fmt.Errorf("object key is required")
	}
	return nil
}

func escapeObjectKey(key string) string {
	parts := strings.Split(strings.TrimLeft(key, "/"), "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
