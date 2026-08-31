package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const AsyncImageTaskTerminationErrorCode = "admin_terminated"

// ErrAsyncImageTaskTerminationNotAllowed is returned when an administrator
// tries to terminate a task that is already in a final state or otherwise
// cannot be safely closed.
var ErrAsyncImageTaskTerminationNotAllowed = infraerrors.New(
	http.StatusConflict,
	"ASYNC_IMAGE_TASK_TERMINATION_NOT_ALLOWED",
	"only non-successful asynchronous image tasks can be manually ended",
)

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

// TerminateAsFailed atomically marks an administrator-selected task as failed.
// The version/status CAS prevents a late worker completion from being
// overwritten and makes concurrent administrator actions idempotent-safe.
func (s *AsyncImageTaskService) TerminateAsFailed(ctx context.Context, taskID string) (*AsyncImageTaskDetails, error) {
	if s == nil || s.repo == nil {
		return nil, ErrAsyncImageInvalidInput
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, ErrAsyncImageTaskNotFound
	}
	details, err := s.GetForAdmin(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if details == nil || details.Task == nil {
		return nil, ErrAsyncImageTaskNotFound
	}
	task := details.Task
	if !CanTerminateAsyncImageTask(task.Status) {
		return nil, ErrAsyncImageTaskTerminationNotAllowed
	}
	code := AsyncImageTaskTerminationErrorCode
	message := "task manually ended as failed by administrator"
	finished := time.Now().UTC()
	if _, err := s.Transition(ctx, AsyncImageTaskTransition{
		TaskID: task.TaskID, ExpectedVersion: task.Version,
		FromStatuses: []string{task.Status}, ToStatus: AsyncImageTaskStatusFailed,
		ErrorCode: &code, ErrorMessage: &message, FinishedAt: &finished,
		ClearRequestPayload: true, EventType: "admin_task_terminated",
	}); err != nil {
		return nil, err
	}
	return s.GetForAdmin(ctx, task.TaskID)
}
