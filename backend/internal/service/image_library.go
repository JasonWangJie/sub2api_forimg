package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	ImageLibraryVisibilityPrivate = "private"
	ImageLibraryVisibilityPublic  = "public"

	ImagePublicationPending   = "pending_review"
	ImagePublicationPublished = "published"
	ImagePublicationRejected  = "rejected"
	ImagePublicationWithdrawn = "withdrawn"
	ImagePublicationHidden    = "admin_hidden"
	ImagePublicationExpired   = "expired"

	MaxAdminImagePublicationBatchSize = 100
)

var (
	ErrImageLibraryNotFound     = apperrors.NotFound("IMAGE_LIBRARY_NOT_FOUND", "image library item not found")
	ErrImagePublicationNotFound = apperrors.NotFound("IMAGE_PUBLICATION_NOT_FOUND", "image publication not found")
	ErrImageLibraryLeaseLost    = apperrors.Conflict("IMAGE_LIBRARY_LEASE_LOST", "image library maintenance lease was lost")
	ErrImageImportInProgress    = apperrors.Conflict("IDEMPOTENCY_IN_PROGRESS", "an image import with this Idempotency-Key is still in progress")
)

type ImageStorageObject struct {
	ID int64 `json:"-"`
	ObjectRef
	State     string    `json:"-"`
	CreatedAt time.Time `json:"-"`
}

type ImageLibraryItem struct {
	ID                int64             `json:"-"`
	AssetID           string            `json:"id"`
	UserID            int64             `json:"-"`
	APIKeyID          *int64            `json:"api_key_id,omitempty"`
	GroupID           *int64            `json:"group_id,omitempty"`
	StorageObjectID   int64             `json:"-"`
	Platform          string            `json:"platform"`
	GenerationMode    string            `json:"generation_mode"`
	SourceType        string            `json:"source_type"`
	SourceTaskID      *string           `json:"source_task_id,omitempty"`
	SourceResultIndex *int              `json:"source_result_index,omitempty"`
	Model             string            `json:"model"`
	RequestedSize     string            `json:"requested_size"`
	ActualSize        string            `json:"actual_size"`
	AspectRatio       string            `json:"aspect_ratio"`
	Quality           string            `json:"quality"`
	Title             string            `json:"title"`
	PrivatePrompt     string            `json:"private_prompt,omitempty"`
	Visibility        string            `json:"visibility"`
	ArchiveStatus     string            `json:"archive_status"`
	ArchiveError      *string           `json:"archive_error,omitempty"`
	ExpiresAt         time.Time         `json:"expires_at"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
	Publication       *ImagePublication `json:"publication,omitempty"`
	ImageURL          string            `json:"image_url"`
	ViewURL           string            `json:"view_url"`
	PreviewURL        string            `json:"preview_url,omitempty"`
	Object            *ObjectRef        `json:"-"`
	Width             int               `json:"width"`
	Height            int               `json:"height"`
	ByteSize          int64             `json:"byte_size"`
	ContentType       string            `json:"content_type"`
}

type ImagePublication struct {
	ID               int64      `json:"-"`
	PublicID         string     `json:"id"`
	LibraryItemID    int64      `json:"-"`
	AssetID          string     `json:"asset_id"`
	UserID           int64      `json:"-"`
	Status           string     `json:"status"`
	PublicTitle      string     `json:"public_title"`
	PublicPrompt     *string    `json:"public_prompt,omitempty"`
	SharePrompt      bool       `json:"share_prompt"`
	ModerationStatus string     `json:"moderation_status"`
	ReviewReason     *string    `json:"review_reason,omitempty"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	ReviewedAt       *time.Time `json:"reviewed_at,omitempty"`
	ExpiresAt        time.Time  `json:"expires_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type PublicImagePlazaItem struct {
	PublicationPK int64     `json:"-"`
	PublicationID string    `json:"id"`
	AssetPK       int64     `json:"-"`
	AssetID       string    `json:"asset_id"`
	UserID        int64     `json:"-"`
	Creator       string    `json:"creator"`
	IsOwner       bool      `json:"is_owner"`
	Title         string    `json:"title"`
	Prompt        *string   `json:"prompt,omitempty"`
	Platform      string    `json:"platform"`
	Model         string    `json:"model"`
	Size          string    `json:"size"`
	AspectRatio   string    `json:"aspect_ratio"`
	Width         int       `json:"width"`
	Height        int       `json:"height"`
	ContentType   string    `json:"content_type"`
	ImageURL      string    `json:"image_url"`
	PreviewURL    string    `json:"preview_url,omitempty"`
	PublishedAt   time.Time `json:"published_at"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
}

type AdminImagePlazaPublication struct {
	PublicImagePlazaItem
	Status           string    `json:"status"`
	ModerationStatus string    `json:"moderation_status"`
	ReviewReason     *string   `json:"review_reason,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type PublicImagePlazaListResult struct {
	Items []PublicImagePlazaItem
	Total int64
}

type AdminImagePublicationBatchItem struct {
	PublicationID string            `json:"publication_id"`
	Success       bool              `json:"success"`
	Publication   *ImagePublication `json:"publication,omitempty"`
	Error         *ImageBatchError  `json:"error,omitempty"`
}

type ImageBatchError struct {
	Code    int    `json:"code"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message"`
}

type AdminImagePublicationBatchResult struct {
	Items     []AdminImagePublicationBatchItem `json:"items"`
	Succeeded int                              `json:"succeeded"`
	Failed    int                              `json:"failed"`
}

