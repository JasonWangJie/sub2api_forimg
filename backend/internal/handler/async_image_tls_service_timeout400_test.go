//go:build unit

package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const serviceTimeout400GatewayMessage = "service timeout, please try again later."
const referenceTLSGatewayMessage = `Error while downloading https://references.example/input.png?token=private Get "https://references.example/input.png?token=private": net/http: TLS handshake timeout`
const referenceMaskedTLSGatewayMessage = `Error while downloading https://***.com/***/***/*** Get "https://***.com/***/***/*** net/http: TLS handshake timeout`

func TestAsyncImageReferenceTLSHybridPersistsLocalThenSwitches(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, message := range []struct{ name, text string }{
			{"raw URL with closing quote", referenceTLSGatewayMessage},
			{"masked URL without closing quote", referenceMaskedTLSGatewayMessage},
		} {
			t.Run(platform+"/"+message.name, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{1: {400, message.text}})
				cfg := referenceLocalFallbackConfig()
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
				flag := canFallbackAsyncImageReferenceLocally(task, cfg)
				require.True(t, flag)
				c, response, capture, _ := invokeReferenceLocalFallbackGateway(t, h, task, &flag, true, true, false)
				require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
				require.Equal(t, service.AsyncImageReferenceLocalFallbackCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				require.Equal(t, []int64{1}, h.upstream.calls, "HTTP 400 pool retries must not repeat A before local fallback")
				require.NotContains(t, response.Body.String(), "token=private")
				require.NotContains(t, response.Body.String(), "references.example")
				failed := false
				for _, attempt := range capture.Attempts() {
					if attempt.Status == service.AsyncImageAccountAttemptFailed {
						failed = true
						require.Equal(t, http.StatusBadRequest, attempt.StatusCode)
						require.Contains(t, attempt.Error, "net/http: TLS handshake timeout")
						require.True(t, service.IsAsyncImageReferenceAccountFailoverMessage(attempt.StatusCode, attempt.Error))
						require.NotContains(t, attempt.Error, "token=private")
					}
				}
				require.True(t, failed)
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, cfg, response.Code, response.Body.Bytes(), response.Header())
				require.True(t, disposition.requeue)
				require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
				require.Equal(t, service.AsyncImageReferenceTransportLocal, *repo.task.ReferenceTransport)
				require.Equal(t, 1, repo.task.ReferenceRetryCount)
				require.Equal(t, 1, repo.task.RetryCount)
				require.Equal(t, map[int64]struct{}{1: {}}, asyncImageNonRetryableAccountIDs(repo.task))
				require.Zero(t, asyncImageReferenceAccountRetryState(repo.task).sameAccountID)
				require.Len(t, repo.transitions, 1)
				require.Equal(t, int64(cfg.ReferenceFetchMaxRetries), gjson.GetBytes(repo.transitions[0].EventPayload, "max").Int())
				require.True(t, gjson.GetBytes(repo.transitions[0].EventPayload, "account_switch").Bool())
				require.True(t, gjson.GetBytes(repo.transitions[0].EventPayload, "fallback_local").Bool())

				h.body = prepareReferenceLocalFallbackBody(t, h)
				flag = canFallbackAsyncImageReferenceLocally(repo.task, cfg)
				require.False(t, flag)
				_, next, nextCapture, usage := invokeReferenceLocalFallbackGateway(t, h, repo.task, &flag, true, true, true)
				require.Equal(t, http.StatusOK, next.Code, next.Body.String())
				require.Equal(t, []int64{1, 2}, h.upstream.calls, "persistent hard exclusion must beat soft reset and stale sticky A")
				for _, attempt := range nextCapture.Attempts() {
					require.Equal(t, int64(2), attempt.AccountID)
				}
				inlineBody := h.upstream.bodies[1]
				require.Contains(t, string(inlineBody), durableAsyncImageOnePixelPNG)
				require.NotContains(t, string(inlineBody), "https://cdn.example/input.png")
				if platform == service.PlatformGemini {
					require.Equal(t, int64(2), usage.Gemini().Account.ID)
					require.Equal(t, h.model, usage.Gemini().Result.Model)
					require.Equal(t, "2K", gjson.GetBytes(inlineBody, "generationConfig.imageConfig.imageSize").String())
					require.Equal(t, "3:2", gjson.GetBytes(inlineBody, "generationConfig.imageConfig.aspectRatio").String())
				} else {
					require.Equal(t, int64(2), usage.OpenAI().Account.ID)
					require.Equal(t, h.model, gjson.GetBytes(inlineBody, "model").String())
					require.Equal(t, "2048x1152", gjson.GetBytes(inlineBody, "size").String())
					require.Equal(t, "high", gjson.GetBytes(inlineBody, "quality").String())
				}
			})
		}
	}
}

