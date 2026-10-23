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
	"github.com/tidwall/gjson"
)

const combinedCapabilityTestMessage = "No eligible upstream image provider supports the requested capability combination. No eligible upstream image provider supports the requested model. Select a supported model or a compatible route. No eligible upstream image provider supports the requested image size or resolution tier. Adjust size/resolution or select a compatible route."

func combinedCapabilityTestEnvelope(message string, reasons any) map[string]any {
	return map[string]any{
		"code": "image_request_capability_not_supported", "status_code": 422,
		"failure_category": "fixed_capability_mismatch",
		"error": map[string]any{
			"type": "invalid_request_error", "code": "image_request_capability_not_supported", "message": message,
		},
		"details": map[string]any{"capability_errors": reasons},
	}
}

func combinedCapabilityTestBody(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	require.NoError(t, err)
	return body
}

func TestAsyncImageCombinedCapabilityRequiresKnownMessageAndStructuredReasons(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		mutate func(map[string]any)
		wrap   bool
		want   bool
	}{
		{name: "production combination", status: 422, want: true},
		{name: "reordered reasons", status: 422, mutate: func(e map[string]any) {
			e["details"] = map[string]any{"capability_errors": []string{"image_size_not_supported", "image_model_not_supported"}}
		}, want: true},
		{name: "nested details and code", status: 422, mutate: func(e map[string]any) {
			e["error"].(map[string]any)["details"] = e["details"]
			delete(e, "details")
			delete(e, "code")
		}, want: true},
		{name: "root code only", status: 422, mutate: func(e map[string]any) { delete(e["error"].(map[string]any), "code") }, want: true},
		{name: "wrapped complete envelope", status: 422, wrap: true, want: true},
		{name: "normalized complete message", status: 422, mutate: func(e map[string]any) {
			e["error"].(map[string]any)["message"] = " \n" + strings.ToUpper(strings.ReplaceAll(strings.TrimSuffix(combinedCapabilityTestMessage, "."), ". ", ".\n\t")) + "。 \t"
		}, want: true},
		{name: "other status", status: 400},
		{name: "missing structured reasons", status: 422, mutate: func(e map[string]any) { delete(e, "details") }},
		{name: "missing code", status: 422, mutate: func(e map[string]any) {
			delete(e, "code")
			delete(e["error"].(map[string]any), "code")
		}},
		{name: "conflicting code", status: 422, mutate: func(e map[string]any) { e["code"] = "invalid_size" }},
		{name: "wrong nested code", status: 422, mutate: func(e map[string]any) { e["error"].(map[string]any)["code"] = "invalid_size" }},
		{name: "codes without complete message", status: 422, mutate: func(e map[string]any) { e["error"].(map[string]any)["message"] = "Unsupported capability combination" }},
		{name: "extra safety reason", status: 422, mutate: func(e map[string]any) {
			e["details"] = map[string]any{"capability_errors": []string{"image_model_not_supported", "image_size_not_supported", "content_policy"}}
		}},
		{name: "unknown reason", status: 422, mutate: func(e map[string]any) {
			e["details"] = map[string]any{"capability_errors": []string{"unknown_capability", "image_size_not_supported"}}
		}},
		{name: "one reason", status: 422, mutate: func(e map[string]any) {
			e["details"] = map[string]any{"capability_errors": []string{"image_model_not_supported"}}
		}},
		{name: "duplicate reasons", status: 422, mutate: func(e map[string]any) {
			e["details"] = map[string]any{"capability_errors": []string{"image_model_not_supported", "image_model_not_supported"}}
		}},
		{name: "reasons are string", status: 422, mutate: func(e map[string]any) {
			e["details"] = map[string]any{"capability_errors": "image_model_not_supported,image_size_not_supported"}
		}},
		{name: "conflicting details", status: 422, mutate: func(e map[string]any) {
			e["error"].(map[string]any)["details"] = map[string]any{"capability_errors": []string{"content_policy", "image_size_not_supported"}}
		}},
		{name: "explicit no regeneration", status: 422, mutate: func(e map[string]any) {
			e["error"].(map[string]any)["message"] = combinedCapabilityTestMessage + " Do not automatically regenerate."
		}},
		{name: "unknown execution", status: 422, mutate: func(e map[string]any) {
			e["error"].(map[string]any)["message"] = combinedCapabilityTestMessage + " Generation may already have been charged."
		}},
		{name: "safety refusal", status: 422, mutate: func(e map[string]any) {
			e["error"].(map[string]any)["message"] = combinedCapabilityTestMessage + " Request rejected by safety policy."
		}},
		{name: "reference fetch", status: 422, mutate: func(e map[string]any) {
			e["error"].(map[string]any)["message"] = "image_url fetch failed: " + combinedCapabilityTestMessage
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			envelope := combinedCapabilityTestEnvelope(combinedCapabilityTestMessage, []string{"image_model_not_supported", "image_size_not_supported"})
			if test.mutate != nil {
				test.mutate(envelope)
			}
			body := combinedCapabilityTestBody(t, envelope)
			if test.wrap {
				body = combinedCapabilityTestBody(t, map[string]any{"error": map[string]any{"message": string(body)}})
			}
			require.Equal(t, test.want, isAsyncImageSizeCapabilityMismatch(test.status, body))
		})
	}
}

