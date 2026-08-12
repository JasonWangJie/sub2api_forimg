package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const settingKeyImageStorageConfig = "image_storage_config"

const (
	ImageStorageProviderCustomS3 = config.ImageStorageProviderCustomS3
	ImageStorageProviderQiniu    = config.ImageStorageProviderQiniu
	ImageStorageProviderAliyun   = config.ImageStorageProviderAliyun
	ImageStorageProviderTencent  = config.ImageStorageProviderTencent
)

// ResolveImageStorageProvider applies vendor endpoint presets while preserving
// an explicit endpoint override. Empty providers retain the legacy custom_s3
// behavior so existing stored settings need no migration.
func ResolveImageStorageProvider(cfg *config.ImageStorageConfig) error {
	if cfg == nil {
		return errors.New("image storage config is nil")
	}
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	if cfg.Provider == "" || cfg.Provider == "s3" {
		cfg.Provider = ImageStorageProviderCustomS3
	}
	cfg.Region = strings.TrimSpace(cfg.Region)
	cfg.Endpoint = strings.TrimSpace(strings.TrimRight(cfg.Endpoint, "/"))

	switch cfg.Provider {
	case ImageStorageProviderCustomS3:
		if cfg.Region == "" {
			cfg.Region = "auto"
		}
	case ImageStorageProviderQiniu, ImageStorageProviderAliyun, ImageStorageProviderTencent:
		if cfg.Region == "" || cfg.Region == "auto" {
			return fmt.Errorf("region is required for image storage provider %s", cfg.Provider)
		}
		if !validImageStorageRegion(cfg.Region) {
			return fmt.Errorf("invalid image storage region %q", cfg.Region)
		}
		if cfg.Endpoint == "" {
			switch cfg.Provider {
			case ImageStorageProviderQiniu:
				cfg.Endpoint = "https://s3-" + cfg.Region + ".qiniucs.com"
			case ImageStorageProviderAliyun:
				cfg.Endpoint = "https://oss-" + cfg.Region + ".aliyuncs.com"
			case ImageStorageProviderTencent:
				cfg.Endpoint = "https://cos." + cfg.Region + ".myqcloud.com"
			}
		}
	default:
		return fmt.Errorf("unsupported image storage provider %q", cfg.Provider)
	}

	if cfg.Endpoint != "" {
		u, err := url.Parse(cfg.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return fmt.Errorf("invalid image storage endpoint %q", cfg.Endpoint)
		}
	}
	return nil
}