func setServiceTimeout400GatewayBodies(t *testing.T, h *referenceDownloadGatewayHarness) {
	t.Helper()
	h.upstream.rawErrorBodies = make(map[int64]string)
	for id, failure := range h.upstream.failures {
		if failure.status != http.StatusBadRequest {
			continue
		}
		body, err := json.Marshal(map[string]any{"error": map[string]any{
			"type": "invalid_request_error", "code": "vendor_timeout", "message": failure.message,
		}})
		require.NoError(t, err)
		h.upstream.rawErrorBodies[id] = string(body)
	}
}

func TestAsyncImageServiceTimeout400DirectAccountSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, scenario := range []struct {
			name                string
			accounts, maxSwitch int
			failures            map[int64]referenceDownloadGatewayFailure
			calls               []int64
			status              int
		}{
			{"A to B succeeds", 2, 3, map[int64]referenceDownloadGatewayFailure{1: {400, serviceTimeout400GatewayMessage}}, []int64{1, 2}, http.StatusOK},
			{"A 503 B 400 C succeeds", 3, 3, map[int64]referenceDownloadGatewayFailure{1: {503, "upstream temporarily unavailable"}, 2: {400, serviceTimeout400GatewayMessage}}, []int64{1, 2, 3}, http.StatusOK},
			{"candidate pool exhausted", 2, 3, map[int64]referenceDownloadGatewayFailure{1: {400, serviceTimeout400GatewayMessage}, 2: {400, serviceTimeout400GatewayMessage}}, []int64{1, 2}, http.StatusBadRequest},
			{"only candidate exhausted", 1, 3, map[int64]referenceDownloadGatewayFailure{1: {400, serviceTimeout400GatewayMessage}}, []int64{1}, http.StatusBadRequest},
			{"switch budget exhausted", 2, 0, map[int64]referenceDownloadGatewayFailure{1: {400, serviceTimeout400GatewayMessage}}, []int64{1}, http.StatusBadRequest},
		} {
			t.Run(platform+"/"+scenario.name, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, scenario.accounts, scenario.maxSwitch, scenario.failures)
				setServiceTimeout400GatewayBodies(t, h)
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
				flag := true
				c, response, capture, usage := invokeReferenceLocalFallbackGateway(t, h, task, &flag, true, true, false)
				require.Equal(t, scenario.status, response.Code, response.Body.String())
				require.Equal(t, scenario.calls, h.upstream.calls, "pool HTTP 400 retry=3 must never repeat a rejected account")
				assertReferenceDownloadGatewayRequest(t, h)
				knownFailure := false
				for _, attempt := range capture.Attempts() {
					if attempt.Status != service.AsyncImageAccountAttemptFailed || attempt.StatusCode != http.StatusBadRequest {
						continue
					}
					knownFailure = true
					require.Equal(t, "vendor_timeout", attempt.ProviderErrorCode)
					require.Equal(t, string(service.AsyncImageServiceTimeout400Reason), attempt.FailureReason)
					require.Equal(t, serviceTimeout400GatewayMessage, attempt.Error)
					require.NotEmpty(t, attempt.UpstreamRequestID)
				}
				require.True(t, knownFailure)
				if scenario.status == http.StatusOK {
					lastID := scenario.calls[len(scenario.calls)-1]
					if platform == service.PlatformGemini {
						require.Equal(t, lastID, usage.Gemini().Account.ID)
					} else {
						require.Equal(t, lastID, usage.OpenAI().Account.ID)
					}
					return
				}
				require.Equal(t, "vendor_timeout", gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				require.Equal(t, serviceTimeout400GatewayMessage, gjson.GetBytes(response.Body.Bytes(), "error.message").String())
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, referenceLocalFallbackConfig(), response.Code, response.Body.Bytes(), response.Header())
				require.False(t, disposition.requeue, "switch exhaustion must remain HTTP 400 and stop the worker")
				require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
				require.Zero(t, repo.task.RetryCount)
				require.Zero(t, repo.task.ReferenceRetryCount)
				require.Equal(t, service.AsyncImageReferenceTransportPassthroughFallbackLocal, *repo.task.ReferenceTransport)
				require.Empty(t, repo.task.RequestPayload)
				require.Equal(t, scenario.calls, h.upstream.calls)
			})
		}
	}
}

