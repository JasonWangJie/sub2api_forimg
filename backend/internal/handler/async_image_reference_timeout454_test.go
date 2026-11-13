//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const referenceTimeout454Message = "下载参考图超时，请稍后重试"
const referenceTimeout454Body = `{"error":{"code":"asset_download_timeout","message":"下载参考图超时，请稍后重试"}}`

func newReferenceTimeout454Harness(t *testing.T, platform string, count int, failAll bool) *referenceDownloadGatewayHarness {
	t.Helper()
	failures, bodies := map[int64]referenceDownloadGatewayFailure{}, map[int64]string{}
	for id := int64(1); id <= int64(count); id++ {
		if id == 1 || failAll {
			failures[id], bodies[id] = referenceDownloadGatewayFailure{454, referenceTimeout454Message}, referenceTimeout454Body
		}
	}
	h := newReferenceDownloadGatewayHarness(t, platform, count, 3, failures, func(account *service.Account) {
		account.Credentials["pool_mode_retry_status_codes"] = []any{454}
	})
	h.upstream.rawErrorBodies = bodies
	return h
}

func TestAsyncImageReferenceTimeout454SwitchAndExhaustion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGemini} {
		for _, failAll := range []bool{false, true} {
			name := platform + "/success"
			if failAll {
				name = platform + "/exhausted"
			}
			t.Run(name, func(t *testing.T) {
				h := newReferenceTimeout454Harness(t, platform, 2, failAll)
				c, response, capture, _ := h.invoke(t, nil, false)
				require.Equal(t, []int64{1, 2}, h.upstream.calls, "454 must switch channels even when pool-mode retries include 454")
				for _, attempt := range capture.Attempts() {
					if attempt.Status == service.AsyncImageAccountAttemptFailed {
						require.Equal(t, 454, attempt.StatusCode)
						require.Equal(t, "asset_download_timeout", attempt.ProviderErrorCode)
						require.Equal(t, service.AsyncImageReferenceAccountFailoverCode, attempt.FailureReason)
						require.True(t, isAsyncImageReferenceAccountFailure(attempt))
					}
				}
				if !failAll {
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
					assertReferenceDownloadGatewayRequest(t, h)
					return
				}
				require.Equal(t, 454, response.Code, response.Body.String())
				require.Equal(t, service.AsyncImageReferenceAccountFailoverCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				require.False(t, h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, referenceLocalFallbackConfig(), response.Code, response.Body.Bytes(), response.Header()).requeue)
				require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
				require.Zero(t, repo.task.RetryCount)
			})
		}
	}
}

func TestAsyncImageReferenceTimeout454HybridPersistsLocalThenSwitches(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGemini} {
		for _, count := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/accounts%d", platform, count), func(t *testing.T) {
				h := newReferenceTimeout454Harness(t, platform, count, false)
				cfg := referenceLocalFallbackConfig()
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
				c, response, capture := invokeProviderRejectionGateway(t, h, task, cfg)
				require.Equal(t, []int64{1}, h.upstream.calls)
				require.Equal(t, 454, response.Code, response.Body.String())
				require.Equal(t, service.AsyncImageReferenceLocalFallbackCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				found := false
				for _, attempt := range capture.Attempts() {
					if attempt.Status == service.AsyncImageAccountAttemptFailed {
						found = true
						require.Equal(t, 454, attempt.StatusCode)
					}
				}
				require.True(t, found)
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				require.True(t, h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, cfg, response.Code, response.Body.Bytes(), response.Header()).requeue)
				require.Equal(t, service.AsyncImageReferenceTransportLocal, *repo.task.ReferenceTransport)
				require.Equal(t, 1, repo.task.ReferenceRetryCount)
				require.Equal(t, 1, repo.task.RetryCount)
				require.Equal(t, map[int64]struct{}{1: {}}, asyncImageNonRetryableAccountIDs(repo.task))
				h.body = prepareReferenceLocalFallbackBody(t, h)
				if count == 1 {
					delete(h.upstream.failures, 1)
					delete(h.upstream.rawErrorBodies, 1)
				}
				_, next, nextCapture := invokeProviderRejectionGateway(t, h, repo.task, cfg)
				require.Equal(t, http.StatusOK, next.Code, next.Body.String())
				require.Equal(t, []int64{1, int64(count)}, h.upstream.calls, "prefer B; only A may be reused after candidate exhaustion with local input")
				for _, attempt := range nextCapture.Attempts() {
					require.Equal(t, int64(count), attempt.AccountID)
				}
				require.NotContains(t, string(h.upstream.bodies[1]), "https://cdn.example/input.png")
				require.Contains(t, string(h.upstream.bodies[1]), durableAsyncImageOnePixelPNG)
				if platform == service.PlatformOpenAI {
					require.Equal(t, h.model, gjson.GetBytes(h.upstream.bodies[1], "model").String())
				} else {
					require.Contains(t, h.upstream.paths[1], h.model)
				}
			})
		}
	}
}

