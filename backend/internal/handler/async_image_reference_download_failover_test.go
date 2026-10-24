//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const referenceDownloadProxyFailureMessage = "Reference image download failed for https://cdn.example/input.png?token=download-secret budgetMs=35000; reason=direct_and_proxy_failed; direct=timeout of 15000ms exceeded; proxy=Request failed with status code 502"
const referenceDownloadNetworkFailureMessage = `Error while downloading https://cdn.example/input.png?token=download-secret Get "https://cdn.example/input.png?token=download-secret": dial tcp 203.0.113.42:443: i/o timeout`

type referenceDownloadGatewayFailure struct {
	status  int
	message string
}

type referenceDownloadGatewayUpstream struct {
	service.HTTPUpstream
	t              *testing.T
	platform       string
	failures       map[int64]referenceDownloadGatewayFailure
	rawErrorBodies map[int64]string
	calls          []int64
	paths          []string
	bodies         [][]byte
}

func (u *referenceDownloadGatewayUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.t.Helper()
	body, err := io.ReadAll(req.Body)
	require.NoError(u.t, err)
	u.calls = append(u.calls, accountID)
	u.paths = append(u.paths, req.URL.Path)
	u.bodies = append(u.bodies, body)
	status := http.StatusOK
	response := []byte(`{"data":[{"b64_json":"` + durableAsyncImageOnePixelPNG + `"}]}`)
	if u.platform == service.PlatformGemini {
		response = []byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + durableAsyncImageOnePixelPNG + `"}}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`)
	}
	if failure, ok := u.failures[accountID]; ok {
		status = failure.status
		response, err = json.Marshal(map[string]any{"error": map[string]any{
			"type": "invalid_request_error", "code": "reference_download_failed", "status": "INVALID_ARGUMENT", "message": failure.message,
		}})
		require.NoError(u.t, err)
	}
	if raw, ok := u.rawErrorBodies[accountID]; ok {
		response = []byte(raw)
	}
	return &http.Response{
		StatusCode: status, Body: io.NopCloser(bytes.NewReader(response)), Request: req,
		Header: http.Header{
			"Content-Type":      []string{"application/json"},
			"X-Request-Id":      []string{fmt.Sprintf("reference-download-%d", accountID)},
			"X-Goog-Request-Id": []string{fmt.Sprintf("reference-download-%d", accountID)},
			"Retry-After":       []string{"17"},
		},
	}, nil
}

type referenceDownloadGatewayHarness struct {
	worker   *DurableAsyncImageHandler
	upstream *referenceDownloadGatewayUpstream
	apiKey   *service.APIKey
	platform string
	model    string
	body     []byte
	path     string
}

func newReferenceDownloadGatewayHarness(t *testing.T, platform string, accountCount, maxSwitch int, failures map[int64]referenceDownloadGatewayFailure) *referenceDownloadGatewayHarness {
	t.Helper()
	groupID := int64(8501)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: platform, Status: service.StatusActive, AllowImageGeneration: true}
	var accounts []service.Account
	for id := int64(1); id <= int64(accountCount); id++ {
		accounts = append(accounts, service.Account{
			ID: id, Name: fmt.Sprintf("reference-download-%d", id), Platform: platform, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Priority: int(id), Concurrency: 1,
			Credentials: map[string]any{
				"api_key": "test-key", "base_url": "https://upstream.example/v1",
				"pool_mode": true, "pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []any{400},
			},
			AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
		})
	}
	upstream := &referenceDownloadGatewayUpstream{t: t, platform: platform, failures: failures}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	harness := &referenceDownloadGatewayHarness{
		worker: &DurableAsyncImageHandler{}, upstream: upstream, platform: platform,
		apiKey: &service.APIKey{
			ID: 8502, UserID: 8503, GroupID: &groupID, Group: group, Status: service.StatusActive,
			User: &service.User{ID: 8503, Concurrency: 2, Balance: 100},
		},
	}
	if platform == service.PlatformGemini {
		var pointers []*service.Account
		for i := range accounts {
			pointers = append(pointers, &accounts[i])
		}
		gateway, cleanup := newTestGatewayHandler(t, group, pointers)
		t.Cleanup(cleanup)
		gateway.cfg = cfg
		gateway.maxAccountSwitchesGemini = maxSwitch
		gateway.geminiCompatService = service.NewGeminiMessagesCompatService(nil, nil, nil, nil, nil, nil, upstream, nil, cfg)
		harness.worker.gateway = gateway
		harness.model, harness.path = "gemini-3-pro-image-preview", EndpointChatCompletions
		harness.body = []byte(`{"model":"gemini-3-pro-image-preview","stream":false,"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://cdn.example/input.png"}},{"type":"text","text":"draw a skyline"}]}],"extra_body":{"google":{"image_config":{"image_size":"2K","aspect_ratio":"3:2"}}}}`)
	} else {
		gateway := service.NewOpenAIGatewayService(openAIImagesFailoverAccountRepo{accounts: accounts}, nil, nil, nil, nil, nil, nil, cfg,
			nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
		billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(billing.Stop)
		handler := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(&fakeConcurrencyCache{}), billing,
			service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
		handler.maxAccountSwitches = maxSwitch
		harness.worker.openAI = handler
		harness.model, harness.path = "gpt-image-2", EndpointImagesGenerations
		harness.body = []byte(`{"model":"gpt-image-2","prompt":"draw a skyline","size":"2048x1152","quality":"high","image_urls":["https://cdn.example/input.png"]}`)
	}
	return harness
}

