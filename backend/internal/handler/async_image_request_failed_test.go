//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const requestFailedGatewayBody = `{"error":{"type":"upstream_error","code":"upstream_error","message":"request failed"}}`

func newRequestFailedGatewayHarness(t *testing.T, platform string, accountCount, maxSwitch int, failedIDs ...int64) *referenceDownloadGatewayHarness {
	t.Helper()
	failures := make(map[int64]referenceDownloadGatewayFailure, len(failedIDs))
	rawBodies := make(map[int64]string, len(failedIDs))
	for _, id := range failedIDs {
		failures[id] = referenceDownloadGatewayFailure{http.StatusBadRequest, "request failed"}
		rawBodies[id] = requestFailedGatewayBody
	}
	h := newReferenceDownloadGatewayHarness(t, platform, accountCount, maxSwitch, failures)
	h.upstream.rawErrorBodies = rawBodies
	return h
}

func requestFailedAttemptBudget(remaining int) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context {
		return service.WithAsyncImageAttemptBudget(ctx, remaining)
	}
}

func TestAsyncImageRequestFailedSwitchesAccountsAndPreservesRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, scenario := range []struct {
			name       string
			accounts   int
			maxSwitch  int
			failedIDs  []int64
			wantCalls  []int64
			wantStatus int
			wantCode   string
		}{
			{"A to B succeeds", 2, 3, []int64{1}, []int64{1, 2}, http.StatusOK, ""},
			{"A and B exhausted", 2, 3, []int64{1, 2}, []int64{1, 2}, http.StatusBadRequest, service.AsyncImageRetryableRejectionCode},
			{"switching disabled", 2, 0, []int64{1}, []int64{1}, http.StatusBadRequest, service.AsyncImageRetryableRejectionCode},
			{"one switch stops before C", 3, 1, []int64{1, 2}, []int64{1, 2}, http.StatusBadRequest, service.AsyncImageRetryableRejectionCode},
		} {
			t.Run(platform+"/"+scenario.name, func(t *testing.T) {
				h := newRequestFailedGatewayHarness(t, platform, scenario.accounts, scenario.maxSwitch, scenario.failedIDs...)
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
				c, response, capture := invokeProviderRejectionGateway(t, h, task, referenceLocalFallbackConfig(), requestFailedAttemptBudget(16))
				require.Equal(t, scenario.wantStatus, response.Code, response.Body.String())
				require.Equal(t, scenario.wantCalls, h.upstream.calls, "pool-mode retry=3 for HTTP400 must not immediately repeat A")
				require.Equal(t, len(scenario.wantCalls), service.AsyncImageUpstreamAttemptCount(c.Request.Context()))
				assertReferenceDownloadGatewayRequest(t, h)
				require.Equal(t, service.AsyncImageReferenceTransportPassthroughFallbackLocal, *task.ReferenceTransport)
				require.Zero(t, task.RetryCount, "request-local switching must not be counted as worker requeue")
				require.Zero(t, task.ReferenceRetryCount, "generic upstream failure must not trigger local reference fallback")
				var invoked []int64
				for _, attempt := range capture.Attempts() {
					if attempt.Status == service.AsyncImageAccountAttemptInvoked {
						invoked = append(invoked, attempt.AccountID)
					}
					if attempt.Status == service.AsyncImageAccountAttemptFailed {
						require.Equal(t, http.StatusBadRequest, attempt.StatusCode)
						require.Equal(t, "upstream_error", attempt.ProviderErrorCode)
						require.Equal(t, string(service.AsyncImageRetryableRejectionReason), attempt.FailureReason)
						require.Equal(t, "request failed", attempt.Error)
						require.NotEmpty(t, attempt.UpstreamRequestID)
					}
				}
				require.Equal(t, scenario.wantCalls, invoked)
				if scenario.wantCode != "" {
					require.Equal(t, scenario.wantCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
					require.Equal(t, "request failed", gjson.GetBytes(response.Body.Bytes(), "error.message").String())
					require.Equal(t, "17", response.Header().Get("Retry-After"))
				}
			})
		}
	}
}