func TestAsyncImageReferenceTimeout454PersistedRetryIsBounded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			h := newReferenceTimeout454Harness(t, platform, 1, true)
			cfg := referenceLocalFallbackConfig()
			cfg.UpstreamTransientMaxRetries, cfg.TotalMaxRetries = 2, 16
			task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
			repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
			h.worker.tasks = service.NewAsyncImageTaskService(repo)
			for n := 0; n <= 2; n++ {
				c, response, _ := invokeProviderRejectionGateway(t, h, repo.task, cfg)
				require.Equal(t, 454, response.Code, response.Body.String())
				require.Equal(t, service.AsyncImageRetryableRejectionCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				require.Equal(t, n < 2, h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), repo.task, cfg, response.Code, response.Body.Bytes(), response.Header()).requeue)
				if n < 2 {
					require.Equal(t, n+1, repo.task.UpstreamRetryCount)
					require.Equal(t, n+1, repo.task.RetryCount)
					repo.task.Status = service.AsyncImageTaskStatusInvoking
				}
			}
			require.Equal(t, []int64{1, 1, 1}, h.upstream.calls)
			require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
		})
	}
}

func TestAsyncImageReferenceTimeout454WorkerBudgetGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, guard := range []string{"reference budget", "total budget", "missing audit"} {
		t.Run(guard, func(t *testing.T) {
			h := newReferenceTimeout454Harness(t, service.PlatformOpenAI, 2, false)
			cfg := referenceLocalFallbackConfig()
			task := referenceLocalFallbackTask(h.platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
			c, response, _ := invokeProviderRejectionGateway(t, h, task, cfg)
			ctx := c.Request.Context()
			switch guard {
			case "reference budget":
				task.ReferenceRetryCount = cfg.ReferenceFetchMaxRetries
			case "total budget":
				task.RetryCount = cfg.TotalMaxRetries
			case "missing audit":
				ctx = context.Background()
			}
			retries := task.RetryCount
			repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
			h.worker.tasks = service.NewAsyncImageTaskService(repo)
			require.False(t, h.worker.handleAsyncImageUpstreamFailure(ctx, ctx, task, cfg, response.Code, response.Body.Bytes(), response.Header()).requeue)
			require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
			require.Equal(t, retries, repo.task.RetryCount)
			require.Equal(t, []int64{1}, h.upstream.calls)
		})
	}
	// An old unmarked 454 with a different error code is not a hard exclusion.
	attempts, err := json.Marshal([]service.AsyncImageAccountAttempt{{AccountID: 1, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 454, ProviderErrorCode: "upstream_error", Error: referenceTimeout454Message}})
	require.NoError(t, err)
	require.Empty(t, asyncImageNonRetryableAccountIDs(&service.AsyncImageTask{AccountAttempts: attempts}))
}
