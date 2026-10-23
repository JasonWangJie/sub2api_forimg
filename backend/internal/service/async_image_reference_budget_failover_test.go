package service

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
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const referenceBudgetTestMessage = "Reference image download exceeded its total time budget for https://references.example/input.png?access_token=private (budgetMs=35000; reason=timeout)"

func referenceBudgetTestBody(message string) string {
	return fmt.Sprintf(`{"error":{"message":%q,"code":"reference_download_error","type":"upstream_error"}}`, message)
}

func TestAsyncImageReferenceBudgetExceededRequiresCompleteKnown400(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		message string
		want    bool
	}{
		{"production message", 400, referenceBudgetTestMessage, true},
		{"normalized whitespace and case", 400, " \n" + strings.ToUpper(referenceBudgetTestMessage) + "\t ", true},
		{"redacted URL", 400, "Reference image download exceeded its total time budget for https://***.com/***/*** (budgetMs=35000; reason=timeout)", true},
		{"redacted diagnostic", 400, "Reference image download exceeded its total time budget for https://*** (budgetMs=35000; reason=timeout)", true},
		{"other positive budget", 400, strings.ReplaceAll(referenceBudgetTestMessage, "35000", "60000"), true},
		{"not 400", 502, referenceBudgetTestMessage, false},
		{"missing URL", 400, "Reference image download exceeded its total time budget (budgetMs=35000; reason=timeout)", false},
		{"HTTP URL", 400, strings.ReplaceAll(referenceBudgetTestMessage, "https://", "http://"), false},
		{"userinfo URL", 400, strings.ReplaceAll(referenceBudgetTestMessage, "https://", "https://user:secret@"), false},
		{"invalid URL escape", 400, strings.ReplaceAll(referenceBudgetTestMessage, "input.png", "%invalid"), false},
		{"zero budget", 400, strings.ReplaceAll(referenceBudgetTestMessage, "35000", "0"), false},
		{"different reason", 400, strings.ReplaceAll(referenceBudgetTestMessage, "reason=timeout", "reason=unknown"), false},
		{"generic timeout", 400, "Reference image download failed: context deadline exceeded", false},
		{"result download", 400, strings.ReplaceAll(referenceBudgetTestMessage, "Reference image", "Generated image"), false},
		{"no regeneration", 400, referenceBudgetTestMessage + " Do not automatically regenerate.", false},
		{"content refusal", 400, referenceBudgetTestMessage + " Request rejected by content policy.", false},
		{"charged unknown", 400, referenceBudgetTestMessage + " Generation may already have been charged.", false},
		{"prefixed arbitrary failure", 400, "generation failed: " + referenceBudgetTestMessage, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsAsyncImageReferenceBudgetExceededMessage(tt.status, tt.message))
			require.Equal(t, tt.want, isAsyncImageReferenceBudgetExceeded(tt.status, []byte(referenceBudgetTestBody(tt.message))))
		})
	}
	wrapped := fmt.Sprintf(`{"error":{"message":%q}}`, referenceBudgetTestBody(referenceBudgetTestMessage))
	require.True(t, isAsyncImageReferenceBudgetExceeded(http.StatusBadRequest, []byte(wrapped)))
}

func TestAsyncImageReferenceBudgetFailoverPreservesDiagnosticsWithoutSourceURL(t *testing.T) {
	message := strings.ReplaceAll(referenceBudgetTestMessage, "input.png", strings.Repeat("sensitive-path", 100))
	body := []byte(referenceBudgetTestBody(message))
	headers := http.Header{"X-Goog-Request-Id": []string{"req-reference-budget"}, "Retry-After": []string{"11"}}
	failover := newAsyncImageReferenceBudgetFailoverError(body, headers)
	require.True(t, failover.IsAsyncImageReferenceBudgetExceeded())
	require.Equal(t, http.StatusBadRequest, failover.StatusCode)
	require.Equal(t, GatewayFailureScopeAccount, failover.Scope)
	require.Equal(t, NextAccountRetry, failover.NextAccountAction)
	require.False(t, failover.RetryableOnSameAccount)
	require.Equal(t, body, failover.ResponseBody)
	require.Equal(t, "reference_download_error", failover.ProviderErrorCode)
	require.Equal(t, "req-reference-budget", failover.UpstreamRequestID)
	require.NotContains(t, failover.ProviderErrorMessage, "sensitive-path")
	require.NotContains(t, failover.ProviderErrorMessage, "private")
	require.True(t, IsAsyncImageReferenceBudgetExceededMessage(400, failover.ProviderErrorMessage))
	headers.Set("Retry-After", "99")
	require.Equal(t, "11", failover.ResponseHeaders.Get("Retry-After"))
	var empty *UpstreamFailoverError
	require.False(t, empty.IsAsyncImageReferenceBudgetExceeded())
}