func validImageStorageRegion(region string) bool {
	for _, r := range region {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return region != ""
}

// ErrImageStorageIncomplete 表示开关已打开但凭证不全，无法启用异步生图。
var ErrImageStorageIncomplete = errors.New("image storage is enabled but bucket/access_key_id/secret_access_key are incomplete")

var ErrImageStorageIdentityInUse = apperrors.Conflict(
	"IMAGE_STORAGE_IDENTITY_IN_USE",
	"image storage provider, bucket, endpoint, region, or addressing mode cannot change while active image objects exist; migrate or clean up the existing objects first",
)

// ImageStorageIdentityGuard prevents a runtime settings update from stranding
// durable references that can only be opened through the currently configured
// provider, bucket, endpoint, region, and addressing mode.
type ImageStorageIdentityGuard interface {
	HasActiveImageStorageObjects(ctx context.Context) (bool, error)
}

// ImageStorageFactory 由 repository 层提供，把配置变成一个可用的对象存储实现。
// 与 BackupObjectStoreFactory 同样的注入方式，避免 service 反向依赖 repository。
type ImageStorageFactory func(ctx context.Context, cfg *config.ImageStorageConfig) (ImageStorage, error)

// ImageStorageSettings 是后台可编辑的异步生图对象存储配置。
//
// ReuseBackupS3 为真时不保存自己的凭证，直接借用数据库备份已配置的 S3 端点与密钥，
// 只用自己的 Bucket/Prefix 区分对象；这样"数据走 backups/、图片走 images/"无需重复配置。
type ImageStorageSettings struct {
	Enabled       bool   `json:"enabled"`
	ReuseBackupS3 bool   `json:"reuse_backup_s3"`
	Provider      string `json:"provider"`

	Bucket           string `json:"bucket"` // 留空且复用备份时，沿用备份桶
	Prefix           string `json:"prefix"`
	PublicBaseURL    string `json:"public_base_url"`
	PresignExpiry    int    `json:"presign_expiry_hours"`
	MaxDownloadBytes int64  `json:"max_download_bytes"`

	// 以下仅在 ReuseBackupS3 为假时使用
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key,omitempty"` //nolint:revive // field name follows AWS convention
	ForcePathStyle  bool   `json:"force_path_style"`

	AsyncImage AsyncImageRuntimeConfig   `json:"async_image"`
	Library    ImageLibraryRuntimeConfig `json:"image_library"`
}

// ImageLibraryRuntimeConfig controls server-side library retention and abuse
// limits. Zero values from older stored settings are normalized to the safe
// defaults below.
type ImageLibraryRuntimeConfig struct {
	RetentionDays       int   `json:"retention_days"`
	MaxItemsPerUser     int   `json:"max_items_per_user"`
	MaxBytesPerUser     int64 `json:"max_bytes_per_user"`
	MaxImageBytes       int64 `json:"max_image_bytes"`
	MaxImagePixels      int64 `json:"max_image_pixels"`
	SignedURLExpirySecs int   `json:"signed_url_expiry_seconds"`
	ImportPerMinute     int   `json:"import_per_minute"`
	PublishPerMinute    int   `json:"publish_per_minute"`
}

// AsyncImageRuntimeConfig is stored with image-storage settings so operational
// changes take effect without restarting the API service.
type AsyncImageRuntimeConfig struct {
	PublicBaseURL           string   `json:"public_base_url"`
	WorkerConcurrency       int      `json:"worker_concurrency"`
	WorkerLeaseSeconds      int      `json:"worker_lease_seconds"`
	RecoveryIntervalSeconds int      `json:"recovery_interval_seconds"`
	ExecutionTimeoutSeconds int      `json:"execution_timeout_seconds"`
	StorageRetryAttempts    int      `json:"storage_retry_attempts"`
	BillingRetryAttempts    int      `json:"billing_retry_attempts"`
	RetryBackoffSeconds     int      `json:"retry_backoff_seconds"`
	DownloadMaxBytes        int64    `json:"download_max_bytes"`
	DownloadMaxPixels       int64    `json:"download_max_pixels"`
	MaxReferenceImages      int      `json:"max_reference_images"`
	MaxReferenceTotalBytes  int64    `json:"max_reference_total_bytes"`
	MaxReferenceTotalPixels int64    `json:"max_reference_total_pixels"`
	DownloadTimeoutSeconds  int      `json:"download_timeout_seconds"`
	DownloadMaxRedirects    int      `json:"download_max_redirects"`
	UploadTimeoutSeconds    int      `json:"upload_timeout_seconds"`
	UploadPerMinute         int      `json:"upload_per_minute"`
	MaxInputBytesPerKey     int64    `json:"max_input_bytes_per_key"`
	SignedURLExpirySeconds  int      `json:"signed_url_expiry_seconds"`
	InputRetentionHours     int      `json:"input_retention_hours"`
	TaskRetentionDays       int      `json:"task_retention_days"`
	ResultRetentionDays     int      `json:"result_retention_days"`
	GeminiHalfKModels       []string `json:"gemini_half_k_models"`
	PromptPreviewEnabled    bool     `json:"prompt_preview_enabled"`
	PromptPreviewMaxChars   int      `json:"prompt_preview_max_chars"`
}

// ImageStorageSettingService 读写后台设置，并把结果解析成一个可直接使用的 uploader。
//
// 解析结果带缓存：网关每次请求都要判断功能是否开启，不能每次都查库。保存设置时调用
// Invalidate 清缓存，下一次请求即重建客户端——这是"后台开关立即生效、无需重启"的实现。
type ImageStorageSettingService struct {
	settingRepo   SettingRepository
	encryptor     SecretEncryptor
	backup        *BackupService
	factory       ImageStorageFactory
	identityGuard ImageStorageIdentityGuard

	// fallback 是 config.yaml 里的配置。后台从未保存过设置时沿用它，
	// 保证升级前已用配置文件开启该功能的部署不被打断。
	fallback      config.ImageStorageConfig
	asyncFallback config.AsyncImageConfig

	mu         sync.Mutex
	resolved   bool
	uploader   *ImageResultUploader
	durable    DurableImageStorage
	enabled    bool
	resolveErr error
}

func NewImageStorageSettingService(
	settingRepo SettingRepository,
	encryptor SecretEncryptor,
	backup *BackupService,
	factory ImageStorageFactory,
	fallback config.ImageStorageConfig,
	asyncFallback ...config.AsyncImageConfig,
) *ImageStorageSettingService {
	var asyncCfg config.AsyncImageConfig
	if len(asyncFallback) > 0 {
		asyncCfg = asyncFallback[0]
	}
	return &ImageStorageSettingService{
		settingRepo:   settingRepo,
		encryptor:     encryptor,
		backup:        backup,
		factory:       factory,
		fallback:      fallback,
		asyncFallback: asyncCfg,
	}
}

// Resolver 返回可注入 ImageTaskService 的解析函数。
func (s *ImageStorageSettingService) Resolver() ImageStorageResolver {
	return func() (*ImageResultUploader, bool) {
		return s.resolve()
	}
}

func (s *ImageStorageSettingService) resolve() (*ImageResultUploader, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolveLocked(context.Background())
	return s.uploader, s.enabled
}

func (s *ImageStorageSettingService) resolveLocked(ctx context.Context) {
	if s.resolved {
		return
	}
	s.resolved = true
	s.uploader, s.durable, s.enabled, s.resolveErr = nil, nil, false, nil

	cfg, err := s.effectiveConfig(ctx)
	if err != nil {
		s.resolveErr = err
		logger.L().Warn("image_storage.settings_load_failed; async image tasks stay disabled", zap.Error(err))
		return
	}
	if !cfg.Enabled {
		return
	}
	if !cfg.IsConfigured() {
		logger.L().Warn("image_storage is enabled but not fully configured; async image tasks are disabled",
			zap.Strings("missing_keys", cfg.MissingCredentialKeys()))
		return
	}

	storage, err := s.factory(ctx, cfg)
	if err != nil {
		s.resolveErr = err
		logger.L().Error("image_storage.client_build_failed; async image tasks stay disabled", zap.Error(err))
		return
	}
	s.uploader = NewImageResultUploader(storage, cfg.Prefix, cfg.MaxDownloadByte, nil)
	s.durable, _ = storage.(DurableImageStorage)
	s.enabled = true
}

// DurableStorage returns the current hot-reloaded durable object store. The
// boolean is false when storage is disabled or incomplete.
func (s *ImageStorageSettingService) DurableStorage(ctx context.Context) (DurableImageStorage, bool, error) {
	if s == nil {
		return nil, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolveLocked(ctx)
	if s.resolveErr != nil {
		return nil, false, s.resolveErr
	}
	if !s.enabled {
		return nil, false, nil
	}
	if s.durable == nil {
		return nil, false, errors.New("configured image storage does not implement durable object operations")
	}
	return s.durable, true, nil
}

// Invalidate 丢弃缓存，使下一次请求按最新设置重新解析。
func (s *ImageStorageSettingService) Invalidate() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.resolved = false
	s.uploader = nil
	s.durable = nil
	s.enabled = false
	s.resolveErr = nil
	s.mu.Unlock()
}

// Get 返回后台设置（SecretAccessKey 已脱敏）。从未保存过时返回 config.yaml 的等价值。
func (s *ImageStorageSettingService) Get(ctx context.Context) (*ImageStorageSettings, error) {
	settings, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if settings == nil {
		settings = settingsFromConfig(s.fallback, s.asyncFallback)
	}
	normalizeImageStorageSettings(settings)
	settings.SecretAccessKey = ""
	return settings, nil
}

// RuntimeConfig returns the hot asynchronous-image runtime settings. Stored
// admin settings take precedence over the config-file/environment fallback.
func (s *ImageStorageSettingService) RuntimeConfig(ctx context.Context) (AsyncImageRuntimeConfig, error) {
	if s == nil {
		cfg := defaultAsyncImageRuntimeConfig()
		return cfg, nil
	}
	settings, err := s.load(ctx)
	if err != nil {
		return AsyncImageRuntimeConfig{}, err
	}
	var out AsyncImageRuntimeConfig
	if settings == nil {
		out = asyncRuntimeFromConfig(s.asyncFallback)
	} else {
		out = settings.AsyncImage
	}
	normalizeAsyncImageRuntimeConfig(&out)
	return out, nil
}

// SecretConfigured 供前端展示"已配置"占位符。
func (s *ImageStorageSettingService) SecretConfigured(ctx context.Context) bool {
	settings, err := s.load(ctx)
	if err != nil || settings == nil {
		return s.fallback.SecretAccessKey != ""
	}
	if settings.ReuseBackupS3 {
		cfg, err := s.backupCredentials(ctx)
		return err == nil && cfg != nil && cfg.SecretAccessKey != ""
	}
	return settings.SecretAccessKey != ""
}

// Update 保存设置并立即生效。SecretAccessKey 留空表示沿用已保存的值。
func (s *ImageStorageSettingService) Update(ctx context.Context, in ImageStorageSettings) (*ImageStorageSettings, error) {
	normalizeImageStorageSettings(&in)

	if in.ReuseBackupS3 {
		// 复用备份凭证时不落自己的密钥，避免同一份密钥在库里存两份。
		in.Endpoint, in.Region, in.AccessKeyID, in.SecretAccessKey = "", "", "", ""
		in.ForcePathStyle = false
		in.Provider = ImageStorageProviderCustomS3
	} else if in.SecretAccessKey == "" {
		if old, err := s.load(ctx); err == nil && old != nil {
			in.SecretAccessKey = old.SecretAccessKey
		}
	} else {
		// 拒绝用自动生成的临时密钥加密：重启后密文无法解密（#4524）。
		// 与备份 S3 配置共用同一把密钥，故复用其配置状态判断。
		if s.backup == nil || !s.backup.EncryptionKeyConfigured() {
			return nil, ErrSecretEncryptionKeyNotConfigured
		}
		encrypted, err := s.encryptor.Encrypt(in.SecretAccessKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt secret: %w", err)
		}
		in.SecretAccessKey = encrypted
	}
	if err := s.preventStorageIdentityChange(ctx, &in); err != nil {
		return nil, err
	}
	if in.Enabled {
		// A configuration cannot become active based only on syntactically valid
		// credentials. Probe the exact value that will be persisted so failed
		// upload/read/delete permissions never strand accepted async tasks.
		if err := s.TestConnection(ctx, in); err != nil {
			return nil, fmt.Errorf("test image storage connection before save: %w", err)
		}
	}

	data, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("marshal image storage settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, settingKeyImageStorageConfig, string(data)); err != nil {
		return nil, fmt.Errorf("save image storage settings: %w", err)
	}
	s.Invalidate()

	in.SecretAccessKey = ""
	return &in, nil
}

type imageStorageIdentity struct {
	Provider       string
	Bucket         string
	Endpoint       string
	Region         string
	ForcePathStyle bool
}

func (s *ImageStorageSettingService) preventStorageIdentityChange(ctx context.Context, next *ImageStorageSettings) error {
	if s == nil || s.identityGuard == nil || next == nil {
		return nil
	}
	currentSettings, err := s.load(ctx)
	if err != nil {
		return err
	}
	if currentSettings == nil {
		currentSettings = settingsFromConfig(s.fallback, s.asyncFallback)
	}
	current, err := s.storageIdentity(ctx, currentSettings)
	if err != nil {
		return fmt.Errorf("resolve current image storage identity: %w", err)
	}
	proposed, err := s.storageIdentity(ctx, next)
	if err != nil {
		return fmt.Errorf("resolve proposed image storage identity: %w", err)
	}
	if current == proposed {
		return nil
	}
	inUse, err := s.identityGuard.HasActiveImageStorageObjects(ctx)
	if err != nil {
		return fmt.Errorf("check active image storage objects: %w", err)
	}
	if inUse {
		return ErrImageStorageIdentityInUse
	}
	return nil
}

func (s *ImageStorageSettingService) storageIdentity(ctx context.Context, settings *ImageStorageSettings) (imageStorageIdentity, error) {
	cfg, err := s.toImageStorageConfig(ctx, settings)
	if err != nil {
		return imageStorageIdentity{}, err
	}
	return imageStorageIdentity{
		Provider:       cfg.Provider,
		Bucket:         cfg.Bucket,
		Endpoint:       cfg.Endpoint,
		Region:         cfg.Region,
		ForcePathStyle: cfg.ForcePathStyle,
	}, nil
}

// TestConnection performs a complete object-store probe: upload, HEAD, read,
// and delete. Merely constructing an SDK client cannot verify credentials,
// bucket permissions, endpoint routing, or cleanup permission.
// 与 Update 一样支持留空 SecretAccessKey 表示沿用已保存的值。
func (s *ImageStorageSettingService) TestConnection(ctx context.Context, in ImageStorageSettings) error {
	normalizeImageStorageSettings(&in)
	if !in.ReuseBackupS3 && in.SecretAccessKey == "" {
		old, err := s.load(ctx)
		if err == nil && old != nil {
			in.SecretAccessKey = old.SecretAccessKey
		}
	}
	cfg, err := s.toImageStorageConfig(ctx, &in)
	if err != nil {
		return err
	}
	if !cfg.IsConfigured() {
		return ErrImageStorageIncomplete
	}
	storage, err := s.factory(ctx, cfg)
	if err != nil {
		return err
	}
	durable, ok := storage.(DurableImageStorage)
	if !ok {
		return errors.New("image storage does not support upload/head/read/delete connection probes")
	}

	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return fmt.Errorf("create image storage probe key: %w", err)
	}
	key := cfg.Prefix + ".sub2api-probe/" + hex.EncodeToString(random) + ".txt"
	payload := []byte("sub2api image storage connection probe")
	ref, err := durable.SaveObject(ctx, key, "text/plain", payload)
	if err != nil {
		return fmt.Errorf("image storage probe upload: %w", err)
	}
	deleted := false
	defer func() {
		if !deleted {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = durable.Delete(cleanupCtx, ref)
		}
	}()

	metadata, err := durable.Head(ctx, ref)
	if err != nil {
		return fmt.Errorf("image storage probe head: %w", err)
	}
	if metadata.SizeBytes != int64(len(payload)) {
		return fmt.Errorf("image storage probe head returned size %d, expected %d", metadata.SizeBytes, len(payload))
	}
	body, err := durable.Read(ctx, ref)
	if err != nil {
		return fmt.Errorf("image storage probe read: %w", err)
	}
	got, readErr := io.ReadAll(io.LimitReader(body, int64(len(payload))+1))
	closeErr := body.Close()
	if readErr != nil {
		return fmt.Errorf("image storage probe read body: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("image storage probe close body: %w", closeErr)
	}
	if !bytes.Equal(got, payload) {
		return errors.New("image storage probe read returned different content")
	}
	if err := durable.Delete(ctx, ref); err != nil {
		return fmt.Errorf("image storage probe delete: %w", err)
	}
	deleted = true
	return nil
}

// effectiveConfig 把后台设置（或 config.yaml 回落）解析成运行时配置。
func (s *ImageStorageSettingService) effectiveConfig(ctx context.Context) (*config.ImageStorageConfig, error) {
	settings, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if settings == nil {
		fallback := s.fallback
		return &fallback, nil
	}
	return s.toImageStorageConfig(ctx, settings)
}

func (s *ImageStorageSettingService) toImageStorageConfig(ctx context.Context, in *ImageStorageSettings) (*config.ImageStorageConfig, error) {
	cfg := &config.ImageStorageConfig{
		Enabled:         in.Enabled,
		Provider:        in.Provider,
		Bucket:          in.Bucket,
		Prefix:          in.Prefix,
		PublicBaseURL:   in.PublicBaseURL,
		PresignExpiry:   in.PresignExpiry,
		MaxDownloadByte: in.MaxDownloadBytes,
		Endpoint:        in.Endpoint,
		Region:          in.Region,
		AccessKeyID:     in.AccessKeyID,
		SecretAccessKey: in.SecretAccessKey,
		ForcePathStyle:  in.ForcePathStyle,
	}

	if in.ReuseBackupS3 {
		backupCfg, err := s.backupCredentials(ctx)
		if err != nil {
			return nil, err
		}
		if backupCfg == nil {
			return nil, errors.New("image storage is set to reuse the backup S3 configuration, but no backup S3 configuration exists")
		}
		cfg.Endpoint = backupCfg.Endpoint
		cfg.Provider = ImageStorageProviderCustomS3
		cfg.Region = backupCfg.Region
		cfg.AccessKeyID = backupCfg.AccessKeyID
		cfg.SecretAccessKey = backupCfg.SecretAccessKey
		cfg.ForcePathStyle = backupCfg.ForcePathStyle
		if cfg.Bucket == "" {
			cfg.Bucket = backupCfg.Bucket
		}
	} else if cfg.SecretAccessKey != "" {
		decrypted, err := s.encryptor.Decrypt(cfg.SecretAccessKey)
		if err != nil {
			// 兼容未加密的旧数据，与备份配置的处理保持一致。
			logger.L().Warn("image_storage secret decrypt failed; treating the stored value as plaintext", zap.Error(err))
		} else {
			cfg.SecretAccessKey = decrypted
		}
	}
	if err := ResolveImageStorageProvider(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// backupCredentials 取备份已配置的 S3 凭证（已解密）。
func (s *ImageStorageSettingService) backupCredentials(ctx context.Context) (*BackupS3Config, error) {
	if s.backup == nil {
		return nil, errors.New("backup service is unavailable")
	}
	return s.backup.loadS3Config(ctx)
}

// load 读出后台设置；从未保存过时返回 nil。
func (s *ImageStorageSettingService) load(ctx context.Context) (*ImageStorageSettings, error) {
	if s.settingRepo == nil {
		return nil, nil //nolint:nilnil // no repository means no stored settings
	}
	raw, err := s.settingRepo.GetValue(ctx, settingKeyImageStorageConfig)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, nil //nolint:nilnil // never configured is a valid state
	}
	var settings ImageStorageSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return nil, fmt.Errorf("parse image storage settings: %w", err)
	}
	return &settings, nil
}

func settingsFromConfig(cfg config.ImageStorageConfig, asyncFallback ...config.AsyncImageConfig) *ImageStorageSettings {
	settings := &ImageStorageSettings{
		Enabled:          cfg.Enabled,
		Provider:         cfg.Provider,
		Bucket:           cfg.Bucket,
		Prefix:           cfg.Prefix,
		PublicBaseURL:    cfg.PublicBaseURL,
		PresignExpiry:    cfg.PresignExpiry,
		MaxDownloadBytes: cfg.MaxDownloadByte,
		Endpoint:         cfg.Endpoint,
		Region:           cfg.Region,
		AccessKeyID:      cfg.AccessKeyID,
		SecretAccessKey:  cfg.SecretAccessKey,
		ForcePathStyle:   cfg.ForcePathStyle,
	}
	if len(asyncFallback) > 0 {
		settings.AsyncImage = asyncRuntimeFromConfig(asyncFallback[0])
	}
	normalizeImageStorageSettings(settings)
	return settings
}

func normalizeImageStorageSettings(in *ImageStorageSettings) {
	in.Provider = strings.ToLower(strings.TrimSpace(in.Provider))
	if in.Provider == "" || in.Provider == "s3" {
		in.Provider = ImageStorageProviderCustomS3
	}
	in.Bucket = strings.TrimSpace(in.Bucket)
	in.Endpoint = strings.TrimSpace(in.Endpoint)
	in.Region = strings.TrimSpace(in.Region)
	in.AccessKeyID = strings.TrimSpace(in.AccessKeyID)
	in.SecretAccessKey = strings.TrimSpace(in.SecretAccessKey)
	in.PublicBaseURL = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(in.PublicBaseURL), "/"))

	in.Prefix = strings.TrimSpace(in.Prefix)
	if in.Prefix == "" {
		in.Prefix = "images/"
	}
	if !strings.HasSuffix(in.Prefix, "/") {
		in.Prefix += "/"
	}
	if in.Region == "" && in.Provider == ImageStorageProviderCustomS3 {
		in.Region = "auto"
	}
	if in.PresignExpiry <= 0 {
		in.PresignExpiry = 24
	}
	if in.MaxDownloadBytes <= 0 {
		in.MaxDownloadBytes = defaultImageMaxDownloadBytes
	}
	normalizeAsyncImageRuntimeConfig(&in.AsyncImage)
	normalizeImageLibraryRuntimeConfig(&in.Library)
}

