package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	AsyncImageTaskTerminationErrorCode  = "admin_terminated"
	AsyncImageTaskBatchTerminationLimit = 100

	AsyncImageTaskBatchTerminationStatusTerminated = "terminated"
	AsyncImageTaskBatchTerminationStatusSkipped    = "skipped"
	AsyncImageTaskBatchTerminationStatusFailed     = "failed"
)

// ErrAsyncImageTaskTerminationNotAllowed is returned when an administrator
// tries to terminate a task that is already in a final state or otherwise
// cannot be safely closed.
var ErrAsyncImageTaskTerminationNotAllowed = infraerrors.New(
	http.StatusConflict,
	"ASYNC_IMAGE_TASK_TERMINATION_NOT_ALLOWED",
	"only non-successful asynchronous image tasks can be manually ended",
)

var ErrAsyncImageTaskBatchTerminationInvalid = infraerrors.New(
	http.StatusBadRequest,
	"ASYNC_IMAGE_TASK_BATCH_TERMINATION_INVALID",
	"task_ids must contain between 1 and 100 unique task IDs",
)

type AsyncImageTaskBatchTerminationItem struct {
	TaskID    string `json:"task_id"`
	Status    string `json:"status"`
	ErrorCode string `json:"error_code,omitempty"`
	Message   string `json:"message,omitempty"`
}

type AsyncImageTaskBatchTerminationResult struct {
	Requested  int                                  `json:"requested"`
	Terminated int                                  `json:"terminated"`
	Skipped    int                                  `json:"skipped"`
	Failed     int                                  `json:"failed"`
	Items      []AsyncImageTaskBatchTerminationItem `json:"items"`
}

var adminTerminableAsyncImageTaskStatuses = map[string]struct{}{
	AsyncImageTaskStatusQueued:            {},
	AsyncImageTaskStatusInvoking:          {},
	AsyncImageTaskStatusUpstreamSucceeded: {},
	AsyncImageTaskStatusUploading:         {},
	AsyncImageTaskStatusBillingPending:    {},
	AsyncImageTaskStatusExecutionUnknown:  {},
	AsyncImageTaskStatusStorageFailed:     {},
	AsyncImageTaskStatusBillingFailed:     {},
}

func CanTerminateAsyncImageTask(status string) bool {
	_, ok := adminTerminableAsyncImageTaskStatuses[strings.TrimSpace(status)]
	return ok
}

// BatchTerminateAsFailed closes a bounded set of administrator-selected tasks.
// Each task still uses the same status/version CAS as the single-task action;
// a task that reaches a non-terminable state during the request is skipped.
func (s *AsyncImageTaskService) BatchTerminateAsFailed(ctx context.Context, taskIDs []string) (*AsyncImageTaskBatchTerminationResult, error) {
	normalized, err := normalizeAsyncImageTaskTerminationIDs(taskIDs)
	if err != nil {
		return nil, err
	}
	result := &AsyncImageTaskBatchTerminationResult{
		Requested: len(normalized),
		Items:     make([]AsyncImageTaskBatchTerminationItem, 0, len(normalized)),
	}
	for _, taskID := range normalized {
		item := AsyncImageTaskBatchTerminationItem{TaskID: taskID}
		_, terminateErr := s.terminateAsFailed(ctx, taskID)
		switch {
		case terminateErr == nil:
			item.Status = AsyncImageTaskBatchTerminationStatusTerminated
			result.Terminated++
		case errors.Is(terminateErr, ErrAsyncImageTaskTerminationNotAllowed), errors.Is(terminateErr, ErrAsyncImageInvalidTransition):
			item.Status = AsyncImageTaskBatchTerminationStatusSkipped
			item.ErrorCode = infraerrors.Reason(terminateErr)
			item.Message = infraerrors.Message(terminateErr)
			result.Skipped++
		default:
			item.Status = AsyncImageTaskBatchTerminationStatusFailed
			item.ErrorCode = infraerrors.Reason(terminateErr)
			item.Message = infraerrors.Message(terminateErr)
			result.Failed++
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// TerminateAsFailed atomically marks an administrator-selected task as failed.
// The version/status CAS prevents a late worker completion from being
// overwritten and makes concurrent administrator actions idempotent-safe.
func (s *AsyncImageTaskService) TerminateAsFailed(ctx context.Context, taskID string) (*AsyncImageTaskDetails, error) {
	task, err := s.terminateAsFailed(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return s.GetForAdmin(ctx, task.TaskID)
}

func (s *AsyncImageTaskService) terminateAsFailed(ctx context.Context, taskID string) (*AsyncImageTask, error) {
	if s == nil || s.repo == nil {
		return nil, ErrAsyncImageInvalidInput
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, ErrAsyncImageTaskNotFound
	}
	task, err := s.repo.GetAsyncImageTaskByTaskID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, ErrAsyncImageTaskNotFound
	}
	if !CanTerminateAsyncImageTask(task.Status) {
		return nil, ErrAsyncImageTaskTerminationNotAllowed
	}
	code := AsyncImageTaskTerminationErrorCode
	message := "task manually ended as failed by administrator"
	finished := time.Now().UTC()
	updated, err := s.Transition(ctx, AsyncImageTaskTransition{
		TaskID: task.TaskID, ExpectedVersion: task.Version,
		FromStatuses: []string{task.Status}, ToStatus: AsyncImageTaskStatusFailed,
		ErrorCode: &code, ErrorMessage: &message, FinishedAt: &finished,
		ClearRequestPayload: true, EventType: "admin_task_terminated",
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func normalizeAsyncImageTaskTerminationIDs(taskIDs []string) ([]string, error) {
	if len(taskIDs) == 0 || len(taskIDs) > AsyncImageTaskBatchTerminationLimit {
		return nil, ErrAsyncImageTaskBatchTerminationInvalid
	}
	seen := make(map[string]struct{}, len(taskIDs))
	normalized := make([]string, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			return nil, ErrAsyncImageTaskBatchTerminationInvalid
		}
		if _, exists := seen[taskID]; exists {
			continue
		}
		seen[taskID] = struct{}{}
		normalized = append(normalized, taskID)
	}
	if len(normalized) == 0 {
		return nil, ErrAsyncImageTaskBatchTerminationInvalid
	}
	return normalized, nil
}
