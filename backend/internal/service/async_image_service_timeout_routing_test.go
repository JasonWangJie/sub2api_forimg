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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiServiceTimeoutFamilyCannotBypassStrictClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		for _, test := range []struct {
			name, message       string
			async, wantFailover bool
			wantSpecificTimeout bool
		}{
			{"known timeout uses dedicated rule", asyncServiceTimeout400TestMessage, true, true, true},
			{"partial timeout", "service timeout", true, false, false},
			{"whitespace partial timeout", "service \t timeout", true, false, false},
			{"prefixed timeout", "generation failed: " + asyncServiceTimeout400TestMessage, true, false, false},
			{"appended no replay", asyncServiceTimeout400TestMessage + " Do not automatically regenerate.", true, false, false},
			{"appended safety refusal", asyncServiceTimeout400TestMessage + " Request rejected by safety policy.", true, false, false},
			{"appended execution unknown", asyncServiceTimeout400TestMessage + " Generation may already have been charged.", true, false, false},
			{"synchronous timeout", asyncServiceTimeout400TestMessage, false, false, false},
			{"unrelated invalid request retains generic rule", "Invalid request for this image account", true, true, false},
			{"unrelated synchronous invalid request", "Invalid request for this image account", false, false, false},
		} {
			t.Run(accountType+"/"+test.name, func(t *testing.T) {
				body := fmt.Sprintf(`{"error":{"type":"invalid_request_error","code":400,"message":%q}}`, test.message)
				if strings.Contains(strings.Join(strings.Fields(strings.ToLower(test.message)), " "), "service timeout") {
					require.False(t, isGeminiAsyncAccountFailover400([]byte(body)), "the family must not bypass its strict dedicated rule through the wrapper type")
				} else {
					require.True(t, isGeminiAsyncAccountFailover400([]byte(body)))
				}
				ctx := context.Background()
				if test.async {
					ctx = WithGeminiAsyncImageGeneration(ctx)
				}
				requestBody := []byte(`{"model":"gemini-3-pro-image-preview","messages":[{"role":"user","content":"draw"}]}`)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(requestBody)).WithContext(ctx)
				upstreamBody := body
				if accountType == AccountTypeOAuth {
					upstreamBody = `{"response":` + body + `}`
				}
				upstream := &geminiCompatHTTPUpstreamStub{response: &http.Response{
					StatusCode: http.StatusBadRequest,
					Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"req-timeout-family"}},
					Body:       io.NopCloser(strings.NewReader(upstreamBody)),
				}}
				svc := &GeminiMessagesCompatService{tokenProvider: &GeminiTokenProvider{}, cfg: &config.Config{}, httpUpstream: upstream}
				account := &Account{ID: 117, Platform: PlatformGemini, Type: accountType, Concurrency: 1, Credentials: map[string]any{
					"api_key": "test", "access_token": "test", "project_id": "project-1",
					"pool_mode": true, "pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []any{400},
				}}
				_, err := svc.ForwardAsChatCompletions(ctx, c, account, requestBody)
				require.Error(t, err)
				require.Equal(t, 1, upstream.calls)
				var failure *UpstreamFailoverError
				if test.wantFailover {
					require.ErrorAs(t, err, &failure)
					require.Equal(t, test.wantSpecificTimeout, failure.IsAsyncImageServiceTimeout400())
					require.False(t, failure.RetryableOnSameAccount)
					require.Empty(t, recorder.Body.Bytes())
				} else {
					require.NotErrorAs(t, err, &failure)
					require.Equal(t, 400, recorder.Code)
					if test.async {
						require.Equal(t, strings.Join(strings.Fields(test.message), " "), gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
					} else {
						require.Equal(t, "Invalid request", gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
					}
				}
			})
		}
	}
}

func TestServiceTimeoutFamilyGuardCannotUseEitherGeneric400Bypass(t *testing.T) {
	for _, message := range []string{
		asyncServiceTimeout400TestMessage,
		"service timeout",
		"service   timeout, please try again later. Do not retry.",
		"service\ttimeout, please try again later. Do not automatically regenerate.",
		"Invalid request: service\n timeout, please try again later. Do not retry.",
		"invalid_request: SERVICE \t TIMEOUT, please try again later.",
	} {
		t.Run(message, func(t *testing.T) {
			require.False(t, isOpenAIAsyncAccountFailover400(message))
			body := fmt.Sprintf(`{"error":{"type":"invalid_request_error","code":400,"message":%q}}`, message)
			require.False(t, isGeminiAsyncAccountFailover400([]byte(body)))
		})
	}
	for _, message := range []string{"Invalid request for this account", "invalid_request for this account"} {
		require.True(t, isOpenAIAsyncAccountFailover400(message))
		body := fmt.Sprintf(`{"error":{"type":"upstream_error","message":%q}}`, message)
		require.True(t, isGeminiAsyncAccountFailover400([]byte(body)))
	}
	// Normalize only the timeout family guard, not the unrelated generic rules.
	message := "invalid\trequest for this account"
	require.False(t, isOpenAIAsyncAccountFailover400(message))
	body := fmt.Sprintf(`{"error":{"type":"upstream_error","message":%q}}`, message)
	require.False(t, isGeminiAsyncAccountFailover400([]byte(body)))
}
