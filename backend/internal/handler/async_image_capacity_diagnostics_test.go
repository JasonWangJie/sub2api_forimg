//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type capacityDiagnosticRepo struct {
	asyncImageLocalCapacityRepoStub
	events []service.AsyncImageEvent
	err    error
}

func (r *capacityDiagnosticRepo) AppendAsyncImageEvent(_ context.Context, event service.AsyncImageEvent) error {
	r.events = append(r.events, event)
	return r.err
}

func TestImageCapacityDiagnosticPersistedOnRetryAndExhaustion(t *testing.T) {
	for _, exhausted := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			repo := &capacityDiagnosticRepo{}
			if fail {
				repo.err = errors.New("diagnostic write failed")
			}
			worker := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo)}
			task := &service.AsyncImageTask{TaskID: "asyncimg_capacity_snapshot", Status: service.AsyncImageTaskStatusInvoking, Version: 1}
			if exhausted {
				task.CapacityRetryCount = 3
				task.RetryCount = 3
			}
			ctx := service.WithImageCapacityDiagnostics(context.Background(), 2, "gemini-image", "2K")
			service.RecordImageCapacityDecision(ctx, "concurrency", "concurrency_full", 25, 0)
			result := worker.deferAsyncImageForLocalCapacity(context.Background(), task, service.AsyncImageRuntimeConfig{CapacityMaxRetries: 3, TotalMaxRetries: 16, CapacityRetryBaseSeconds: 30, CapacityRetryMaxSeconds: 300}, ctx)
			require.Equal(t, !exhausted, result.requeue)
			require.Len(t, repo.events, 1)
			require.Equal(t, "capacity_diagnostic", repo.events[0].EventType)
			var payload service.ImageCapacityDiagnostics
			require.NoError(t, json.Unmarshal(repo.events[0].Payload, &payload))
			require.Equal(t, int64(25), payload.Observations[0].Reasons[0].AccountIDs[0])
			if exhausted {
				require.Equal(t, "local_capacity_exhausted", *repo.transition.ErrorCode)
			} else {
				require.True(t, repo.transition.IncrementCapacityRetry)
			}
		}
	}
}

func TestImageCapacityDiagnosticAdminOnlyAndLegacy(t *testing.T) {
	h := &AsyncImageTaskCenterHandler{}
	details := &service.AsyncImageTaskDetails{Task: &service.AsyncImageTask{TaskID: "asyncimg_diag", Status: service.AsyncImageTaskStatusFailed}, Events: []service.AsyncImageEvent{
		{EventType: "capacity_diagnostic", Payload: json.RawMessage(`{"group_id":2,"model":"gemini-image","observations":[{"source":"pool_snapshot","candidate_count":2,"reasons":[{"code":"scheduling_disabled","count":2,"account_ids":[12,39]}]}]}`)},
		{EventType: "task_failed", Payload: json.RawMessage(`{"message":"no capacity"}`)},
	}}
	admin, err := h.detailView(context.Background(), details, true)
	require.NoError(t, err)
	require.Len(t, admin.Events, 2)
	require.NotNil(t, admin.Events[0].CapacityDiagnostics)
	user, err := h.detailView(context.Background(), details, false)
	require.NoError(t, err)
	require.Len(t, user.Events, 1)
	encoded, err := json.Marshal(user)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "capacity_diagnostics")
	require.NotContains(t, string(encoded), "account_ids")
	details.Events = details.Events[1:]
	legacy, err := h.detailView(context.Background(), details, true)
	require.NoError(t, err)
	require.Nil(t, legacy.Events[0].CapacityDiagnostics)
}
