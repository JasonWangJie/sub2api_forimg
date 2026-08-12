package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const AsyncImageOutboxEventPostProcessingResume = "post_processing_resume"

var ErrAsyncImagePostProcessingResumeNotAllowed = infraerrors.New(
	http.StatusConflict,
	"ASYNC_IMAGE_POST_PROCESSING_RESUME_NOT_ALLOWED",
	"only failed storage or billing post-processing can be resumed",
)

// ListResults exposes result metadata to trusted application handlers without
// exposing the repository itself. Callers must establish task ownership first.
func (s *AsyncImageTaskService) ListResults(ctx context.Context, taskID string) ([]AsyncImageResult, error) {
	if s == nil || s.repo == nil || strings.TrimSpace(taskID) == "" {
		return nil, ErrAsyncImageTaskNotFound
	}
	return s.repo.ListAsyncImageResults(ctx, strings.TrimSpace(taskID))
}

// ResumePostProcessing durably schedules the post-processing half of a task.
// The outbox row is written before the state transition so a transient process
// failure cannot leave a task looking active without a durable delivery record.
// Workers must honor the payload mode and must never invoke the upstream again.
func (s *AsyncImageTaskService) ResumePostProcessing(ctx context.Context, taskID string) (*AsyncImageTaskDetails, error) {
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
	task := details.Task
	var targetStatus string
	var progress int
	switch task.Status {
	case AsyncImageTaskStatusStorageFailed:
		targetStatus = AsyncImageTaskStatusUploading
		progress = 70
	case AsyncImageTaskStatusBillingFailed:
		targetStatus = AsyncImageTaskStatusBillingPending
		progress = 90
	default:
		return nil, ErrAsyncImagePostProcessingResumeNotAllowed
	}

	payload, err := json.Marshal(map[string]any{
		"mode":        "post_processing_only",
		"resume_from": task.Status,
		"version":     task.Version,
	})
	if err != nil {
		return nil, err
	}
	if err := s.repo.EnqueueAsyncImageOutbox(ctx, AsyncImageOutboxEntry{
		TaskID:      task.TaskID,
		EventType:   AsyncImageOutboxEventPostProcessingResume,
		DedupKey:    fmt.Sprintf("%s:post-processing-resume:%d", task.TaskID, task.Version),
		Payload:     payload,
		AvailableAt: time.Now().UTC(),
	}); err != nil {
		return nil, err
	}

	transition := AsyncImageTaskTransition{
		TaskID:          task.TaskID,
		ExpectedVersion: task.Version,
		FromStatuses:    []string{task.Status},
		ToStatus:        targetStatus,
		Progress:        &progress,
		ClearError:      true,
		IncrementRetry:  true,
		EventType:       "post_processing_resume_requested",
		EventPayload:    payload,
	}
	if task.Status == AsyncImageTaskStatusBillingFailed {
		billingStatus := AsyncImageBillingStatusPrepared
		transition.BillingStatus = &billingStatus
	}
	if _, err := s.Transition(ctx, transition); err != nil {
		// The outbox is already durable. A racing worker may have advanced the
		// task before this request updates it, so return the current state when
		// it has entered the expected post-processing phase.
		current, getErr := s.GetForAdmin(ctx, task.TaskID)
		if getErr == nil && current != nil && current.Task != nil && current.Task.Status == targetStatus {
			return current, nil
		}
		return nil, err
	}
	return s.GetForAdmin(ctx, task.TaskID)
}