func defaultImageLibraryRuntimeConfig() ImageLibraryRuntimeConfig {
	return ImageLibraryRuntimeConfig{
		RetentionDays:       90,
		MaxItemsPerUser:     1000,
		MaxBytesPerUser:     5 << 30,
		MaxImageBytes:       DefaultImageLibraryMaxBytes,
		MaxImagePixels:      DefaultImageLibraryMaxPixels,
		SignedURLExpirySecs: 3600,
		ImportPerMinute:     20,
		PublishPerMinute:    10,
	}
}

func normalizeImageLibraryRuntimeConfig(in *ImageLibraryRuntimeConfig) {
	defaults := defaultImageLibraryRuntimeConfig()
	if in.RetentionDays <= 0 {
		in.RetentionDays = defaults.RetentionDays
	}
	if in.MaxItemsPerUser <= 0 {
		in.MaxItemsPerUser = defaults.MaxItemsPerUser
	}
	if in.MaxBytesPerUser <= 0 {
		in.MaxBytesPerUser = defaults.MaxBytesPerUser
	}
	if in.MaxImageBytes <= 0 {
		in.MaxImageBytes = defaults.MaxImageBytes
	}
	if in.MaxImagePixels <= 0 {
		in.MaxImagePixels = defaults.MaxImagePixels
	}
	if in.SignedURLExpirySecs <= 0 {
		in.SignedURLExpirySecs = defaults.SignedURLExpirySecs
	}
	if in.ImportPerMinute <= 0 {
		in.ImportPerMinute = defaults.ImportPerMinute
	}
	if in.PublishPerMinute <= 0 {
		in.PublishPerMinute = defaults.PublishPerMinute
	}
}

