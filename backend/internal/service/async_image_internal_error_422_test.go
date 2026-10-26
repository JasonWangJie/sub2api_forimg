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

const asyncInternal422TestMessage = "Internal error encountered."

func asyncInternal422TestBody(shape, message string) string {
	switch shape {
	case "code":
		return fmt.Sprintf(`{"error":{"code":"UNKNOWN","message":%q}}`, message)
	case "wrapped":
		inner := asyncInternal422TestBody("status", message)
		return fmt.Sprintf(`{"error":{"message":%q}}`, inner)
	case "other code":
		return fmt.Sprintf(`{"error":{"status":"INVALID_ARGUMENT","code":"UNKNOWN","message":%q}}`, message)
	default:
		return fmt.Sprintf(`{"error":{"status":"UNKNOWN","code":422,"message":%q}}`, message)
	}
}

func TestAsyncImageInternalError422RequiresCompleteMessageAndProviderCode(t *testing.T) {
	for _, test := range []struct {
		name, shape, message string
		status               int
		want                 bool
	}{
		{"provider status", "status", asyncInternal422TestMessage, 422, true},
		{"provider code", "code", asyncInternal422TestMessage, 422, true},
		{"wrapped status", "wrapped", asyncInternal422TestMessage, 422, true},
		{"normalized full message", "code", " \nINTERNAL\tERROR ENCOUNTERED。 \t", 422, true},
		{"wrong HTTP400", "status", asyncInternal422TestMessage, 400, false},
		{"wrong HTTP500", "status", asyncInternal422TestMessage, 500, false},
		{"wrong HTTP502", "status", asyncInternal422TestMessage, 502, false},
		{"status overrides code", "other code", asyncInternal422TestMessage, 422, false},
		{"generic message", "code", "Invalid request", 422, false},
		{"partial message", "code", "Internal error", 422, false},
		{"prefix", "code", "Generation failed: " + asyncInternal422TestMessage, 422, false},
		{"no replay", "code", asyncInternal422TestMessage + " Do not automatically regenerate.", 422, false},
		{"content refusal", "code", asyncInternal422TestMessage + " Rejected by content policy.", 422, false},
		{"unknown generation", "code", asyncInternal422TestMessage + " Generation may already have been charged.", 422, false},
		{"suffix beyond diagnostics limit", "code", asyncInternal422TestMessage + strings.Repeat(" ", 600) + " Do not retry.", 422, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(asyncInternal422TestBody(test.shape, test.message))
			require.Equal(t, test.want, isAsyncImageInternalError422(test.status, body))
		})
	}
	for _, body := range []string{
		fmt.Sprintf(`{"error":{"message":%q}}`, asyncInternal422TestMessage),
		fmt.Sprintf(`{"error":{"status":"unknown","message":%q}}`, asyncInternal422TestMessage),
		fmt.Sprintf(`{"error":{"code":422,"message":%q}}`, asyncInternal422TestMessage),
	} {
		require.False(t, isAsyncImageInternalError422(422, []byte(body)), body)
	}
	require.False(t, IsAsyncImageInternalError422AccountFailure(0, "UNKNOWN", asyncInternal422TestMessage))
	require.False(t, IsAsyncImageInternalError422AccountFailure(422, "", asyncInternal422TestMessage))
}

func TestAsyncImageInternalError422PreservesProviderDiagnosticsAndReason(t *testing.T) {
	for _, shape := range []string{"status", "code", "wrapped"} {
		t.Run(shape, func(t *testing.T) {
			body := []byte(asyncInternal422TestBody(shape, asyncInternal422TestMessage))
			headers := http.Header{"X-Goog-Request-Id": []string{"req-internal-422"}, "Retry-After": []string{"12"}}
			failure := newAsyncImageInternalError422FailoverError(body, headers)
			require.True(t, failure.IsAsyncImageInternalError422())
			require.Equal(t, AsyncImageInternalError422Reason, failure.Reason)
			require.Equal(t, GatewayFailureReason(AsyncImageInternalError422Code), failure.Reason)
			require.Equal(t, 422, failure.StatusCode)
			require.Equal(t, body, failure.ResponseBody)
			require.Equal(t, "UNKNOWN", failure.ProviderErrorCode)
			require.Equal(t, asyncInternal422TestMessage, failure.ProviderErrorMessage)
			require.Equal(t, "req-internal-422", failure.UpstreamRequestID)
			require.Equal(t, "12", failure.ResponseHeaders.Get("Retry-After"))
			require.Equal(t, GatewayFailureScopeAccount, failure.Scope)
			require.Equal(t, NextAccountRetry, failure.NextAccountAction)
			require.False(t, failure.RetryableOnSameAccount)
			require.True(t, IsAsyncImageInternalError422AccountFailure(failure.StatusCode, failure.ProviderErrorCode, failure.ProviderErrorMessage))
			headers.Set("Retry-After", "99")
			require.Equal(t, "12", failure.ResponseHeaders.Get("Retry-After"))
		})
	}
	var empty *UpstreamFailoverError
	require.False(t, empty.IsAsyncImageInternalError422())
}

