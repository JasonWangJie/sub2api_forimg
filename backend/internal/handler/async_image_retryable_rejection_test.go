//go:build unit

package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

var providerRejectionMessages = []string{
	"reference image exceeds limit: 20 MB limit exceeded",
	"Provider API error: fileData requires a user YouTube reference or a gateway file_id (request id: variable)",
	"The prompt or input images could not be processed. Adjust the request and try again.",
}

func invokeProviderRejectionGateway(t *testing.T, h *referenceDownloadGatewayHarness, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, configure ...func(context.Context) context.Context) (*gin.Context, *httptest.ResponseRecorder, *service.AsyncImageAccountAttemptCapture) {
	t.Helper()
	c, recorder := newAsyncGeminiWorkerContext(h.apiKey)
	capture := &service.AsyncImageAccountAttemptCapture{}
	ctx := service.WithAsyncImageAccountAttemptCapture(c.Request.Context(), capture)
	ctx = service.WithAsyncImageProviderRetryPolicy(ctx)
	ctx = service.WithAsyncImageInputReferenceTask(ctx, task.RequestType == service.AsyncImageRequestTypeImageToImage)
	ctx = service.WithAsyncImageReferenceLocalFallback(ctx, canFallbackAsyncImageReferenceLocally(task, cfg))
	ctx = service.WithAsyncImageExcludedAccountIDs(ctx, asyncImageReferenceRetryExcludedAccountIDs(task, cfg))
	ctx = service.WithAsyncImageNonRetryableAccountIDs(ctx, asyncImageFixedCapabilityAccountIDs(task))
	ctx = service.WithAsyncImageRoutingSessionHash(ctx, "async-image:"+task.TaskID)
	ctx = withAsyncImageUsageCapture(ctx, &AsyncImageUsageCapture{})
	ctx = service.WithPrefetchedStickySession(ctx, 1, *h.apiKey.GroupID, true)
	if h.platform == service.PlatformGemini {
		ctx = service.WithGeminiAsyncImageGeneration(ctx)
		ctx = service.WithGeminiImageResponseCapture(ctx, &service.GeminiImageResponseCapture{})
	}
	for _, apply := range configure {
		ctx = apply(ctx)
	}
	c.Request = httptest.NewRequest(http.MethodPost, h.path, bytes.NewReader(h.body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	require.True(t, h.worker.forwardAsyncImageUpstream(c, h.platform))
	return c, recorder, capture
}

func TestAsyncImageRetryableRejectionSwitchesBeforeWorkerRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, message := range providerRejectionMessages {
			t.Run(platform+"/"+message, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{1: {400, message}})
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
				if message == providerRejectionMessages[2] {
					task.RequestType = service.AsyncImageRequestTypeTextToImage
				}
				_, response, capture := invokeProviderRejectionGateway(t, h, task, referenceLocalFallbackConfig())
				require.Equal(t, 200, response.Code, response.Body.String())
				require.Equal(t, []int64{1, 2}, h.upstream.calls, "pool-mode retries must not repeat A before trying B")
				for _, attempt := range capture.Attempts() {
					if attempt.Status == service.AsyncImageAccountAttemptFailed {
						require.Equal(t, 400, attempt.StatusCode)
						require.NotEmpty(t, attempt.ProviderErrorCode)
						require.NotEmpty(t, attempt.FailureReason)
						require.NotEmpty(t, attempt.Error)
					}
				}
			})
		}
	}
}

func TestAsyncImageRetryableRejectionOnlyAccountRetriesArePersistedAndBounded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		// Fixed byte limits are covered by TestAsyncImageFixedCapabilityStopsOnlyFixedFailures.
		// Only transport or transient processing can recover on the original account.
		for _, message := range providerRejectionMessages[1:] {
			t.Run(platform+"/"+message, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 1, 3, map[int64]referenceDownloadGatewayFailure{1: {400, message}})
				cfg := referenceLocalFallbackConfig()
				cfg.UpstreamTransientMaxRetries, cfg.TotalMaxRetries = 2, 16
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				for attempt := 0; attempt <= 2; attempt++ {
					c, response, _ := invokeProviderRejectionGateway(t, h, repo.task, cfg)
					require.Equal(t, 400, response.Code, response.Body.String())
					require.Equal(t, service.AsyncImageRetryableRejectionCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
					disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), repo.task, cfg, response.Code, response.Body.Bytes(), response.Header())
					require.Equal(t, attempt < 2, disposition.requeue)
					if disposition.requeue {
						require.Equal(t, attempt+1, repo.task.UpstreamRetryCount)
						require.Equal(t, attempt+1, repo.task.RetryCount)
						require.Empty(t, asyncImageNonRetryableAccountIDs(repo.task))
						repo.task.Status = service.AsyncImageTaskStatusInvoking
					}
				}
				require.Equal(t, []int64{1, 1, 1}, h.upstream.calls)
				require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
				require.Equal(t, 2, repo.task.UpstreamRetryCount)
				require.Equal(t, 2, repo.task.RetryCount)
			})
		}
	}
}