// LibraryRuntimeConfig returns hot-configured library policy without exposing
// object-store credentials.
func (s *ImageStorageSettingService) LibraryRuntimeConfig(ctx context.Context) (ImageLibraryRuntimeConfig, error) {
	settings, err := s.Get(ctx)
	if err != nil {
		return ImageLibraryRuntimeConfig{}, err
	}
	return settings.Library, nil
}

func asyncRuntimeFromConfig(in config.AsyncImageConfig) AsyncImageRuntimeConfig {
	return AsyncImageRuntimeConfig{
		PublicBaseURL:           in.PublicBaseURL,
		WorkerConcurrency:       in.WorkerConcurrency,
		WorkerLeaseSeconds:      in.WorkerLeaseSeconds,
		RecoveryIntervalSeconds: in.RecoveryIntervalSeconds,
		ExecutionTimeoutSeconds: in.ExecutionTimeoutSeconds,
		StorageRetryAttempts:    in.StorageRetryAttempts,
		BillingRetryAttempts:    in.BillingRetryAttempts,
		RetryBackoffSeconds:     in.RetryBackoffSeconds,
		DownloadMaxBytes:        in.DownloadMaxBytes,
		DownloadMaxPixels:       in.DownloadMaxPixels,
		MaxReferenceImages:      in.MaxReferenceImages,
		MaxReferenceTotalBytes:  in.MaxReferenceTotalBytes,
		MaxReferenceTotalPixels: in.MaxReferenceTotalPixels,
		DownloadTimeoutSeconds:  in.DownloadTimeoutSeconds,
		DownloadMaxRedirects:    in.DownloadMaxRedirects,
		UploadTimeoutSeconds:    in.UploadTimeoutSeconds,
		UploadPerMinute:         in.UploadPerMinute,
		MaxInputBytesPerKey:     in.MaxInputBytesPerKey,
		SignedURLExpirySeconds:  in.SignedURLExpirySeconds,
		InputRetentionHours:     in.InputRetentionHours,
		TaskRetentionDays:       in.TaskRetentionDays,
		ResultRetentionDays:     in.ResultRetentionDays,
		GeminiHalfKModels:       append([]string(nil), in.GeminiHalfKModels...),
		PromptPreviewEnabled:    in.PromptPreviewEnabled,
		PromptPreviewMaxChars:   in.PromptPreviewMaxChars,
	}
}

