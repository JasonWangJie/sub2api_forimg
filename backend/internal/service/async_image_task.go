package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	AsyncImageProtocolBB = "bb"
	AsyncImageProtocolSC = "sc"

	AsyncImageRequestTypeTextToImage  = "text_to_image"
	AsyncImageRequestTypeImageToImage = "image_to_image"
)

const (
	AsyncImageTaskStatusQueued            = "queued"
	AsyncImageTaskStatusInvoking          = "invoking"
	AsyncImageTaskStatusUpstreamSucceeded = "upstream_succeeded"
	AsyncImageTaskStatusUploading         = "uploading"
	AsyncImageTaskStatusBillingPending    = "billing_pending"
	AsyncImageTaskStatusSucceeded         = "succeeded"
	AsyncImageTaskStatusFailed            = "failed"
	AsyncImageTaskStatusExecutionUnknown  = "execution_unknown"
	AsyncImageTaskStatusStorageFailed     = "storage_failed"
	AsyncImageTaskStatusBillingFailed     = "billing_failed"
	AsyncImageTaskStatusExpired           = "expired"
)

const (
	AsyncImageBillingStatusPending     = "pending"
	AsyncImageBillingStatusPrepared    = "prepared"
	AsyncImageBillingStatusApplying    = "applying"
	AsyncImageBillingStatusSucceeded   = "succeeded"
	AsyncImageBillingStatusFailed      = "failed"
	AsyncImageBillingStatusNotBillable = "not_billable"
)

var (
	ErrAsyncImageTaskNotFound        = infraerrors.New(http.StatusNotFound, "ASYNC_IMAGE_TASK_NOT_FOUND", "asynchronous image task not found")
	ErrAsyncImageTaskExists          = infraerrors.New(http.StatusConflict, "ASYNC_IMAGE_TASK_EXISTS", "asynchronous image task already exists")
	ErrAsyncImageIdempotencyConflict = infraerrors.New(http.StatusConflict, "ASYNC_IMAGE_IDEMPOTENCY_CONFLICT", "idempotency key reused with a different asynchronous image request")
	ErrAsyncImageInvalidInput        = infraerrors.New(http.StatusBadRequest, "ASYNC_IMAGE_INVALID_INPUT", "invalid asynchronous image task input")
	ErrAsyncImageInvalidTransition   = infraerrors.New(http.StatusConflict, "ASYNC_IMAGE_INVALID_TRANSITION", "asynchronous image task state changed or transition is invalid")
	ErrAsyncImageOutboxClaimLost     = infraerrors.New(http.StatusConflict, "ASYNC_IMAGE_OUTBOX_CLAIM_LOST", "asynchronous image outbox claim is no longer owned by this dispatcher")
)

