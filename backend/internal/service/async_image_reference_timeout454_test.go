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

const referenceTimeout454Message = "下载参考图超时，请稍后重试"

func referenceTimeout454Body(t *testing.T, code, message string, detail any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"error": map[string]any{"code": code, "message": message, "details": detail}})
	require.NoError(t, err)
	return body
}

func TestAsyncImageReferenceTimeout454Classification(t *testing.T) {
	base := WithAsyncImageAccountAttemptCapture(context.Background(), &AsyncImageAccountAttemptCapture{})
	base = WithAsyncImageInputReferenceTask(base, true)
	for _, test := range []struct {
		name, code, message string
		status              int
		detail              any
		want                bool
	}{
		{"exact", "asset_download_timeout", referenceTimeout454Message, 454, nil, true},
		{"normalized", " ASSET_DOWNLOAD_TIMEOUT ", "  " + referenceTimeout454Message + "。 \n", 454, nil, true},
		{"other 454", "asset_download_timeout", "服务繁忙，请稍后重试", 454, nil, false},
		{"wrong code", "upstream_error", referenceTimeout454Message, 454, nil, false},
		{"other status", "asset_download_timeout", referenceTimeout454Message, 400, nil, false},
		{"execution unknown", "asset_download_timeout", referenceTimeout454Message, 454, "generation outcome is unknown", false},
		{"charged nested", "asset_download_timeout", referenceTimeout454Message, 454, map[string]any{"warning": "may already have been charged"}, false},
		{"safety nested", "asset_download_timeout", referenceTimeout454Message, 454, []any{map[string]any{"reason": "content policy"}}, false},
		{"embedded stop JSON", "asset_download_timeout", referenceTimeout454Message, 454, `{"warning":"Do not automatically regenerate"}`, false},
		{"appended stop", "asset_download_timeout", referenceTimeout454Message + "; do not retry", 454, nil, false},
		{"unrecognized suffix", "asset_download_timeout", referenceTimeout454Message + "（原因未知）", 454, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, isAsyncImageReferenceAccountFailoverForContext(base, test.status, referenceTimeout454Body(t, test.code, test.message, test.detail)))
		})
	}
	body := referenceTimeout454Body(t, "asset_download_timeout", referenceTimeout454Message, nil)
	for _, ctx := range []context.Context{context.Background(), WithAsyncImageInputReferenceTask(context.Background(), true), WithAsyncImageInputReferenceTask(base, false), WithAsyncImageAccountAttemptCapture(context.Background(), &AsyncImageAccountAttemptCapture{})} {
		require.False(t, isAsyncImageReferenceAccountFailoverForContext(ctx, 454, body))
	}
	failure := newAsyncImageReferenceAccountFailoverErrorWithStatus(454, body, http.Header{"X-Request-Id": []string{"req-454"}})
	require.Equal(t, 454, failure.StatusCode)
	require.Equal(t, "asset_download_timeout", failure.ProviderErrorCode)
	require.Equal(t, referenceTimeout454Message, failure.ProviderErrorMessage)
	require.Equal(t, "req-454", failure.UpstreamRequestID)
	require.True(t, failure.IsAsyncImageReferenceAccountFailover())
	require.True(t, failure.IsAsyncImageBoundedRetryFailure())
	require.False(t, failure.RetryableOnSameAccount)
}

func TestAsyncImageReferenceTimeout454ServicePolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{PlatformOpenAI, PlatformGemini} {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, mode := range []string{"async", "sync", "text task", "missing task flag", "admin passthrough", "stop detail"} {
				t.Run(platform+"/"+accountType+"/"+mode, func(t *testing.T) {
					ctx := context.Background()
					if mode != "sync" {
						ctx = WithAsyncImageAccountAttemptCapture(ctx, &AsyncImageAccountAttemptCapture{})
						ctx = WithGeminiAsyncImageGeneration(ctx)
						if mode != "missing task flag" {
							ctx = WithAsyncImageInputReferenceTask(ctx, mode != "text task")
						}
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
						status, custom := http.StatusConflict, "reference route disabled"
						BindErrorPassthroughService(c, &ErrorPassthroughService{localCache: []*cachedPassthroughRule{{ErrorPassthroughRule: &model.ErrorPassthroughRule{ID: 1, Enabled: true, Platforms: []string{platform}, MatchMode: model.MatchModeAny, ErrorCodes: []int{454}, ResponseCode: &status, CustomMessage: &custom}, lowerPlatforms: []string{platform}, errorCodeSet: map[int]struct{}{454: {}}}}})
					}
					account := &Account{ID: 110, Platform: platform, Type: accountType, Concurrency: 1, Credentials: map[string]any{"api_key": "test", "access_token": "test", "project_id": "project-1", "base_url": "https://images.example/v1", "pool_mode": true, "pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []int{454}}}
					var detail any
					if mode == "stop detail" {
						detail = "Do not automatically regenerate"
					}
					upstreamBody := string(referenceTimeout454Body(t, "asset_download_timeout", referenceTimeout454Message, detail))
					if platform == PlatformGemini && accountType == AccountTypeOAuth {
						upstreamBody = `{"response":` + upstreamBody + `}`
					}
					response := &http.Response{StatusCode: 454, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(upstreamBody))}
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
						require.Equal(t, 454, failure.StatusCode)
						require.True(t, failure.IsAsyncImageReferenceAccountFailover())
						require.False(t, failure.RetryableOnSameAccount)
						require.Empty(t, recorder.Body.Bytes())
					} else if mode == "text task" || mode == "missing task flag" {
						require.ErrorAs(t, err, &failure)
						require.True(t, failure.IsAsyncImageTimeoutRejection())
						require.False(t, failure.IsAsyncImageReferenceAccountFailover())
					} else if mode == "stop detail" {
						require.ErrorAs(t, err, &failure)
						require.True(t, failure.IsAsyncImageTimeoutTerminal())
						require.False(t, failure.ShouldRetryNextAccount())
					} else {
						require.NotErrorAs(t, err, &failure)
						if mode == "admin passthrough" {
							require.Equal(t, http.StatusConflict, recorder.Code)
						}
					}
				})
			}
		}
	}
}
