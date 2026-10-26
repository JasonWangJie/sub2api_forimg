//go:build unit

package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const internal422StatusUnknownBody = `{"error":{"type":"invalid_request_error","code":422,"status":"UNKNOWN","message":"Internal error encountered."}}`
const internal422CodeUnknownBody = `{"error":{"type":"invalid_request_error","code":"UNKNOWN","message":"Internal error encountered."}}`

func TestAsyncImageMaskedReferenceVariablePathFallsBackLocally(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, path := range []struct{ name, value string }{
			{"four segments", "/***/***/***/***"},
			{"six segments", "/***/***/***/***/***/***"},
		} {
			t.Run(platform+"/"+path.name, func(t *testing.T) {
				source := "https://***.com" + path.value
				message := `Error while downloading ` + source + ` Get "` + source + ` dial tcp ***.***.***.***:443: i/o timeout`
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{1: {400, message}})
				cfg := referenceLocalFallbackConfig()
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
				flag := canFallbackAsyncImageReferenceLocally(task, cfg)
				c, response, _, _ := invokeReferenceLocalFallbackGateway(t, h, task, &flag, true, true, false)
				require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
				require.Equal(t, service.AsyncImageReferenceLocalFallbackCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				require.Equal(t, []int64{1}, h.upstream.calls)
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, cfg, response.Code, response.Body.Bytes(), response.Header())
				require.True(t, disposition.requeue)
				require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
				require.Equal(t, service.AsyncImageReferenceTransportLocal, *repo.task.ReferenceTransport)
				require.Equal(t, 1, repo.task.ReferenceRetryCount)
				require.Equal(t, 1, repo.task.RetryCount)
				require.Equal(t, map[int64]struct{}{1: {}}, asyncImageNonRetryableAccountIDs(repo.task))

				h.body = prepareReferenceLocalFallbackBody(t, h)
				flag = canFallbackAsyncImageReferenceLocally(repo.task, cfg)
				require.False(t, flag)
				_, next, capture, usage := invokeReferenceLocalFallbackGateway(t, h, repo.task, &flag, true, true, true)
				require.Equal(t, http.StatusOK, next.Code, next.Body.String())
				require.Equal(t, []int64{1, 2}, h.upstream.calls)
				for _, attempt := range capture.Attempts() {
					require.Equal(t, int64(2), attempt.AccountID)
				}
				inlineBody := h.upstream.bodies[1]
				require.Contains(t, string(inlineBody), durableAsyncImageOnePixelPNG)
				require.NotContains(t, string(inlineBody), "https://cdn.example/input.png")
				if platform == service.PlatformGemini {
					require.Equal(t, int64(2), usage.Gemini().Account.ID)
					require.Equal(t, "2K", gjson.GetBytes(inlineBody, "generationConfig.imageConfig.imageSize").String())
					require.Equal(t, "3:2", gjson.GetBytes(inlineBody, "generationConfig.imageConfig.aspectRatio").String())
				} else {
					require.Equal(t, int64(2), usage.OpenAI().Account.ID)
					require.Equal(t, h.model, gjson.GetBytes(inlineBody, "model").String())
					require.Equal(t, "2048x1152", gjson.GetBytes(inlineBody, "size").String())
				}
			})
		}
	}
}

