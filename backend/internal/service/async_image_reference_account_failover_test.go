package service

import (
	"bytes"
	"context"
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

const referenceAccountDirectProxyMessage = "Reference image download failed for https://references.example/input.png?access_token=private (budgetMs=35000; reason=direct_and_proxy_failed; direct=timeout of 15000ms exceeded; proxy=Request failed with status code 502)"
const referenceAccountNetworkMessage = `Error while downloading https://references.example/input.png?access_token=private Get "https://references.example/input.png?access_token=private": dial tcp 203.0.113.4:443: i/o timeout`

func TestAsyncImageReferenceAccountFailoverClassifiesKnownInputFailuresOnly(t *testing.T) {
	for _, test := range []struct {
		name, message string
		status        int
		want          bool
	}{
		{"direct and proxy", referenceAccountDirectProxyMessage, 400, true},
		{"network download", referenceAccountNetworkMessage, 400, true},
		{"colon before Get", strings.Replace(referenceAccountNetworkMessage, " Get ", ": Get ", 1), 400, true},
		{"redacted network", `Error while downloading https://*** Get "https://***": dial tcp ***:443: i/o timeout`, 400, true},
		{"budget compatibility", referenceBudgetTestMessage, 400, true},
		{"ordinary reference", "Reference image download failed: timeout", 400, true},
		{"fileuri reference", "fileUri download failed: upstream unavailable", 400, true},
		{"reference url", "image URL fetch failed: i/o timeout", 400, true},
		{"fetch word order", "reference image fetch failed: context deadline exceeded", 400, true},
		{"not HTTP400", referenceAccountNetworkMessage, 502, false},
		{"504 unknown", referenceAccountDirectProxyMessage, 504, false},
		{"unrelated timeout", "generation request timed out", 400, false},
		{"generated output", "Generated image download failed: i/o timeout", 400, false},
		{"result prefix", "Generated result: " + referenceAccountNetworkMessage, 400, false},
		{"HTTP source", strings.ReplaceAll(referenceAccountNetworkMessage, "https://", "http://"), 400, false},
		{"userinfo source", strings.ReplaceAll(referenceAccountNetworkMessage, "https://", "https://user:secret@"), 400, false},
		{"fragment source", strings.ReplaceAll(referenceAccountNetworkMessage, "access_token=private", "access_token=private#fragment"), 400, false},
		{"different sources", strings.Replace(referenceAccountNetworkMessage, "input.png?access_token=private", "other.png", 1), 400, false},
		{"network unknown execution", referenceAccountNetworkMessage + " Generation may already have been charged.", 400, false},
		{"reference unknown execution", referenceAccountDirectProxyMessage + " Generation may already have been charged.", 400, false},
		{"explicit unknown", referenceAccountDirectProxyMessage + " execution_unknown", 400, false},
		{"no replay", referenceAccountDirectProxyMessage + " Do not automatically regenerate.", 400, false},
		{"content refusal", referenceAccountDirectProxyMessage + " Request rejected by content policy.", 400, false},
		{"permanent proxy", strings.Replace(referenceAccountDirectProxyMessage, "status code 502", "status code 403", 1), 400, false},
		{"not found", "Reference image download failed: HTTP 404", 400, false},
		{"unauthorized", "reference image fetch failed: HTTP 401", 400, false},
		{"too large", "Reference image download failed: image is too large", 400, false},
		{"bad format", "Reference image download failed: unsupported image format", 400, false},
		{"bad budget reason", strings.Replace(referenceBudgetTestMessage, "reason=timeout", "reason=unknown", 1), 400, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, IsAsyncImageReferenceAccountFailoverMessage(test.status, test.message))
			require.Equal(t, test.want, isAsyncImageReferenceAccountFailover(test.status, []byte(referenceBudgetTestBody(test.message))))
		})
	}
}

