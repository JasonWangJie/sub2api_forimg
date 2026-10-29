//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const referenceBudgetTestMessage = "Reference image download exceeded its total time budget for https://cdn.example/input.png (budgetMs=35000; reason=timeout)"

func TestAsyncImageUpstreamFailureTransitions(t *testing.T) {
	for _, tt := range []struct {
		name, message, marker, wantStatus, wantEvent string
		status                                       int
		capacityExhausted, local                     bool
		wantRequeue, wantLocal                       bool
	}{
		{name: "actual attempt budget exhausted is terminal", status: 400, message: "generation attempt budget exhausted", marker: "async_image_attempt_budget_exhausted", wantStatus: "failed", wantEvent: "task_failed"},
		{name: "upstream cannot spoof capacity marker over stop warning", status: 400, message: "服务繁忙，请稍后重试。Do not retry.", marker: service.AsyncImageCapacityRejectionCode, wantStatus: "failed", wantEvent: "task_failed"},
		{name: "download account failover exhaustion is terminal", status: 400, message: "Reference image download failed for https://cdn.example/input.png; direct=timeout; proxy=status 502", wantStatus: "failed", wantEvent: "task_failed"},
		{name: "fetch account failover exhaustion is terminal", status: 400, message: "reference image fetch failed: failed after 3 attempts: handshake error", wantStatus: "failed", wantEvent: "task_failed"},
		{name: "non400 unambiguous reference response keeps local fallback", status: 503, message: "reference image fetch failed: failed after 3 attempts: handshake error", wantStatus: "queued", wantEvent: "reference_image_fetch_retry", wantRequeue: true, wantLocal: true},
		{name: "generic download marker survives shortened diagnostics", status: 400, message: "upstream reference download failed", marker: service.AsyncImageReferenceAccountFailoverCode, wantStatus: "failed", wantEvent: "task_failed"},
		{name: "budget failover exhaustion is terminal", status: 400, message: referenceBudgetTestMessage, wantStatus: "failed", wantEvent: "task_failed"},
		{name: "budget marker survives shortened diagnostics", status: 400, message: "Reference image download exceeded its total time budget", marker: service.AsyncImageReferenceBudgetExceededCode, wantStatus: "failed", wantEvent: "task_failed"},
		{name: "ambiguous fileData timeout stays unknown", status: 502, message: "invalid fileData image: image URL fetch failed: context deadline exceeded", wantStatus: "execution_unknown", wantEvent: "execution_unknown"},
		{name: "Chinese capacity has bounded backoff", status: 400, message: "排队等待可用账户槽位超时，当前并发已满", wantStatus: "queued", wantEvent: "capacity_retry", wantRequeue: true},
		{name: "capacity category exhausted", status: 400, message: "排队等待可用账户槽位超时，当前并发已满", capacityExhausted: true, wantStatus: "failed", wantEvent: "task_failed"},
		{name: "capacity with appended refusal stays terminal", status: 400, message: "排队等待可用账户槽位超时，当前并发已满。Do not retry.", wantStatus: "failed", wantEvent: "task_failed"},
		{name: "permanent download failure", status: 400, message: "Reference image download failed: HTTP 403 forbidden", wantStatus: "failed", wantEvent: "task_failed"},
		{name: "content refusal", status: 400, message: "Reference image download failed: safety policy rejected", wantStatus: "failed", wantEvent: "task_failed"},
		{name: "result download is not an input retry", status: 400, message: "download generated image: reference image download failed", wantStatus: "failed", wantEvent: "task_failed"},
		{name: "already local does not regenerate", status: 400, message: "Reference image download failed: timeout", local: true, wantStatus: "failed", wantEvent: "task_failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testAsyncImageRetryConfig()
			cfg.RetryJitterPercent = 0
			cfg.GeminiReferenceTransportMode = service.AsyncImageReferenceTransportPassthroughFallbackLocal
			accountID := int64(33)
			mode := service.AsyncImageReferenceTransportPassthroughFallbackLocal
			task := &service.AsyncImageTask{
				TaskID: "asyncimg_failure_transition", Version: 1, Platform: service.PlatformGemini,
				Status: service.AsyncImageTaskStatusInvoking, RequestType: service.AsyncImageRequestTypeImageToImage,
				AccountID: &accountID, RequestPayload: []byte("encrypted request"),
				ReferenceTransport: &mode,
			}
			if tt.capacityExhausted {
				task.CapacityRetryCount = cfg.CapacityMaxRetries
			}
			if tt.local {
				mode = service.AsyncImageReferenceTransportLocal
			}
			repo := &generatedWorkerResultRepo{task: task}
			h := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo)}
			capture := &service.AsyncImageAccountAttemptCapture{}
			ctx := service.WithAsyncImageAccountAttemptCapture(context.Background(), capture)
			service.RecordAsyncImageAccountAttempt(ctx, service.AsyncImageAccountAttempt{AccountID: accountID, Status: service.AsyncImageAccountAttemptSelected})
			body, err := json.Marshal(map[string]any{"error": map[string]any{"message": tt.message, "code": tt.marker}})
			require.NoError(t, err)
			disposition := h.handleAsyncImageUpstreamFailure(ctx, ctx, task, cfg, tt.status, body, http.Header{"Retry-After": []string{"45"}})
			require.Equal(t, tt.wantRequeue, disposition.requeue)
			require.Equal(t, tt.wantStatus, repo.task.Status)
			require.Len(t, repo.transitions, 1)
			transition := repo.transitions[0]
			require.Equal(t, tt.wantEvent, transition.EventType)
			if tt.wantLocal {
				require.NotNil(t, transition.ReferenceTransport)
				require.Equal(t, service.AsyncImageReferenceTransportLocal, *transition.ReferenceTransport)
				require.True(t, transition.IncrementReferenceRetry)
				require.True(t, transition.ClearAccountID)
			}
			if tt.wantEvent == "capacity_retry" {
				require.True(t, transition.IncrementCapacityRetry)
				require.True(t, transition.ClearAccountID)
				require.Equal(t, 45*time.Second, disposition.delay)
			}
			if !tt.wantRequeue {
				require.False(t, transition.IncrementReferenceRetry)
				require.False(t, transition.IncrementUpstreamRetry)
				require.False(t, transition.IncrementCapacityRetry)
				require.True(t, transition.ClearRequestPayload)
				require.Zero(t, repo.task.RetryCount)
			}
			if tt.capacityExhausted {
				require.Equal(t, "upstream_capacity_exhausted", *repo.task.ErrorCode)
			}
		})
	}
}