func TestAsyncImageServiceTimeout400HardExclusionSurvivesWorkerAndSticky(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, legacyAudit := range []bool{false, true} {
			name := "typed failure reason"
			if legacyAudit {
				name = "legacy message audit"
			}
			t.Run(platform+"/"+name, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{
					1: {400, serviceTimeout400GatewayMessage}, 2: {503, "upstream temporarily unavailable"},
				})
				setServiceTimeout400GatewayBodies(t, h)
				c, first, capture, _ := h.invoke(t, nil, false)
				wantStatus := http.StatusServiceUnavailable
				if platform == service.PlatformOpenAI {
					wantStatus = http.StatusBadGateway
				}
				require.Equal(t, wantStatus, first.Code, first.Body.String())
				require.Equal(t, []int64{1, 2}, h.upstream.calls)
				attempts := capture.Attempts()
				for i := range attempts {
					if attempts[i].AccountID == 1 && attempts[i].Status == service.AsyncImageAccountAttemptFailed {
						require.Equal(t, string(service.AsyncImageServiceTimeout400Reason), attempts[i].FailureReason)
						if legacyAudit {
							attempts[i].FailureReason = ""
						}
					}
				}
				history, _ := service.MergeAsyncImageAccountAttempts(nil, attempts)
				history, _ = service.MergeAsyncImageAccountAttempts(history, []service.AsyncImageAccountAttempt{
					{AccountID: 2, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 503, Error: "upstream temporarily unavailable"},
					{AccountID: 2, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 503, Error: "upstream temporarily unavailable"},
					{AccountID: 2, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 503, Error: "upstream temporarily unavailable"},
				})
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
				task.AccountAttempts = history
				require.Equal(t, map[int64]struct{}{1: {}}, asyncImageNonRetryableAccountIDs(task))
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, referenceLocalFallbackConfig(), first.Code, first.Body.Bytes(), first.Header())
				require.True(t, disposition.requeue, "B's 503 may requeue while A remains permanently excluded for this task")
				require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
				require.Zero(t, repo.task.ReferenceRetryCount)
				delete(h.upstream.failures, 2)
				before := len(h.upstream.calls)
				_, next, nextCapture, _ := h.invoke(t, repo.task, true)
				require.Equal(t, http.StatusOK, next.Code, next.Body.String())
				require.Equal(t, []int64{2}, h.upstream.calls[before:])
				for _, attempt := range nextCapture.Attempts() {
					require.Equal(t, int64(2), attempt.AccountID)
				}
				assertReferenceDownloadGatewayRequest(t, h)
			})
		}
	}
}