// The shared fixture configures same-account HTTP 400 retries. Rebuild only the
// gateway with explicit HTTP 422 retries to verify the new rule overrides them.
func newInternal422GatewayHarness(t *testing.T, platform string, maxSwitch int, failures map[int64]referenceDownloadGatewayFailure) *referenceDownloadGatewayHarness {
	t.Helper()
	h := newReferenceDownloadGatewayHarness(t, platform, 2, maxSwitch, failures)
	group, groupID := h.apiKey.Group, *h.apiKey.GroupID
	var accounts []service.Account
	for _, id := range []int64{1, 2} {
		accounts = append(accounts, service.Account{
			ID: id, Platform: platform, Type: service.AccountTypeAPIKey, Status: service.StatusActive,
			Schedulable: true, Priority: int(id), Concurrency: 1,
			Credentials: map[string]any{
				"api_key": "test-key", "base_url": "https://upstream.example/v1", "pool_mode": true,
				"pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []any{422},
			},
			AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
		})
		require.Equal(t, 3, accounts[len(accounts)-1].GetPoolModeRetryCount())
		require.True(t, accounts[len(accounts)-1].IsPoolModeRetryableStatus(http.StatusUnprocessableEntity))
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	if platform == service.PlatformGemini {
		var pointers []*service.Account
		for i := range accounts {
			pointers = append(pointers, &accounts[i])
		}
		gateway, cleanup := newTestGatewayHandler(t, group, pointers)
		t.Cleanup(cleanup)
		gateway.cfg, gateway.maxAccountSwitchesGemini = cfg, maxSwitch
		gateway.geminiCompatService = service.NewGeminiMessagesCompatService(nil, nil, nil, nil, nil, nil, h.upstream, nil, cfg)
		h.worker.gateway = gateway
	} else {
		gateway := service.NewOpenAIGatewayService(openAIImagesFailoverAccountRepo{accounts: accounts}, nil, nil, nil, nil, nil, nil, cfg,
			nil, nil, nil, nil, nil, h.upstream, nil, nil, nil, nil, nil, nil, nil, nil)
		billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(billing.Stop)
		handler := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(&fakeConcurrencyCache{}), billing,
			service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
		handler.maxAccountSwitches = maxSwitch
		h.worker.openAI = handler
	}
	return h
}

func TestAsyncImageInternal422DirectAccountSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, body := range []struct{ name, value string }{
			{"status UNKNOWN", internal422StatusUnknownBody},
			{"code UNKNOWN", internal422CodeUnknownBody},
		} {
			for _, scenario := range []struct {
				name      string
				failBoth  bool
				maxSwitch int
				calls     []int64
				status    int
			}{
				{"A to B succeeds", false, 3, []int64{1, 2}, http.StatusOK},
				{"candidate pool exhausted", true, 3, []int64{1, 2}, http.StatusUnprocessableEntity},
				{"switch budget exhausted", false, 0, []int64{1}, http.StatusUnprocessableEntity},
			} {
				t.Run(platform+"/"+body.name+"/"+scenario.name, func(t *testing.T) {
					failures := map[int64]referenceDownloadGatewayFailure{1: {422, "raw response overrides this message"}}
					h := newInternal422GatewayHarness(t, platform, scenario.maxSwitch, failures)
					h.upstream.rawErrorBodies = map[int64]string{1: body.value}
					if scenario.failBoth {
						h.upstream.failures[2] = referenceDownloadGatewayFailure{422, "raw response overrides this message"}
						h.upstream.rawErrorBodies[2] = body.value
					}
					c, response, capture, usage := h.invoke(t, nil, false)
					require.Equal(t, scenario.status, response.Code, response.Body.String())
					require.Equal(t, scenario.calls, h.upstream.calls, "configured HTTP 422 same-account retry=3 must not repeat A")
					assertReferenceDownloadGatewayRequest(t, h)
					for _, attempt := range capture.Attempts() {
						if attempt.Status != service.AsyncImageAccountAttemptFailed {
							continue
						}
						require.Equal(t, http.StatusUnprocessableEntity, attempt.StatusCode)
						require.Equal(t, "UNKNOWN", attempt.ProviderErrorCode)
						require.Equal(t, string(service.AsyncImageInternalError422Reason), attempt.FailureReason)
						require.Equal(t, "Internal error encountered.", attempt.Error)
					}
					if scenario.status == http.StatusOK {
						if platform == service.PlatformGemini {
							require.Equal(t, int64(2), usage.Gemini().Account.ID)
						} else {
							require.Equal(t, int64(2), usage.OpenAI().Account.ID)
						}
						return
					}
					require.Contains(t, []string{"422", "UNKNOWN"}, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
					require.Equal(t, "Internal error encountered.", gjson.GetBytes(response.Body.Bytes(), "error.message").String())
					task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
					repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
					h.worker.tasks = service.NewAsyncImageTaskService(repo)
					disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, referenceLocalFallbackConfig(), response.Code, response.Body.Bytes(), response.Header())
					require.False(t, disposition.requeue)
					require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
					require.Zero(t, repo.task.RetryCount)
					require.Empty(t, repo.task.RequestPayload)
					require.Equal(t, scenario.calls, h.upstream.calls)
				})
			}
		}
	}
}

func TestAsyncImageInternal422HardExclusionSurvivesWorkerAndSticky(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			h := newInternal422GatewayHarness(t, platform, 3, map[int64]referenceDownloadGatewayFailure{
				1: {422, "raw response overrides this message"}, 2: {503, "upstream temporarily unavailable"},
			})
			h.upstream.rawErrorBodies = map[int64]string{1: internal422StatusUnknownBody}
			c, first, capture, _ := h.invoke(t, nil, false)
			require.Equal(t, []int64{1, 2}, h.upstream.calls)
			history, _ := service.MergeAsyncImageAccountAttempts(nil, capture.Attempts())
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
			require.True(t, disposition.requeue, "B's transient error may retry while A remains excluded")
			require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
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

func TestAsyncImageInternal422StrictAndSynchronousBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, scenario := range []struct {
			name, body string
			async      bool
		}{
			{"different provider status", strings.ReplaceAll(internal422StatusUnknownBody, "UNKNOWN", "INVALID_ARGUMENT"), true},
			{"appended no replay", strings.ReplaceAll(internal422StatusUnknownBody, "Internal error encountered.", "Internal error encountered. Do not automatically regenerate."), true},
			{"appended safety refusal", strings.ReplaceAll(internal422StatusUnknownBody, "Internal error encountered.", "Internal error encountered. Rejected by safety policy."), true},
			{"ordinary 422", `{"error":{"status":"UNKNOWN","code":422,"message":"Invalid size"}}`, true},
			{"synchronous", internal422StatusUnknownBody, false},
		} {
			t.Run(platform+"/"+scenario.name, func(t *testing.T) {
				h := newInternal422GatewayHarness(t, platform, 3, map[int64]referenceDownloadGatewayFailure{1: {422, "raw response overrides this message"}})
				h.upstream.rawErrorBodies = map[int64]string{1: scenario.body}
				_, response, capture, _ := invokeReferenceLocalFallbackGateway(t, h, nil, nil, scenario.async, true, false)
				wantStatus := http.StatusUnprocessableEntity
				if !scenario.async && platform == service.PlatformGemini {
					// Keep Gemini's existing generic synchronous error mapping.
					wantStatus = http.StatusBadGateway
				}
				require.Equal(t, wantStatus, response.Code, response.Body.String())
				require.Equal(t, []int64{1}, h.upstream.calls)
				for _, attempt := range capture.Attempts() {
					require.NotEqual(t, string(service.AsyncImageInternalError422Reason), attempt.FailureReason)
				}
			})
		}
	}
}
