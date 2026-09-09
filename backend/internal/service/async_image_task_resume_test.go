package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type asyncImageResumeRepositoryStub struct {
	AsyncImageTaskRepository
	task       *AsyncImageTask
	outbox     AsyncImageOutboxEntry
	transition AsyncImageTaskTransition
	sequence   []string
}

func (s *asyncImageResumeRepositoryStub) GetAsyncImageTaskByTaskID(context.Context, string) (*AsyncImageTask, error) {
	return s.task, nil
}

func (s *asyncImageResumeRepositoryStub) ListAsyncImageResults(context.Context, string) ([]AsyncImageResult, error) {
	return []AsyncImageResult{}, nil
}

func (s *asyncImageResumeRepositoryStub) ListAsyncImageEvents(context.Context, string) ([]AsyncImageEvent, error) {
	return []AsyncImageEvent{}, nil
}

func (s *asyncImageResumeRepositoryStub) EnqueueAsyncImageOutbox(_ context.Context, entry AsyncImageOutboxEntry) error {
	s.sequence = append(s.sequence, "outbox")
	s.outbox = entry
	return nil
}

func (s *asyncImageResumeRepositoryStub) TransitionAsyncImageTask(_ context.Context, transition AsyncImageTaskTransition) (*AsyncImageTask, error) {
	s.sequence = append(s.sequence, "transition")
	s.transition = transition
	s.task.Status = transition.ToStatus
	s.task.Version++
	return s.task, nil
}

func TestAsyncImageTaskResumePostProcessingQueuesBeforeTransition(t *testing.T) {
	repo := &asyncImageResumeRepositoryStub{task: &AsyncImageTask{
		TaskID: "asyncimg_retry", Status: AsyncImageTaskStatusStorageFailed,
		Version: 4, SubmittedAt: time.Now().UTC(),
	}}
	svc := NewAsyncImageTaskService(repo)

	details, err := svc.ResumePostProcessing(context.Background(), "asyncimg_retry")
	require.NoError(t, err)
	require.Equal(t, []string{"outbox", "transition"}, repo.sequence)
	require.Equal(t, AsyncImageOutboxEventPostProcessingResume, repo.outbox.EventType)
	require.Equal(t, "asyncimg_retry:post-processing-resume:4", repo.outbox.DedupKey)
	require.Equal(t, AsyncImageTaskStatusUploading, repo.transition.ToStatus)
	require.True(t, repo.transition.ClearError)
	require.True(t, repo.transition.IncrementRetry)
	require.Equal(t, AsyncImageTaskStatusUploading, details.Task.Status)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(repo.outbox.Payload, &payload))
	require.Equal(t, "post_processing_only", payload["mode"])
	require.Equal(t, AsyncImageTaskStatusStorageFailed, payload["resume_from"])
}

func TestAsyncImageTaskResumePostProcessingRejectsExecutionState(t *testing.T) {
	repo := &asyncImageResumeRepositoryStub{task: &AsyncImageTask{
		TaskID: "asyncimg_unknown", Status: AsyncImageTaskStatusExecutionUnknown,
		Version: 2, SubmittedAt: time.Now().UTC(),
	}}
	svc := NewAsyncImageTaskService(repo)

	_, err := svc.ResumePostProcessing(context.Background(), "asyncimg_unknown")
	require.ErrorIs(t, err, ErrAsyncImagePostProcessingResumeNotAllowed)
	require.Empty(t, repo.sequence)
}

func TestAsyncImageTaskResumeBillingUsesPreparedPlan(t *testing.T) {
	repo := &asyncImageResumeRepositoryStub{task: &AsyncImageTask{
		TaskID: "asyncimg_billing", Status: AsyncImageTaskStatusBillingFailed,
		BillingStatus: AsyncImageBillingStatusFailed, Version: 7,
		SubmittedAt: time.Now().UTC(),
	}}
	svc := NewAsyncImageTaskService(repo)

	_, err := svc.ResumePostProcessing(context.Background(), "asyncimg_billing")
	require.NoError(t, err)
	require.Equal(t, AsyncImageTaskStatusBillingPending, repo.transition.ToStatus)
	require.NotNil(t, repo.transition.BillingStatus)
	require.Equal(t, AsyncImageBillingStatusPrepared, *repo.transition.BillingStatus)
}

func TestAsyncImageTaskTerminateAsFailedAllowsUnknownAndUsesCAS(t *testing.T) {
	repo := &asyncImageResumeRepositoryStub{task: &AsyncImageTask{
		TaskID: "asyncimg_unknown", Status: AsyncImageTaskStatusExecutionUnknown,
		Version: 9, SubmittedAt: time.Now().UTC(),
	}}
	svc := NewAsyncImageTaskService(repo)

	details, err := svc.TerminateAsFailed(context.Background(), "asyncimg_unknown")
	require.NoError(t, err)
	require.Equal(t, AsyncImageTaskStatusFailed, details.Task.Status)
	require.Equal(t, int64(9), repo.transition.ExpectedVersion)
	require.Equal(t, []string{AsyncImageTaskStatusExecutionUnknown}, repo.transition.FromStatuses)
	require.Equal(t, AsyncImageTaskTerminationErrorCode, *repo.transition.ErrorCode)
	require.Equal(t, "admin_task_terminated", repo.transition.EventType)
	require.True(t, repo.transition.ClearRequestPayload)
}