func TestAsyncImageCombinedCapabilityForwardingPreservesPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{PlatformOpenAI, PlatformGemini} {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, mode := range []string{"async", "sync", "admin passthrough", "no regeneration"} {
				t.Run(platform+"/"+accountType+"/"+mode, func(t *testing.T) {
					ctx := context.Background()
					if mode != "sync" {
						ctx = WithAsyncImageAccountAttemptCapture(ctx, &AsyncImageAccountAttemptCapture{})
						ctx = WithGeminiAsyncImageGeneration(ctx)
					}
					message := combinedCapabilityTestMessage
					if mode == "no regeneration" {
						message += " Do not automatically regenerate."
					}
					errorBody := combinedCapabilityTestBody(t, combinedCapabilityTestEnvelope(message, []string{"image_model_not_supported", "image_size_not_supported"}))
					requestBody := []byte(`{"model":"gpt-image-2.5-flare","prompt":"draw a cat","size":"2048x1152","response_format":"b64_json"}`)
					path := "/v1/images/generations"
					if platform == PlatformGemini {
						requestBody = []byte(`{"model":"gemini-3-pro-image-preview","messages":[{"role":"user","content":"draw a cat"}]}`)
						path = "/v1/chat/completions"
					}
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(requestBody)).WithContext(ctx)
					c.Request.Header.Set("Content-Type", "application/json")
					if mode == "admin passthrough" {
						status, message := http.StatusForbidden, "Route disabled by administrator"
						BindErrorPassthroughService(c, &ErrorPassthroughService{localCache: []*cachedPassthroughRule{{
							ErrorPassthroughRule: &model.ErrorPassthroughRule{
								ID: 1, Enabled: true, Platforms: []string{platform}, MatchMode: model.MatchModeAny,
								ErrorCodes: []int{422}, ResponseCode: &status, CustomMessage: &message,
							},
							lowerPlatforms: []string{platform}, errorCodeSet: map[int]struct{}{422: {}},
						}}})
					}
					account := &Account{
						ID: 105, Platform: platform, Type: accountType, Concurrency: 1,
						Credentials: map[string]any{
							"api_key": "test-api-key", "access_token": "test-access-token", "project_id": "project-1",
							"base_url": "https://images.example/v1", "pool_mode": true,
							"pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []int{422},
						},
					}
					upstreamBody := errorBody
					if platform == PlatformGemini && accountType == AccountTypeOAuth {
						upstreamBody = append(append([]byte(`{"response":`), errorBody...), '}')
					}
					response := &http.Response{
						StatusCode: 422,
						Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"req-combined-capability"}, "Retry-After": []string{"15"}},
						Body:       io.NopCloser(bytes.NewReader(upstreamBody)),
					}
					var err error
					if platform == PlatformOpenAI {
						upstream := &httpUpstreamRecorder{resp: response}
						svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
						parsed, parseErr := svc.ParseOpenAIImagesRequest(c, requestBody)
						require.NoError(t, parseErr)
						_, err = svc.ForwardImages(ctx, c, account, requestBody, parsed, "")
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
						_, err = svc.ForwardAsChatCompletions(ctx, c, account, requestBody)
						require.Equal(t, 1, upstream.calls)
					}
					require.Error(t, err)
					var failoverErr *UpstreamFailoverError
					if mode == "async" {
						require.ErrorAs(t, err, &failoverErr)
						require.True(t, failoverErr.IsAsyncImageSizeCapabilityMismatch())
						require.Equal(t, 422, failoverErr.StatusCode)
						require.Equal(t, "image_request_capability_not_supported", failoverErr.ProviderErrorCode)
						require.Equal(t, combinedCapabilityTestMessage, failoverErr.ProviderErrorMessage)
						require.Equal(t, "req-combined-capability", failoverErr.UpstreamRequestID)
						require.Equal(t, "15", failoverErr.ResponseHeaders.Get("Retry-After"))
						require.False(t, failoverErr.RetryableOnSameAccount)
						require.Equal(t, NextAccountRetry, failoverErr.NextAccountAction)
						require.JSONEq(t, string(errorBody), string(failoverErr.ResponseBody))
						require.Empty(t, recorder.Body.Bytes())
					} else {
						require.NotErrorAs(t, err, &failoverErr)
						if mode == "admin passthrough" {
							require.Equal(t, http.StatusForbidden, recorder.Code)
							require.Equal(t, "Route disabled by administrator", gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
						} else if platform == PlatformOpenAI || mode != "sync" {
							require.Equal(t, 422, recorder.Code)
						}
					}
				})
			}
		}
	}
}