func defaultAsyncImageRuntimeConfig() AsyncImageRuntimeConfig {
	return AsyncImageRuntimeConfig{
		WorkerConcurrency:       4,
		WorkerLeaseSeconds:      120,
		RecoveryIntervalSeconds: 30,
		ExecutionTimeoutSeconds: 1200,
		StorageRetryAttempts:    5,
		BillingRetryAttempts:    10,
		RetryBackoffSeconds:     30,
		DownloadMaxBytes:        defaultImageMaxDownloadBytes,
		DownloadMaxPixels:       40_000_000,
		MaxReferenceImages:      8,
		MaxReferenceTotalBytes:  64 << 20,
		MaxReferenceTotalPixels: 80_000_000,
		DownloadTimeoutSeconds:  30,
		DownloadMaxRedirects:    3,
		UploadTimeoutSeconds:    300,
		UploadPerMinute:         20,
		MaxInputBytesPerKey:     1 << 30,
		SignedURLExpirySeconds:  3600,
		InputRetentionHours:     24,
		TaskRetentionDays:       90,
		ResultRetentionDays:     90,
		PromptPreviewEnabled:    true,
		PromptPreviewMaxChars:   160,
	}
}

const (
	maxAsyncImageWorkerConcurrency = 64
	maxAsyncImageDownloadBytes     = int64(64 << 20)
	maxAsyncImageDownloadPixels    = int64(80_000_000)
	maxAsyncImageReferenceImages   = 16
	maxAsyncImageReferenceBytes    = int64(256 << 20)
	maxAsyncImageReferencePixels   = int64(320_000_000)
	minAsyncImageWorkerLease       = 45
	maxAsyncImageInputRetention    = 24 * 30
	maxAsyncImageUploadTimeout     = 600
	maxAsyncImageUploadsPerMinute  = 1000
	maxAsyncImageInputBytesPerKey  = int64(100 << 30)
)