// AsyncImageTask is the durable PostgreSQL representation. RequestPayload is
// encrypted by the caller before it crosses this persistence boundary.
type AsyncImageTask struct {
	ID                  int64           `json:"-"`
	TaskID              string          `json:"task_id"`
	UserID              int64           `json:"user_id"`
	APIKeyID            int64           `json:"api_key_id"`
	GroupID             int64           `json:"group_id"`
	AccountID           *int64          `json:"account_id,omitempty"`
	Protocol            string          `json:"protocol"`
	Platform            string          `json:"platform"`
	RequestType         string          `json:"request_type"`
	Model               string          `json:"model"`
	Status              string          `json:"status"`
	BillingStatus       string          `json:"billing_status"`
	Progress            int             `json:"progress"`
	RequestedImageSize  *string         `json:"requested_image_size,omitempty"`
	ActualImageSize     *string         `json:"actual_image_size,omitempty"`
	AspectRatio         *string         `json:"aspect_ratio,omitempty"`
	ImageCount          int             `json:"image_count"`
	ActualCost          *float64        `json:"actual_cost,omitempty"`
	Currency            string          `json:"currency"`
	IdempotencyKey      *string         `json:"-"`
	RequestHash         string          `json:"-"`
	RequestPayload      []byte          `json:"-"`
	PromptPreview       *string         `json:"prompt_preview,omitempty"`
	UpstreamRequestID   *string         `json:"upstream_request_id,omitempty"`
	BillingRequestID    *string         `json:"billing_request_id,omitempty"`
	BillingPayload      json.RawMessage `json:"-"`
	RetryCount          int             `json:"retry_count"`
	StorageRetryCount   int             `json:"storage_retry_count"`
	BillingRetryCount   int             `json:"billing_retry_count"`
	Version             int64           `json:"version"`
	ErrorCode           *string         `json:"error_code,omitempty"`
	ErrorMessage        *string         `json:"error_message,omitempty"`
	SubmittedAt         time.Time       `json:"submitted_at"`
	StartedAt           *time.Time      `json:"started_at,omitempty"`
	UpstreamSucceededAt *time.Time      `json:"upstream_succeeded_at,omitempty"`
	FinishedAt          *time.Time      `json:"finished_at,omitempty"`
	ExpiresAt           *time.Time      `json:"expires_at,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type CreateAsyncImageTaskParams struct {
	TaskID             string
	UserID             int64
	APIKeyID           int64
	GroupID            int64
	Protocol           string
	Platform           string
	RequestType        string
	Model              string
	RequestedImageSize *string
	AspectRatio        *string
	ImageCount         int
	IdempotencyKey     *string
	RequestHash        string
	RequestPayload     []byte
	PromptPreview      *string
	ExpiresAt          *time.Time
	OutboxPayload      json.RawMessage
	InputObjectIDs     []int64
}

type AsyncImageTaskFilter struct {
	UserID          *int64
	APIKeyID        *int64
	GroupID         *int64
	AccountID       *int64
	TaskID          string
	Protocol        string
	Platform        string
	RequestType     string
	Status          string
	BillingStatus   string
	Model           string
	Search          string
	StorageProvider string
	CreatedAfter    *time.Time
	CreatedBefore   *time.Time
	SortBy          string
	SortOrder       string
	Limit           int
	Offset          int
}

type AsyncImageTaskTransition struct {
	TaskID                string
	ExpectedVersion       int64
	UpdatedBefore         *time.Time
	FromStatuses          []string
	ToStatus              string
	Progress              *int
	AccountID             *int64
	BillingStatus         *string
	ActualCost            *float64
	ActualImageSize       *string
	ImageCount            *int
	UpstreamRequestID     *string
	BillingRequestID      *string
	BillingPayload        json.RawMessage
	ErrorCode             *string
	ErrorMessage          *string
	ClearError            bool
	IncrementRetry        bool
	IncrementStorageRetry bool
	IncrementBillingRetry bool
	StartedAt             *time.Time
	UpstreamSucceededAt   *time.Time
	FinishedAt            *time.Time
	ExpiresAt             *time.Time
	ClearRequestPayload   bool
	EventType             string
	EventPayload          json.RawMessage
}

type AsyncImageStagingObject struct {
	ID          int64     `json:"-"`
	TaskID      string    `json:"task_id"`
	ImageIndex  int       `json:"image_index"`
	Content     []byte    `json:"-"`
	ContentType string    `json:"content_type"`
	ByteSize    int64     `json:"byte_size"`
	Checksum    string    `json:"checksum"`
	Width       *int      `json:"width,omitempty"`
	Height      *int      `json:"height,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type RecordAsyncImageUpstreamSuccessParams struct {
	TaskID              string
	ExpectedVersion     int64
	AccountID           int64
	UpstreamRequestID   *string
	ActualImageSize     *string
	ImageCount          int
	BillingRequestID    string
	BillingPayload      json.RawMessage
	StagingObjects      []AsyncImageStagingObject
	UpstreamSucceededAt time.Time
	EventPayload        json.RawMessage
}

