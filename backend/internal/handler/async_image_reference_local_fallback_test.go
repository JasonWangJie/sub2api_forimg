//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const referenceDownloadUnclosedNetworkMessage = `Error while downloading https://***.com/***/***/*** Get "https://***.com/***/***/*** dial tcp ***.***.***.***:443: i/o timeout`

// Persist the transport, audit and category counters that the older result-only
// fixture does not need. The next invocation must read them from task state.
type referenceLocalFallbackRepo struct{ *generatedWorkerResultRepo }

func (r *referenceLocalFallbackRepo) TransitionAsyncImageTask(ctx context.Context, tr service.AsyncImageTaskTransition) (*service.AsyncImageTask, error) {
	task, err := r.generatedWorkerResultRepo.TransitionAsyncImageTask(ctx, tr)
	if err != nil {
		return nil, err
	}
	if tr.ReferenceTransport != nil {
		mode := *tr.ReferenceTransport
		task.ReferenceTransport = &mode
	}
	if tr.AccountAttempts != nil {
		task.AccountAttempts = append(json.RawMessage(nil), tr.AccountAttempts...)
	}
	if tr.AttemptedAccountIDs != nil {
		task.AttemptedAccountIDs = append(json.RawMessage(nil), tr.AttemptedAccountIDs...)
	}
	if tr.IncrementReferenceRetry {
		task.ReferenceRetryCount++
	}
	if tr.IncrementCapacityRetry {
		task.CapacityRetryCount++
	}
	if tr.IncrementUpstreamRetry {
		task.UpstreamRetryCount++
	}
	if tr.ClearAccountID {
		task.AccountID = nil
	}
	r.task = task
	copy := *task
	return &copy, nil
}

func referenceLocalFallbackTask(platform, model, mode string) *service.AsyncImageTask {
	accountID, size := int64(1), "2K"
	return &service.AsyncImageTask{
		TaskID: "asyncimg_hybrid_local", Version: 1, Status: service.AsyncImageTaskStatusInvoking,
		Platform: platform, Model: model, RequestType: service.AsyncImageRequestTypeImageToImage,
		AccountID: &accountID, RequestedImageSize: &size, ReferenceTransport: &mode,
		RequestPayload: []byte("encrypted original request"),
	}
}

func referenceLocalFallbackConfig() service.AsyncImageRuntimeConfig {
	cfg := testAsyncImageRetryConfig()
	cfg.OpenAIReferenceTransportMode = service.AsyncImageReferenceTransportPassthroughFallbackLocal
	cfg.GeminiReferenceTransportMode = service.AsyncImageReferenceTransportPassthroughFallbackLocal
	cfg.RetryJitterPercent = 0
	return cfg
}