type ImagePlazaReport struct {
	ID             int64      `json:"id"`
	PublicationID  string     `json:"publication_id"`
	ReporterUserID int64      `json:"reporter_user_id,omitempty"`
	Reason         string     `json:"reason"`
	Details        string     `json:"details"`
	Status         string     `json:"status"`
	Resolution     *string    `json:"resolution,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type ImageLibraryCursor struct {
	CreatedAt time.Time
	ID        int64
}

type ImageLibraryListParams struct {
	UserID     int64
	Visibility string
	SourceType string
	Platform   string
	Status     string
	Query      string
	Cursor     *ImageLibraryCursor
	Limit      int
}

type ImagePublicationListParams struct {
	UserID      *int64
	Status      string
	Platform    string
	Model       string
	AspectRatio string
	Query       string
	Sort        string
	Cursor      *ImageLibraryCursor
	Limit       int
	Offset      int
}

type CreateImageLibraryAssetParams struct {
	UploadIntentID    int64
	UserID            int64
	APIKeyID          *int64
	GroupID           *int64
	Object            ObjectRef
	Platform          string
	GenerationMode    string
	SourceType        string
	SourceTaskID      *string
	SourceResultIndex *int
	Model             string
	RequestedSize     string
	ActualSize        string
	AspectRatio       string
	Quality           string
	Title             string
	PrivatePrompt     string
	IdempotencyKey    *string
	RequestHash       string
	ExpiresAt         time.Time
	MaxItems          int
	MaxBytes          int64
	RateLimit         int
}

type ImageLibraryUploadIntent struct {
	ID int64
	ObjectRef
	ExpiresAt        time.Time
	CleanupClaimedAt *time.Time
	Referenced       bool
}

type ImageLibraryImportPreflightParams struct {
	UserID          int64
	IdempotencyKey  *string
	RequestHash     string
	IncomingBytes   int64
	MaxItems        int
	MaxBytes        int64
	RateLimit       int
	RecordAttempt   bool
	ContinueAttempt bool
}

type UpdateImageLibraryItemParams struct {
	UserID        int64
	AssetID       string
	Title         *string
	PrivatePrompt *string
}

type CreateImagePublicationParams struct {
	UserID        int64
	AssetID       string
	PublicTitle   string
	SharePrompt   bool
	PublicPrompt  *string
	ExpiresAt     time.Time
	RateLimit     int
	InitialStatus string // empty defaults to pending_review; sync uses published
}

type AdminImageLibraryStats struct {
	ItemCount     int64 `json:"item_count"`
	ObjectCount   int64 `json:"object_count"`
	TotalBytes    int64 `json:"total_bytes"`
	PendingReview int64 `json:"pending_review"`
	Published     int64 `json:"published"`
	OpenReports   int64 `json:"open_reports"`
}

type ImageLibraryCleanupJob struct {
	ID           int64           `json:"id"`
	RequestedBy  *int64          `json:"requested_by,omitempty"`
	Scope        string          `json:"scope"`
	Filters      json.RawMessage `json:"filters"`
	Status       string          `json:"status"`
	LeaseVersion int64           `json:"-"`
	ScannedCount int64           `json:"scanned_count"`
	DeletedCount int64           `json:"deleted_count"`
	DeletedBytes int64           `json:"deleted_bytes"`
	LastError    *string         `json:"last_error,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

type ImageLibraryCleanupPreview struct {
	MatchedItems int64 `json:"matched_items"`
	MatchedBytes int64 `json:"matched_bytes"`
}

type ImageLibraryCleanupBatch struct {
	MatchedItems int64
	Objects      []ObjectRef
	Done         bool
}

type ImageLibraryOutboxEntry struct {
	ID            int64
	AggregateType string
	AggregateID   int64
	EventType     string
	Attempts      int
}

type LegacyImagePlazaItem struct {
	ID          int64
	UserID      int64
	Prompt      string
	Title       string
	Model       string
	Size        string
	Quality     string
	StoragePath string
	ContentType string
}

type ImageLibraryMigrationState struct {
	MigrationKey     string     `json:"migration_key"`
	Status           string     `json:"status"`
	LeaseVersion     int64      `json:"-"`
	LastLegacyID     int64      `json:"last_legacy_id"`
	MigratedCount    int64      `json:"migrated_count"`
	QuarantinedCount int64      `json:"quarantined_count"`
	LastError        *string    `json:"last_error,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type ImageLibraryRepository interface {
	ImageStorageIdentityGuard
	PreflightImport(ctx context.Context, in ImageLibraryImportPreflightParams) (*ImageLibraryItem, bool, error)
	ReleaseImportAttempt(ctx context.Context, userID int64, idempotencyKey *string, requestHash string) error
	PrepareLibraryUploadIntent(ctx context.Context, intent ImageLibraryUploadIntent) (int64, error)
	CompleteLibraryUploadIntent(ctx context.Context, id int64) error
	ClaimExpiredLibraryUploadIntents(ctx context.Context, before, staleBefore time.Time, limit int) ([]ImageLibraryUploadIntent, error)
	CompleteLibraryUploadIntentDeletion(ctx context.Context, id int64, claimedAt time.Time) error
	ReleaseLibraryUploadIntentDeletion(ctx context.Context, id int64, claimedAt time.Time) error
	CreateAsset(ctx context.Context, in CreateImageLibraryAssetParams) (*ImageLibraryItem, bool, error)
	PrepareAssetFromTask(ctx context.Context, userID int64, taskID string, imageIndex int) (*ImageLibraryItem, *ObjectRef, bool, error)
	CreateAssetFromTask(ctx context.Context, userID int64, taskID string, imageIndex int, validated *ObjectRef, title string, expiresAt time.Time, maxItems int, maxBytes int64) (*ImageLibraryItem, bool, error)
	QuarantineAssetFromTask(ctx context.Context, userID int64, taskID string, imageIndex int, ref ObjectRef, reason string) error
	ListForUser(ctx context.Context, in ImageLibraryListParams) ([]ImageLibraryItem, error)
	GetForUser(ctx context.Context, userID int64, assetID string) (*ImageLibraryItem, *ObjectRef, error)
	GetObjectAdmin(ctx context.Context, assetID string) (*ObjectRef, error)
	UpdateForUser(ctx context.Context, in UpdateImageLibraryItemParams) (*ImageLibraryItem, error)
	DeleteForUser(ctx context.Context, userID int64, assetID string) error
	DeleteLegacyPlazaForUser(ctx context.Context, userID int64, identifier, legacyIdempotencyKey string) (bool, error)
	CreatePublication(ctx context.Context, in CreateImagePublicationParams) (*ImagePublication, error)
	WithdrawPublication(ctx context.Context, userID int64, assetID string) error
	ListPublished(ctx context.Context, viewerUserID int64, in ImagePublicationListParams) (*PublicImagePlazaListResult, error)
	GetPublishedObject(ctx context.Context, publicationID string) (*ObjectRef, error)
	GetPublicationObjectAdmin(ctx context.Context, publicationID string) (*ObjectRef, error)
	CreateReport(ctx context.Context, reporterUserID int64, publicationID string, reason, details string) (*ImagePlazaReport, error)
	ListPublicationsAdmin(ctx context.Context, in ImagePublicationListParams) ([]AdminImagePlazaPublication, error)
	TransitionPublication(ctx context.Context, adminUserID int64, publicationID string, action, reason string, retentionUntil time.Time) (*ImagePublication, error)
	ListReportsAdmin(ctx context.Context, status string, cursor *ImageLibraryCursor, limit int) ([]ImagePlazaReport, error)
	ResolveReport(ctx context.Context, adminUserID, reportID int64, status, resolution string) (*ImagePlazaReport, error)
	ListLibraryAdmin(ctx context.Context, in ImageLibraryListParams) ([]ImageLibraryItem, error)
	Stats(ctx context.Context) (*AdminImageLibraryStats, error)
	CreateCleanupJob(ctx context.Context, adminUserID int64, scope string, filters json.RawMessage) (*ImageLibraryCleanupJob, error)
	ListCleanupJobs(ctx context.Context, limit int) ([]ImageLibraryCleanupJob, error)
	PreviewCleanup(ctx context.Context, scope string, filters json.RawMessage) (*ImageLibraryCleanupPreview, error)
	EnsureExpiredCleanupJob(ctx context.Context) error
	ClaimCleanupJob(ctx context.Context, staleBefore time.Time) (*ImageLibraryCleanupJob, error)
	HeartbeatCleanupJob(ctx context.Context, jobID, leaseVersion int64) (bool, error)
	PrepareCleanupBatch(ctx context.Context, jobID, leaseVersion int64, scope string, filters json.RawMessage, limit int) (*ImageLibraryCleanupBatch, error)
	ClaimStaleCleanupObjects(ctx context.Context, staleBefore time.Time, limit int) ([]ObjectRef, error)
	CompleteCleanupObject(ctx context.Context, jobID, leaseVersion int64, ref ObjectRef) error
	ReleaseCleanupObject(ctx context.Context, ref ObjectRef) error
	FinishCleanupJob(ctx context.Context, jobID, leaseVersion int64, status, message string) error
	ClaimLibraryOutbox(ctx context.Context, limit int, staleBefore time.Time) ([]ImageLibraryOutboxEntry, error)
	HeartbeatLibraryOutbox(ctx context.Context, id int64, attempts int) (bool, error)
	PrepareOutboxCleanup(ctx context.Context, itemID int64) ([]ObjectRef, error)
	CompleteLibraryOutbox(ctx context.Context, id int64, attempts int) error
	RetryLibraryOutbox(ctx context.Context, id int64, attempts int, availableAt time.Time, message string) error
	GetMigrationState(ctx context.Context, key string) (*ImageLibraryMigrationState, error)
	ClaimMigration(ctx context.Context, key string, staleBefore time.Time) (*ImageLibraryMigrationState, error)
	HeartbeatMigration(ctx context.Context, key string, leaseVersion int64) (bool, error)
	ListLegacyPlazaItems(ctx context.Context, afterID int64, limit int) ([]LegacyImagePlazaItem, error)
	AdvanceMigration(ctx context.Context, key string, leaseVersion, legacyID, migrated, quarantined int64) error
	FinishMigration(ctx context.Context, key string, leaseVersion int64, status, message string) error
}

type ImageLibraryImportInput struct {
	APIKeyID       *int64
	GroupID        *int64
	Platform       string
	GenerationMode string
	SourceType     string
	Model          string
	RequestedSize  string
	ActualSize     string
	AspectRatio    string
	Quality        string
	Title          string
	Prompt         string
	ImageData      []byte
	DeclaredMIME   string
	IdempotencyKey string
}

type ImageLibraryService struct {
	repo            ImageLibraryRepository
	submissions     ImagePlazaSubmissionRepository
	storageSettings *ImageStorageSettingService
	durableStorage  *ImageDurableStorageService
}

func NewImageLibraryService(
	repo ImageLibraryRepository,
	storageSettings *ImageStorageSettingService,
	durableStorage *ImageDurableStorageService,
) *ImageLibraryService {
	svc := &ImageLibraryService{repo: repo, storageSettings: storageSettings, durableStorage: durableStorage}
	if submissions, ok := repo.(ImagePlazaSubmissionRepository); ok {
		svc.submissions = submissions
	}
	return svc
}

func (s *ImageLibraryService) ImportBytes(ctx context.Context, userID int64, in ImageLibraryImportInput) (*ImageLibraryItem, bool, error) {
	if userID <= 0 || s == nil || s.repo == nil || s.storageSettings == nil {
		return nil, false, apperrors.Unauthorized("UNAUTHORIZED", "unauthorized")
	}
	policy, err := s.storageSettings.LibraryRuntimeConfig(ctx)
	if err != nil {
		return nil, false, err
	}
	idempotencyKey, err := imageLibraryIdempotencyKey(in.IdempotencyKey)
	if err != nil {
		return nil, false, err
	}
	rawChecksum := sha256.Sum256(in.ImageData)
	requestHash := imageLibraryRequestHash(hex.EncodeToString(rawChecksum[:]), in)
	item, reused, err := s.repo.PreflightImport(ctx, ImageLibraryImportPreflightParams{
		UserID: userID, IdempotencyKey: idempotencyKey, RequestHash: requestHash,
		IncomingBytes: int64(len(in.ImageData)), MaxItems: policy.MaxItemsPerUser,
		MaxBytes: policy.MaxBytesPerUser, RateLimit: policy.ImportPerMinute,
		RecordAttempt: true,
	})
	if err != nil {
		return nil, false, err
	}
	if reused {
		s.decorateUserItem(ctx, item)
		return item, true, nil
	}
	validated, err := ValidateImageBytes(in.ImageData, in.DeclaredMIME, policy.MaxImageBytes, policy.MaxImagePixels)
	if err != nil {
		s.releaseImportAttempt(userID, idempotencyKey, requestHash)
		return nil, false, err
	}
	return s.importValidated(ctx, userID, in, policy, validated, requestHash, idempotencyKey, false)
}

// importLegacyBytes is reserved for the recoverable server-side migration.
// Legacy files still pass the current byte, pixel, MIME, and full-decode checks,
// but existing site data is grandfathered past interactive user quotas and
// rate limits so one large account cannot permanently block migration progress.
func (s *ImageLibraryService) importLegacyBytes(
	ctx context.Context,
	userID int64,
	in ImageLibraryImportInput,
	policy ImageLibraryRuntimeConfig,
) (*ImageLibraryItem, bool, error) {
	if userID <= 0 || s == nil || s.repo == nil || s.storageSettings == nil {
		return nil, false, apperrors.Unauthorized("UNAUTHORIZED", "unauthorized")
	}
	validated, err := ValidateImageBytes(in.ImageData, in.DeclaredMIME, policy.MaxImageBytes, policy.MaxImagePixels)
	if err != nil {
		return nil, false, err
	}
	idempotencyKey, err := imageLibraryIdempotencyKey(in.IdempotencyKey)
	if err != nil {
		return nil, false, err
	}
	policy.MaxItemsPerUser = 0
	policy.MaxBytesPerUser = 0
	policy.ImportPerMinute = 0
	return s.importValidated(ctx, userID, in, policy, validated, imageLibraryRequestHash(validated.SHA256, in), idempotencyKey, true)
}

func (s *ImageLibraryService) importValidated(
	ctx context.Context,
	userID int64,
	in ImageLibraryImportInput,
	policy ImageLibraryRuntimeConfig,
	validated *ValidatedImage,
	requestHash string,
	idempotencyKey *string,
	recordAttempt bool,
) (*ImageLibraryItem, bool, error) {
	item, reused, err := s.repo.PreflightImport(ctx, ImageLibraryImportPreflightParams{
		UserID: userID, IdempotencyKey: idempotencyKey, RequestHash: requestHash,
		IncomingBytes: validated.SizeBytes, MaxItems: policy.MaxItemsPerUser,
		MaxBytes: policy.MaxBytesPerUser, RateLimit: policy.ImportPerMinute,
		RecordAttempt: recordAttempt, ContinueAttempt: !recordAttempt,
	})
	if err != nil {
		return nil, false, err
	}
	if reused {
		s.decorateUserItem(ctx, item)
		return item, true, nil
	}

	storage, err := s.writeStorage(ctx)
	if err != nil {
		s.releaseImportAttempt(userID, idempotencyKey, requestHash)
		return nil, false, err
	}

	mode := strings.ToLower(strings.TrimSpace(in.GenerationMode))
	if mode != "realtime" && mode != "async" {
		mode = "import"
	}
	source := strings.ToLower(strings.TrimSpace(in.SourceType))
	if source != "realtime_import" && source != "manual_import" && source != "legacy_plaza" {
		source = "manual_import"
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = truncateRunes(in.Prompt, 80)
	}
	if title == "" {
		title = "Generated image"
	}

	relativeKey := fmt.Sprintf("%d/%s/%s.%s", userID, ImageObjectDatePartition(time.Now()), uuid.NewString(), extFromFormat(validated.Format))
	key, err := s.durableStorage.ObjectKey(relativeKey)
	if err != nil {
		s.releaseImportAttempt(userID, idempotencyKey, requestHash)
		return nil, false, apperrors.ServiceUnavailable("IMAGE_ARCHIVE_FAILED", "failed to prepare image object key").WithCause(err)
	}
	resolver, ok := storage.(DurableImageStorageIntentResolver)
	if !ok {
		s.releaseImportAttempt(userID, idempotencyKey, requestHash)
		return nil, false, apperrors.ServiceUnavailable("IMAGE_ARCHIVE_FAILED", "durable image storage cannot prepare upload intents")
	}
	intentObject, err := resolver.ObjectIntent(key, validated.MIMEType, validated.SizeBytes, validated.SHA256)
	if err != nil {
		s.releaseImportAttempt(userID, idempotencyKey, requestHash)
		return nil, false, apperrors.ServiceUnavailable("IMAGE_ARCHIVE_FAILED", "failed to prepare image upload").WithCause(err)
	}
	intentID, err := s.repo.PrepareLibraryUploadIntent(ctx, ImageLibraryUploadIntent{
		ObjectRef: intentObject,
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	})
	if err != nil {
		s.releaseImportAttempt(userID, idempotencyKey, requestHash)
		return nil, false, apperrors.ServiceUnavailable("IMAGE_ARCHIVE_FAILED", "failed to record image upload intent").WithCause(err)
	}
	object, err := storage.SaveObject(ctx, key, validated.MIMEType, validated.Data)
	if err != nil {
		s.releaseImportAttempt(userID, idempotencyKey, requestHash)
		return nil, false, apperrors.ServiceUnavailable("IMAGE_ARCHIVE_FAILED", "failed to archive image").WithCause(err)
	}
	if !sameImageUploadObject(intentObject, object) {
		s.releaseImportAttempt(userID, idempotencyKey, requestHash)
		return nil, false, apperrors.ServiceUnavailable("IMAGE_ARCHIVE_FAILED", "image storage returned an unexpected object identity")
	}
	object.Width, object.Height = validated.Width, validated.Height
	object.SizeBytes, object.ChecksumSHA256 = validated.SizeBytes, validated.SHA256

	item, reused, err = s.repo.CreateAsset(ctx, CreateImageLibraryAssetParams{
		UploadIntentID: intentID, UserID: userID, APIKeyID: in.APIKeyID, GroupID: in.GroupID, Object: object,
		Platform: cleanLibraryText(in.Platform, 32), GenerationMode: mode, SourceType: source,
		Model: cleanLibraryText(in.Model, 255), RequestedSize: cleanLibraryText(in.RequestedSize, 32),
		ActualSize: cleanLibraryText(in.ActualSize, 32), AspectRatio: cleanLibraryText(in.AspectRatio, 32),
		Quality: cleanLibraryText(in.Quality, 32), Title: cleanLibraryText(title, 200),
		PrivatePrompt: cleanLibraryText(in.Prompt, 8000), IdempotencyKey: idempotencyKey,
		RequestHash: requestHash, ExpiresAt: time.Now().UTC().AddDate(0, 0, policy.RetentionDays),
		MaxItems: policy.MaxItemsPerUser, MaxBytes: policy.MaxBytesPerUser,
	})
	if err != nil {
		s.releaseImportAttempt(userID, idempotencyKey, requestHash)
		return nil, false, err
	}
	if reused {
		// A retry uploaded under a fresh random key before the database resolved
		// idempotency. Keep the intent unless immediate deletion fully succeeds.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if deleteErr := storage.Delete(cleanupCtx, object); deleteErr == nil {
			// On failure the persisted intent remains available to the maintenance worker.
			_ = s.repo.CompleteLibraryUploadIntent(cleanupCtx, intentID)
		}
		cancel()
	}
	s.decorateUserItem(ctx, item)
	return item, reused, nil
}

func sameImageUploadObject(expected, actual ObjectRef) bool {
	return strings.EqualFold(strings.TrimSpace(expected.Provider), strings.TrimSpace(actual.Provider)) &&
		strings.TrimSpace(expected.Bucket) == strings.TrimSpace(actual.Bucket) &&
		expected.ObjectKey == actual.ObjectKey &&
		strings.EqualFold(strings.TrimSpace(expected.ContentType), strings.TrimSpace(actual.ContentType)) &&
		expected.SizeBytes == actual.SizeBytes &&
		strings.EqualFold(strings.TrimSpace(expected.ChecksumSHA256), strings.TrimSpace(actual.ChecksumSHA256))
}

func (s *ImageLibraryService) ImportURL(ctx context.Context, userID int64, rawURL string, in ImageLibraryImportInput) (*ImageLibraryItem, bool, error) {
	if userID <= 0 || s == nil || s.repo == nil || s.storageSettings == nil {
		return nil, false, apperrors.Unauthorized("UNAUTHORIZED", "unauthorized")
	}
	policy, err := s.storageSettings.LibraryRuntimeConfig(ctx)
	if err != nil {
		return nil, false, err
	}
	rawURL = strings.TrimSpace(rawURL)
	idempotencyKey, err := imageLibraryIdempotencyKey(in.IdempotencyKey)
	if err != nil {
		return nil, false, err
	}
	requestHash := imageLibraryRequestHash("url:"+rawURL, in)
	item, reused, err := s.repo.PreflightImport(ctx, ImageLibraryImportPreflightParams{
		UserID: userID, IdempotencyKey: idempotencyKey, RequestHash: requestHash,
		MaxItems: policy.MaxItemsPerUser, MaxBytes: policy.MaxBytesPerUser,
		RateLimit: policy.ImportPerMinute, RecordAttempt: true,
	})
	if err != nil {
		return nil, false, err
	}
	if reused {
		s.decorateUserItem(ctx, item)
		return item, true, nil
	}
	ref, err := (AsyncImageReferenceDownloader{MaxBytes: policy.MaxImageBytes, MaxPixels: policy.MaxImagePixels}).Download(ctx, rawURL)
	if err != nil {
		s.releaseImportAttempt(userID, idempotencyKey, requestHash)
		return nil, false, apperrors.BadRequest("INVALID_IMAGE_URL", "image URL could not be downloaded safely").WithCause(err)
	}
	validated, err := ValidateImageBytes(ref.Data, ref.MIMEType, policy.MaxImageBytes, policy.MaxImagePixels)
	if err != nil {
		s.releaseImportAttempt(userID, idempotencyKey, requestHash)
		return nil, false, apperrors.BadRequest("INVALID_IMAGE_URL", "downloaded image failed strict validation").WithCause(err)
	}
	return s.importValidated(ctx, userID, in, policy, validated, requestHash, idempotencyKey, false)
}

func (s *ImageLibraryService) FromTask(ctx context.Context, userID int64, taskID string, imageIndex int, title string) (*ImageLibraryItem, bool, error) {
	if userID <= 0 || s == nil || s.repo == nil || s.storageSettings == nil || imageIndex < 0 || strings.TrimSpace(taskID) == "" {
		return nil, false, apperrors.BadRequest("INVALID_TASK_RESULT", "task_id and a non-negative image_index are required")
	}
	taskID = strings.TrimSpace(taskID)
	item, ref, reused, err := s.repo.PrepareAssetFromTask(ctx, userID, taskID, imageIndex)
	if err != nil {
		return nil, false, err
	}
	if reused {
		s.decorateUserItem(ctx, item)
		return item, true, nil
	}
	if ref == nil {
		return nil, false, apperrors.NotFound("ASYNC_IMAGE_RESULT_NOT_ARCHIVABLE", "successful and settled task result not found")
	}
	policy, err := s.storageSettings.LibraryRuntimeConfig(ctx)
	if err != nil {
		return nil, false, err
	}
	storage, enabled, err := s.storageSettings.DurableStorage(ctx)
	if err != nil {
		return nil, false, err
	}
	if !enabled || storage == nil {
		return nil, false, apperrors.ServiceUnavailable("IMAGE_STORAGE_DISABLED", "image storage is not configured")
	}
	reader, err := storage.Read(ctx, *ref)
	if err != nil {
		return nil, false, apperrors.ServiceUnavailable("IMAGE_ARCHIVE_FAILED", "failed to read the asynchronous image result").WithCause(err)
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, policy.MaxImageBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, false, apperrors.ServiceUnavailable("IMAGE_ARCHIVE_FAILED", "failed to read the asynchronous image result").WithCause(readErr)
	}
	if closeErr != nil {
		return nil, false, apperrors.ServiceUnavailable("IMAGE_ARCHIVE_FAILED", "failed to close the asynchronous image result").WithCause(closeErr)
	}
	validated, err := ValidateImageBytes(data, ref.ContentType, policy.MaxImageBytes, policy.MaxImagePixels)
	if err != nil {
		if quarantineErr := s.repo.QuarantineAssetFromTask(ctx, userID, taskID, imageIndex, *ref, cleanLibraryText(err.Error(), 1000)); quarantineErr != nil {
			return nil, false, apperrors.ServiceUnavailable("IMAGE_QUARANTINE_FAILED", "invalid asynchronous image result could not be isolated").WithCause(quarantineErr)
		}
		return nil, false, apperrors.BadRequest("ASYNC_IMAGE_RESULT_QUARANTINED", "asynchronous image result failed strict image validation").WithCause(err)
	}
	verified := *ref
	verified.ContentType = validated.MIMEType
	verified.SizeBytes = validated.SizeBytes
	verified.ChecksumSHA256 = validated.SHA256
	verified.Width = validated.Width
	verified.Height = validated.Height
	item, reused, err = s.repo.CreateAssetFromTask(ctx, userID, taskID, imageIndex, &verified, cleanLibraryText(title, 200), time.Now().UTC().AddDate(0, 0, policy.RetentionDays), policy.MaxItemsPerUser, policy.MaxBytesPerUser)
	if err != nil {
		return nil, false, err
	}
	s.decorateUserItem(ctx, item)
	return item, reused, nil
}

func imageLibraryIdempotencyKey(raw string) (*string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return nil, nil
	}
	if len(key) > 255 {
		return nil, apperrors.BadRequest("INVALID_IDEMPOTENCY_KEY", "Idempotency-Key is too long")
	}
	return &key, nil
}

func (s *ImageLibraryService) releaseImportAttempt(userID int64, idempotencyKey *string, requestHash string) {
	if s == nil || s.repo == nil || idempotencyKey == nil {
		return
	}
	_ = s.repo.ReleaseImportAttempt(context.Background(), userID, idempotencyKey, requestHash)
}

func (s *ImageLibraryService) ListForUser(ctx context.Context, in ImageLibraryListParams) ([]ImageLibraryItem, error) {
	items, err := s.repo.ListForUser(ctx, in)
	if err != nil {
		return nil, err
	}
	for i := range items {
		s.decorateUserItem(ctx, &items[i])
	}
	return items, nil
}

func (s *ImageLibraryService) GetForUser(ctx context.Context, userID int64, assetID string) (*ImageLibraryItem, error) {
	item, _, err := s.repo.GetForUser(ctx, userID, assetID)
	if err != nil {
		return nil, err
	}
	s.decorateUserItem(ctx, item)
	return item, nil
}

func (s *ImageLibraryService) OpenPublishedObject(ctx context.Context, publicationID string) (ObjectAccess, io.ReadCloser, string, error) {
	ref, err := s.repo.GetPublishedObject(ctx, publicationID)
	if err != nil {
		return ObjectAccess{}, nil, "", err
	}
	access, err := s.signObject(ctx, *ref)
	if err != nil {
		return ObjectAccess{}, nil, "", err
	}
	if IsLocalObjectAccess(access) {
		reader, contentType, readErr := s.ReadObject(ctx, *ref)
		if readErr != nil {
			return ObjectAccess{}, nil, "", readErr
		}
		return access, reader, contentType, nil
	}
	return access, nil, "", nil
}

func (s *ImageLibraryService) ResolveUserObject(ctx context.Context, userID int64, assetID string) (ObjectAccess, error) {
	_, ref, err := s.repo.GetForUser(ctx, userID, assetID)
	if err != nil {
		return ObjectAccess{}, err
	}
	return s.signObject(ctx, *ref)
}

func (s *ImageLibraryService) ResolvePublishedObject(ctx context.Context, publicationID string) (ObjectAccess, error) {
	ref, err := s.repo.GetPublishedObject(ctx, publicationID)
	if err != nil {
		return ObjectAccess{}, err
	}
	return s.signObject(ctx, *ref)
}

func (s *ImageLibraryService) ResolveAdminObject(ctx context.Context, assetID string) (ObjectAccess, error) {
	ref, err := s.repo.GetObjectAdmin(ctx, assetID)
	if err != nil {
		return ObjectAccess{}, err
	}
	return s.signObject(ctx, *ref)
}

func (s *ImageLibraryService) ResolveAdminPublicationObject(ctx context.Context, publicID string) (ObjectAccess, error) {
	ref, err := s.repo.GetPublicationObjectAdmin(ctx, publicID)
	if err != nil {
		return ObjectAccess{}, err
	}
	return s.signObject(ctx, *ref)
}

func (s *ImageLibraryService) Policy(ctx context.Context) (ImageLibraryRuntimeConfig, error) {
	return s.storageSettings.LibraryRuntimeConfig(ctx)
}

func (s *ImageLibraryService) signObject(ctx context.Context, ref ObjectRef) (ObjectAccess, error) {
	storage, err := s.storageForObject(ctx, ref)
	if err != nil {
		return ObjectAccess{}, err
	}
	policy, err := s.storageSettings.LibraryRuntimeConfig(ctx)
	if err != nil {
		return ObjectAccess{}, err
	}
	return storage.SignURL(ctx, ref, time.Duration(policy.SignedURLExpirySecs)*time.Second)
}

func (s *ImageLibraryService) writeStorage(ctx context.Context) (DurableImageStorage, error) {
	if s == nil || s.durableStorage == nil {
		return nil, apperrors.ServiceUnavailable("IMAGE_DURABLE_STORAGE_DISABLED", "durable image storage is not configured")
	}
	return s.durableStorage.WriteStorage(ctx)
}

func (s *ImageLibraryService) storageForObject(ctx context.Context, ref ObjectRef) (DurableImageStorage, error) {
	if s == nil || s.durableStorage == nil {
		return nil, apperrors.ServiceUnavailable("IMAGE_DURABLE_STORAGE_DISABLED", "durable image storage is not configured")
	}
	return s.durableStorage.StorageForObject(ctx, ref)
}

func (s *ImageLibraryService) ReadObject(ctx context.Context, ref ObjectRef) (io.ReadCloser, string, error) {
	storage, err := s.storageForObject(ctx, ref)
	if err != nil {
		return nil, "", err
	}
	reader, err := storage.Read(ctx, ref)
	if err != nil {
		return nil, "", apperrors.ServiceUnavailable("IMAGE_ARCHIVE_FAILED", "failed to read image object").WithCause(err)
	}
	contentType := strings.TrimSpace(ref.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return reader, contentType, nil
}

func (s *ImageLibraryService) OpenUserObject(ctx context.Context, userID int64, assetID string) (ObjectAccess, io.ReadCloser, string, error) {
	_, ref, err := s.repo.GetForUser(ctx, userID, assetID)
	if err != nil {
		return ObjectAccess{}, nil, "", err
	}
	access, err := s.signObject(ctx, *ref)
	if err != nil {
		return ObjectAccess{}, nil, "", err
	}
	if IsLocalObjectAccess(access) {
		reader, contentType, readErr := s.ReadObject(ctx, *ref)
		if readErr != nil {
			return ObjectAccess{}, nil, "", readErr
		}
		return access, reader, contentType, nil
	}
	return access, nil, "", nil
}

func (s *ImageLibraryService) OpenAdminObject(ctx context.Context, assetID string) (ObjectAccess, io.ReadCloser, string, error) {
	ref, err := s.repo.GetObjectAdmin(ctx, assetID)
	if err != nil {
		return ObjectAccess{}, nil, "", err
	}
	access, err := s.signObject(ctx, *ref)
	if err != nil {
		return ObjectAccess{}, nil, "", err
	}
	if IsLocalObjectAccess(access) {
		reader, contentType, readErr := s.ReadObject(ctx, *ref)
		if readErr != nil {
			return ObjectAccess{}, nil, "", readErr
		}
		return access, reader, contentType, nil
	}
	return access, nil, "", nil
}

func (s *ImageLibraryService) OpenAdminPublicationObject(ctx context.Context, publicID string) (ObjectAccess, io.ReadCloser, string, error) {
	ref, err := s.repo.GetPublicationObjectAdmin(ctx, publicID)
	if err != nil {
		return ObjectAccess{}, nil, "", err
	}
	access, err := s.signObject(ctx, *ref)
	if err != nil {
		return ObjectAccess{}, nil, "", err
	}
	if IsLocalObjectAccess(access) {
		reader, contentType, readErr := s.ReadObject(ctx, *ref)
		if readErr != nil {
			return ObjectAccess{}, nil, "", readErr
		}
		return access, reader, contentType, nil
	}
	return access, nil, "", nil
}

func (s *ImageLibraryService) Update(ctx context.Context, in UpdateImageLibraryItemParams) (*ImageLibraryItem, error) {
	if in.Title != nil {
		v := cleanLibraryText(*in.Title, 200)
		in.Title = &v
	}
	if in.PrivatePrompt != nil {
		v := cleanLibraryText(*in.PrivatePrompt, 8000)
		in.PrivatePrompt = &v
	}
	item, err := s.repo.UpdateForUser(ctx, in)
	if err == nil {
		s.decorateUserItem(ctx, item)
	}
	return item, err
}

func (s *ImageLibraryService) Delete(ctx context.Context, userID int64, assetID string) error {
	return s.repo.DeleteForUser(ctx, userID, assetID)
}

// DeleteLegacyPlazaIdentifier resolves every identifier emitted by the old
// plaza during migration. The bool is false only for an unmigrated positive
// numeric ID, allowing the legacy handler to fall back to its original table.
func (s *ImageLibraryService) DeleteLegacyPlazaIdentifier(ctx context.Context, userID int64, identifier string) (bool, error) {
	if userID <= 0 || s == nil || s.repo == nil {
		return true, apperrors.Unauthorized("UNAUTHORIZED", "unauthorized")
	}
	identifier = strings.TrimSpace(identifier)
	legacyKey := ""
	isNumeric := false
	if legacyID, err := strconv.ParseInt(identifier, 10, 64); err == nil && legacyID > 0 {
		legacyKey = fmt.Sprintf("legacy-image-plaza:%d", legacyID)
		isNumeric = true
	} else if !validImageLibraryIdentifier(identifier, "img_") && !validImageLibraryIdentifier(identifier, "imgpub_") {
		return true, ErrImageLibraryNotFound
	}
	found, err := s.repo.DeleteLegacyPlazaForUser(ctx, userID, identifier, legacyKey)
	if err != nil {
		return true, err
	}
	if found {
		return true, nil
	}
	if isNumeric {
		return false, nil
	}
	return true, ErrImageLibraryNotFound
}

func validImageLibraryIdentifier(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) <= len(prefix) || len(value) > 64 {
		return false
	}
	for _, char := range value[len(prefix):] {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			continue
		}
		return false
	}
	return true
}

func (s *ImageLibraryService) Publish(ctx context.Context, in CreateImagePublicationParams) (*ImagePublication, error) {
	policy, err := s.storageSettings.LibraryRuntimeConfig(ctx)
	if err != nil {
		return nil, err
	}
	in.PublicTitle = cleanLibraryText(in.PublicTitle, 200)
	if in.PublicPrompt != nil {
		v := cleanLibraryText(*in.PublicPrompt, 8000)
		in.PublicPrompt = &v
	}
	in.ExpiresAt = time.Now().UTC().AddDate(0, 0, policy.RetentionDays)
	in.RateLimit = policy.PublishPerMinute
	return s.repo.CreatePublication(ctx, in)
}

func (s *ImageLibraryService) Withdraw(ctx context.Context, userID int64, assetID string) error {
	return s.repo.WithdrawPublication(ctx, userID, assetID)
}

func (s *ImageLibraryService) ListPublished(ctx context.Context, viewerUserID int64, in ImagePublicationListParams) (*PublicImagePlazaListResult, error) {
	var err error
	in.Sort, err = normalizeImagePublicationSort(in.Sort)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.ListPublished(ctx, viewerUserID, in)
	if err != nil {
		return nil, err
	}
	for i := range result.Items {
		result.Items[i].Creator = publicImageCreator(result.Items[i].PublicationID)
		result.Items[i].ImageURL = fmt.Sprintf("/api/v1/image-plaza/%s/content", result.Items[i].PublicationID)
	}
	return result, nil
}

func (s *ImageLibraryService) Report(ctx context.Context, userID int64, publicationID string, reason, details string) (*ImagePlazaReport, error) {
	reason = strings.ToLower(cleanLibraryText(reason, 64))
	switch reason {
	case "spam", "sexual", "violence", "copyright", "privacy", "other":
	default:
		return nil, apperrors.BadRequest("INVALID_REPORT_REASON", "unsupported report reason")
	}
	return s.repo.CreateReport(ctx, userID, publicationID, reason, cleanLibraryText(details, 2000))
}

func (s *ImageLibraryService) AdminTransition(ctx context.Context, adminID int64, publicationID string, action, reason string) (*ImagePublication, error) {
	action, reason, err := normalizeAdminImagePublicationTransition(action, reason)
	if err != nil {
		return nil, err
	}
	policy, err := s.storageSettings.LibraryRuntimeConfig(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.TransitionPublication(ctx, adminID, publicationID, action, reason, time.Now().UTC().AddDate(0, 0, policy.RetentionDays))
}

func (s *ImageLibraryService) AdminBatchTransition(ctx context.Context, adminID int64, publicationIDs []string, action, reason string) (*AdminImagePublicationBatchResult, error) {
	if len(publicationIDs) == 0 {
		return nil, apperrors.BadRequest("PUBLICATION_IDS_REQUIRED", "publication_ids must not be empty")
	}
	if len(publicationIDs) > MaxAdminImagePublicationBatchSize {
		return nil, apperrors.BadRequest("PUBLICATION_BATCH_TOO_LARGE", fmt.Sprintf("publication_ids must contain at most %d items", MaxAdminImagePublicationBatchSize))
	}
	action, reason, err := normalizeAdminImagePublicationTransition(action, reason)
	if err != nil {
		return nil, err
	}
	policy, err := s.storageSettings.LibraryRuntimeConfig(ctx)
	if err != nil {
		return nil, err
	}
	retentionUntil := time.Now().UTC().AddDate(0, 0, policy.RetentionDays)
	result := &AdminImagePublicationBatchResult{Items: make([]AdminImagePublicationBatchItem, 0, len(publicationIDs))}
	for _, rawID := range publicationIDs {
		publicationID := strings.TrimSpace(rawID)
		item := AdminImagePublicationBatchItem{PublicationID: publicationID}
		if !validImageLibraryIdentifier(publicationID, "imgpub_") {
			item.Error = imageBatchError(apperrors.BadRequest("INVALID_PUBLICATION_ID", "invalid publication id"))
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}
		publication, transitionErr := s.repo.TransitionPublication(ctx, adminID, publicationID, action, reason, retentionUntil)
		if transitionErr != nil {
			item.Error = imageBatchError(transitionErr)
			result.Failed++
		} else {
			item.Success = true
			item.Publication = publication
			result.Succeeded++
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func (s *ImageLibraryService) AdminListPublications(ctx context.Context, in ImagePublicationListParams) ([]AdminImagePlazaPublication, error) {
	var err error
	in.Sort, err = normalizeImagePublicationSort(in.Sort)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.ListPublicationsAdmin(ctx, in)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Creator = publicImageCreator(items[i].PublicationID)
		items[i].ImageURL = fmt.Sprintf("/api/v1/image-plaza/%s/content", items[i].PublicationID)
		if ref, refErr := s.repo.GetPublicationObjectAdmin(ctx, items[i].PublicationID); refErr == nil {
			if access, signErr := s.signObject(ctx, *ref); signErr == nil {
				items[i].PreviewURL = access.URL
			}
		}
	}
	return items, nil
}

func normalizeImagePublicationSort(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "newest":
		return "newest", nil
	case "oldest":
		return "oldest", nil
	default:
		return "", apperrors.BadRequest("INVALID_IMAGE_PLAZA_SORT", "sort must be newest or oldest")
	}
}

func normalizeAdminImagePublicationTransition(action, reason string) (string, string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "approve", "reject", "hide", "restore":
	default:
		return "", "", apperrors.BadRequest("INVALID_PUBLICATION_ACTION", "unsupported publication action")
	}
	reason = cleanLibraryText(reason, 2000)
	if (action == "reject" || action == "hide") && reason == "" {
		return "", "", apperrors.BadRequest("REVIEW_REASON_REQUIRED", "a review reason is required for this action")
	}
	return action, reason, nil
}

func imageBatchError(err error) *ImageBatchError {
	return &ImageBatchError{Code: apperrors.Code(err), Reason: apperrors.Reason(err), Message: apperrors.Message(err)}
}

func (s *ImageLibraryService) AdminListReports(ctx context.Context, status string, cursor *ImageLibraryCursor, limit int) ([]ImagePlazaReport, error) {
	return s.repo.ListReportsAdmin(ctx, status, cursor, limit)
}

func (s *ImageLibraryService) AdminResolveReport(ctx context.Context, adminID, reportID int64, status, resolution string) (*ImagePlazaReport, error) {
	return s.repo.ResolveReport(ctx, adminID, reportID, strings.ToLower(strings.TrimSpace(status)), cleanLibraryText(resolution, 2000))
}

func (s *ImageLibraryService) AdminListLibrary(ctx context.Context, in ImageLibraryListParams) ([]ImageLibraryItem, error) {
	items, err := s.repo.ListLibraryAdmin(ctx, in)
	if err != nil {
		return nil, err
	}
	for i := range items {
		s.decorateUserItem(ctx, &items[i])
	}
	return items, nil
}

func (s *ImageLibraryService) AdminStats(ctx context.Context) (*AdminImageLibraryStats, error) {
	return s.repo.Stats(ctx)
}

func (s *ImageLibraryService) AdminCreateCleanupJob(ctx context.Context, adminID int64, scope string, filters json.RawMessage) (*ImageLibraryCleanupJob, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if _, err := s.repo.PreviewCleanup(ctx, scope, filters); err != nil {
		return nil, err
	}
	return s.repo.CreateCleanupJob(ctx, adminID, scope, filters)
}

func (s *ImageLibraryService) AdminPreviewCleanup(ctx context.Context, scope string, filters json.RawMessage) (*ImageLibraryCleanupPreview, error) {
	return s.repo.PreviewCleanup(ctx, strings.ToLower(strings.TrimSpace(scope)), filters)
}

func (s *ImageLibraryService) AdminListCleanupJobs(ctx context.Context, limit int) ([]ImageLibraryCleanupJob, error) {
	return s.repo.ListCleanupJobs(ctx, limit)
}

func imageLibraryRequestHash(checksum string, in ImageLibraryImportInput) string {
	payload, _ := json.Marshal([]string{checksum, in.Platform, in.GenerationMode, in.SourceType, in.Model, in.RequestedSize, in.ActualSize, in.AspectRatio, in.Quality, in.Title, in.Prompt})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func cleanLibraryText(value string, max int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > max {
		runes = runes[:max]
	}
	return string(runes)
}

func publicImageCreator(publicationID string) string {
	sum := sha256.Sum256([]byte("sub2api:image-creator:" + publicationID))
	return "creator-" + hex.EncodeToString(sum[:])[:10]
}

func (s *ImageLibraryService) decorateUserItem(ctx context.Context, item *ImageLibraryItem) {
	if item == nil {
		return
	}
	item.ViewURL = fmt.Sprintf("/api/v1/user/image-library/%s/view", item.AssetID)
	if item.Object != nil {
		if access, err := s.signObject(ctx, *item.Object); err == nil {
			item.PreviewURL = access.URL
			item.ImageURL = access.URL
			return
		}
	}
	item.ImageURL = item.ViewURL
}
