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

const asyncServiceTimeout400TestMessage = "service timeout, please try again later."

func asyncServiceTimeout400TestBody(message string) string {
	return fmt.Sprintf(`{"error":{"type":"upstream_error","code":400,"message":%q}}`, message)
}

func TestAsyncImageServiceTimeout400RequiresCompleteKnownResponse(t *testing.T) {
	for _, test := range []struct {
		name, message string
		status        int
		want          bool
	}{
		{"complete known message", asyncServiceTimeout400TestMessage, 400, true},
		{"normalized message", " \nSERVICE TIMEOUT,\tPLEASE TRY AGAIN LATER。\t ", 400, true},
		{"other status", asyncServiceTimeout400TestMessage, 422, false},
		{"ambiguous status", asyncServiceTimeout400TestMessage, 502, false},
		{"missing retry instruction", "service timeout", 400, false},
		{"other timeout", "image generation timed out, please try again later.", 400, false},
		{"appended no replay", asyncServiceTimeout400TestMessage + " Do not automatically regenerate.", 400, false},
		{"appended safety refusal", asyncServiceTimeout400TestMessage + " Rejected by safety policy.", 400, false},
		{"appended unknown outcome", asyncServiceTimeout400TestMessage + " Generation may already have been charged.", 400, false},
		{"output prefix", "Generated image download failed: " + asyncServiceTimeout400TestMessage, 400, false},
		{"suffix beyond diagnostic limit", asyncServiceTimeout400TestMessage + strings.Repeat(" ", 600) + " Do not retry.", 400, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, IsAsyncImageServiceTimeout400AccountFailure(test.status, test.message))
			require.Equal(t, test.want, isAsyncImageServiceTimeout400(test.status, []byte(asyncServiceTimeout400TestBody(test.message))))
		})
	}
	inner := asyncServiceTimeout400TestBody(asyncServiceTimeout400TestMessage)
	wrapped := fmt.Sprintf(`{"error":{"message":%q}}`, inner)
	require.True(t, isAsyncImageServiceTimeout400(400, []byte(wrapped)))
}

func TestAsyncImageServiceTimeout400KeepsOriginalProviderDiagnostics(t *testing.T) {
	for _, body := range []string{
		asyncServiceTimeout400TestBody(asyncServiceTimeout400TestMessage),
		fmt.Sprintf(`{"error":{"status":"UNKNOWN","code":400,"message":%q}}`, asyncServiceTimeout400TestMessage),
	} {
		headers := http.Header{"X-Request-Id": []string{"req-service-timeout"}, "Retry-After": []string{"19"}}
		failure := newAsyncImageServiceTimeout400FailoverError([]byte(body), headers)
		require.True(t, failure.IsAsyncImageServiceTimeout400())
		require.Equal(t, 400, failure.StatusCode)
		require.Equal(t, []byte(body), failure.ResponseBody)
		_, expectedCode, _ := geminiAsyncUpstreamDiagnostics([]byte(body), "")
		require.Equal(t, expectedCode, failure.ProviderErrorCode)
		require.Equal(t, asyncServiceTimeout400TestMessage, failure.ProviderErrorMessage)
		require.Equal(t, "req-service-timeout", failure.UpstreamRequestID)
		require.Equal(t, "19", failure.ResponseHeaders.Get("Retry-After"))
		require.Equal(t, AsyncImageServiceTimeout400Reason, failure.Reason)
		require.Equal(t, GatewayFailureReason(AsyncImageServiceTimeout400Code), failure.Reason)
		require.Equal(t, NextAccountRetry, failure.NextAccountAction)
		require.False(t, failure.RetryableOnSameAccount)
		require.True(t, IsAsyncImageServiceTimeout400AccountFailure(failure.StatusCode, failure.ProviderErrorMessage))
		headers.Set("Retry-After", "99")
		require.Equal(t, "19", failure.ResponseHeaders.Get("Retry-After"))
	}
	var empty *UpstreamFailoverError
	require.False(t, empty.IsAsyncImageServiceTimeout400())
}

func TestAsyncImageServiceTimeout400FourServicePathsPreserveBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{PlatformOpenAI, PlatformGemini} {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, mode := range []string{"async", "sync", "admin passthrough", "no replay", "other message"} {
				t.Run(platform+"/"+accountType+"/"+mode, func(t *testing.T) {
					ctx := context.Background()
					if mode != "sync" {
						ctx = WithAsyncImageAccountAttemptCapture(ctx, &AsyncImageAccountAttemptCapture{})
						ctx = WithGeminiAsyncImageGeneration(ctx)
					}
					message := asyncServiceTimeout400TestMessage
					if mode == "no replay" {
						message += " Do not automatically regenerate."
					} else if mode == "other message" {
						message = "some other upstream timeout"
					}
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
						status, custom := http.StatusConflict, "service-timeout route disabled"
						BindErrorPassthroughService(c, &ErrorPassthroughService{localCache: []*cachedPassthroughRule{{
							ErrorPassthroughRule: &model.ErrorPassthroughRule{
								ID: 1, Enabled: true, Platforms: []string{platform}, MatchMode: model.MatchModeAny,
								ErrorCodes: []int{400}, ResponseCode: &status, CustomMessage: &custom,
							}, lowerPlatforms: []string{platform}, errorCodeSet: map[int]struct{}{400: {}},
						}}})
					}
					account := &Account{ID: 116, Platform: platform, Type: accountType, Concurrency: 1, Credentials: map[string]any{
						"api_key": "test", "access_token": "test", "project_id": "project-1", "base_url": "https://images.example/v1",
						"pool_mode": true, "pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []any{400},
					}}
					errorBody := asyncServiceTimeout400TestBody(message)
					upstreamBody := errorBody
					if platform == PlatformGemini && accountType == AccountTypeOAuth {
						upstreamBody = `{"response":` + upstreamBody + `}`
					}
					response := &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"req-service-timeout"}, "Retry-After": []string{"19"}}, Body: io.NopCloser(strings.NewReader(upstreamBody))}
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
						require.True(t, failure.IsAsyncImageServiceTimeout400())
						require.Equal(t, "400", failure.ProviderErrorCode)
						require.Equal(t, "req-service-timeout", failure.UpstreamRequestID)
						require.Equal(t, "19", failure.ResponseHeaders.Get("Retry-After"))
						require.JSONEq(t, errorBody, string(failure.ResponseBody))
						require.False(t, failure.RetryableOnSameAccount)
						require.Empty(t, recorder.Body.Bytes())
					} else {
						require.NotErrorAs(t, err, &failure)
						if mode == "admin passthrough" {
							require.Equal(t, http.StatusConflict, recorder.Code)
							require.Equal(t, "service-timeout route disabled", gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
						}
					}
				})
			}
		}
	}
}