func invokeReferenceLocalFallbackGateway(t *testing.T, h *referenceDownloadGatewayHarness, task *service.AsyncImageTask, fallbackFlag *bool, async, input, staleSticky bool) (*gin.Context, *httptest.ResponseRecorder, *service.AsyncImageAccountAttemptCapture, *AsyncImageUsageCapture) {
	t.Helper()
	c, recorder := newAsyncGeminiWorkerContext(h.apiKey)
	capture, usage := &service.AsyncImageAccountAttemptCapture{}, &AsyncImageUsageCapture{}
	ctx := service.WithAsyncImageInputReferenceTask(c.Request.Context(), input)
	ctx = service.WithAsyncImageRoutingSessionHash(ctx, "async-image:hybrid-local")
	if fallbackFlag != nil {
		ctx = service.WithAsyncImageReferenceLocalFallback(ctx, *fallbackFlag)
	}
	if async {
		ctx = service.WithAsyncImageAccountAttemptCapture(ctx, capture)
		ctx = withAsyncImageUsageCapture(ctx, usage)
		if h.platform == service.PlatformGemini {
			ctx = service.WithGeminiAsyncImageGeneration(ctx)
			ctx = service.WithGeminiImageResponseCapture(ctx, &service.GeminiImageResponseCapture{})
		}
	}
	if task != nil && len(task.AccountAttempts) > 0 {
		ctx = service.WithAsyncImageNonRetryableAccountIDs(ctx, asyncImageNonRetryableAccountIDs(task))
		ctx = service.WithAsyncImageExcludedAccountIDs(ctx, map[int64]struct{}{1: {}, 2: {}})
	}
	if staleSticky {
		ctx = service.WithPrefetchedStickySession(ctx, 1, *h.apiKey.GroupID, true)
	}
	c.Request = httptest.NewRequest(http.MethodPost, h.path, bytes.NewReader(h.body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	require.True(t, h.worker.forwardAsyncImageUpstream(c, h.platform))
	return c, recorder, capture, usage
}

func prepareReferenceLocalFallbackBody(t *testing.T, h *referenceDownloadGatewayHarness) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(durableAsyncImageOnePixelPNG)
	require.NoError(t, err)
	ref, err := (service.AsyncImageReferenceDownloader{}).ValidateBytes(data, "image/png")
	require.NoError(t, err)
	loads := 0
	downloader := service.AsyncImageReferenceDownloader{
		BoundLoader: func(_ context.Context, rawURL string) (*service.AsyncImageReference, bool, error) {
			loads++
			require.Equal(t, "https://cdn.example/input.png", rawURL)
			return ref, true, nil
		},
	}
	var body []byte
	if h.platform == service.PlatformGemini {
		normalized, parseErr := service.ParseBBGeminiImageRequest(h.body, EndpointChatCompletions)
		require.NoError(t, parseErr)
		body, err = service.BuildGeminiAsyncChatBodyWithTransport(context.Background(), normalized, downloader, service.AsyncImageReferenceTransportLocal)
	} else {
		var root any
		require.NoError(t, json.Unmarshal(h.body, &root))
		changed, inlineErr := inlineAsyncOpenAIReferencesWithTransport(context.Background(), &root, downloader, true)
		require.NoError(t, inlineErr)
		require.True(t, changed)
		body, err = json.Marshal(root)
	}
	require.NoError(t, err)
	require.Equal(t, 1, loads, "reference bytes must come from the in-memory loader, never external HTTP")
	require.NotContains(t, string(body), "https://cdn.example/input.png")
	return body
}

func TestAsyncImageHybridReferenceFailurePersistsLocalThenSwitchesAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, message := range []struct{ name, text string }{
			{"network", referenceDownloadNetworkFailureMessage},
			{"network without closing quote", referenceDownloadUnclosedNetworkMessage},
			{"direct and proxy", referenceDownloadProxyFailureMessage},
			{"total download budget", referenceBudgetTestMessage},
		} {
			t.Run(platform+"/"+message.name, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{1: {400, message.text}})
				cfg := referenceLocalFallbackConfig()
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
				flag := canFallbackAsyncImageReferenceLocally(task, cfg)
				require.True(t, flag)
				c, response, _, _ := invokeReferenceLocalFallbackGateway(t, h, task, &flag, true, true, false)
				require.Equal(t, []int64{1}, h.upstream.calls, "hybrid must hand the first failed account back to the worker")
				require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
				require.Equal(t, service.AsyncImageReferenceLocalFallbackCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				require.NotContains(t, response.Body.String(), "download-secret")
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, cfg, response.Code, response.Body.Bytes(), response.Header())
				require.True(t, disposition.requeue)
				require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
				require.Equal(t, service.AsyncImageReferenceTransportLocal, *repo.task.ReferenceTransport)
				require.Equal(t, 1, repo.task.ReferenceRetryCount)
				require.Equal(t, 1, repo.task.RetryCount)
				require.Nil(t, repo.task.AccountID)
				require.NotEmpty(t, repo.task.RequestPayload)
				require.Equal(t, map[int64]struct{}{1: {}}, asyncImageNonRetryableAccountIDs(repo.task))
				require.Zero(t, asyncImageReferenceAccountRetryState(repo.task).sameAccountID, "the failed account must not become a sticky same-account retry")
				require.Len(t, repo.transitions, 1)
				require.True(t, repo.transitions[0].IncrementReferenceRetry)
				require.Equal(t, "reference_image_fetch_retry", repo.transitions[0].EventType)
				require.Equal(t, int64(cfg.ReferenceFetchMaxRetries), gjson.GetBytes(repo.transitions[0].EventPayload, "max").Int())
				require.True(t, gjson.GetBytes(repo.transitions[0].EventPayload, "account_switch").Bool())
				require.True(t, gjson.GetBytes(repo.transitions[0].EventPayload, "fallback_local").Bool())

				h.body = prepareReferenceLocalFallbackBody(t, h)
				flag = canFallbackAsyncImageReferenceLocally(repo.task, cfg)
				require.False(t, flag)
				_, nextResponse, nextCapture, usage := invokeReferenceLocalFallbackGateway(t, h, repo.task, &flag, true, true, true)
				require.Equal(t, http.StatusOK, nextResponse.Code, nextResponse.Body.String())
				require.Equal(t, []int64{1, 2}, h.upstream.calls, "hard exclusion must beat relaxed history and stale sticky A")
				for _, attempt := range nextCapture.Attempts() {
					require.Equal(t, int64(2), attempt.AccountID)
				}
				localBody := h.upstream.bodies[1]
				require.NotContains(t, string(localBody), "https://cdn.example/input.png")
				require.Contains(t, string(localBody), durableAsyncImageOnePixelPNG)
				if platform == service.PlatformGemini {
					require.Equal(t, h.model, usage.Gemini().Result.Model)
					require.Equal(t, int64(2), usage.Gemini().Account.ID)
					require.Equal(t, "2K", gjson.GetBytes(localBody, "generationConfig.imageConfig.imageSize").String())
					require.Equal(t, "3:2", gjson.GetBytes(localBody, "generationConfig.imageConfig.aspectRatio").String())
				} else {
					require.Equal(t, int64(2), usage.OpenAI().Account.ID)
					require.Equal(t, h.model, gjson.GetBytes(localBody, "model").String())
					require.Equal(t, "2048x1152", gjson.GetBytes(localBody, "size").String())
					require.True(t, strings.HasPrefix(gjson.GetBytes(localBody, "images.0.image_url").String(), "data:image/png;base64,"))
				}
			})
		}
	}
}