func TestAsyncImageTaskTerminateAsFailedRejectsSuccessfulTask(t *testing.T) {
	repo := &asyncImageResumeRepositoryStub{task: &AsyncImageTask{
		TaskID: "asyncimg_done", Status: AsyncImageTaskStatusSucceeded,
		Version: 2, SubmittedAt: time.Now().UTC(),
	}}
	svc := NewAsyncImageTaskService(repo)

	_, err := svc.TerminateAsFailed(context.Background(), "asyncimg_done")
	require.ErrorIs(t, err, ErrAsyncImageTaskTerminationNotAllowed)
	require.Empty(t, repo.sequence)
}

type asyncImageBatchTerminateRepositoryStub struct {
	AsyncImageTaskRepository
	tasks           map[string]*AsyncImageTask
	transitionError map[string]error
	transitions     []AsyncImageTaskTransition
}

func (s *asyncImageBatchTerminateRepositoryStub) GetAsyncImageTaskByTaskID(_ context.Context, taskID string) (*AsyncImageTask, error) {
	return s.tasks[taskID], nil
}

func (s *asyncImageBatchTerminateRepositoryStub) TransitionAsyncImageTask(_ context.Context, transition AsyncImageTaskTransition) (*AsyncImageTask, error) {
	s.transitions = append(s.transitions, transition)
	if err := s.transitionError[transition.TaskID]; err != nil {
		return nil, err
	}
	task := s.tasks[transition.TaskID]
	task.Status = transition.ToStatus
	task.Version++
	return task, nil
}

func TestAsyncImageTaskBatchTerminateAsFailedReportsPerTaskOutcomes(t *testing.T) {
	now := time.Now().UTC()
	repo := &asyncImageBatchTerminateRepositoryStub{
		tasks: map[string]*AsyncImageTask{
			"asyncimg_queue": {TaskID: "asyncimg_queue", Status: AsyncImageTaskStatusQueued, Version: 1, CreatedAt: now},
			"asyncimg_done":  {TaskID: "asyncimg_done", Status: AsyncImageTaskStatusSucceeded, Version: 2, CreatedAt: now},
			"asyncimg_race":  {TaskID: "asyncimg_race", Status: AsyncImageTaskStatusInvoking, Version: 3, CreatedAt: now},
		},
		transitionError: map[string]error{
			"asyncimg_race": ErrAsyncImageInvalidTransition,
		},
	}
	svc := NewAsyncImageTaskService(repo)

	result, err := svc.BatchTerminateAsFailed(context.Background(), []string{
		" asyncimg_queue ", "asyncimg_done", "asyncimg_race", "asyncimg_missing", "asyncimg_queue",
	})
	require.NoError(t, err)
	require.Equal(t, 4, result.Requested)
	require.Equal(t, 1, result.Terminated)
	require.Equal(t, 2, result.Skipped)
	require.Equal(t, 1, result.Failed)
	require.Equal(t, []AsyncImageTaskBatchTerminationItem{
		{TaskID: "asyncimg_queue", Status: AsyncImageTaskBatchTerminationStatusTerminated},
		{TaskID: "asyncimg_done", Status: AsyncImageTaskBatchTerminationStatusSkipped, ErrorCode: "ASYNC_IMAGE_TASK_TERMINATION_NOT_ALLOWED", Message: "only non-successful asynchronous image tasks can be manually ended"},
		{TaskID: "asyncimg_race", Status: AsyncImageTaskBatchTerminationStatusSkipped, ErrorCode: "ASYNC_IMAGE_INVALID_TRANSITION", Message: "asynchronous image task state changed or transition is invalid"},
		{TaskID: "asyncimg_missing", Status: AsyncImageTaskBatchTerminationStatusFailed, ErrorCode: "ASYNC_IMAGE_TASK_NOT_FOUND", Message: "asynchronous image task not found"},
	}, result.Items)
	require.Equal(t, AsyncImageTaskStatusFailed, repo.tasks["asyncimg_queue"].Status)
	require.Len(t, repo.transitions, 2)
}

func TestAsyncImageTaskBatchTerminateAsFailedRejectsMoreThanPageLimit(t *testing.T) {
	ids := make([]string, AsyncImageTaskBatchTerminationLimit+1)
	for i := range ids {
		ids[i] = "asyncimg_limit"
	}

	svc := NewAsyncImageTaskService(&asyncImageBatchTerminateRepositoryStub{tasks: map[string]*AsyncImageTask{}})
	_, err := svc.BatchTerminateAsFailed(context.Background(), ids)
	require.ErrorIs(t, err, ErrAsyncImageTaskBatchTerminationInvalid)
}