func TestAsyncGeminiUpstreamRequestFailedSwitchesBeforeWorkerRetry(t *testing.T) {
	const message = "Upstream request failed. Please retry later."
	const body = `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"Upstream request failed. Please retry later."}}`
	for _, accounts := range []int{2, 1} {
		t.Run(fmt.Sprintf("%d accounts", accounts), func(t *testing.T) {
			h := newReferenceDownloadGatewayHarness(t, service.PlatformGemini, accounts, 3, map[int64]referenceDownloadGatewayFailure{1: {400, message}})
			h.upstream.rawErrorBodies = map[int64]string{1: body}
			task := referenceLocalFallbackTask(h.platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
			cfg := referenceLocalFallbackConfig()
			cfg.UpstreamTransientMaxRetries = 1
			repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
			h.worker.tasks = service.NewAsyncImageTaskService(repo)
			c, response, capture := invokeProviderRejectionGateway(t, h, task, cfg, requestFailedAttemptBudget(16))
			var failures []service.AsyncImageAccountAttempt
			for _, attempt := range capture.Attempts() {
				if attempt.Status == service.AsyncImageAccountAttemptFailed {
					failures = append(failures, attempt)
				}
			}
			require.Len(t, failures, 1)
			require.Equal(t, "INVALID_ARGUMENT", failures[0].ProviderErrorCode)
			require.Equal(t, message, failures[0].Error)
			require.Equal(t, string(service.AsyncImageRetryableRejectionReason), failures[0].FailureReason)
			assertReferenceDownloadGatewayRequest(t, h)
			if accounts == 2 {
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Equal(t, []int64{1, 2}, h.upstream.calls)
				require.Zero(t, task.RetryCount)
				return
			}
			require.Equal(t, []int64{1}, h.upstream.calls)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Equal(t, service.AsyncImageRetryableRejectionCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
			disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, cfg, response.Code, response.Body.Bytes(), response.Header())
			require.True(t, disposition.requeue)
			require.Greater(t, disposition.delay, time.Duration(0))
			require.Equal(t, 1, repo.task.UpstreamRetryCount)
			require.Zero(t, repo.task.ReferenceRetryCount)
			require.Equal(t, service.AsyncImageReferenceTransportPassthroughFallbackLocal, *repo.task.ReferenceTransport)
		})
	}
}

func TestAsyncImageRequestFailedWorkerRetriesArePersistedAndBounded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, accountCount := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/%d accounts", platform, accountCount), func(t *testing.T) {
				failedIDs := []int64{1}
				if accountCount == 2 {
					failedIDs = append(failedIDs, 2)
				}
				h := newRequestFailedGatewayHarness(t, platform, accountCount, 3, failedIDs...)
				cfg := referenceLocalFallbackConfig()
				cfg.UpstreamTransientMaxRetries, cfg.TotalMaxRetries = 2, 16
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
				task.SubmittedAt = time.Now().UTC()
				originalDeadline := asyncImageTaskDeadline(task)
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				for attempt := 0; attempt <= cfg.UpstreamTransientMaxRetries; attempt++ {
					before := len(h.upstream.calls)
					c, response, _ := invokeProviderRejectionGateway(t, h, repo.task, cfg, requestFailedAttemptBudget(asyncImageRemainingUpstreamAttempts(repo.task)))
					require.Equal(t, failedIDs, h.upstream.calls[before:], "one invocation must try each channel once before deferred retry")
					require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
					require.Equal(t, service.AsyncImageRetryableRejectionCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
					disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), repo.task, cfg, response.Code, response.Body.Bytes(), response.Header())
					require.Equal(t, attempt < cfg.UpstreamTransientMaxRetries, disposition.requeue)
					require.Equal(t, originalDeadline, asyncImageTaskDeadline(repo.task), "requeue must not extend the original generation deadline")
					require.Empty(t, asyncImageFixedCapabilityAccountIDs(repo.task), "transient request failure must not permanently ban a channel")
					require.Empty(t, asyncImageNonRetryableAccountIDs(repo.task))
					require.Zero(t, repo.task.ReferenceRetryCount)
					require.Zero(t, repo.task.CapacityRetryCount)
					require.Equal(t, service.AsyncImageReferenceTransportPassthroughFallbackLocal, *repo.task.ReferenceTransport)
					if disposition.requeue {
						require.Greater(t, disposition.delay, time.Duration(0), "same-channel recovery must wait for persisted worker backoff")
						require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
						require.Equal(t, attempt+1, repo.task.UpstreamRetryCount)
						require.Equal(t, attempt+1, repo.task.RetryCount)
						require.Equal(t, "upstream_transient_retry", repo.transitions[len(repo.transitions)-1].EventType)
						require.Equal(t, []byte("encrypted original request"), repo.task.RequestPayload)
						repo.task.Status = service.AsyncImageTaskStatusInvoking
					}
				}
				require.Equal(t, 3*accountCount, len(h.upstream.calls))
				require.Equal(t, 16-3*accountCount, asyncImageRemainingUpstreamAttempts(repo.task), "each new generation call must consume the durable actual-call budget")
				require.Equal(t, 2, repo.task.UpstreamRetryCount)
				require.Equal(t, 2, repo.task.RetryCount)
				require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
				require.NotNil(t, repo.task.ErrorCode)
				require.Equal(t, "upstream_failed", *repo.task.ErrorCode)
				assertReferenceDownloadGatewayRequest(t, h)
			})
		}
	}
}