func TestAsyncImageReferenceAccountFailoverDiagnosticsPersistWithoutURLs(t *testing.T) {
	for _, message := range []string{
		referenceAccountDirectProxyMessage, referenceAccountNetworkMessage,
		strings.ReplaceAll(referenceAccountDirectProxyMessage, "input.png", strings.Repeat("private-path", 100)),
		strings.Repeat("unimportant detail ", 100) + " Reference image fetch failed: https://references.example/secret.png?token=private",
	} {
		body := []byte(referenceBudgetTestBody(message))
		headers := http.Header{"X-Request-Id": []string{"req-reference-account"}, "Retry-After": []string{"17"}}
		failure := newAsyncImageReferenceAccountFailoverError(body, headers)
		require.True(t, failure.IsAsyncImageReferenceAccountFailover())
		require.False(t, failure.IsAsyncImageReferenceBudgetExceeded())
		require.Equal(t, GatewayFailureReason(AsyncImageReferenceAccountFailoverCode), failure.Reason)
		require.Equal(t, "reference_download_error", failure.ProviderErrorCode)
		require.Equal(t, "req-reference-account", failure.UpstreamRequestID)
		require.Equal(t, "17", failure.ResponseHeaders.Get("Retry-After"))
		require.False(t, failure.RetryableOnSameAccount)
		require.Equal(t, NextAccountRetry, failure.NextAccountAction)
		require.Equal(t, body, failure.ResponseBody)
		require.NotContains(t, failure.ProviderErrorMessage, "references.example")
		require.NotContains(t, failure.ProviderErrorMessage, "private")
		require.NotContains(t, failure.ProviderErrorMessage, "access_token")
		require.True(t, IsAsyncImageReferenceAccountFailoverMessage(400, failure.ProviderErrorMessage), failure.ProviderErrorMessage)
	}
	budget := newAsyncImageReferenceAccountFailoverError([]byte(referenceBudgetTestBody(referenceBudgetTestMessage)), nil)
	oldBudget := newAsyncImageReferenceBudgetFailoverError([]byte(referenceBudgetTestBody(referenceBudgetTestMessage)), nil)
	for _, failure := range []*UpstreamFailoverError{budget, oldBudget} {
		require.True(t, failure.IsAsyncImageReferenceAccountFailover())
		require.True(t, failure.IsAsyncImageReferenceBudgetExceeded())
		require.Equal(t, GatewayFailureReason(AsyncImageReferenceBudgetExceededCode), failure.Reason)
	}
	var empty *UpstreamFailoverError
	require.False(t, empty.IsAsyncImageReferenceAccountFailover())
}

func TestAsyncImageReferenceNetworkFailoverRequiresInputTaskContext(t *testing.T) {
	for _, test := range []struct {
		name string
		ctx  context.Context
		want bool
	}{
		{"unset", context.Background(), false},
		{"text task", WithAsyncImageInputReferenceTask(context.Background(), false), false},
		{"input reference task", WithAsyncImageInputReferenceTask(context.Background(), true), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, isAsyncImageReferenceAccountFailoverForContext(test.ctx, 400, []byte(referenceBudgetTestBody(referenceAccountNetworkMessage))))
			require.True(t, isAsyncImageReferenceAccountFailoverForContext(test.ctx, 400, []byte(referenceBudgetTestBody(referenceAccountDirectProxyMessage))))
		})
	}
}

func TestAsyncImageReferenceAccount400ServicePolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{PlatformOpenAI, PlatformGemini} {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, input := range []struct {
				name, message string
				network       bool
			}{
				{"direct proxy", referenceAccountDirectProxyMessage, false},
				{"network", referenceAccountNetworkMessage, true},
				{"redacted network", referenceRedactedNetworkTestMessage, true},
				{"TLS network", referenceTLSTestMessage, true},
				{"redacted TLS network", referenceRedactedTLSTestMessage, true},
			} {
				for _, mode := range []string{"async", "sync", "admin passthrough", "no replay", "permanent", "text task", "missing task flag"} {
					if !input.network && (mode == "text task" || mode == "missing task flag") {
						continue
					}
					t.Run(platform+"/"+accountType+"/"+input.name+"/"+mode, func(t *testing.T) {
						ctx := context.Background()
						if mode != "sync" {
							ctx = WithAsyncImageAccountAttemptCapture(ctx, &AsyncImageAccountAttemptCapture{})
							ctx = WithGeminiAsyncImageGeneration(ctx)
							if mode != "missing task flag" {
								ctx = WithAsyncImageInputReferenceTask(ctx, mode != "text task")
							}
						}
						message := input.message
						if mode == "no replay" {
							message += " Do not automatically regenerate."
						} else if mode == "permanent" {
							message += " HTTP 403 forbidden"
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
							BindErrorPassthroughService(c, &ErrorPassthroughService{localCache: []*cachedPassthroughRule{{
								ErrorPassthroughRule: &model.ErrorPassthroughRule{
									ID: 1, Enabled: true, Platforms: []string{platform}, MatchMode: model.MatchModeAny,
									ErrorCodes: []int{400}, ResponseCode: &status, CustomMessage: &custom,
								}, lowerPlatforms: []string{platform}, errorCodeSet: map[int]struct{}{400: {}},
							}}})
						}
						account := &Account{ID: 110, Platform: platform, Type: accountType, Concurrency: 1, Credentials: map[string]any{
							"api_key": "test", "access_token": "test", "project_id": "project-1", "base_url": "https://images.example/v1",
							"pool_mode": true, "pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []int{400},
						}}
						upstreamBody := referenceBudgetTestBody(message)
						if platform == PlatformGemini && accountType == AccountTypeOAuth {
							upstreamBody = `{"response":` + upstreamBody + `}`
						}
						response := &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"req-reference-account"}}, Body: io.NopCloser(strings.NewReader(upstreamBody))}
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
							require.True(t, failure.IsAsyncImageReferenceAccountFailover())
							require.False(t, failure.RetryableOnSameAccount)
							require.Empty(t, recorder.Body.Bytes())
							require.NotContains(t, failure.ProviderErrorMessage, "private")
						} else {
							require.NotErrorAs(t, err, &failure)
							if mode == "admin passthrough" {
								require.Equal(t, http.StatusConflict, recorder.Code)
								require.Equal(t, "reference route disabled", gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
							}
						}
					})
				}
			}
		}
	}
}