func TestAsyncImageInternalError422FourServicePathsKeepBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{PlatformOpenAI, PlatformGemini} {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, shape := range []string{"status", "code", "wrapped"} {
				for _, mode := range []string{"async", "sync", "admin passthrough", "other code", "no replay"} {
					t.Run(platform+"/"+accountType+"/"+shape+"/"+mode, func(t *testing.T) {
						ctx := context.Background()
						if mode != "sync" {
							ctx = WithAsyncImageAccountAttemptCapture(ctx, &AsyncImageAccountAttemptCapture{})
							ctx = WithGeminiAsyncImageGeneration(ctx)
						}
						message := asyncInternal422TestMessage
						if mode == "no replay" {
							message += " Do not automatically regenerate."
						}
						bodyShape := shape
						if mode == "other code" {
							bodyShape = "other code"
						}
						errorBody := asyncInternal422TestBody(bodyShape, message)
						body := []byte(`{"model":"gpt-image-2.5-flare","prompt":"draw","size":"2048x1152","response_format":"b64_json"}`)
						path := "/v1/images/generations"
						if platform == PlatformGemini {
							body = []byte(`{"model":"gemini-3-pro-image-preview","messages":[{"role":"user","content":"draw"}]}`)
							path = "/v1/chat/completions"
						}
						recorder := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(recorder)
						c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)).WithContext(ctx)
						c.Request.Header.Set("Content-Type", "application/json")
						if mode == "admin passthrough" {
							status, custom := http.StatusConflict, "internal-error route disabled"
							BindErrorPassthroughService(c, &ErrorPassthroughService{localCache: []*cachedPassthroughRule{{
								ErrorPassthroughRule: &model.ErrorPassthroughRule{
									ID: 1, Enabled: true, Platforms: []string{platform}, MatchMode: model.MatchModeAny,
									ErrorCodes: []int{422}, ResponseCode: &status, CustomMessage: &custom,
								}, lowerPlatforms: []string{platform}, errorCodeSet: map[int]struct{}{422: {}},
							}}})
						}
						account := &Account{ID: 114, Platform: platform, Type: accountType, Concurrency: 1, Credentials: map[string]any{
							"api_key": "test", "access_token": "test", "project_id": "project-1", "base_url": "https://images.example/v1",
							"pool_mode": true, "pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []any{422},
						}}
						upstreamBody := errorBody
						if platform == PlatformGemini && accountType == AccountTypeOAuth {
							upstreamBody = `{"response":` + upstreamBody + `}`
						}
						response := &http.Response{StatusCode: 422, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"req-internal-422"}, "Retry-After": []string{"12"}}, Body: io.NopCloser(strings.NewReader(upstreamBody))}
						var err error
						if platform == PlatformOpenAI {
							upstream := &httpUpstreamRecorder{resp: response}
							svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
							parsed, parseErr := svc.ParseOpenAIImagesRequest(c, body)
							require.NoError(t, parseErr)
							_, err = svc.ForwardImages(ctx, c, account, body, parsed, "")
							require.Len(t, upstream.requests, 1)
							modelPath, sizePath := "model", "size"
							if accountType == AccountTypeOAuth {
								modelPath, sizePath = "tools.0.model", "tools.0.size"
							}
							require.Equal(t, "gpt-image-2.5-flare", gjson.GetBytes(upstream.lastBody, modelPath).String())
							require.Equal(t, "2048x1152", gjson.GetBytes(upstream.lastBody, sizePath).String())
						} else {
							upstream := &geminiCompatHTTPUpstreamStub{response: response}
							svc := &GeminiMessagesCompatService{tokenProvider: &GeminiTokenProvider{}, cfg: &config.Config{}, httpUpstream: upstream}
							_, err = svc.ForwardAsChatCompletions(ctx, c, account, body)
							require.Equal(t, 1, upstream.calls)
						}
						require.Error(t, err)
						var failure *UpstreamFailoverError
						if mode == "async" {
							require.ErrorAs(t, err, &failure)
							require.True(t, failure.IsAsyncImageInternalError422())
							require.Equal(t, 422, failure.StatusCode)
							require.Equal(t, "UNKNOWN", failure.ProviderErrorCode)
							require.Equal(t, "req-internal-422", failure.UpstreamRequestID)
							require.Equal(t, "12", failure.ResponseHeaders.Get("Retry-After"))
							require.JSONEq(t, errorBody, string(failure.ResponseBody))
							require.False(t, failure.RetryableOnSameAccount)
							require.Empty(t, recorder.Body.Bytes())
						} else {
							require.NotErrorAs(t, err, &failure)
							if mode == "admin passthrough" {
								require.Equal(t, http.StatusConflict, recorder.Code)
								require.Equal(t, "internal-error route disabled", gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
							}
						}
					})
				}
			}
		}
	}
}
