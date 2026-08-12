package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const defaultAsyncImageUploadReservationLease = 15 * time.Minute

const (
	AsyncImageUploadStatusReserved  = "reserved"
	AsyncImageUploadStatusCompleted = "completed"
	AsyncImageUploadStatusFailed    = "failed"
)

var (
	ErrAsyncImageUploadRateLimited = infraerrors.New(
		http.StatusTooManyRequests,
		"ASYNC_IMAGE_UPLOAD_RATE_LIMITED",
		"too many asynchronous image uploads; retry later",
	)
	ErrAsyncImageUploadQuotaExceeded = infraerrors.New(
		http.StatusConflict,
		"ASYNC_IMAGE_UPLOAD_BYTE_QUOTA",
		"asynchronous image input storage quota exceeded",
	)
	ErrAsyncImageUploadIdempotencyConflict = infraerrors.New(
		http.StatusConflict,
		"ASYNC_IMAGE_UPLOAD_IDEMPOTENCY_CONFLICT",
		"Idempotency-Key was already used with a different image upload",
	)
	ErrAsyncImageUploadInProgress = infraerrors.New(
		http.StatusConflict,
		"ASYNC_IMAGE_UPLOAD_IN_PROGRESS",
		"an image upload with this Idempotency-Key is still in progress",
	)
	ErrAsyncImageUploadResultUnavailable = infraerrors.New(
		http.StatusConflict,
		"ASYNC_IMAGE_UPLOAD_RESULT_UNAVAILABLE",
		"the completed upload is no longer available; use a new Idempotency-Key",
	)
	ErrAsyncImageUploadAliasLimit = infraerrors.New(
		http.StatusTooManyRequests,
		"ASYNC_IMAGE_UPLOAD_ALIAS_LIMIT",
		"too many signed URL aliases exist for this uploaded image",
	)
	ErrAsyncImageUploadReservationInvalid = infraerrors.New(
		http.StatusConflict,
		"ASYNC_IMAGE_UPLOAD_RESERVATION_INVALID",
		"asynchronous image upload reservation is no longer active",
	)
	ErrAsyncImageUploadUnavailable = infraerrors.New(
		http.StatusServiceUnavailable,
		"ASYNC_IMAGE_UPLOAD_ADMISSION_UNAVAILABLE",
		"asynchronous image upload admission control is unavailable",
	)
)

