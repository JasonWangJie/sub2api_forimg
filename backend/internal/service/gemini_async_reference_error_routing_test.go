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

func TestGeminiAsyncReferenceErrorsRemainAvailableToWorker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		for _, message := range []string{
			"Reference image download failed for https://cdn.example/input.png: timeout",
			"reference image fetch failed: failed after 3 attempts: handshake error",
			"Reference image download failed: HTTP 403 forbidden",
		} {
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
					Header:     http.Header{"Content-Type": []string{"application/json"}},
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
				require.NotErrorAs(t, err, &failover)
				require.Equal(t, 1, upstream.calls)
				require.Equal(t, http.StatusBadRequest, recorder.Code)
				require.Contains(t, recorder.Body.String(), message)
			})
		}
	}
}