func TestAsyncImageRequestFailedWorkerHonorsZeroAndTotalRetryBudgets(t *testing.T) {
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, exhausted := range []string{"category", "total"} {
			t.Run(platform+"/"+exhausted, func(t *testing.T) {
				h := newRequestFailedGatewayHarness(t, platform, 1, 3, 1)
				cfg := referenceLocalFallbackConfig()
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
				if exhausted == "category" {
					cfg.UpstreamTransientMaxRetries = 0
				} else {
					task.RetryCount = cfg.TotalMaxRetries
				}
				originalRetries := task.RetryCount
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				c, response, _ := invokeProviderRejectionGateway(t, h, task, cfg, requestFailedAttemptBudget(16))
				require.Equal(t, service.AsyncImageRetryableRejectionCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, cfg, response.Code, response.Body.Bytes(), response.Header())
				require.False(t, disposition.requeue)
				require.Equal(t, []int64{1}, h.upstream.calls)
				require.Equal(t, originalRetries, repo.task.RetryCount)
				require.Zero(t, repo.task.UpstreamRetryCount)
				require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
			})
		}
	}
}

func TestAsyncImageRequestFailedCannotExceedDurableActualCallBudget(t *testing.T) {
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			h := newRequestFailedGatewayHarness(t, platform, 2, 3, 1)
			cfg := referenceLocalFallbackConfig()
			task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
			var history []service.AsyncImageAccountAttempt
			for i := 0; i < 15; i++ {
				history = append(history, service.AsyncImageAccountAttempt{
					EventID: fmt.Sprintf("past-request-failed-call-%d", i), AccountID: 99,
					Status:      service.AsyncImageAccountAttemptInvoked,
					AttemptedAt: time.Date(2026, 11, 3, 0, 0, i, 0, time.UTC),
				})
			}
			var err error
			task.AccountAttempts, err = json.Marshal(history)
			require.NoError(t, err)
			require.Equal(t, 1, asyncImageRemainingUpstreamAttempts(task))
			repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
			h.worker.tasks = service.NewAsyncImageTaskService(repo)
			c, response, _ := invokeProviderRejectionGateway(t, h, task, cfg, requestFailedAttemptBudget(asyncImageRemainingUpstreamAttempts(task)))
			require.Equal(t, []int64{1}, h.upstream.calls, "A's sixteenth call must prevent an additional generation call to B")
			require.Equal(t, 1, service.AsyncImageUpstreamAttemptCount(c.Request.Context()))
			require.Equal(t, http.StatusBadRequest, response.Code)
			require.Equal(t, "async_image_attempt_budget_exhausted", gjson.GetBytes(response.Body.Bytes(), "error.code").String())
			disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, cfg, response.Code, response.Body.Bytes(), response.Header())
			require.False(t, disposition.requeue)
			require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
			require.NotNil(t, repo.task.ErrorCode)
			require.Equal(t, "generation_budget_exhausted", *repo.task.ErrorCode)
			require.Zero(t, repo.task.RetryCount)
			require.Zero(t, repo.task.UpstreamRetryCount)
			require.Zero(t, asyncImageRemainingUpstreamAttempts(repo.task))
		})
	}
}