func TestAsyncImageReferenceBudget400SwitchesWithoutSameAccountRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{PlatformOpenAI, PlatformGemini} {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, mode := range []string{"async", "sync", "admin passthrough", "appended stop", "other reference timeout"} {
				t.Run(platform+"/"+accountType+"/"+mode, func(t *testing.T) {
					ctx := context.Background()
					if mode != "sync" {
						ctx = WithAsyncImageAccountAttemptCapture(ctx, &AsyncImageAccountAttemptCapture{})
						ctx = WithGeminiAsyncImageGeneration(ctx)
					}
					message := referenceBudgetTestMessage
					if mode == "appended stop" {
						message += " Do not automatically regenerate."
					} else if mode == "other reference timeout" {
						message = "Reference image download failed: timeout"
					}
					body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","response_format":"b64_json"}`)
					path := "/v1/images/generations"
					if platform == PlatformGemini {
						body = []byte(`{"model":"gemini-3-pro-image-preview","messages":[{"role":"user","content":"draw a cat"}]}`)
						path = "/v1/chat/completions"
					}
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)).WithContext(ctx)
					c.Request.Header.Set("Content-Type", "application/json")
					if mode == "admin passthrough" {
						status, customMessage := http.StatusForbidden, "Reference route disabled by administrator"
						BindErrorPassthroughService(c, &ErrorPassthroughService{localCache: []*cachedPassthroughRule{{
							ErrorPassthroughRule: &model.ErrorPassthroughRule{
								ID: 1, Enabled: true, Platforms: []string{platform}, MatchMode: model.MatchModeAny,
								ErrorCodes: []int{400}, ResponseCode: &status, CustomMessage: &customMessage,
							},
							lowerPlatforms: []string{platform}, errorCodeSet: map[int]struct{}{400: {}},
						}}})
					}
					account := &Account{
						ID: 109, Platform: platform, Type: accountType, Concurrency: 1,
						Credentials: map[string]any{
							"api_key": "test-api-key", "access_token": "test-access-token", "project_id": "project-1",
							"base_url": "https://image-upstream.example/v1", "pool_mode": true,
							"pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []int{400},
						},
					}
					upstreamBody := referenceBudgetTestBody(message)
					if platform == PlatformGemini && accountType == AccountTypeOAuth {
						upstreamBody = `{"response":` + upstreamBody + `}`
					}
					response := &http.Response{
						StatusCode: http.StatusBadRequest,
						Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"req-budget-400"}, "Retry-After": []string{"13"}},
						Body:       io.NopCloser(strings.NewReader(upstreamBody)),
					}
					var err error
					if platform == PlatformOpenAI {
						upstream := &httpUpstreamRecorder{resp: response}
						svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
						parsed, parseErr := svc.ParseOpenAIImagesRequest(c, body)
						require.NoError(t, parseErr)
						_, err = svc.ForwardImages(ctx, c, account, body, parsed, "")
						require.Len(t, upstream.requests, 1)
					} else {
						upstream := &geminiCompatHTTPUpstreamStub{response: response}
						svc := &GeminiMessagesCompatService{tokenProvider: &GeminiTokenProvider{}, cfg: &config.Config{}, httpUpstream: upstream}
						_, err = svc.ForwardAsChatCompletions(ctx, c, account, body)
						require.Equal(t, 1, upstream.calls)
					}
					require.Error(t, err)
					var failover *UpstreamFailoverError
					if mode == "async" {
						require.ErrorAs(t, err, &failover)
						require.True(t, failover.IsAsyncImageReferenceBudgetExceeded())
						require.False(t, failover.RetryableOnSameAccount, "pool retry 400 must not override direct switching")
						require.Equal(t, "reference_download_error", failover.ProviderErrorCode)
						require.Equal(t, "req-budget-400", failover.UpstreamRequestID)
						require.Equal(t, "13", failover.ResponseHeaders.Get("Retry-After"))
						require.Empty(t, recorder.Body.Bytes())
					} else {
						require.NotErrorAs(t, err, &failover)
						if mode == "admin passthrough" {
							require.Equal(t, http.StatusForbidden, recorder.Code)
							require.Equal(t, "Reference route disabled by administrator", gjson.Get(recorder.Body.String(), "error.message").String())
						}
					}
				})
			}
		}
	}
}