func (h *referenceDownloadGatewayHarness) invoke(t *testing.T, history *service.AsyncImageTask, staleSticky bool) (*gin.Context, *httptest.ResponseRecorder, *service.AsyncImageAccountAttemptCapture, *AsyncImageUsageCapture) {
	t.Helper()
	return h.invokeWithReferenceFlag(t, history, staleSticky, true, true)
}

func (h *referenceDownloadGatewayHarness) invokeWithReferenceFlag(t *testing.T, history *service.AsyncImageTask, staleSticky, bindReferenceFlag, inputReferenceTask bool) (*gin.Context, *httptest.ResponseRecorder, *service.AsyncImageAccountAttemptCapture, *AsyncImageUsageCapture) {
	t.Helper()
	c, recorder := newAsyncGeminiWorkerContext(h.apiKey)
	capture, usage := &service.AsyncImageAccountAttemptCapture{}, &AsyncImageUsageCapture{}
	ctx := service.WithAsyncImageAccountAttemptCapture(c.Request.Context(), capture)
	if bindReferenceFlag {
		ctx = service.WithAsyncImageInputReferenceTask(ctx, inputReferenceTask)
	}
	ctx = withAsyncImageUsageCapture(ctx, usage)
	ctx = service.WithAsyncImageRoutingSessionHash(ctx, "async-image:reference-download")
	if h.platform == service.PlatformGemini {
		ctx = service.WithGeminiAsyncImageGeneration(ctx)
		ctx = service.WithGeminiImageResponseCapture(ctx, &service.GeminiImageResponseCapture{})
	}
	if history != nil {
		hardIDs := asyncImageNonRetryableAccountIDs(history)
		require.Equal(t, map[int64]struct{}{1: {}}, hardIDs)
		ctx = service.WithAsyncImageExcludedAccountIDs(ctx, map[int64]struct{}{1: {}, 2: {}})
		ctx = service.WithAsyncImageNonRetryableAccountIDs(ctx, hardIDs)
	}
	if staleSticky {
		ctx = service.WithPrefetchedStickySession(ctx, 1, *h.apiKey.GroupID, true)
	}
	c.Request = httptest.NewRequest(http.MethodPost, h.path, bytes.NewReader(h.body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	require.True(t, h.worker.forwardAsyncImageUpstream(c, h.platform))
	return c, recorder, capture, usage
}

func TestAsyncImageReferenceDownloadNetworkShapeNeverRegeneratesTextTasks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, scenario := range []struct {
			name string
			bind bool
		}{
			{"missing input reference task flag", false},
			{"explicit text task flag", true},
		} {
			t.Run(platform+"/"+scenario.name, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{
					1: {http.StatusBadRequest, referenceDownloadNetworkFailureMessage},
				})
				if platform == service.PlatformGemini {
					h.body = []byte(`{"model":"gemini-3-pro-image-preview","stream":false,"messages":[{"role":"user","content":"draw a skyline"}]}`)
				} else {
					h.body = []byte(`{"model":"gpt-image-2","prompt":"draw a skyline","size":"2048x1152","quality":"high"}`)
				}
				c, response, _, _ := h.invokeWithReferenceFlag(t, nil, false, scenario.bind, false)
				require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
				require.Equal(t, []int64{1}, h.upstream.calls, "an output-shaped download error must never switch to B")
				require.NotEqual(t, service.AsyncImageReferenceAccountFailoverCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				task := &service.AsyncImageTask{
					TaskID: "asyncimg_text_output_download", Version: 1, Status: service.AsyncImageTaskStatusInvoking,
					Platform: platform, Model: h.model, RequestType: service.AsyncImageRequestTypeTextToImage,
					RequestPayload: []byte("encrypted request"),
				}
				repo := &generatedWorkerResultRepo{task: task}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				disposition := h.worker.handleAsyncImageUpstreamFailure(context.Background(), c.Request.Context(), task, testAsyncImageRetryConfig(), response.Code, response.Body.Bytes(), response.Header())
				require.False(t, disposition.requeue)
				require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
				require.Zero(t, repo.task.RetryCount)
				require.Equal(t, []int64{1}, h.upstream.calls)
			})
		}
	}
}

