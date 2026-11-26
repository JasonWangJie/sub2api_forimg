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
	"post-processing can resume from a recoverable held download, storage failure, or billing failure",
)

// CanResumeAsyncImageHeldDownload describes the current admin action. The
// repository rechecks the hold and artifact expiry when it schedules recovery.
func CanResumeAsyncImageHeldDownload(ctx context.Context, repo AsyncImageTaskRepository, taskID string) (bool, error) {
	lookup, ok := repo.(interface {
		CanResumeAsyncImageHeldDownload(context.Context, string) (bool, error)
	})
	if !ok {
		return false, nil
	}
	return lookup.CanResumeAsyncImageHeldDownload(ctx, taskID)
}

// ListResults exposes result metadata to trusted application handlers without
// exposing the repository itself. Callers must establish task ownership first.
func (s *AsyncImageTaskService) ListResults(ctx context.Context, taskID string) ([]AsyncImageResult, error) {
	if s == nil || s.repo == nil || strings.TrimSpace(taskID) == "" {
		return nil, ErrAsyncImageTaskNotFound
	}
	return s.repo.ListAsyncImageResults(ctx, strings.TrimSpace(taskID))
}

// ResumePostProcessing durably schedules the post-processing half of a task.
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
	case AsyncImageTaskStatusUpstreamSucceeded:
		if _, ok := s.repo.(asyncImageRetryPostProcessingRepository); !ok {
			return nil, ErrAsyncImagePostProcessingResumeNotAllowed
		}
		canResume, err := CanResumeAsyncImageHeldDownload(ctx, s.repo, task.TaskID)
		if err != nil {
			return nil, err
		}
		if !canResume {
			return nil, ErrAsyncImagePostProcessingResumeNotAllowed
		}
		// Keep the download phase until the persisted result has been retrieved.
		targetStatus = task.Status
		progress = task.Progress
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
	outbox := AsyncImageOutboxEntry{
		TaskID:      task.TaskID,
		EventType:   AsyncImageOutboxEventPostProcessingResume,
		DedupKey:    fmt.Sprintf("%s:post-processing-resume:%d", task.TaskID, task.Version),
		Payload:     payload,
		AvailableAt: time.Now().UTC(),
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
	if repo, ok := s.repo.(asyncImageRetryPostProcessingRepository); ok {
		_, err = repo.ResumeAsyncImageRetryPostProcessing(ctx, transition, outbox)
	} else {
		if err := s.repo.EnqueueAsyncImageOutbox(ctx, outbox); err != nil {
			return nil, err
		}
		_, err = s.Transition(ctx, transition)
	}
	if err != nil {
		// A racing worker or administrator may have already resumed the task.
		current, getErr := s.GetForAdmin(ctx, task.TaskID)
		if getErr == nil && current != nil && current.Task != nil && current.Task.Status == targetStatus && current.Task.Version > task.Version {
			return current, nil
		}
		return nil, err
	}
	return s.GetForAdmin(ctx, task.TaskID)
}