type AsyncImageResult struct {
	ID               int64     `json:"-"`
	TaskID           string    `json:"task_id"`
	ImageIndex       int       `json:"image_index"`
	Provider         string    `json:"provider"`
	Bucket           string    `json:"bucket"`
	ObjectKey        string    `json:"object_key"`
	ContentType      string    `json:"content_type"`
	ByteSize         int64     `json:"byte_size"`
	Checksum         string    `json:"checksum"`
	Width            *int      `json:"width,omitempty"`
	Height           *int      `json:"height,omitempty"`
	CleanupClaimedAt time.Time `json:"-"`
	CreatedAt        time.Time `json:"created_at"`
}

type AsyncImageResultUploadIntent struct {
	ID               int64
	TaskID           string
	ImageIndex       int
	ObjectRef        ObjectRef
	ExpiresAt        time.Time
	CleanupClaimedAt *time.Time
}

type AsyncImageEvent struct {
	ID         int64           `json:"id"`
	TaskID     string          `json:"task_id"`
	EventType  string          `json:"event_type"`
	FromStatus *string         `json:"from_status,omitempty"`
	ToStatus   *string         `json:"to_status,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

type AsyncImageOutboxEntry struct {
	ID          int64           `json:"id"`
	TaskID      string          `json:"task_id"`
	EventType   string          `json:"event_type"`
	DedupKey    string          `json:"dedup_key"`
	Payload     json.RawMessage `json:"payload"`
	Attempts    int             `json:"attempts"`
	AvailableAt time.Time       `json:"available_at"`
	ClaimedAt   *time.Time      `json:"claimed_at,omitempty"`
	ClaimToken  string          `json:"-"`
	PublishedAt *time.Time      `json:"published_at,omitempty"`
	LastError   *string         `json:"last_error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type AsyncImageTaskDetails struct {
	Task    *AsyncImageTask    `json:"task"`
	Results []AsyncImageResult `json:"results"`
	Events  []AsyncImageEvent  `json:"events"`
}

type AsyncImageTaskRepository interface {
	// CreateAsyncImageTask atomically writes the task, initial event, and ready
	// outbox row. Reused is true when the same API key/idempotency key and
	// request hash already exist; a different hash returns the conflict error.
	CreateAsyncImageTask(ctx context.Context, params CreateAsyncImageTaskParams) (task *AsyncImageTask, reused bool, err error)
	GetAsyncImageTaskByTaskID(ctx context.Context, taskID string) (*AsyncImageTask, error)
	GetAsyncImageTaskForAPIKey(ctx context.Context, apiKeyID int64, taskID string) (*AsyncImageTask, error)
	GetAsyncImageTaskForUser(ctx context.Context, userID int64, taskID string) (*AsyncImageTask, error)
	ListAsyncImageTasks(ctx context.Context, filter AsyncImageTaskFilter) ([]*AsyncImageTask, int64, error)
	ListRecoverableAsyncImageTasks(ctx context.Context, statuses []string, updatedBefore time.Time, storageRetryLimit, billingRetryLimit, limit int) ([]*AsyncImageTask, error)
	TouchAsyncImageTask(ctx context.Context, taskID string, statuses []string) error
	TransitionAsyncImageTask(ctx context.Context, transition AsyncImageTaskTransition) (*AsyncImageTask, error)
	RecordAsyncImageUpstreamSuccess(ctx context.Context, params RecordAsyncImageUpstreamSuccessParams) (*AsyncImageTask, error)
	ReplaceAsyncImageResults(ctx context.Context, taskID string, results []AsyncImageResult) error
	PrepareAsyncImageResultUploadIntents(ctx context.Context, taskID string, intents []AsyncImageResultUploadIntent) error
	ListAsyncImageResults(ctx context.Context, taskID string) ([]AsyncImageResult, error)
	ListAsyncImageStagingObjects(ctx context.Context, taskID string) ([]AsyncImageStagingObject, error)
	DeleteAsyncImageStagingObjects(ctx context.Context, taskID string) error
	DeleteExpiredAsyncImageStagingObjects(ctx context.Context, before time.Time, limit int) (int64, error)
	AppendAsyncImageEvent(ctx context.Context, event AsyncImageEvent) error
	ListAsyncImageEvents(ctx context.Context, taskID string) ([]AsyncImageEvent, error)
	EnqueueAsyncImageOutbox(ctx context.Context, entry AsyncImageOutboxEntry) error
	ClaimAsyncImageOutbox(ctx context.Context, limit int, staleBefore time.Time) ([]AsyncImageOutboxEntry, error)
	MarkAsyncImageOutboxPublished(ctx context.Context, id int64, claimToken string, publishedAt time.Time) error
	MarkAsyncImageOutboxFailed(ctx context.Context, id int64, claimToken string, availableAt time.Time, message string) error
}