func assertReferenceDownloadGatewayRequest(t *testing.T, h *referenceDownloadGatewayHarness) {
	t.Helper()
	for i, body := range h.upstream.bodies {
		if h.platform == service.PlatformGemini {
			require.True(t, strings.HasSuffix(h.upstream.paths[i], "/"+h.model+":generateContent"))
			require.Equal(t, "2K", gjson.GetBytes(body, "generationConfig.imageConfig.imageSize").String())
			require.Equal(t, "3:2", gjson.GetBytes(body, "generationConfig.imageConfig.aspectRatio").String())
			require.Contains(t, string(body), "https://cdn.example/input.png")
		} else {
			require.True(t, strings.HasSuffix(h.upstream.paths[i], "/images/edits"))
			require.Equal(t, h.model, gjson.GetBytes(body, "model").String())
			require.Equal(t, "2048x1152", gjson.GetBytes(body, "size").String())
			require.Equal(t, "high", gjson.GetBytes(body, "quality").String())
			require.Equal(t, "https://cdn.example/input.png", gjson.GetBytes(body, "images.0.image_url").String())
		}
		require.JSONEq(t, string(h.upstream.bodies[0]), string(body))
	}
}

func TestAsyncImageReferenceDownloadDirectAccountFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, message := range []struct{ name, text string }{
			{"direct and proxy download failed", referenceDownloadProxyFailureMessage},
			{"downloading network timeout", referenceDownloadNetworkFailureMessage},
		} {
			for _, scenario := range []struct {
				name      string
				failBoth  bool
				maxSwitch int
				wantCalls []int64
				status    int
			}{
				{"A to B", false, 3, []int64{1, 2}, http.StatusOK},
				{"A and B exhausted", true, 3, []int64{1, 2}, http.StatusBadRequest},
				{"switch budget exhausted", false, 0, []int64{1}, http.StatusBadRequest},
			} {
				t.Run(platform+"/"+message.name+"/"+scenario.name, func(t *testing.T) {
					require.True(t, service.IsAsyncImageReferenceAccountFailoverMessage(http.StatusBadRequest, message.text))
					failures := map[int64]referenceDownloadGatewayFailure{1: {http.StatusBadRequest, message.text}}
					if scenario.failBoth {
						failures[2] = referenceDownloadGatewayFailure{http.StatusBadRequest, message.text}
					}
					h := newReferenceDownloadGatewayHarness(t, platform, 2, scenario.maxSwitch, failures)
					c, recorder, capture, usage := h.invoke(t, nil, false)
					require.Equal(t, scenario.status, recorder.Code, "%s; attempts=%+v", recorder.Body.String(), capture.Attempts())
					require.Equal(t, scenario.wantCalls, h.upstream.calls, "pool 400 retry=3 must never repeat the failed account")
					assertReferenceDownloadGatewayRequest(t, h)
					var selected []int64
					for _, attempt := range capture.Attempts() {
						if attempt.Status == service.AsyncImageAccountAttemptSelected {
							selected = append(selected, attempt.AccountID)
						}
						if attempt.Status == service.AsyncImageAccountAttemptFailed {
							require.Equal(t, http.StatusBadRequest, attempt.StatusCode)
							require.NotContains(t, attempt.Error, "download-secret")
							require.NotContains(t, attempt.Error, "cdn.example")
						}
					}
					require.Equal(t, scenario.wantCalls, selected)
					if scenario.status == http.StatusOK {
						if platform == service.PlatformGemini {
							require.Equal(t, int64(2), usage.Gemini().Account.ID)
						} else {
							require.Equal(t, int64(2), usage.OpenAI().Account.ID)
						}
						return
					}
					require.Equal(t, service.AsyncImageReferenceAccountFailoverCode, gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
					require.NotContains(t, recorder.Body.String(), "download-secret")
					mode := service.AsyncImageReferenceTransportPassthroughFallbackLocal
					task := &service.AsyncImageTask{
						TaskID: "asyncimg_reference_download", Version: 1, Status: service.AsyncImageTaskStatusInvoking,
						Platform: platform, Model: h.model, RequestType: service.AsyncImageRequestTypeImageToImage,
						ReferenceTransport: &mode, RequestPayload: []byte("encrypted request"),
					}
					repo := &generatedWorkerResultRepo{task: task}
					h.worker.tasks = service.NewAsyncImageTaskService(repo)
					disposition := h.worker.handleAsyncImageUpstreamFailure(context.Background(), c.Request.Context(), task, testAsyncImageRetryConfig(), recorder.Code, recorder.Body.Bytes(), recorder.Header())
					require.False(t, disposition.requeue)
					require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
					require.Zero(t, repo.task.RetryCount)
					require.Empty(t, repo.task.RequestPayload)
					require.Len(t, repo.transitions, 1)
					require.False(t, repo.transitions[0].IncrementReferenceRetry)
					require.False(t, repo.transitions[0].IncrementUpstreamRetry)
					require.Equal(t, scenario.wantCalls, h.upstream.calls)
				})
			}
		}
		t.Run(platform+"/different download errors advance A B C", func(t *testing.T) {
			h := newReferenceDownloadGatewayHarness(t, platform, 3, 3, map[int64]referenceDownloadGatewayFailure{
				1: {http.StatusBadRequest, referenceDownloadProxyFailureMessage},
				2: {http.StatusBadRequest, referenceDownloadNetworkFailureMessage},
			})
			_, recorder, _, _ := h.invoke(t, nil, false)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, []int64{1, 2, 3}, h.upstream.calls)
			assertReferenceDownloadGatewayRequest(t, h)
		})
	}
}

