package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	AsyncImageGenerationTimeBudget = 30 * time.Minute
	AsyncImageRetryStatePending    = "pending"
	AsyncImageRetryStateStarted    = "started"
	AsyncImageRetryStateManual     = "manual"
	AsyncImageRetryStateClear      = "clear"
)

var (
	ErrAsyncImageRetryRevisionConflict = infraerrors.New(http.StatusConflict, "ASYNC_IMAGE_RETRY_REVISION_CONFLICT", "the retry policy revision has changed")
	ErrAsyncImageRetryEventNotFound    = infraerrors.New(http.StatusNotFound, "ASYNC_IMAGE_RETRY_EVENT_NOT_FOUND", "retry event not found")
	ErrAsyncImageRetryVersionNotFound  = infraerrors.New(http.StatusNotFound, "ASYNC_IMAGE_RETRY_VERSION_NOT_FOUND", "retry policy version not found")
	ErrAsyncImageRetryActionChanged    = infraerrors.New(http.StatusConflict, "ASYNC_IMAGE_RETRY_ACTION_CHANGED", "the task or retry action has changed")
	ErrAsyncImageRetryGenerationPaused = infraerrors.New(http.StatusConflict, "ASYNC_IMAGE_RETRY_GENERATION_PAUSED", "automatic generation retries are paused")
)

type AsyncImageRetryVersion struct {
	Revision     int64                 `json:"revision"`
	Policy       AsyncImageRetryPolicy `json:"policy"`
	ActorID      int64                 `json:"actor_id"`
	Operation    string                `json:"operation"`
	RollbackFrom *int64                `json:"rollback_from,omitempty"`
	CreatedAt    time.Time             `json:"created_at"`
}

type AsyncImageRetryEvent struct {
	ID                  int64                   `json:"id"`
	Fact                AsyncImageRetryFact     `json:"fact"`
	Decision            AsyncImageRetryDecision `json:"decision"`
	Fingerprint         string                  `json:"fingerprint"`
	StartedAt           *time.Time              `json:"started_at,omitempty"`
	GenerationStartedAt *time.Time              `json:"generation_started_at,omitempty"`
	TargetAccountID     *int64                  `json:"target_account_id,omitempty"`
	FinalStatus         string                  `json:"final_status,omitempty"`
	CreatedAt           time.Time               `json:"created_at"`
}

type AsyncImageRetrySample struct {
	Fingerprint    string               `json:"fingerprint"`
	Latest         AsyncImageRetryEvent `json:"latest"`
	Occurrences    int64                `json:"occurrences"`
	Tasks          int64                `json:"tasks"`
	Executions     int64                `json:"executions"`
	SucceededTasks int64                `json:"succeeded_tasks"`
	LastSeenAt     time.Time            `json:"last_seen_at"`
}

type AsyncImageRetryFilter struct {
	From, To           *time.Time
	Platform, Model    string
	Stage, Category    string
	ErrorCode, Source  string
	GroupID, AccountID int64
	HTTPStatus         int
	Limit, Offset      int
}

type AsyncImageRetryPending struct {
	Event  AsyncImageRetryEvent
	Status string
	DueAt  time.Time
}

type AsyncImageRetryState struct {
	Pending *AsyncImageRetryPending
	Usage   AsyncImageRetryUsage
}

type AsyncImageRetrySaveParams struct {
	ExpectedTaskVersion    int64
	ExpectedPendingEventID int64
	Event                  AsyncImageRetryEvent
	State                  string
	DueAt                  time.Time
	Transition             *AsyncImageTaskTransition
}

type AsyncImageRetryStartParams struct {
	TaskID              string
	ExpectedTaskVersion int64
	EventID             int64
	Revision            int64
	AccountID           int64
	Generation          bool
	FirstGeneration     bool
}

type asyncImageRetryPostProcessingRepository interface {
	ResumeAsyncImageRetryPostProcessing(context.Context, AsyncImageTaskTransition, AsyncImageOutboxEntry) (*AsyncImageTask, error)
}

// AsyncImageRetryRepository is implemented by the durable task repository.
// Preview can use its reads without acquiring a slot or changing task state.
type AsyncImageRetryRepository interface {
	GetAsyncImageRetryPolicy(context.Context) (AsyncImageRetryPolicy, error)
	SaveAsyncImageRetryPolicy(context.Context, int64, AsyncImageRetryPolicy, int64, *int64) (AsyncImageRetryVersion, error)
	GetAsyncImageRetryVersion(context.Context, int64) (AsyncImageRetryVersion, error)
	ListAsyncImageRetryVersions(context.Context, int, int) ([]AsyncImageRetryVersion, int64, error)
	GetAsyncImageRetryState(context.Context, string) (AsyncImageRetryState, error)
	SaveAsyncImageRetryDecision(context.Context, AsyncImageRetrySaveParams) (AsyncImageRetryEvent, error)
	StartAsyncImageRetryAction(context.Context, AsyncImageRetryStartParams) error
	ListAsyncImageRetrySamples(context.Context, AsyncImageRetryFilter) ([]AsyncImageRetrySample, int64, error)
	ListAsyncImageRetryEvents(context.Context, AsyncImageRetryFilter, string) ([]AsyncImageRetryEvent, int64, error)
	GetAsyncImageRetryEvent(context.Context, int64) (AsyncImageRetryEvent, error)
	CleanupAsyncImageRetryEvents(context.Context, time.Time, int) (int64, error)
}

var asyncImageRetryVariableID = regexp.MustCompile(`(?i)\b(?:[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}|[a-f0-9]{20,}|[0-9]{6,})\b`)

func AsyncImageRetryFingerprint(fact AsyncImageRetryFact) string {
	message := asyncImageRetryVariableID.ReplaceAllString(strings.ToLower(fact.Message), "[id]")
	key, _ := json.Marshal([]any{fact.Platform, fact.Stage, fact.ErrorCode, fact.HTTPStatus, message})
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:])
}