// AsyncImageLibraryArchiveOutboxRepository is implemented by persistent
// repositories after the image library migration. Keeping it separate lets
// alternate/test task repositories continue to implement the core contract.
type AsyncImageLibraryArchiveOutboxRepository interface {
	EnqueueMissingAsyncImageLibraryArchives(ctx context.Context, limit int) (int64, error)
	MarkAsyncImageOutboxTerminal(ctx context.Context, id int64, claimToken string, publishedAt time.Time, message string) error
}

// AsyncImageInvocationTimeoutRepository lists hung invoking tasks by wall-clock
// start time. Heartbeats keep updated_at fresh, so lease-only recovery cannot
// terminate upstream calls that ignore context cancellation.
type AsyncImageInvocationTimeoutRepository interface {
	ListTimedOutInvokingAsyncImageTasks(ctx context.Context, startedBefore time.Time, limit int) ([]*AsyncImageTask, error)
}

type AsyncImageTaskService struct {
	repo AsyncImageTaskRepository
}

func NewAsyncImageTaskService(repo AsyncImageTaskRepository) *AsyncImageTaskService {
	return &AsyncImageTaskService{repo: repo}
}

func (s *AsyncImageTaskService) Create(ctx context.Context, params CreateAsyncImageTaskParams) (*AsyncImageTask, bool, error) {
	if s == nil || s.repo == nil {
		return nil, false, ErrAsyncImageInvalidInput
	}
	params.TaskID = strings.TrimSpace(params.TaskID)
	if params.TaskID == "" {
		var err error
		params.TaskID, err = NewAsyncImageTaskID()
		if err != nil {
			return nil, false, err
		}
	}
	params.Protocol = strings.ToLower(strings.TrimSpace(params.Protocol))
	params.Platform = strings.ToLower(strings.TrimSpace(params.Platform))
	params.RequestType = strings.ToLower(strings.TrimSpace(params.RequestType))
	params.Model = strings.TrimSpace(params.Model)
	params.RequestHash = strings.TrimSpace(params.RequestHash)
	if params.IdempotencyKey != nil {
		key := strings.TrimSpace(*params.IdempotencyKey)
		if key == "" {
			params.IdempotencyKey = nil
		} else {
			params.IdempotencyKey = &key
		}
	}
	if !validAsyncImageCreateParams(params) {
		return nil, false, ErrAsyncImageInvalidInput
	}
	return s.repo.CreateAsyncImageTask(ctx, params)
}

func (s *AsyncImageTaskService) GetForAPIKey(ctx context.Context, apiKeyID int64, taskID string) (*AsyncImageTaskDetails, error) {
	if apiKeyID <= 0 || strings.TrimSpace(taskID) == "" {
		return nil, ErrAsyncImageTaskNotFound
	}
	task, err := s.repo.GetAsyncImageTaskForAPIKey(ctx, apiKeyID, strings.TrimSpace(taskID))
	if err != nil {
		return nil, err
	}
	return s.loadDetails(ctx, task)
}

func (s *AsyncImageTaskService) GetForUser(ctx context.Context, userID int64, taskID string) (*AsyncImageTaskDetails, error) {
	if userID <= 0 || strings.TrimSpace(taskID) == "" {
		return nil, ErrAsyncImageTaskNotFound
	}
	task, err := s.repo.GetAsyncImageTaskForUser(ctx, userID, strings.TrimSpace(taskID))
	if err != nil {
		return nil, err
	}
	return s.loadDetails(ctx, task)
}

