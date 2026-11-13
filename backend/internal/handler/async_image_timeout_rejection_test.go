//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAsyncImageTimeoutGatewaySwitchesAndRetainsStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGemini} {
		for _, status := range []int{401, 403, 404, 408, 418, 429, 454, 502, 504, 599} {
			t.Run(fmt.Sprintf("%s/%d", platform, status), func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{1: {status, "Provider request Time-out"}}, func(a *service.Account) { a.Credentials["pool_mode_retry_status_codes"] = []any{status} })
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
				_, response, capture := invokeProviderRejectionGateway(t, h, task, referenceLocalFallbackConfig())
				require.Equal(t, 200, response.Code, response.Body.String())
				require.Equal(t, []int64{1, 2}, h.upstream.calls)
				for _, attempt := range capture.Attempts() {
					if attempt.Status == service.AsyncImageAccountAttemptFailed {
						require.Equal(t, status, attempt.StatusCode)
						require.Equal(t, string(service.AsyncImageTimeoutRejectionReason), attempt.FailureReason)
					}
				}
			})
		}
	}
}

func TestAsyncImageTimeoutWorkerExhaustionUsesOnlyPrivateTimeoutMarker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			h := newReferenceDownloadGatewayHarness(t, platform, 1, 3, map[int64]referenceDownloadGatewayFailure{1: {504, "Provider request timed out"}})
			cfg := referenceLocalFallbackConfig()
			cfg.UpstreamTransientMaxRetries = 2
			task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
			repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
			h.worker.tasks = service.NewAsyncImageTaskService(repo)
			for n := 0; n <= 2; n++ {
				c, response, _ := invokeProviderRejectionGateway(t, h, repo.task, cfg)
				require.Equal(t, 504, response.Code, response.Body.String())
				require.Equal(t, service.AsyncImageTimeoutRetryCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				require.Equal(t, n < 2, h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), repo.task, cfg, response.Code, response.Body.Bytes(), response.Header()).requeue)
				if n < 2 {
					repo.task.Status = service.AsyncImageTaskStatusInvoking
				}
			}
			require.Equal(t, []int64{1, 1, 1}, h.upstream.calls)
			require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
			require.Equal(t, 2, repo.task.UpstreamRetryCount)
		})
	}
	// An arbitrary 418 + the old shared marker is not newly replayable.
	task := referenceLocalFallbackTask(service.PlatformOpenAI, "gpt-image-2", service.AsyncImageReferenceTransportPassthrough)
	repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
	worker := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo)}
	body := []byte(`{"error":{"code":"async_image_retryable_rejection","message":"unrelated upstream error"}}`)
	require.False(t, worker.handleAsyncImageUpstreamFailure(context.Background(), context.Background(), task, referenceLocalFallbackConfig(), 418, body, nil).requeue)
}

func TestAsyncImageMissingFile404GatewayFallbackAndAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGemini} {
		for _, hybrid := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/hybrid%v", platform, hybrid), func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{1: {404, "文件引用不存在: https://cdn.example/ref.png?sig=private"}})
				h.upstream.rawErrorBodies = map[int64]string{1: `{"error":{"code":404,"message":"文件引用不存在: https://cdn.example/ref.png?sig=private"}}`}
				mode := service.AsyncImageReferenceTransportPassthrough
				if hybrid {
					mode = service.AsyncImageReferenceTransportPassthroughFallbackLocal
				}
				task := referenceLocalFallbackTask(platform, h.model, mode)
				c, response, capture := invokeProviderRejectionGateway(t, h, task, referenceLocalFallbackConfig())
				for _, attempt := range capture.Attempts() {
					if attempt.Status == service.AsyncImageAccountAttemptFailed {
						require.Equal(t, 404, attempt.StatusCode)
						require.True(t, isAsyncImageReferenceAccountFailure(attempt))
						require.NotContains(t, attempt.Error, "private")
					}
				}
				if !hybrid {
					require.Equal(t, 200, response.Code)
					require.Equal(t, []int64{1, 2}, h.upstream.calls)
					return
				}
				require.Equal(t, 404, response.Code, response.Body.String())
				require.Equal(t, []int64{1}, h.upstream.calls)
				require.Equal(t, service.AsyncImageReferenceLocalFallbackCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				require.NotContains(t, response.Body.String(), "private")
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				require.True(t, h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, referenceLocalFallbackConfig(), response.Code, response.Body.Bytes(), response.Header()).requeue)
				require.Equal(t, service.AsyncImageReferenceTransportLocal, *repo.task.ReferenceTransport)
				require.Equal(t, 1, repo.task.ReferenceRetryCount)
				var attempts []service.AsyncImageAccountAttempt
				require.NoError(t, json.Unmarshal(repo.task.AccountAttempts, &attempts))
				require.NotEmpty(t, attempts)
			})
		}
	}
}

func TestAsyncImageTimeoutGatewayTerminalDetailsDoNotSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGemini} {
		for _, status := range []int{401, 418, 503, 454} {
			for _, detail := range []string{"do not retry", "generation outcome is unknown", "may already have been charged", "content policy", "generated image", "已产图", "请勿重试", "已经扣费"} {
				t.Run(fmt.Sprintf("%s/%d/%s", platform, status, detail), func(t *testing.T) {
					h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{1: {status, "timeout"}})
					body, err := json.Marshal(map[string]any{"error": map[string]any{"code": "upstream_error", "message": "timeout", "details": []any{map[string]string{"warning": detail}}}})
					require.NoError(t, err)
					h.upstream.rawErrorBodies = map[int64]string{1: string(body)}
					task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
					c, response, _ := invokeProviderRejectionGateway(t, h, task, referenceLocalFallbackConfig())
					require.NotEqual(t, 200, response.Code)
					require.Equal(t, status, response.Code)
					require.Equal(t, []int64{1}, h.upstream.calls)
					require.NotEqual(t, service.AsyncImageTimeoutRetryCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
					repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
					h.worker.tasks = service.NewAsyncImageTaskService(repo)
					require.False(t, h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, referenceLocalFallbackConfig(), response.Code, response.Body.Bytes(), response.Header()).requeue)
					if detail == "generation outcome is unknown" || detail == "may already have been charged" || detail == "已经扣费" {
						require.Equal(t, service.AsyncImageTaskStatusExecutionUnknown, repo.task.Status)
					} else {
						require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
					}
					require.Zero(t, repo.task.RetryCount)
				})
			}
		}
	}
}