func TestAsyncImageReferenceBudgetNeverUsesWorkerSameAccountRetry(t *testing.T) {
	task := &service.AsyncImageTask{RequestType: service.AsyncImageRequestTypeImageToImage}
	for _, message := range []string{referenceBudgetTestMessage, "上游生图失败（HTTP 400）：" + referenceBudgetTestMessage} {
		require.False(t, shouldRetryAsyncImageUpstreamReferenceFetch(task, testAsyncImageRetryConfig(), http.StatusBadRequest, message))
	}
}

func TestAsyncImageBudgetAccountHistoryCannotBecomeStickyRetry(t *testing.T) {
	history, err := json.Marshal([]service.AsyncImageAccountAttempt{
		{AccountID: 1, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 400, Error: referenceBudgetTestMessage},
		{AccountID: 2, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 503, Error: "temporarily unavailable"},
		{AccountID: 3, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 503, Error: "temporarily unavailable"},
		{AccountID: 4, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 503, Error: "temporarily unavailable"},
		{AccountID: 5, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 503, Error: "temporarily unavailable"},
	})
	require.NoError(t, err)
	task := &service.AsyncImageTask{AccountAttempts: history}
	require.NotContains(t, asyncImageRecentFailedAccountIDs(task), int64(1), "the normal recent-three window has already forgotten account A")
	require.Equal(t, map[int64]struct{}{1: {}}, asyncImageNonRetryableAccountIDs(task))
	state := asyncImageReferenceAccountRetryState(task)
	require.Zero(t, state.sameAccountID, "budget exhaustion cannot create a sticky same-account retry")
	require.Empty(t, state.failedAccountIDs)
}