func (s *AsyncImageTaskService) GetForAdmin(ctx context.Context, taskID string) (*AsyncImageTaskDetails, error) {
	task, err := s.repo.GetAsyncImageTaskByTaskID(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return nil, err
	}
	return s.loadDetails(ctx, task)
}

func (s *AsyncImageTaskService) ListForUser(ctx context.Context, userID int64, filter AsyncImageTaskFilter) ([]*AsyncImageTask, int64, error) {
	if userID <= 0 {
		return nil, 0, ErrAsyncImageInvalidInput
	}
	filter.UserID = &userID
	normalizeAsyncImageTaskFilter(&filter)
	return s.repo.ListAsyncImageTasks(ctx, filter)
}

func (s *AsyncImageTaskService) ListForAdmin(ctx context.Context, filter AsyncImageTaskFilter) ([]*AsyncImageTask, int64, error) {
	normalizeAsyncImageTaskFilter(&filter)
	return s.repo.ListAsyncImageTasks(ctx, filter)
}

func (s *AsyncImageTaskService) Transition(ctx context.Context, transition AsyncImageTaskTransition) (*AsyncImageTask, error) {
	if s == nil || s.repo == nil || strings.TrimSpace(transition.TaskID) == "" || !IsAsyncImageTaskStatus(transition.ToStatus) {
		return nil, ErrAsyncImageInvalidTransition
	}
	for _, from := range transition.FromStatuses {
		if !CanTransitionAsyncImageTask(from, transition.ToStatus) {
			return nil, ErrAsyncImageInvalidTransition
		}
	}
	return s.repo.TransitionAsyncImageTask(ctx, transition)
}

func (s *AsyncImageTaskService) RecordUpstreamSuccess(ctx context.Context, params RecordAsyncImageUpstreamSuccessParams) (*AsyncImageTask, error) {
	if s == nil || s.repo == nil || strings.TrimSpace(params.TaskID) == "" || params.AccountID <= 0 || params.ImageCount < 1 || len(params.StagingObjects) < 1 || len(params.BillingPayload) == 0 {
		return nil, ErrAsyncImageInvalidInput
	}
	if params.UpstreamSucceededAt.IsZero() {
		params.UpstreamSucceededAt = time.Now().UTC()
	}
	return s.repo.RecordAsyncImageUpstreamSuccess(ctx, params)
}

func (s *AsyncImageTaskService) Repository() AsyncImageTaskRepository {
	if s == nil {
		return nil
	}
	return s.repo
}

func (s *AsyncImageTaskService) loadDetails(ctx context.Context, task *AsyncImageTask) (*AsyncImageTaskDetails, error) {
	results, err := s.repo.ListAsyncImageResults(ctx, task.TaskID)
	if err != nil {
		return nil, err
	}
	events, err := s.repo.ListAsyncImageEvents(ctx, task.TaskID)
	if err != nil {
		return nil, err
	}
	return &AsyncImageTaskDetails{Task: task, Results: results, Events: events}, nil
}

func NewAsyncImageTaskID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "asyncimg_" + hex.EncodeToString(random[:]), nil
}

func IsAsyncImageTaskStatus(status string) bool {
	switch status {
	case AsyncImageTaskStatusQueued, AsyncImageTaskStatusInvoking, AsyncImageTaskStatusUpstreamSucceeded,
		AsyncImageTaskStatusUploading, AsyncImageTaskStatusBillingPending, AsyncImageTaskStatusSucceeded,
		AsyncImageTaskStatusFailed, AsyncImageTaskStatusExecutionUnknown, AsyncImageTaskStatusStorageFailed,
		AsyncImageTaskStatusBillingFailed, AsyncImageTaskStatusExpired:
		return true
	default:
		return false
	}
}