func TestAsyncImageHybridLocalFallbackWorkerGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, guard := range []string{"reference budget disabled", "reference budget exhausted", "total budget exhausted", "already local", "passthrough", "text task", "unrelated marker message", "502 deadline response", "missing failure audit"} {
			t.Run(platform+"/"+guard, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{1: {400, referenceDownloadProxyFailureMessage}})
				cfg := referenceLocalFallbackConfig()
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
				flag := true
				c, response, _, _ := invokeReferenceLocalFallbackGateway(t, h, task, &flag, true, true, false)
				require.Equal(t, service.AsyncImageReferenceLocalFallbackCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				ctx := c.Request.Context()
				status, body, wantStatus := response.Code, response.Body.Bytes(), service.AsyncImageTaskStatusFailed
				switch guard {
				case "reference budget disabled":
					cfg.ReferenceFetchMaxRetries = 0
				case "reference budget exhausted":
					task.ReferenceRetryCount = cfg.ReferenceFetchMaxRetries
				case "total budget exhausted":
					task.RetryCount = cfg.TotalMaxRetries
				case "already local":
					mode := service.AsyncImageReferenceTransportLocal
					task.ReferenceTransport = &mode
				case "passthrough":
					mode := service.AsyncImageReferenceTransportPassthrough
					task.ReferenceTransport = &mode
				case "text task":
					task.RequestType = service.AsyncImageRequestTypeTextToImage
				case "unrelated marker message", "502 deadline response":
					message := "upstream capacity unavailable"
					if guard == "502 deadline response" {
						status, wantStatus = http.StatusBadGateway, service.AsyncImageTaskStatusExecutionUnknown
						message = "image URL fetch failed: context deadline exceeded"
					}
					var err error
					body, err = json.Marshal(map[string]any{"error": map[string]any{"code": service.AsyncImageReferenceLocalFallbackCode, "message": message}})
					require.NoError(t, err)
				case "missing failure audit":
					ctx = context.Background()
				}
				retries := task.RetryCount
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				disposition := h.worker.handleAsyncImageUpstreamFailure(ctx, ctx, task, cfg, status, body, response.Header())
				require.False(t, disposition.requeue)
				require.Equal(t, wantStatus, repo.task.Status)
				require.Equal(t, retries, repo.task.RetryCount)
				require.Len(t, repo.transitions, 1)
				require.False(t, repo.transitions[0].IncrementReferenceRetry)
				require.Equal(t, []int64{1}, h.upstream.calls)
			})
		}
	}
}

