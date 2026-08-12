package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type asyncImageTaskRepositoryStub struct {
	AsyncImageTaskRepository
	createParams     CreateAsyncImageTaskParams
	createTask       *AsyncImageTask
	createReused     bool
	createErr        error
	transitionParams AsyncImageTaskTransition
	transitionTask   *AsyncImageTask
	transitionErr    error
}

func (s *asyncImageTaskRepositoryStub) CreateAsyncImageTask(_ context.Context, params CreateAsyncImageTaskParams) (*AsyncImageTask, bool, error) {
	s.createParams = params
	return s.createTask, s.createReused, s.createErr
}

func (s *asyncImageTaskRepositoryStub) TransitionAsyncImageTask(_ context.Context, params AsyncImageTaskTransition) (*AsyncImageTask, error) {
	s.transitionParams = params
	return s.transitionTask, s.transitionErr
}

func TestAsyncImageTaskServiceCreateNormalizesAndDelegates(t *testing.T) {
	idempotencyKey := "  retry-1  "
	repo := &asyncImageTaskRepositoryStub{
		createTask:   &AsyncImageTask{TaskID: "asyncimg_existing"},
		createReused: true,
	}
	svc := NewAsyncImageTaskService(repo)

	task, reused, err := svc.Create(context.Background(), CreateAsyncImageTaskParams{
		UserID: 1, APIKeyID: 2, GroupID: 3,
		Protocol: " BB ", Platform: " GEMINI ", RequestType: " TEXT_TO_IMAGE ",
		Model: " gemini-image ", RequestHash: "abc123", RequestPayload: []byte("ciphertext"),
		IdempotencyKey: &idempotencyKey,
	})
	require.NoError(t, err)
	require.True(t, reused)
	require.Equal(t, "asyncimg_existing", task.TaskID)
	require.Equal(t, AsyncImageProtocolBB, repo.createParams.Protocol)
	require.Equal(t, PlatformGemini, repo.createParams.Platform)
	require.Equal(t, AsyncImageRequestTypeTextToImage, repo.createParams.RequestType)
	require.Equal(t, "gemini-image", repo.createParams.Model)
	require.Equal(t, "retry-1", *repo.createParams.IdempotencyKey)
	require.Contains(t, repo.createParams.TaskID, "asyncimg_")
}

func TestAsyncImageTaskServiceCreateRejectsUnsupportedPlatform(t *testing.T) {
	svc := NewAsyncImageTaskService(&asyncImageTaskRepositoryStub{})
	_, _, err := svc.Create(context.Background(), CreateAsyncImageTaskParams{
		UserID: 1, APIKeyID: 2, GroupID: 3,
		Protocol: AsyncImageProtocolBB, Platform: PlatformAnthropic,
		RequestType: AsyncImageRequestTypeTextToImage, Model: "model",
		RequestHash: "hash", RequestPayload: []byte("ciphertext"),
	})
	require.ErrorIs(t, err, ErrAsyncImageInvalidInput)
}

func TestAsyncImageTaskServiceTransitionValidatesStateGraph(t *testing.T) {
	repo := &asyncImageTaskRepositoryStub{transitionTask: &AsyncImageTask{TaskID: "asyncimg_1", Status: AsyncImageTaskStatusInvoking}}
	svc := NewAsyncImageTaskService(repo)

	_, err := svc.Transition(context.Background(), AsyncImageTaskTransition{
		TaskID: "asyncimg_1", FromStatuses: []string{AsyncImageTaskStatusQueued},
		ToStatus: AsyncImageTaskStatusSucceeded,
	})
	require.ErrorIs(t, err, ErrAsyncImageInvalidTransition)

	startedAt := time.Now().UTC()
	task, err := svc.Transition(context.Background(), AsyncImageTaskTransition{
		TaskID: "asyncimg_1", FromStatuses: []string{AsyncImageTaskStatusQueued},
		ToStatus: AsyncImageTaskStatusInvoking, StartedAt: &startedAt,
		EventPayload: json.RawMessage(`{"worker":"worker-1"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "asyncimg_1", task.TaskID)
	require.Equal(t, AsyncImageTaskStatusInvoking, repo.transitionParams.ToStatus)
	require.Equal(t, startedAt, *repo.transitionParams.StartedAt)
}

func TestCanTransitionAsyncImageTaskOnlyRetriesPostProcessing(t *testing.T) {
	require.True(t, CanTransitionAsyncImageTask(AsyncImageTaskStatusInvoking, AsyncImageTaskStatusQueued))
	require.True(t, CanTransitionAsyncImageTask(AsyncImageTaskStatusStorageFailed, AsyncImageTaskStatusUploading))
	require.True(t, CanTransitionAsyncImageTask(AsyncImageTaskStatusBillingFailed, AsyncImageTaskStatusBillingPending))
	require.False(t, CanTransitionAsyncImageTask(AsyncImageTaskStatusExecutionUnknown, AsyncImageTaskStatusInvoking))
	require.False(t, CanTransitionAsyncImageTask(AsyncImageTaskStatusSucceeded, AsyncImageTaskStatusUploading))
	require.False(t, CanTransitionAsyncImageTask(AsyncImageTaskStatusInvoking, AsyncImageTaskStatusInvoking))
}