type AsyncImageUploadReservation struct {
	ID                   int64
	ReservationID        string
	UserID               int64
	APIKeyID             int64
	IdempotencyKey       *string
	RequestHash          string
	ByteSize             int64
	Status               string
	InputObjectID        *int64
	FailureReason        *string
	LeaseExpiresAt       *time.Time
	ReservedAt           time.Time
	CompletedAt          *time.Time
	FailedAt             *time.Time
	ObjectIntent         *ObjectRef
	CleanupClaimedAt     *time.Time
	CleanupDeleteCount   int
	LastDeletedAt        *time.Time
	IdempotencyExpiresAt *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type ReserveAsyncImageUploadParams struct {
	AdmissionID         string
	ReservationID       string
	UserID              int64
	APIKeyID            int64
	IdempotencyKey      *string
	RequestHash         string
	ByteSize            int64
	UploadPerMinute     int
	MaxInputBytesPerKey int64
	Now                 time.Time
	LeaseExpiresAt      time.Time
}

type AdmitAsyncImageUploadParams struct {
	AdmissionID     string
	UserID          int64
	APIKeyID        int64
	UploadPerMinute int
	Now             time.Time
}

type AsyncImageUploadAdmission struct {
	AdmissionID string
	AttemptedAt time.Time
}

type AsyncImageUploadReservationResult struct {
	Reservation *AsyncImageUploadReservation
	InputObject *AsyncImageInputObject
	Reused      bool
}

type CompleteAsyncImageUploadParams struct {
	ReservationID string
	UserID        int64
	APIKeyID      int64
	RequestHash   string
	ObjectRef     ObjectRef
	URLHash       string
	Filename      string
	ExpiresAt     time.Time
}

type SetAsyncImageUploadObjectIntentParams struct {
	ReservationID string
	UserID        int64
	APIKeyID      int64
	RequestHash   string
	ObjectRef     ObjectRef
}

type AsyncImageUploadCleanupIntent struct {
	ReservationID    string
	ObjectRef        ObjectRef
	CleanupClaimedAt time.Time
}

type RegisterAsyncImageInputURLAliasParams struct {
	InputObjectID int64
	UserID        int64
	APIKeyID      int64
	URLHash       string
	ExpiresAt     time.Time
}

// AsyncImageUploadReservationRepository owns the PostgreSQL admission state
// for SC uploads. It is deliberately separate from the core task repository
// interface so existing alternate repositories and test doubles remain valid.
type AsyncImageUploadReservationRepository interface {
	AdmitAsyncImageUpload(ctx context.Context, params AdmitAsyncImageUploadParams) (*AsyncImageUploadAdmission, error)
	ReserveAsyncImageUpload(ctx context.Context, params ReserveAsyncImageUploadParams) (*AsyncImageUploadReservationResult, error)
	SetAsyncImageUploadObjectIntent(ctx context.Context, params SetAsyncImageUploadObjectIntentParams) error
	CompleteAsyncImageUpload(ctx context.Context, params CompleteAsyncImageUploadParams) (*AsyncImageInputObject, error)
	FailAsyncImageUpload(ctx context.Context, reservationID, requestHash, reason string) (released bool, err error)
	RegisterAsyncImageInputURLAlias(ctx context.Context, params RegisterAsyncImageInputURLAliasParams) error
}

func (s *AsyncImageTaskService) AdmitInputUpload(ctx context.Context, params AdmitAsyncImageUploadParams) (*AsyncImageUploadAdmission, error) {
	repo, ok := s.uploadReservationRepository()
	if !ok {
		return nil, ErrAsyncImageUploadUnavailable
	}
	if params.AdmissionID == "" {
		var err error
		params.AdmissionID, err = NewAsyncImageTaskID()
		if err != nil {
			return nil, ErrAsyncImageUploadUnavailable.WithCause(err)
		}
	}
	if params.Now.IsZero() {
		params.Now = time.Now().UTC()
	} else {
		params.Now = params.Now.UTC()
	}
	if params.UserID <= 0 || params.APIKeyID <= 0 || params.UploadPerMinute <= 0 || len(params.AdmissionID) > 64 {
		return nil, ErrAsyncImageInvalidInput
	}
	admission, err := repo.AdmitAsyncImageUpload(ctx, params)
	if err != nil {
		return nil, normalizeAsyncImageUploadRepositoryError(err)
	}
	if admission == nil || strings.TrimSpace(admission.AdmissionID) == "" {
		return nil, ErrAsyncImageUploadUnavailable
	}
	return admission, nil
}

type AsyncImageUploadIntentRetentionRepository interface {
	DeleteExpiredAsyncImageUploadAdmissionState(ctx context.Context, before time.Time, limit int) (int64, error)
	ClaimAsyncImageUploadCleanupIntents(ctx context.Context, before, staleBefore time.Time, limit int) ([]AsyncImageUploadCleanupIntent, error)
	CompleteAsyncImageUploadIntentDeletion(ctx context.Context, reservationID string, claimedAt time.Time) (removed bool, err error)
	ReleaseAsyncImageUploadIntentDeletion(ctx context.Context, reservationID string, claimedAt time.Time) error
}

// AsyncImageUploadRequestHash binds an Idempotency-Key to the bounded raw
// upload and its declared metadata. It can be calculated before full image
// decoding, so rate and byte admission happen before CPU-intensive validation.
func AsyncImageUploadRequestHash(data []byte, contentType, filename string) string {
	contentSum := sha256.Sum256(data)
	canonical := strings.Join([]string{
		hex.EncodeToString(contentSum[:]),
		strings.ToLower(strings.TrimSpace(contentType)),
		strings.TrimSpace(filename),
		fmt.Sprintf("%d", len(data)),
	}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func (s *AsyncImageTaskService) ReserveInputUpload(ctx context.Context, params ReserveAsyncImageUploadParams) (*AsyncImageUploadReservationResult, error) {
	repo, ok := s.uploadReservationRepository()
	if !ok {
		return nil, ErrAsyncImageUploadUnavailable
	}
	params.ReservationID = strings.TrimSpace(params.ReservationID)
	params.RequestHash = strings.ToLower(strings.TrimSpace(params.RequestHash))
	if params.IdempotencyKey != nil {
		key := strings.TrimSpace(*params.IdempotencyKey)
		if key == "" {
			params.IdempotencyKey = nil
		} else {
			params.IdempotencyKey = &key
		}
	}
	if params.ReservationID == "" {
		var err error
		params.ReservationID, err = NewAsyncImageTaskID()
		if err != nil {
			return nil, ErrAsyncImageUploadUnavailable.WithCause(err)
		}
	}
	if params.Now.IsZero() {
		params.Now = time.Now().UTC()
	} else {
		params.Now = params.Now.UTC()
	}
	if params.LeaseExpiresAt.IsZero() {
		params.LeaseExpiresAt = params.Now.Add(defaultAsyncImageUploadReservationLease)
	} else {
		params.LeaseExpiresAt = params.LeaseExpiresAt.UTC()
	}
	if !validAsyncImageUploadReservation(params) {
		return nil, ErrAsyncImageInvalidInput
	}
	result, err := repo.ReserveAsyncImageUpload(ctx, params)
	if err != nil {
		return nil, normalizeAsyncImageUploadRepositoryError(err)
	}
	if result == nil || result.Reservation == nil {
		return nil, ErrAsyncImageUploadUnavailable
	}
	return result, nil
}

func (s *AsyncImageTaskService) CompleteInputUpload(ctx context.Context, params CompleteAsyncImageUploadParams) (*AsyncImageInputObject, error) {
	repo, ok := s.uploadReservationRepository()
	if !ok || !validCompleteAsyncImageUpload(params) {
		return nil, ErrAsyncImageUploadUnavailable
	}
	object, err := repo.CompleteAsyncImageUpload(ctx, params)
	if err != nil {
		return nil, normalizeAsyncImageUploadRepositoryError(err)
	}
	return object, nil
}

func (s *AsyncImageTaskService) SetInputUploadObjectIntent(ctx context.Context, params SetAsyncImageUploadObjectIntentParams) error {
	repo, ok := s.uploadReservationRepository()
	ref := params.ObjectRef
	if !ok || strings.TrimSpace(params.ReservationID) == "" || params.UserID <= 0 || params.APIKeyID <= 0 ||
		len(strings.TrimSpace(params.RequestHash)) != 64 || strings.TrimSpace(ref.Provider) == "" ||
		strings.TrimSpace(ref.Bucket) == "" || strings.TrimSpace(ref.ObjectKey) == "" ||
		strings.TrimSpace(ref.ContentType) == "" || ref.SizeBytes <= 0 || len(strings.TrimSpace(ref.ChecksumSHA256)) != 64 {
		return ErrAsyncImageUploadUnavailable
	}
	if err := repo.SetAsyncImageUploadObjectIntent(ctx, params); err != nil {
		return normalizeAsyncImageUploadRepositoryError(err)
	}
	return nil
}

func (s *AsyncImageTaskService) FailInputUpload(ctx context.Context, reservationID, requestHash, reason string) (bool, error) {
	repo, ok := s.uploadReservationRepository()
	if !ok {
		return false, ErrAsyncImageUploadUnavailable
	}
	released, err := repo.FailAsyncImageUpload(ctx, strings.TrimSpace(reservationID), strings.ToLower(strings.TrimSpace(requestHash)), strings.TrimSpace(reason))
	if err != nil {
		return false, normalizeAsyncImageUploadRepositoryError(err)
	}
	return released, nil
}

func (s *AsyncImageTaskService) RegisterInputURLAlias(ctx context.Context, params RegisterAsyncImageInputURLAliasParams) error {
	repo, ok := s.uploadReservationRepository()
	if !ok || params.InputObjectID <= 0 || params.UserID <= 0 || params.APIKeyID <= 0 ||
		len(strings.TrimSpace(params.URLHash)) != 64 || params.ExpiresAt.IsZero() {
		return ErrAsyncImageUploadUnavailable
	}
	if err := repo.RegisterAsyncImageInputURLAlias(ctx, params); err != nil {
		return normalizeAsyncImageUploadRepositoryError(err)
	}
	return nil
}

func (s *AsyncImageTaskService) uploadReservationRepository() (AsyncImageUploadReservationRepository, bool) {
	if s == nil || s.repo == nil {
		return nil, false
	}
	repo, ok := s.repo.(AsyncImageUploadReservationRepository)
	return repo, ok
}

func validAsyncImageUploadReservation(params ReserveAsyncImageUploadParams) bool {
	if strings.TrimSpace(params.AdmissionID) == "" || params.UserID <= 0 || params.APIKeyID <= 0 || params.ByteSize <= 0 ||
		params.UploadPerMinute <= 0 || params.MaxInputBytesPerKey <= 0 ||
		len(params.ReservationID) > 64 || len(params.RequestHash) != 64 ||
		!params.LeaseExpiresAt.After(params.Now) {
		return false
	}
	if _, err := hex.DecodeString(params.RequestHash); err != nil {
		return false
	}
	return params.IdempotencyKey == nil || len(*params.IdempotencyKey) <= 255
}

func validCompleteAsyncImageUpload(params CompleteAsyncImageUploadParams) bool {
	ref := params.ObjectRef
	return strings.TrimSpace(params.ReservationID) != "" && params.UserID > 0 && params.APIKeyID > 0 &&
		len(strings.TrimSpace(params.RequestHash)) == 64 && len(strings.TrimSpace(params.URLHash)) == 64 &&
		strings.TrimSpace(ref.Provider) != "" && strings.TrimSpace(ref.Bucket) != "" &&
		strings.TrimSpace(ref.ObjectKey) != "" && strings.TrimSpace(ref.ContentType) != "" &&
		ref.SizeBytes > 0 && strings.TrimSpace(ref.ChecksumSHA256) != "" && !params.ExpiresAt.IsZero()
}

func normalizeAsyncImageUploadRepositoryError(err error) error {
	if err == nil {
		return nil
	}
	known := []error{
		ErrAsyncImageUploadRateLimited,
		ErrAsyncImageUploadQuotaExceeded,
		ErrAsyncImageUploadIdempotencyConflict,
		ErrAsyncImageUploadInProgress,
		ErrAsyncImageUploadResultUnavailable,
		ErrAsyncImageUploadAliasLimit,
		ErrAsyncImageUploadReservationInvalid,
		ErrAsyncImageInvalidInput,
	}
	for _, candidate := range known {
		if errors.Is(err, candidate) {
			return err
		}
	}
	return ErrAsyncImageUploadUnavailable.WithCause(err)
}