func IsTerminalAsyncImageTaskStatus(status string) bool {
	switch status {
	case AsyncImageTaskStatusSucceeded, AsyncImageTaskStatusFailed, AsyncImageTaskStatusExecutionUnknown, AsyncImageTaskStatusExpired:
		return true
	default:
		return false
	}
}

func CanTransitionAsyncImageTask(from, to string) bool {
	if !IsAsyncImageTaskStatus(from) || !IsAsyncImageTaskStatus(to) || from == to || IsTerminalAsyncImageTaskStatus(from) {
		return false
	}
	if to == AsyncImageTaskStatusFailed || to == AsyncImageTaskStatusExpired {
		return true
	}
	allowed := map[string]map[string]struct{}{
		AsyncImageTaskStatusQueued: {
			AsyncImageTaskStatusInvoking: {},
		},
		AsyncImageTaskStatusInvoking: {
			AsyncImageTaskStatusUpstreamSucceeded: {},
			AsyncImageTaskStatusExecutionUnknown:  {},
			AsyncImageTaskStatusQueued:            {},
		},
		AsyncImageTaskStatusUpstreamSucceeded: {
			AsyncImageTaskStatusUploading:      {},
			AsyncImageTaskStatusBillingPending: {},
			AsyncImageTaskStatusStorageFailed:  {},
			AsyncImageTaskStatusBillingFailed:  {},
		},
		AsyncImageTaskStatusUploading: {
			AsyncImageTaskStatusBillingPending: {},
			AsyncImageTaskStatusStorageFailed:  {},
			AsyncImageTaskStatusBillingFailed:  {},
			AsyncImageTaskStatusSucceeded:      {},
		},
		AsyncImageTaskStatusStorageFailed: {
			AsyncImageTaskStatusUploading: {},
		},
		AsyncImageTaskStatusBillingPending: {
			AsyncImageTaskStatusBillingFailed: {},
			AsyncImageTaskStatusSucceeded:     {},
		},
		AsyncImageTaskStatusBillingFailed: {
			AsyncImageTaskStatusBillingPending: {},
			AsyncImageTaskStatusSucceeded:      {},
		},
	}
	_, ok := allowed[from][to]
	return ok
}

func validAsyncImageCreateParams(params CreateAsyncImageTaskParams) bool {
	if params.UserID <= 0 || params.APIKeyID <= 0 || params.GroupID <= 0 || params.TaskID == "" || len(params.TaskID) > 64 || params.Model == "" || len(params.Model) > 255 || params.RequestHash == "" || len(params.RequestHash) > 64 || len(params.RequestPayload) == 0 {
		return false
	}
	if params.IdempotencyKey != nil && len(*params.IdempotencyKey) > 255 {
		return false
	}
	if params.Protocol != AsyncImageProtocolBB && params.Protocol != AsyncImageProtocolSC {
		return false
	}
	if params.Platform != PlatformGemini && params.Platform != PlatformOpenAI {
		return false
	}
	return params.RequestType == AsyncImageRequestTypeTextToImage || params.RequestType == AsyncImageRequestTypeImageToImage
}

func normalizeAsyncImageTaskFilter(filter *AsyncImageTaskFilter) {
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 20
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	filter.TaskID = strings.TrimSpace(filter.TaskID)
	filter.Protocol = strings.ToLower(strings.TrimSpace(filter.Protocol))
	filter.Platform = strings.ToLower(strings.TrimSpace(filter.Platform))
	filter.RequestType = strings.ToLower(strings.TrimSpace(filter.RequestType))
	filter.Status = strings.ToLower(strings.TrimSpace(filter.Status))
	filter.BillingStatus = strings.ToLower(strings.TrimSpace(filter.BillingStatus))
	filter.Model = strings.TrimSpace(filter.Model)
	filter.Search = strings.TrimSpace(filter.Search)
	filter.StorageProvider = strings.ToLower(strings.TrimSpace(filter.StorageProvider))
	filter.SortBy = strings.ToLower(strings.TrimSpace(filter.SortBy))
	filter.SortOrder = strings.ToLower(strings.TrimSpace(filter.SortOrder))
}
