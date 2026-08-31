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