func TestAsyncImageRetryableRejectionOnlyAccountCanRecover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			h := newReferenceDownloadGatewayHarness(t, platform, 1, 3, map[int64]referenceDownloadGatewayFailure{1: {400, providerRejectionMessages[2]}})
			cfg := referenceLocalFallbackConfig()
			task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
			repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
			h.worker.tasks = service.NewAsyncImageTaskService(repo)
			c, response, _ := invokeProviderRejectionGateway(t, h, task, cfg)
			disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, cfg, response.Code, response.Body.Bytes(), response.Header())
			require.True(t, disposition.requeue)
			delete(h.upstream.failures, 1)
			_, response, _ = invokeProviderRejectionGateway(t, h, repo.task, cfg)
			require.Equal(t, 200, response.Code, response.Body.String())
			require.Equal(t, []int64{1, 1}, h.upstream.calls)
		})
	}
}

func TestAsyncImageRetryableRejectionExistingFamiliesUseWorkerBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, message := range []string{serviceTimeout400GatewayMessage, "Reference image fetch failed: timeout"} {
			t.Run(platform+"/"+message, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 1, 3, map[int64]referenceDownloadGatewayFailure{1: {400, message}})
				cfg := referenceLocalFallbackConfig()
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				c, response, _ := invokeProviderRejectionGateway(t, h, task, cfg)
				require.Equal(t, service.AsyncImageRetryableRejectionCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, cfg, response.Code, response.Body.Bytes(), response.Header())
				require.True(t, disposition.requeue)
				delete(h.upstream.failures, 1)
				_, response, _ = invokeProviderRejectionGateway(t, h, repo.task, cfg)
				require.Equal(t, 200, response.Code, response.Body.String())
				require.Equal(t, []int64{1, 1}, h.upstream.calls)
			})
		}
	}
}

func TestAsyncImageRetryableRejectionHonorsZeroAndTotalBudgets(t *testing.T) {
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, exhausted := range []string{"category", "total"} {
			t.Run(platform+"/"+exhausted, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 1, 3, map[int64]referenceDownloadGatewayFailure{1: {400, providerRejectionMessages[2]}})
				cfg := referenceLocalFallbackConfig()
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
				if exhausted == "category" {
					cfg.UpstreamTransientMaxRetries = 0
				} else {
					task.RetryCount = cfg.TotalMaxRetries
				}
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				c, response, _ := invokeProviderRejectionGateway(t, h, task, cfg)
				disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, cfg, response.Code, response.Body.Bytes(), response.Header())
				require.False(t, disposition.requeue)
				require.Equal(t, []int64{1}, h.upstream.calls)
				require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
			})
		}
	}
}

func TestAsyncImageReferenceTransportRejectionPersistsInlineAndPrefersAlternate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, accounts := range []int{1, 2} {
			t.Run(platform+string(rune('0'+accounts)), func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, accounts, 3, map[int64]referenceDownloadGatewayFailure{1: {400, providerRejectionMessages[1]}})
				cfg := referenceLocalFallbackConfig()
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				c, response, _ := invokeProviderRejectionGateway(t, h, task, cfg)
				require.Equal(t, []int64{1}, h.upstream.calls)
				require.Equal(t, service.AsyncImageReferenceTransportRetryCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, cfg, response.Code, response.Body.Bytes(), response.Header())
				require.True(t, disposition.requeue)
				require.Equal(t, service.AsyncImageReferenceTransportLocal, *repo.task.ReferenceTransport)
				require.Equal(t, 1, repo.task.ReferenceRetryCount)
				require.Empty(t, asyncImageNonRetryableAccountIDs(repo.task))
				h.body = prepareReferenceLocalFallbackBody(t, h)
				if accounts == 1 {
					delete(h.upstream.failures, 1)
				}
				_, response, _ = invokeProviderRejectionGateway(t, h, repo.task, cfg)
				require.Equal(t, 200, response.Code, response.Body.String())
				require.Equal(t, []int64{1, int64(accounts)}, h.upstream.calls)
				require.Contains(t, string(h.upstream.bodies[1]), durableAsyncImageOnePixelPNG)
				require.NotContains(t, string(h.upstream.bodies[1]), "https://cdn.example/input.png")
			})
		}
	}
}
