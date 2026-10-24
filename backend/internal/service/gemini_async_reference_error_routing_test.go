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
)

func geminiReferenceRoutingTestBody(message string) string {
	return fmt.Sprintf(`{"error":{"type":"invalid_request_error","code":"bad_request","message":%q}}`, message)
}

func TestGeminiAsyncReferenceErrorsDoNotBecomeGenericInvalidRequestFailover(t *testing.T) {
	for _, message := range []string{
		"Reference image download failed for https://cdn.example/input.png: timeout",
		"reference image fetch failed: failed after 3 attempts: handshake error",
		"Reference image download exceeded its total time budget for https://cdn.example/input.png (budgetMs=35000; reason=timeout)",
		"Reference image download failed: HTTP 403 forbidden",
		"Reference image download failed: corrupt image",
		"reference image fetch failed: invalid base64 input",
		"Reference image download failed: safety policy rejected",
		"Reference image download failed: Do not automatically regenerate",
		"reference image fetch failed: generation outcome is unknown",
		"fileUri download failed: HTTP 404",
		"download OpenAI reference image: HTTP 401",
		"could not fetch image: unsupported image format",
		"unable to fetch image: image is too large",
	} {
		t.Run(message, func(t *testing.T) {
			body := geminiReferenceRoutingTestBody(message)
			require.False(t, isGeminiAsyncAccountFailover400([]byte(body)), "the wrapper type must not turn a reference failure into generic account failover")
			wrapped := fmt.Sprintf(`{"error":{"message":%q}}`, body)
			require.False(t, isGeminiAsyncAccountFailover400([]byte(wrapped)))
		})
	}
	require.True(t, isGeminiAsyncAccountFailover400([]byte(`Invalid request`)))
	ordinaryInvalidRequest := geminiReferenceRoutingTestBody("Invalid request for this image account")
	require.True(t, isGeminiAsyncAccountFailover400([]byte(ordinaryInvalidRequest)))
}

func TestGeminiAsyncReferenceErrorsSwitchTemporaryAndPreservePermanent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		for _, test := range []struct {
			message      string
			wantFailover bool
		}{
			{"Reference image download failed for https://cdn.example/input.png: timeout", true},
			{"reference image fetch failed: failed after 3 attempts: handshake error", true},
			{"Reference image download failed: HTTP 403 forbidden", false},
		} {
			message := test.message
			t.Run(accountType+"/"+message, func(t *testing.T) {
				ctx := WithGeminiAsyncImageGeneration(context.Background())
				body := []byte(`{"model":"gemini-3-pro-image-preview","messages":[{"role":"user","content":"draw a cat"}]}`)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)).WithContext(ctx)
				upstreamBody := geminiReferenceRoutingTestBody(message)
				if accountType == AccountTypeOAuth {
					upstreamBody = `{"response":` + upstreamBody + `}`
				}
				upstream := &geminiCompatHTTPUpstreamStub{response: &http.Response{
					StatusCode: http.StatusBadRequest,
					Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"req-reference-routing"}},
					Body:       io.NopCloser(strings.NewReader(upstreamBody)),
				}}
				svc := &GeminiMessagesCompatService{tokenProvider: &GeminiTokenProvider{}, cfg: &config.Config{}, httpUpstream: upstream}
				account := &Account{
					ID: 110, Platform: PlatformGemini, Type: accountType, Concurrency: 1,
					Credentials: map[string]any{"api_key": "test-key", "access_token": "test-token", "project_id": "test-project"},
				}
				_, err := svc.ForwardAsChatCompletions(ctx, c, account, body)
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.Equal(t, 1, upstream.calls)
				if test.wantFailover {
					require.ErrorAs(t, err, &failover)
					require.True(t, failover.IsAsyncImageReferenceAccountFailover())
					require.False(t, failover.RetryableOnSameAccount)
					require.Equal(t, http.StatusBadRequest, failover.StatusCode)
					require.Equal(t, "bad_request", failover.ProviderErrorCode)
					require.Equal(t, "req-reference-routing", failover.UpstreamRequestID)
					require.True(t, IsAsyncImageReferenceAccountFailoverMessage(http.StatusBadRequest, failover.ProviderErrorMessage))
					require.NotContains(t, failover.ProviderErrorMessage, "cdn.example")
					require.Empty(t, recorder.Body.Bytes(), "the gateway must retain the opportunity to switch accounts")
				} else {
					require.NotErrorAs(t, err, &failover)
					require.Equal(t, http.StatusBadRequest, recorder.Code)
					require.Contains(t, recorder.Body.String(), message)
				}
			})
		}
	}
}