func TestAsyncImageLocalFallbackPrivateFlagBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, scenario := range []string{"missing flag", "false flag", "passthrough", "synchronous", "text task"} {
			t.Run(platform+"/"+scenario, func(t *testing.T) {
				message := referenceDownloadProxyFailureMessage
				if scenario == "text task" {
					message = referenceDownloadUnclosedNetworkMessage
				}
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{1: {400, message}})
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
				flag, async, input := false, true, true
				flagPtr := &flag
				if scenario == "missing flag" {
					flagPtr = nil
				} else if scenario == "synchronous" {
					flag, async = true, false
				} else if scenario == "text task" {
					flag, input = true, false
					task.RequestType = service.AsyncImageRequestTypeTextToImage
					if platform == service.PlatformGemini {
						h.body = []byte(`{"model":"gemini-3-pro-image-preview","stream":false,"messages":[{"role":"user","content":"draw a skyline"}]}`)
					} else {
						h.body = []byte(`{"model":"gpt-image-2","prompt":"draw a skyline","size":"2048x1152"}`)
					}
				} else if scenario == "passthrough" {
					mode := service.AsyncImageReferenceTransportPassthrough
					task.ReferenceTransport = &mode
				}
				_, response, _, _ := invokeReferenceLocalFallbackGateway(t, h, task, flagPtr, async, input, false)
				require.NotEqual(t, service.AsyncImageReferenceLocalFallbackCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				if scenario == "synchronous" || scenario == "text task" {
					require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
					require.Equal(t, []int64{1}, h.upstream.calls)
				} else {
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
					require.Equal(t, []int64{1, 2}, h.upstream.calls)
					assertReferenceDownloadGatewayRequest(t, h)
				}
			})
		}
	}
}

func TestAsyncImageHybridLocalFallbackNeverRevivesOnlyFailedAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			h := newReferenceDownloadGatewayHarness(t, platform, 1, 3, map[int64]referenceDownloadGatewayFailure{1: {400, referenceDownloadProxyFailureMessage}})
			cfg := referenceLocalFallbackConfig()
			cfg.CapacityMaxRetries, cfg.TotalMaxRetries = 1, 4
			task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
			flag := true
			c, response, _, _ := invokeReferenceLocalFallbackGateway(t, h, task, &flag, true, true, false)
			repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
			h.worker.tasks = service.NewAsyncImageTaskService(repo)
			require.True(t, h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, cfg, response.Code, response.Body.Bytes(), response.Header()).requeue)
			h.body = prepareReferenceLocalFallbackBody(t, h)
			for n := 0; n <= cfg.TotalMaxRetries; n++ {
				repo.task.Status = service.AsyncImageTaskStatusInvoking // Simulate the next successful worker claim.
				flag = canFallbackAsyncImageReferenceLocally(repo.task, cfg)
				require.False(t, flag)
				ctx, exhausted, _, _ := invokeReferenceLocalFallbackGateway(t, h, repo.task, &flag, true, true, true)
				require.NotEqual(t, http.StatusOK, exhausted.Code)
				var disposition asyncImageWorkerDisposition
				if isOpsRoutingCapacityLimited(ctx) {
					disposition = h.worker.deferAsyncImageForLocalCapacity(ctx.Request.Context(), repo.task, cfg)
				} else {
					disposition = h.worker.handleAsyncImageUpstreamFailure(ctx.Request.Context(), ctx.Request.Context(), repo.task, cfg, exhausted.Code, exhausted.Body.Bytes(), exhausted.Header())
				}
				require.Equal(t, []int64{1}, h.upstream.calls)
				if !disposition.requeue {
					require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
					require.LessOrEqual(t, repo.task.CapacityRetryCount, cfg.CapacityMaxRetries)
					require.LessOrEqual(t, repo.task.RetryCount, cfg.TotalMaxRetries)
					return
				}
			}
			t.Fatal("no-candidate task exceeded its bounded retry budget")
		})
	}
}
