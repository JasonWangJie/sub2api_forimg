package service

import (
	"context"
	"errors"
	"time"
)

var (
	ErrImageResultCleanupConflict       = errors.New("image result cleanup state changed")
	ErrImageResultCleanupPreviewExpired = errors.New("image result cleanup preview expired or unavailable")
)

type ImageResultCleanupPolicy struct {
	Configured        bool       `json:"configured"`
	Enabled           bool       `json:"enabled"`
	RetentionValue    int        `json:"retention_value"`
	RetentionUnit     string     `json:"retention_unit"`
	ScheduleMode      string     `json:"schedule_mode"`
	IntervalMinutes   int        `json:"interval_minutes"`
	DailyTime         string     `json:"daily_time"`
	Timezone          string     `json:"timezone"`
	BatchSize         int        `json:"batch_size"`
	RequestsPerSecond int        `json:"requests_per_second"`
	Revision          int64      `json:"revision"`
	NextRunAt         *time.Time `json:"next_run_at,omitempty"`
	UpdatedBy         *int64     `json:"updated_by,omitempty"`
}

// Identity is a non-secret digest of the physical storage location, not its credentials.
type ImageResultCleanupTarget struct {
	Provider string `json:"provider"`
	Bucket   string `json:"bucket"`
	Identity string `json:"identity"`
}

type ImageResultCleanupRequest struct {
	Before         time.Time                `json:"before"`
	Target         ImageResultCleanupTarget `json:"target"`
	Source         string                   `json:"source"`
	PolicyRevision int64                    `json:"policy_revision"`
	PolicySnapshot ImageResultCleanupPolicy `json:"policy_snapshot"`
}

type ImageResultCleanupPreview struct {
	MatchedItems   int64     `json:"matched_items"`
	MatchedBytes   int64     `json:"matched_bytes"`
	ProtectedCount int64     `json:"protected_count"`
	ScannedCount   int64     `json:"scanned_count"`
	Truncated      bool      `json:"truncated"`
	Before         time.Time `json:"before"`
	PreviewToken   string    `json:"preview_token,omitempty"`
}

type ImageResultCleanupJob struct {
	ID              int64                    `json:"id"`
	RequestedBy     *int64                   `json:"requested_by,omitempty"`
	Scope           string                   `json:"scope"`
	Status          string                   `json:"status"`
	Before          time.Time                `json:"before"`
	Source          string                   `json:"source"`
	Target          ImageResultCleanupTarget `json:"target"`
	PolicyRevision  int64                    `json:"policy_revision"`
	PolicySnapshot  ImageResultCleanupPolicy `json:"policy_snapshot"`
	LeaseVersion    int64                    `json:"-"`
	LeaseUntil      *time.Time               `json:"-"`
	Cursor          int64                    `json:"-"`
	CursorCreatedAt time.Time                `json:"-"`
	ScannedCount    int64                    `json:"scanned_count"`
	DeletedCount    int64                    `json:"deleted_count"`
	DeletedBytes    int64                    `json:"deleted_bytes"`
	DetachedCount   int64                    `json:"detached_count"`
	ProtectedCount  int64                    `json:"protected_count"`
	SkippedCount    int64                    `json:"skipped_count"`
	FailedCount     int64                    `json:"failed_count"`
	CancelRequested bool                     `json:"cancel_requested"`
	NextAttemptAt   *time.Time               `json:"next_attempt_at,omitempty"`
	LastError       *string                  `json:"last_error,omitempty"`
	CreatedAt       time.Time                `json:"created_at"`
	FinishedAt      *time.Time               `json:"finished_at,omitempty"`
}

// A ticket keeps the result row and object identity until deletion is confirmed.
// Action is delete, protected, or skipped. Non-delete actions are already committed.
type ImageResultCleanupObject struct {
	ID       int64
	JobID    int64
	ResultID int64
	ObjectID int64
	Object   ObjectRef
	Action   string
	Attempts int
}

type ImageResultCleanupRepository interface {
	GetImageResultCleanupPolicy(context.Context) (*ImageResultCleanupPolicy, error)
	SaveImageResultCleanupPolicy(context.Context, ImageResultCleanupPolicy, int64) (*ImageResultCleanupPolicy, error)
	PreviewImageResultCleanup(context.Context, ImageResultCleanupRequest, int) (*ImageResultCleanupPreview, error)
	SaveImageResultCleanupPreview(context.Context, string, int64, ImageResultCleanupRequest, time.Time) error
	CreateImageResultCleanupJobFromPreview(context.Context, string, int64, time.Time) (*ImageResultCleanupJob, error)
	EnqueueScheduledImageResultCleanup(context.Context, ImageResultCleanupRequest, time.Time, time.Time) (*ImageResultCleanupJob, error)
	ListImageResultCleanupJobs(context.Context, int) ([]ImageResultCleanupJob, error)
	GetImageResultCleanupJob(context.Context, int64) (*ImageResultCleanupJob, error)
	ControlImageResultCleanupJob(context.Context, int64, string) (*ImageResultCleanupJob, error)
	ClaimImageResultCleanupJob(context.Context, time.Time, time.Time) (*ImageResultCleanupJob, error)
	HeartbeatImageResultCleanupJob(context.Context, int64, int64, time.Time) (bool, error)
	ClaimImageResultCleanupObject(context.Context, int64, int64, time.Time) (*ImageResultCleanupObject, error)
	VerifyImageResultCleanupObject(context.Context, int64, int64, int64, string) error
	CompleteImageResultCleanupObject(context.Context, int64, int64, int64, string, *time.Time, string) error
	ReleaseImageResultCleanupJob(context.Context, int64, int64, string, *time.Time, string) error
	PrepareLegacyImageResultCleanup(context.Context, int64, int64, ImageResultCleanupRequest) (bool, error)
}