func normalizeAsyncImageRuntimeConfig(in *AsyncImageRuntimeConfig) {
	defaults := defaultAsyncImageRuntimeConfig()
	in.PublicBaseURL = strings.TrimRight(strings.TrimSpace(in.PublicBaseURL), "/")
	if in.WorkerConcurrency <= 0 {
		in.WorkerConcurrency = defaults.WorkerConcurrency
	} else if in.WorkerConcurrency > maxAsyncImageWorkerConcurrency {
		in.WorkerConcurrency = maxAsyncImageWorkerConcurrency
	}
	if in.WorkerLeaseSeconds <= 0 {
		in.WorkerLeaseSeconds = defaults.WorkerLeaseSeconds
	} else if in.WorkerLeaseSeconds < minAsyncImageWorkerLease {
		in.WorkerLeaseSeconds = minAsyncImageWorkerLease
	}
	if in.RecoveryIntervalSeconds <= 0 {
		in.RecoveryIntervalSeconds = defaults.RecoveryIntervalSeconds
	}
	if in.ExecutionTimeoutSeconds <= 0 {
		in.ExecutionTimeoutSeconds = defaults.ExecutionTimeoutSeconds
	}
	if in.StorageRetryAttempts <= 0 {
		in.StorageRetryAttempts = defaults.StorageRetryAttempts
	}
	if in.BillingRetryAttempts <= 0 {
		in.BillingRetryAttempts = defaults.BillingRetryAttempts
	}
	if in.RetryBackoffSeconds <= 0 {
		in.RetryBackoffSeconds = defaults.RetryBackoffSeconds
	}
	if in.DownloadMaxBytes <= 0 {
		in.DownloadMaxBytes = defaults.DownloadMaxBytes
	} else if in.DownloadMaxBytes > maxAsyncImageDownloadBytes {
		in.DownloadMaxBytes = maxAsyncImageDownloadBytes
	}
	if in.DownloadMaxPixels <= 0 {
		in.DownloadMaxPixels = defaults.DownloadMaxPixels
	} else if in.DownloadMaxPixels > maxAsyncImageDownloadPixels {
		in.DownloadMaxPixels = maxAsyncImageDownloadPixels
	}
	if in.MaxReferenceImages <= 0 {
		in.MaxReferenceImages = defaults.MaxReferenceImages
	} else if in.MaxReferenceImages > maxAsyncImageReferenceImages {
		in.MaxReferenceImages = maxAsyncImageReferenceImages
	}
	if in.MaxReferenceTotalBytes <= 0 {
		in.MaxReferenceTotalBytes = defaults.MaxReferenceTotalBytes
	} else if in.MaxReferenceTotalBytes > maxAsyncImageReferenceBytes {
		in.MaxReferenceTotalBytes = maxAsyncImageReferenceBytes
	}
	if in.MaxReferenceTotalPixels <= 0 {
		in.MaxReferenceTotalPixels = defaults.MaxReferenceTotalPixels
	} else if in.MaxReferenceTotalPixels > maxAsyncImageReferencePixels {
		in.MaxReferenceTotalPixels = maxAsyncImageReferencePixels
	}
	if in.DownloadTimeoutSeconds <= 0 {
		in.DownloadTimeoutSeconds = defaults.DownloadTimeoutSeconds
	}
	if in.DownloadMaxRedirects <= 0 {
		in.DownloadMaxRedirects = defaults.DownloadMaxRedirects
	}
	if in.UploadTimeoutSeconds <= 0 {
		in.UploadTimeoutSeconds = defaults.UploadTimeoutSeconds
	} else if in.UploadTimeoutSeconds > maxAsyncImageUploadTimeout {
		in.UploadTimeoutSeconds = maxAsyncImageUploadTimeout
	}
	if in.UploadPerMinute <= 0 {
		in.UploadPerMinute = defaults.UploadPerMinute
	} else if in.UploadPerMinute > maxAsyncImageUploadsPerMinute {
		in.UploadPerMinute = maxAsyncImageUploadsPerMinute
	}
	if in.MaxInputBytesPerKey <= 0 {
		in.MaxInputBytesPerKey = defaults.MaxInputBytesPerKey
	} else if in.MaxInputBytesPerKey > maxAsyncImageInputBytesPerKey {
		in.MaxInputBytesPerKey = maxAsyncImageInputBytesPerKey
	}
	if in.SignedURLExpirySeconds <= 0 {
		in.SignedURLExpirySeconds = defaults.SignedURLExpirySeconds
	}
	if in.InputRetentionHours <= 0 {
		in.InputRetentionHours = defaults.InputRetentionHours
	} else if in.InputRetentionHours > maxAsyncImageInputRetention {
		in.InputRetentionHours = maxAsyncImageInputRetention
	}
	if in.TaskRetentionDays <= 0 {
		in.TaskRetentionDays = defaults.TaskRetentionDays
	}
	if in.ResultRetentionDays <= 0 {
		in.ResultRetentionDays = defaults.ResultRetentionDays
	}
	models := make([]string, 0, len(in.GeminiHalfKModels))
	seenModels := make(map[string]struct{}, len(in.GeminiHalfKModels))
	for _, model := range in.GeminiHalfKModels {
		model = strings.ToLower(strings.TrimSpace(model))
		if model == "" {
			continue
		}
		if _, exists := seenModels[model]; exists {
			continue
		}
		seenModels[model] = struct{}{}
		models = append(models, model)
	}
	in.GeminiHalfKModels = models
	if in.PromptPreviewMaxChars <= 0 {
		in.PromptPreviewMaxChars = defaults.PromptPreviewMaxChars
	}
}

// AsyncImageGeminiModelSupportsHalfK checks the explicit capability allowlist.
// Entries ending in '*' are prefix matches; every other entry is exact.
func AsyncImageGeminiModelSupportsHalfK(cfg AsyncImageRuntimeConfig, model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	for _, pattern := range cfg.GeminiHalfKModels {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if strings.HasSuffix(pattern, "*") {
			if prefix := strings.TrimSuffix(pattern, "*"); prefix != "" && strings.HasPrefix(model, prefix) {
				return true
			}
			continue
		}
		if model == pattern {
			return true
		}
	}
	return false
}