func TestAsyncImageServiceTimeout400StrictAndSynchronousBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, scenario := range []struct {
			name, message string
			status        int
			async         bool
		}{
			{"incomplete timeout family 400", "service timeout", 400, true},
			{"appended no replay", serviceTimeout400GatewayMessage + " Do not automatically regenerate.", 400, true},
			{"appended safety refusal", serviceTimeout400GatewayMessage + " Rejected by safety policy.", 400, true},
			{"prefixed extra explanation", "upstream: " + serviceTimeout400GatewayMessage, 400, true},
			{"wrong HTTP status", serviceTimeout400GatewayMessage, 422, true},
			{"synchronous", serviceTimeout400GatewayMessage, 400, false},
		} {
			t.Run(platform+"/"+scenario.name, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{1: {scenario.status, scenario.message}})
				setServiceTimeout400GatewayBodies(t, h)
				flag := true
				_, response, capture, _ := invokeReferenceLocalFallbackGateway(t, h, nil, &flag, scenario.async, true, false)
				require.Equal(t, scenario.status, response.Code, response.Body.String())
				require.Equal(t, []int64{1}, h.upstream.calls, "the precise timeout rule must not broaden generic or synchronous failures")
				require.NotEqual(t, service.AsyncImageReferenceLocalFallbackCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				for _, attempt := range capture.Attempts() {
					require.NotEqual(t, string(service.AsyncImageServiceTimeout400Reason), attempt.FailureReason)
				}
			})
		}
	}
}

func TestAsyncImageServiceTimeout400UnrelatedInvalidRequestCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{
				1: {400, "service unavailable"},
			})
			setServiceTimeout400GatewayBodies(t, h)
			_, response, capture, _ := h.invoke(t, nil, false)
			if platform == service.PlatformGemini {
				// Preserve the existing generic invalid_request failover outside
				// the newly guarded service-timeout message family.
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Equal(t, []int64{1, 2}, h.upstream.calls)
			} else {
				require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
				require.Equal(t, []int64{1}, h.upstream.calls)
			}
			for _, attempt := range capture.Attempts() {
				require.NotEqual(t, string(service.AsyncImageServiceTimeout400Reason), attempt.FailureReason)
			}
			assertReferenceDownloadGatewayRequest(t, h)
		})
	}
}

func TestAsyncImageReferenceTLSStrictAndSynchronousBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, scenario := range []struct {
			name, message string
			async, input  bool
		}{
			{"unmasked URL without closing quote", strings.ReplaceAll(referenceTLSGatewayMessage, `": net/http`, ` net/http`), true, true},
			{"different masked URLs", strings.Replace(referenceMaskedTLSGatewayMessage, `Get "https://***.com/***/***/***`, `Get "https://***.com/***/***`, 1), true, true},
			{"text task", referenceMaskedTLSGatewayMessage, true, false},
			{"synchronous", referenceTLSGatewayMessage, false, true},
		} {
			t.Run(platform+"/"+scenario.name, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{1: {400, scenario.message}})
				if !scenario.input {
					if platform == service.PlatformGemini {
						h.body = []byte(`{"model":"gemini-3-pro-image-preview","stream":false,"messages":[{"role":"user","content":"draw a skyline"}]}`)
					} else {
						h.body = []byte(`{"model":"gpt-image-2","prompt":"draw a skyline","size":"2048x1152","quality":"high"}`)
					}
				}
				flag := true
				_, response, capture, _ := invokeReferenceLocalFallbackGateway(t, h, nil, &flag, scenario.async, scenario.input, false)
				require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
				require.Equal(t, []int64{1}, h.upstream.calls)
				require.NotEqual(t, service.AsyncImageReferenceLocalFallbackCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				for _, attempt := range capture.Attempts() {
					require.NotEqual(t, service.AsyncImageReferenceAccountFailoverCode, attempt.FailureReason)
				}
			})
		}
	}
}
