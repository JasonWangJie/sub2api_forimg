//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageRetryableRejectionFourServicePaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{PlatformOpenAI, PlatformGemini} {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, message := range []string{
				"reference image exceeds limit: 20 MB limit exceeded",
				"Provider API error: fileData requires a user YouTube reference or a gateway file_id (request id: changing)",
				"The prompt or input images could not be processed. Adjust the request and try again.",
			} {
				for _, mode := range []string{"async", "sync", "admin", "stop"} {
					t.Run(platform+"/"+accountType+"/"+message+"/"+mode, func(t *testing.T) {
						ctx := context.Background()
						if mode != "sync" {
							ctx = WithAsyncImageAccountAttemptCapture(ctx, &AsyncImageAccountAttemptCapture{})
							ctx = WithGeminiAsyncImageGeneration(ctx)
							ctx = WithAsyncImageInputReferenceTask(ctx, true)
						}
						text := message
						if mode == "stop" {
							text += " Do not automatically regenerate."
						}
						errorBody, marshalErr := json.Marshal(map[string]any{"error": map[string]any{
							"type": "invalid_request_error", "code": "invalid_image_request", "message": text,
						}})
						require.NoError(t, marshalErr)
						body := []byte(`{"model":"gpt-image-2","prompt":"draw","size":"1024x1024"}`)
						path := "/v1/images/generations"
						if platform == PlatformGemini {
							body = []byte(`{"model":"gemini-3-pro-image-preview","messages":[{"role":"user","content":"draw"}]}`)
							path = "/v1/chat/completions"
						}
						recorder := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(recorder)
						c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)).WithContext(ctx)
						c.Request.Header.Set("Content-Type", "application/json")
						if mode == "admin" {
							status, custom := 409, "route disabled"
							BindErrorPassthroughService(c, &ErrorPassthroughService{localCache: []*cachedPassthroughRule{{
								ErrorPassthroughRule: &model.ErrorPassthroughRule{
									ID: 1, Enabled: true, Platforms: []string{platform}, MatchMode: model.MatchModeAny,
									ErrorCodes: []int{400}, ResponseCode: &status, CustomMessage: &custom,
								}, lowerPlatforms: []string{platform}, errorCodeSet: map[int]struct{}{400: {}},
							}}})
						}
						account := &Account{ID: 117, Platform: platform, Type: accountType, Concurrency: 1, Credentials: map[string]any{
							"api_key": "test", "access_token": "test", "project_id": "project-1", "base_url": "https://images.example/v1",
							"pool_mode": true, "pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []any{400},
						}}
						upstreamBody := string(errorBody)
						if platform == PlatformGemini && accountType == AccountTypeOAuth {
							upstreamBody = `{"response":` + upstreamBody + `}`
						}
						response := &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"req-rejection"}, "Retry-After": {"19"}}, Body: io.NopCloser(strings.NewReader(upstreamBody))}
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
						var failure *UpstreamFailoverError
						if mode == "async" {
							require.ErrorAs(t, err, &failure)
							require.True(t, failure.IsAsyncImageRetryableRejection())
							require.False(t, failure.RetryableOnSameAccount)
							require.Equal(t, "invalid_image_request", failure.ProviderErrorCode)
							require.Equal(t, "req-rejection", failure.UpstreamRequestID)
							require.Equal(t, "19", failure.ResponseHeaders.Get("Retry-After"))
							require.Empty(t, recorder.Body.Bytes())
						} else {
							require.NotErrorAs(t, err, &failure)
							if mode == "admin" {
								require.Equal(t, 409, recorder.Code)
							}
						}
					})
				}
			}
		}
	}
}
