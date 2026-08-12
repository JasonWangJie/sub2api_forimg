package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/config"
	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ImageDurableStorageService resolves the write/read backends for plaza and
// library durable assets. It never falls back to async image_storage for writes.
type ImageDurableStorageService struct {
	cfg            config.ImageDurableStorageConfig
	pricingDataDir string
	factory        ImageStorageFactory
	async          *ImageStorageSettingService

	mu    sync.Mutex
	local *LocalImageStorage
	oss   DurableImageStorage
}

func NewImageDurableStorageService(
	cfg config.ImageDurableStorageConfig,
	pricingDataDir string,
	factory ImageStorageFactory,
	async *ImageStorageSettingService,
) *ImageDurableStorageService {
	return &ImageDurableStorageService{
		cfg:            cfg,
		pricingDataDir: pricingDataDir,
		factory:        factory,
		async:          async,
	}
}

// WriteStorage returns the backend used for new library/plaza uploads.
func (s *ImageDurableStorageService) WriteStorage(ctx context.Context) (DurableImageStorage, error) {
	if s == nil {
		return nil, apperrors.ServiceUnavailable("IMAGE_DURABLE_STORAGE_DISABLED", "durable image storage is not configured")
	}
	switch s.cfg.NormalizedBackend() {
	case config.ImageDurableBackendLocal:
		return s.localStorage()
	case config.ImageDurableBackendOSS:
		return s.ossStorage(ctx)
	default:
		return nil, apperrors.ServiceUnavailable("IMAGE_DURABLE_STORAGE_DISABLED", "image_durable_storage.backend is invalid")
	}
}

// ObjectKey applies the durable backend's configured namespace to a relative
// library path. Local storage keeps its fixed library/ namespace; OSS uses the
// operator-provided prefix.
func (s *ImageDurableStorageService) ObjectKey(relative string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("durable image storage is nil")
	}
	relative = strings.Trim(strings.ReplaceAll(strings.TrimSpace(relative), `\`, "/"), "/")
	if relative == "" || strings.Contains(relative, "..") {
		return "", fmt.Errorf("durable image object key is invalid")
	}
	prefix := "library"
	switch s.cfg.NormalizedBackend() {
	case config.ImageDurableBackendLocal:
	case config.ImageDurableBackendOSS:
		prefix = strings.Trim(strings.ReplaceAll(strings.TrimSpace(s.cfg.OSS.Prefix), `\`, "/"), "/")
	default:
		return "", fmt.Errorf("durable image storage backend is invalid")
	}
	if prefix == "" {
		return relative, nil
	}
	return prefix + "/" + relative, nil
}

// StorageForObject picks the backend that owns the given object identity.
// Historical from-task assets may still live in the async image_storage bucket.
func (s *ImageDurableStorageService) StorageForObject(ctx context.Context, ref ObjectRef) (DurableImageStorage, error) {
	if s == nil {
		return nil, apperrors.ServiceUnavailable("IMAGE_DURABLE_STORAGE_DISABLED", "durable image storage is not configured")
	}
	provider := strings.ToLower(strings.TrimSpace(ref.Provider))
	if provider == config.ImageStorageProviderLocal {
		return s.localStorage()
	}

	if s.cfg.NormalizedBackend() == config.ImageDurableBackendOSS {
		oss, err := s.ossStorage(ctx)
		if err == nil && oss != nil {
			if s.cfg.OSS.Bucket != "" && strings.EqualFold(strings.TrimSpace(ref.Bucket), strings.TrimSpace(s.cfg.OSS.Bucket)) {
				return oss, nil
			}
			if provider != "" && strings.EqualFold(provider, strings.ToLower(strings.TrimSpace(s.cfg.OSS.Provider))) &&
				(s.cfg.OSS.Bucket == "" || strings.EqualFold(ref.Bucket, s.cfg.OSS.Bucket)) {
				return oss, nil
			}
		}
	}

	if s.async != nil {
		storage, enabled, err := s.async.DurableStorage(ctx)
		if err != nil {
			return nil, err
		}
		if enabled && storage != nil {
			return storage, nil
		}
	}
	return nil, apperrors.ServiceUnavailable("IMAGE_STORAGE_DISABLED", "no storage backend can serve this object")
}

func (s *ImageDurableStorageService) localStorage() (*LocalImageStorage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.local != nil {
		return s.local, nil
	}
	root := strings.TrimSpace(s.cfg.Local.DataDir)
	if root == "" {
		base := strings.TrimSpace(s.pricingDataDir)
		if base == "" {
			base = "./data"
		}
		root = filepath.Join(base, "image_durable")
	}
	local, err := NewLocalImageStorage(root)
	if err != nil {
		return nil, apperrors.ServiceUnavailable("IMAGE_DURABLE_STORAGE_DISABLED", "failed to initialize local durable storage").WithCause(err)
	}
	s.local = local
	return local, nil
}

func (s *ImageDurableStorageService) ossStorage(ctx context.Context) (DurableImageStorage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.oss != nil {
		return s.oss, nil
	}
	ossCfg := s.cfg.OSS
	if !ossCfg.Enabled || !ossCfg.IsConfigured() {
		return nil, apperrors.ServiceUnavailable("IMAGE_DURABLE_STORAGE_DISABLED", "image_durable_storage.oss is not fully configured")
	}
	if s.factory == nil {
		return nil, apperrors.ServiceUnavailable("IMAGE_DURABLE_STORAGE_DISABLED", "durable OSS factory is not available")
	}
	built, err := s.factory(ctx, &ossCfg)
	if err != nil {
		return nil, apperrors.ServiceUnavailable("IMAGE_DURABLE_STORAGE_DISABLED", "failed to initialize durable OSS storage").WithCause(err)
	}
	durable, ok := built.(DurableImageStorage)
	if !ok || durable == nil {
		return nil, fmt.Errorf("durable OSS factory did not return DurableImageStorage")
	}
	s.oss = durable
	return durable, nil
}

// IsLocalObjectAccess reports whether an ObjectAccess URL is a local-disk marker.
func IsLocalObjectAccess(access ObjectAccess) bool {
	return strings.HasPrefix(access.URL, localObjectURLPrefix)
}
