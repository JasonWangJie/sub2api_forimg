//go:build unit

package handler

import (
	"bytes"
	"context"
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

type geminiReferenceBudgetUpstream struct {
	service.HTTPUpstream
	t        *testing.T
	failures map[int64]bool
	accounts []int64
	paths    []string
	bodies   [][]byte
}

func (u *geminiReferenceBudgetUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.t.Helper()
	body, err := io.ReadAll(req.Body)
	require.NoError(u.t, err)
	u.accounts = append(u.accounts, accountID)
	u.paths = append(u.paths, req.URL.Path)
	u.bodies = append(u.bodies, body)
	status := http.StatusOK
	responseBody := `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + durableAsyncImageOnePixelPNG + `"}}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`
	if u.failures[accountID] {
		status = http.StatusBadRequest
		responseBody = `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"` + referenceBudgetTestMessage + `"}}`
	}
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type":      []string{"application/json"},
			"X-Goog-Request-Id": []string{fmt.Sprintf("reference-budget-%d", accountID)},
			"Retry-After":       []string{"9"},
		},
		Body: io.NopCloser(strings.NewReader(responseBody)), Request: req,
	}, nil
}

func TestAsyncGeminiReferenceBudgetDirectAccountFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name       string
		failures   map[int64]bool
		maxSwitch  int
		history    bool
		wantCalls  []int64
		wantStatus int
	}{
		{name: "next account succeeds", failures: map[int64]bool{1: true}, maxSwitch: 3, wantCalls: []int64{1, 2}, wantStatus: http.StatusOK},
		{name: "candidate pool exhausted", failures: map[int64]bool{1: true, 2: true}, maxSwitch: 3, wantCalls: []int64{1, 2}, wantStatus: http.StatusBadRequest},
		{name: "switch budget exhausted", failures: map[int64]bool{1: true}, maxSwitch: 0, wantCalls: []int64{1}, wantStatus: http.StatusBadRequest},
		{name: "later invocation never reselects budget failed account", failures: map[int64]bool{1: true}, maxSwitch: 3, history: true, wantCalls: []int64{2}, wantStatus: http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const requestModel = "gemini-3-pro-image-preview"
			groupID := int64(8401)
			group := &service.Group{
				ID: groupID, Hydrated: true, Platform: service.PlatformGemini,
				Status: service.StatusActive, AllowImageGeneration: true,
			}
			var accounts []*service.Account
			for _, id := range []int64{1, 2} {
				accounts = append(accounts, &service.Account{
					ID: id, Name: fmt.Sprintf("reference-budget-%d", id),
					Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, Priority: int(id), Concurrency: 1,
					Credentials: map[string]any{
						"api_key": "test-key", "base_url": "https://gemini.example",
						"pool_mode": true, "pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []any{400},
					},
					AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
				})
			}
			gateway, cleanup := newTestGatewayHandler(t, group, accounts)
			t.Cleanup(cleanup)
			upstream := &geminiReferenceBudgetUpstream{t: t, failures: tt.failures}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			gateway.cfg = cfg
			gateway.maxAccountSwitchesGemini = 3
			gateway.geminiCompatService = service.NewGeminiMessagesCompatService(nil, nil, nil, nil, nil, nil, upstream, nil, cfg)
			apiKey := &service.APIKey{
				ID: 8402, UserID: 8403, GroupID: &groupID, Group: group, Status: service.StatusActive,
				User: &service.User{ID: 8403, Concurrency: 2, Balance: 100},
			}
			c, recorder := newAsyncGeminiWorkerContext(apiKey)
			body := []byte(`{"model":"` + requestModel + `","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://cdn.example/input.png"}},{"type":"text","text":"draw a skyline"}]}],"stream":false,"extra_body":{"google":{"image_config":{"image_size":"2K","aspect_ratio":"3:2"}}}}`)
			ctx := service.WithGeminiAsyncImageGeneration(c.Request.Context())
			ctx = service.WithAsyncImageGeminiMaxAccountSwitches(ctx, tt.maxSwitch)
			if tt.history {
				// A's budget failure remains in the durable audit even after more
				// than three newer failures on B. Relaxing recent exclusions must
				// make B available again without ever making A eligible.
				history, _ := service.MergeAsyncImageAccountAttempts(nil, []service.AsyncImageAccountAttempt{
					{AccountID: 1, Status: service.AsyncImageAccountAttemptFailed, StatusCode: http.StatusBadRequest,
						ProviderErrorCode: "INVALID_ARGUMENT", Error: "Reference image download exceeded its total time budget for https://*** (budgetMs=35000; reason=timeout)"},
					{AccountID: 2, Status: service.AsyncImageAccountAttemptFailed, StatusCode: http.StatusServiceUnavailable, Error: "upstream temporarily unavailable"},
					{AccountID: 2, Status: service.AsyncImageAccountAttemptFailed, StatusCode: http.StatusServiceUnavailable, Error: "upstream temporarily unavailable"},
					{AccountID: 2, Status: service.AsyncImageAccountAttemptFailed, StatusCode: http.StatusServiceUnavailable, Error: "upstream temporarily unavailable"},
				})
				task := &service.AsyncImageTask{AccountAttempts: history}
				hardIDs := asyncImageNonRetryableAccountIDs(task)
				require.Equal(t, map[int64]struct{}{1: {}}, hardIDs)
				ctx = service.WithAsyncImageExcludedAccountIDs(ctx, map[int64]struct{}{1: {}, 2: {}})
				ctx = service.WithAsyncImageNonRetryableAccountIDs(ctx, hardIDs)
			}
			attempts, usage, image := &service.AsyncImageAccountAttemptCapture{}, &AsyncImageUsageCapture{}, &service.GeminiImageResponseCapture{}
			ctx = service.WithAsyncImageAccountAttemptCapture(ctx, attempts)
			ctx = service.WithGeminiImageResponseCapture(ctx, image)
			ctx = withAsyncImageUsageCapture(ctx, usage)
			c.Request = httptest.NewRequest(http.MethodPost, EndpointChatCompletions, bytes.NewReader(body)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			worker := &DurableAsyncImageHandler{gateway: gateway}

			require.True(t, worker.forwardAsyncImageUpstream(c, service.PlatformGemini))

			require.Equal(t, tt.wantCalls, upstream.accounts, "budget failures must never use the configured same-account 400 retries")
			require.Equal(t, tt.wantStatus, recorder.Code)
			var selectedIDs []int64
			for _, attempt := range attempts.Attempts() {
				if attempt.Status == service.AsyncImageAccountAttemptSelected {
					selectedIDs = append(selectedIDs, attempt.AccountID)
				}
				if attempt.Status == service.AsyncImageAccountAttemptFailed {
					require.Equal(t, http.StatusBadRequest, attempt.StatusCode)
					require.Equal(t, "INVALID_ARGUMENT", attempt.ProviderErrorCode)
					require.Equal(t, fmt.Sprintf("reference-budget-%d", attempt.AccountID), attempt.UpstreamRequestID)
					require.NotContains(t, attempt.Error, "cdn.example")
				}
			}
			require.Equal(t, tt.wantCalls, selectedIDs)
			for i, requestBody := range upstream.bodies {
				require.Equal(t, "/v1beta/models/"+requestModel+":generateContent", upstream.paths[i])
				require.Equal(t, "2K", gjson.GetBytes(requestBody, "generationConfig.imageConfig.imageSize").String())
				require.Equal(t, "3:2", gjson.GetBytes(requestBody, "generationConfig.imageConfig.aspectRatio").String())
				require.Contains(t, string(requestBody), "https://cdn.example/input.png")
				require.JSONEq(t, string(upstream.bodies[0]), string(requestBody), "account switching must preserve the complete upstream request")
			}
			if tt.wantStatus == http.StatusOK {
				require.NotNil(t, usage.Gemini())
				require.Equal(t, int64(2), usage.Gemini().Account.ID)
				require.Len(t, image.Images(), 1)
				require.NoError(t, image.Error())
				return
			}
			require.Equal(t, service.AsyncImageReferenceBudgetExceededCode, gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
			require.Contains(t, gjson.GetBytes(recorder.Body.Bytes(), "error.message").String(), "Reference image download exceeded its total time budget")
			require.NotContains(t, recorder.Body.String(), "cdn.example")
			require.Equal(t, "9", recorder.Header().Get("Retry-After"))
			require.Nil(t, usage.Gemini())
			require.Empty(t, image.Images())

			transport := service.AsyncImageReferenceTransportPassthroughFallbackLocal
			task := &service.AsyncImageTask{
				TaskID: "asyncimg_gemini_budget", Version: 1, Platform: service.PlatformGemini,
				Model: requestModel, Status: service.AsyncImageTaskStatusInvoking,
				RequestType: service.AsyncImageRequestTypeImageToImage, RequestPayload: []byte("encrypted request"),
				ReferenceTransport: &transport,
			}
			repo := &generatedWorkerResultRepo{task: task}
			worker.tasks = service.NewAsyncImageTaskService(repo)
			disposition := worker.handleAsyncImageUpstreamFailure(context.Background(), c.Request.Context(), task, testAsyncImageRetryConfig(), recorder.Code, recorder.Body.Bytes(), recorder.Header())
			require.False(t, disposition.requeue, "exhausted account switching must not restart worker reference retries")
			require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
			require.Zero(t, repo.task.RetryCount)
			require.Empty(t, repo.task.RequestPayload)
			require.Len(t, repo.transitions, 1)
			require.Equal(t, "task_failed", repo.transitions[0].EventType)
			require.False(t, repo.transitions[0].IncrementReferenceRetry)
			require.False(t, repo.transitions[0].IncrementUpstreamRetry)
			require.False(t, repo.transitions[0].IncrementCapacityRetry)
			require.Equal(t, tt.wantCalls, upstream.accounts)
		})
	}
}
