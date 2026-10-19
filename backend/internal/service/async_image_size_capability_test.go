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

const imageSizeCapabilityTestMessage = "No eligible upstream image provider supports the requested image size or resolution tier. Adjust size/resolution or select a compatible route."

func imageSizeCapabilityTestBody(message string) string {
	return fmt.Sprintf(`{"code":"image_size_not_supported","details":null,"error":{"code":"image_size_not_supported","message":%q,"param":"size","type":"invalid_request_error"},"failure_category":"fixed_capability_mismatch","message":%q,"public_message":%q,"status_code":422,"task_id":null,"trace_id":"trace_imgsync_test","upstream":null}`, message, message, message)
}

func TestAsyncImageSizeCapabilityMismatchRequiresDefiniteProviderSelectionFailure(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{name: "production envelope", status: 422, body: imageSizeCapabilityTestBody(imageSizeCapabilityTestMessage), want: true},
		{name: "message only", status: 422, body: fmt.Sprintf(`{"message":%q}`, imageSizeCapabilityTestMessage), want: true},
		{name: "normalized whitespace and punctuation", status: 422, body: imageSizeCapabilityTestBody(" \n" + strings.ToUpper(strings.TrimSuffix(imageSizeCapabilityTestMessage, ".")) + "。 \t"), want: true},
		{name: "wrapped message", status: 422, body: fmt.Sprintf(`{"error":{"message":%q}}`, imageSizeCapabilityTestBody(imageSizeCapabilityTestMessage)), want: true},
		{name: "other status", status: 400, body: imageSizeCapabilityTestBody(imageSizeCapabilityTestMessage)},
		{name: "generic validation", status: 422, body: `{"error":{"message":"Invalid request"}}`},
		{name: "invalid size with same code and category", status: 422, body: imageSizeCapabilityTestBody("Invalid size: expected 1024x1024")},
		{name: "explicit no regeneration", status: 422, body: imageSizeCapabilityTestBody(imageSizeCapabilityTestMessage + " Do not automatically regenerate.")},
		{name: "content policy", status: 422, body: imageSizeCapabilityTestBody(imageSizeCapabilityTestMessage + " Request rejected by content policy.")},
		{name: "reference transport", status: 422, body: imageSizeCapabilityTestBody("image_url fetch failed: " + imageSizeCapabilityTestMessage)},
		{name: "unknown result", status: 422, body: imageSizeCapabilityTestBody(imageSizeCapabilityTestMessage + " Generation may already have been charged.")},
		{name: "short fragment", status: 422, body: `{"error":{"message":"image size or resolution tier is unsupported"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isAsyncImageSizeCapabilityMismatch(tt.status, []byte(tt.body)))
		})
	}
}

func TestOpenAIImagesAsyncSizeCapability422RetryPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name         string
		message      string
		async        bool
		wantFailover bool
	}{
		{name: "async known capability", message: imageSizeCapabilityTestMessage, async: true, wantFailover: true},
		{name: "sync stays terminal", message: imageSizeCapabilityTestMessage},
		{name: "invalid size stays terminal", message: "Invalid size: expected 1024x1024", async: true},
		{name: "no regeneration stays terminal", message: imageSizeCapabilityTestMessage + " Do not automatically regenerate.", async: true},
		{name: "safety refusal stays terminal", message: imageSizeCapabilityTestMessage + " Request rejected by safety policy.", async: true},
	}
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		t.Run(accountType, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					ctx := context.Background()
					if tt.async {
						ctx = WithAsyncImageAccountAttemptCapture(ctx, &AsyncImageAccountAttemptCapture{})
					}
					body := []byte(`{"model":"gpt-image-2.5-flare","prompt":"draw a cat","size":"2048x1152","response_format":"b64_json"}`)
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body)).WithContext(ctx)
					c.Request.Header.Set("Content-Type", "application/json")
					errorBody := imageSizeCapabilityTestBody(tt.message)
					svc := &OpenAIGatewayService{
						cfg: &config.Config{},
						httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
							StatusCode: http.StatusUnprocessableEntity,
							Header: http.Header{
								"Content-Type": []string{"application/json"},
								"X-Request-Id": []string{"req-size-capability"},
								"Retry-After":  []string{"15"},
							},
							Body: io.NopCloser(strings.NewReader(errorBody)),
						}},
					}
					parsed, err := svc.ParseOpenAIImagesRequest(c, body)
					require.NoError(t, err)
					account := &Account{
						ID: 101, Name: "size-limited-image-account", Platform: PlatformOpenAI, Type: accountType,
						Credentials: map[string]any{
							"api_key": "test-api-key", "access_token": "test-access-token",
							"base_url": "https://image-upstream.example/v1", "pool_mode": true,
							"pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []int{422},
						},
					}

					result, err := svc.ForwardImages(ctx, c, account, body, parsed, "")
					require.Nil(t, result)
					if tt.wantFailover {
						var failoverErr *UpstreamFailoverError
						require.ErrorAs(t, err, &failoverErr)
						require.True(t, failoverErr.IsAsyncImageSizeCapabilityMismatch())
						require.Equal(t, http.StatusUnprocessableEntity, failoverErr.StatusCode)
						require.Equal(t, errorBody, string(failoverErr.ResponseBody))
						require.Equal(t, "req-size-capability", failoverErr.ResponseHeaders.Get("x-request-id"))
						require.Equal(t, "15", failoverErr.ResponseHeaders.Get("Retry-After"))
						require.Equal(t, "image_size_not_supported", failoverErr.ProviderErrorCode)
						require.Equal(t, imageSizeCapabilityTestMessage, failoverErr.ProviderErrorMessage)
						require.Equal(t, NextAccountRetry, failoverErr.NextAccountAction)
						require.False(t, failoverErr.RetryableOnSameAccount, "fixed capability must switch accounts even when a pool retries HTTP 422")
						require.Empty(t, recorder.Body.Bytes(), "the handler must retain the opportunity to switch accounts")
					} else {
						var upstreamErr *OpenAIImagesUpstreamError
						require.ErrorAs(t, err, &upstreamErr)
						require.Equal(t, http.StatusUnprocessableEntity, upstreamErr.StatusCode)
						require.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
						require.Equal(t, "image_size_not_supported", gjson.Get(recorder.Body.String(), "error.code").String())
						require.Equal(t, tt.message, gjson.Get(recorder.Body.String(), "error.message").String())
					}
				})
			}
		})
	}
}

func TestGeminiAsyncSizeCapability422RetryPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		t.Run(accountType, func(t *testing.T) {
			for _, mode := range []string{"async known capability", "sync known capability", "async invalid size", "async no regeneration"} {
				t.Run(mode, func(t *testing.T) {
					ctx := context.Background()
					if strings.HasPrefix(mode, "async") {
						ctx = WithGeminiAsyncImageGeneration(ctx)
					}
					message := imageSizeCapabilityTestMessage
					if mode == "async invalid size" {
						message = "Invalid size: expected 1024x1024"
					} else if mode == "async no regeneration" {
						message += " Do not automatically regenerate."
					}
					errorBody := imageSizeCapabilityTestBody(message)
					upstreamBody := errorBody
					if accountType == AccountTypeOAuth {
						upstreamBody = `{"response":` + upstreamBody + `}`
					}
					upstream := &geminiCompatHTTPUpstreamStub{response: &http.Response{
						StatusCode: http.StatusUnprocessableEntity,
						Header: http.Header{
							"Content-Type":      []string{"application/json"},
							"X-Goog-Request-Id": []string{"req-gemini-size-capability"},
						},
						Body: io.NopCloser(strings.NewReader(upstreamBody)),
					}}
					svc := &GeminiMessagesCompatService{tokenProvider: &GeminiTokenProvider{}, httpUpstream: upstream, cfg: &config.Config{}}
					account := &Account{
						ID: 102, Platform: PlatformGemini, Type: accountType, Concurrency: 1,
						Credentials: map[string]any{
							"api_key": "test-api-key", "access_token": "test-access-token", "project_id": "project-1",
							"pool_mode": true, "pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []int{422},
						},
					}
					body := []byte(`{"model":"gemini-3-pro-image-preview","messages":[{"role":"user","content":"draw a cat"}]}`)
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)).WithContext(ctx)

					result, err := svc.ForwardAsChatCompletions(ctx, c, account, body)
					require.Nil(t, result)
					require.Equal(t, 1, upstream.calls)
					var failoverErr *UpstreamFailoverError
					if mode == "async known capability" {
						require.ErrorAs(t, err, &failoverErr)
						require.True(t, failoverErr.IsAsyncImageSizeCapabilityMismatch())
						require.Equal(t, http.StatusUnprocessableEntity, failoverErr.StatusCode)
						require.JSONEq(t, errorBody, string(failoverErr.ResponseBody))
						require.Equal(t, "image_size_not_supported", failoverErr.ProviderErrorCode)
						require.Equal(t, "req-gemini-size-capability", failoverErr.UpstreamRequestID)
						require.False(t, failoverErr.RetryableOnSameAccount)
						require.Empty(t, recorder.Body.Bytes())
					} else {
						require.Error(t, err)
						require.NotErrorAs(t, err, &failoverErr)
						if strings.HasPrefix(mode, "async") {
							require.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
							require.Contains(t, recorder.Body.String(), message)
						} else {
							require.Equal(t, http.StatusBadGateway, recorder.Code)
							require.Equal(t, imageSizeCapabilityTestMessage, gjson.Get(recorder.Body.String(), "error.message").String())
						}
					}
				})
			}
		})
	}
}

func TestAsyncImageSizeCapability422HonorsPassthroughBeforeSwitching(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{PlatformOpenAI, PlatformGemini} {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			t.Run(platform+"/"+accountType, func(t *testing.T) {
				ctx := WithAsyncImageAccountAttemptCapture(context.Background(), &AsyncImageAccountAttemptCapture{})
				ctx = WithGeminiAsyncImageGeneration(ctx)
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
				status := http.StatusForbidden
				message := "Image route disabled by administrator"
				BindErrorPassthroughService(c, &ErrorPassthroughService{localCache: []*cachedPassthroughRule{{
					ErrorPassthroughRule: &model.ErrorPassthroughRule{
						ID: 1, Enabled: true, Platforms: []string{platform}, MatchMode: model.MatchModeAny,
						ErrorCodes: []int{422}, ResponseCode: &status, CustomMessage: &message,
					},
					lowerPlatforms: []string{platform}, errorCodeSet: map[int]struct{}{422: {}},
				}}})
				account := &Account{
					ID: 103, Platform: platform, Type: accountType, Concurrency: 1,
					Credentials: map[string]any{
						"api_key": "test-api-key", "access_token": "test-access-token", "project_id": "project-1",
						"base_url": "https://image-upstream.example/v1",
					},
				}
				response := &http.Response{
					StatusCode: http.StatusUnprocessableEntity,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(imageSizeCapabilityTestBody(imageSizeCapabilityTestMessage))),
				}
				var err error
				if platform == PlatformOpenAI {
					svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: &httpUpstreamRecorder{resp: response}}
					var parsed *OpenAIImagesRequest
					parsed, err = svc.ParseOpenAIImagesRequest(c, body)
					require.NoError(t, err)
					_, err = svc.ForwardImages(ctx, c, account, body, parsed, "")
				} else {
					svc := &GeminiMessagesCompatService{tokenProvider: &GeminiTokenProvider{}, cfg: &config.Config{}, httpUpstream: &geminiCompatHTTPUpstreamStub{response: response}}
					_, err = svc.ForwardAsChatCompletions(ctx, c, account, body)
				}
				require.Error(t, err)
				var failoverErr *UpstreamFailoverError
				require.NotErrorAs(t, err, &failoverErr)
				require.Equal(t, status, recorder.Code)
				require.Equal(t, message, gjson.Get(recorder.Body.String(), "error.message").String())
			})
		}
	}
}