func TestAsyncImageReferenceDownloadHardExclusionSurvivesWorkerRetryAndSticky(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, message := range []struct{ name, text string }{
			{"direct and proxy download failed", referenceDownloadProxyFailureMessage},
			{"downloading network timeout", referenceDownloadNetworkFailureMessage},
		} {
			t.Run(platform+"/"+message.name, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{
					1: {http.StatusBadRequest, message.text},
					2: {http.StatusServiceUnavailable, "upstream temporarily unavailable"},
				})
				firstContext, firstResponse, firstCapture, _ := h.invoke(t, nil, false)
				require.Equal(t, []int64{1, 2}, h.upstream.calls)
				wantFirstStatus := http.StatusServiceUnavailable
				if platform == service.PlatformOpenAI {
					// The Images exhaustion response maps ordinary 5xx to 502;
					// its account audit must still preserve B's actual HTTP 503.
					wantFirstStatus = http.StatusBadGateway
				}
				require.Equal(t, wantFirstStatus, firstResponse.Code, firstResponse.Body.String())
				var lastFailed service.AsyncImageAccountAttempt
				for _, attempt := range firstCapture.Attempts() {
					if attempt.Status == service.AsyncImageAccountAttemptFailed {
						lastFailed = attempt
					}
				}
				require.Equal(t, int64(2), lastFailed.AccountID)
				require.Equal(t, http.StatusServiceUnavailable, lastFailed.StatusCode)
				history, _ := service.MergeAsyncImageAccountAttempts(nil, firstCapture.Attempts())
				// Age A's failure beyond the normal recent-three audit window.
				history, _ = service.MergeAsyncImageAccountAttempts(history, []service.AsyncImageAccountAttempt{
					{AccountID: 2, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 503, Error: "upstream temporarily unavailable"},
					{AccountID: 2, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 503, Error: "upstream temporarily unavailable"},
					{AccountID: 2, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 503, Error: "upstream temporarily unavailable"},
				})
				task := &service.AsyncImageTask{
					TaskID: "asyncimg_reference_download_later", Version: 1, Platform: platform, Model: h.model,
					Status: service.AsyncImageTaskStatusInvoking, RequestType: service.AsyncImageRequestTypeImageToImage,
					AccountAttempts: history, RequestPayload: []byte("encrypted request"),
				}
				repo := &generatedWorkerResultRepo{task: task}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				disposition := h.worker.handleAsyncImageUpstreamFailure(context.Background(), firstContext.Request.Context(), task, testAsyncImageRetryConfig(), firstResponse.Code, firstResponse.Body.Bytes(), firstResponse.Header())
				require.True(t, disposition.requeue, "B's transient failure may retry without making A eligible")
				require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
				require.Equal(t, map[int64]struct{}{1: {}}, asyncImageNonRetryableAccountIDs(repo.task))

				delete(h.upstream.failures, 2)
				callCount := len(h.upstream.calls)
				_, secondResponse, secondCapture, _ := h.invoke(t, repo.task, true)
				require.Equal(t, http.StatusOK, secondResponse.Code)
				require.Equal(t, []int64{2}, h.upstream.calls[callCount:], "soft exclusion reset and stale sticky A must only retry B")
				for _, attempt := range secondCapture.Attempts() {
					require.Equal(t, int64(2), attempt.AccountID)
				}
				assertReferenceDownloadGatewayRequest(t, h)
			})
		}
	}
}
