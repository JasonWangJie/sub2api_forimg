package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func receivedAsyncImageErrorContext(t *testing.T, status int, body []byte, input bool) context.Context {
	t.Helper()
	ctx := WithAsyncImageAccountAttemptCapture(context.Background(), &AsyncImageAccountAttemptCapture{})
	ctx = WithAsyncImageInputReferenceTask(ctx, input)
	response := &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body))}
	ctx = trackAsyncImageErrorResponse(ctx, response)
	_, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	return ctx
}

func timeoutErrorBody(t *testing.T, message string, details any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"error": map[string]any{"code": "upstream_error", "message": message, "details": details}})
	require.NoError(t, err)
	return body
}

func TestAsyncImageReceivedTimeoutRejectionClassification(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 408, 418, 422, 429, 454, 500, 502, 503, 504, 529, 599} {
		for _, message := range []string{"生图超时，请重试", "TIMEOUT", "TimeOut waiting for provider", "read time out", "request timed out", "i/o timeout", "context deadline exceeded", "Gateway Time-out", "request timed-out"} {
			body := timeoutErrorBody(t, message, nil)
			failure := newAsyncImageRetryableRejectionForContext(receivedAsyncImageErrorContext(t, status, body, false), status, body, nil)
			require.NotNil(t, failure, "%d: %s", status, message)
			require.True(t, failure.IsAsyncImageTimeoutRejection())
			require.Equal(t, status, failure.StatusCode)
			require.True(t, failure.IsAsyncImageBoundedRetryFailure())
			require.False(t, failure.RetryableOnSameAccount)
		}
	}
	for _, test := range []struct {
		name, message string
		detail        any
	}{
		{"empty", "", nil}, {"unrelated", "invalid input", nil}, {"time outcome", "time-outcome invalid", nil},
		{"URL only", "Unable to access https://timeout.example/ref.png?timeout=1", nil},
		{"details URL only", "invalid image", map[string]any{"url": "https://timeout.example/input.png"}},
		{"message stop", "timeout; do not retry", nil},
		{"plain stop", "context deadline exceeded; generation outcome is unknown", nil},
		{"charged detail", "timeout", []any{map[string]any{"warning": "may already have been charged"}}},
		{"Chinese charged", "超时，可能已扣费", nil},
		{"JSON encoded stop", "timeout", `{"warning":"Do not automatically regenerate"}`},
		{"safety detail", "timeout", map[string]any{"reason": "content policy"}},
		{"generated detail", "timeout", "generated image already available"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := timeoutErrorBody(t, test.message, test.detail)
			failure := newAsyncImageTimeoutRejectionForContext(receivedAsyncImageErrorContext(t, 408, body, true), 408, body, nil)
			switch test.name {
			case "message stop", "plain stop", "charged detail", "Chinese charged", "JSON encoded stop", "safety detail", "generated detail":
				require.NotNil(t, failure)
				require.True(t, failure.IsAsyncImageTimeoutTerminal())
				require.False(t, failure.ShouldRetryNextAccount())
			default:
				require.Nil(t, failure)
			}
		})
	}
	for _, body := range [][]byte{[]byte(`{"error":{"code":"timeout","param":"timeout","message":"invalid value"}}`), []byte(`{"error":{"message":"timeout"},"data":[{"url":"https://images.example/result.png"}]}`), []byte(`{"error":{"message":"timeout"`), []byte("timeout; do not retry")} {
		failure := newAsyncImageTimeoutRejectionForContext(receivedAsyncImageErrorContext(t, 408, body, true), 408, body, nil)
		if strings.Contains(string(body), `"data":`) || strings.Contains(string(body), "do not retry") || strings.Contains(string(body), `"message":"timeout"`) {
			require.NotNil(t, failure)
			require.True(t, failure.IsAsyncImageTimeoutTerminal())
		} else {
			require.Nil(t, failure)
		}
	}
	for _, status := range []int{0, 200, 399, 600} {
		body := timeoutErrorBody(t, "timeout", nil)
		require.Nil(t, newAsyncImageTimeoutRejectionForContext(receivedAsyncImageErrorContext(t, status, body, true), status, body, nil))
	}
	body := []byte("upstream processing timed out")
	require.NotNil(t, newAsyncImageTimeoutRejectionForContext(receivedAsyncImageErrorContext(t, 408, body, false), 408, body, nil))
	require.Nil(t, newAsyncImageTimeoutRejectionForContext(context.Background(), 408, body, nil))
	require.Nil(t, newAsyncImageTimeoutRejectionForContext(WithGeminiAsyncImageGeneration(context.Background()), 408, body, nil))
}

func TestAsyncImageTimeoutProducedOutputStopsReferenceAndGeneralFallback(t *testing.T) {
	for _, status := range []int{404, 454, 503} {
		for _, field := range []string{"data", "output", "images", "candidates", "response", "details", "encoded details"} {
			message, code := "upstream timeout", any("upstream_error")
			if status == 404 {
				message, code = "文件引用不存在: https://cdn.example/input.png", 404
			}
			if status == 454 {
				message, code = referenceTimeout454Message, "asset_download_timeout"
			}
			errorObject := map[string]any{"code": code, "message": message}
			root := map[string]any{"error": errorObject}
			output := []any{map[string]any{"url": "https://images.example/result.png"}}
			switch field {
			case "response":
				root[field] = map[string]any{"data": output}
			case "details":
				errorObject["details"] = map[string]any{"images": output}
			case "encoded details":
				errorObject["details"] = `{"output":[{"url":"https://images.example/result.png"}]}`
			default:
				root[field] = output
			}
			body, err := json.Marshal(root)
			require.NoError(t, err)
			ctx := receivedAsyncImageErrorContext(t, status, body, true)
			failure := newAsyncImageRetryableRejectionForContext(ctx, status, body, nil)
			require.NotNil(t, failure, "%d/%s", status, field)
			require.True(t, failure.IsAsyncImageTimeoutTerminal())
			require.False(t, failure.ShouldRetryNextAccount())
			require.False(t, isAsyncImageReferenceAccountFailoverForContext(ctx, status, body))
		}
	}
}

func TestAsyncImageTimeoutUnknownDetailsDecodeBeforeClassification(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"error":{"message":"timeout","details":{"warning":"\u6267\u884c\u7ed3\u679c\u672a\u77e5"}}}`),
		[]byte(`{"error":{"message":"timeout","details":"{\"warning\":\"\\u5df2\\u6263\\u8d39\"}"}}`),
		[]byte(`{"error":{"code":400,"message":"timeout","details":[false,null,true,0,{"warning":"execution unknown"}]}}`),
	} {
		failure := newAsyncImageRetryableRejectionForContext(receivedAsyncImageErrorContext(t, 503, body, true), 503, body, nil)
		require.NotNil(t, failure)
		require.True(t, failure.IsAsyncImageTimeoutTerminal())
		require.Equal(t, AsyncImageTimeoutUnknownReason, failure.Reason)
	}
}

func TestAsyncImageMissingFileReference404(t *testing.T) {
	for _, message := range []string{"文件引用不存在: https://cdn.example/ref.png?sig=private", "文件引用不存在：https://8.8.8.8/ref.png", "文件引用不存在: https://***"} {
		body := []byte(`{"error":{"code":404,"message":` + timeoutQuotedMessage(message) + `}}`)
		ctx := receivedAsyncImageErrorContext(t, 404, body, true)
		require.True(t, isAsyncImageReferenceAccountFailoverForContext(ctx, 404, body))
		failure := newAsyncImageReferenceAccountFailoverErrorWithStatus(404, body, nil)
		require.Equal(t, 404, failure.StatusCode)
		require.True(t, failure.IsAsyncImageBoundedRetryFailure())
		require.True(t, IsAsyncImageReferenceAccountFailoverMessage(404, failure.ProviderErrorMessage))
		require.NotContains(t, failure.ProviderErrorMessage, "private")
		require.False(t, isAsyncImageReferenceAccountFailoverForContext(receivedAsyncImageErrorContext(t, 404, body, false), 404, body))
	}
	for _, message := range []string{"文件引用不存在: http://cdn.example/ref.png", "文件引用不存在: https://user:pass@cdn.example/ref.png", "文件引用不存在: https://cdn.example/ref.png#part", "文件引用不存在: https://cdn.example/ref.png do not retry", "文件引用不存在: ref.png", "model not found"} {
		body := []byte(`{"error":{"code":404,"message":` + timeoutQuotedMessage(message) + `}}`)
		require.False(t, isAsyncImageReferenceAccountFailoverForContext(receivedAsyncImageErrorContext(t, 404, body, true), 404, body))
	}
	for _, body := range [][]byte{[]byte(`{"error":{"code":"upstream_error","message":"文件引用不存在: https://cdn.example/ref.png"}}`), []byte(`{"error":{"code":404,"message":"文件引用不存在: https://cdn.example/ref.png","details":{"warning":"may already have been charged"}}}`)} {
		require.False(t, isAsyncImageReferenceAccountFailoverForContext(receivedAsyncImageErrorContext(t, 404, body, true), 404, body))
	}
}

func timeoutQuotedMessage(s string) string { b, _ := json.Marshal(s); return string(b) }

type asyncImageInterruptedErrorBody struct{}

func (asyncImageInterruptedErrorBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestAsyncImageReceivedTimeoutServiceResponseCompleteness(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{PlatformOpenAI, PlatformGemini} {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, mode := range []string{"complete JSON", "complete plaintext", "valid JSON then read error", "limit truncation", "sync"} {
				t.Run(platform+"/"+accountType+"/"+mode, func(t *testing.T) {
					ctx := context.Background()
					if mode != "sync" {
						ctx = WithAsyncImageAccountAttemptCapture(ctx, &AsyncImageAccountAttemptCapture{})
						ctx = WithGeminiAsyncImageGeneration(ctx)
					}
					requestBody := []byte(`{"model":"gpt-image-2","prompt":"draw a cat"}`)
					path := "/v1/images/generations"
					if platform == PlatformGemini {
						requestBody = []byte(`{"model":"gemini-3-pro-image-preview","messages":[{"role":"user","content":"draw a cat"}]}`)
						path = "/v1/chat/completions"
					}
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(requestBody)).WithContext(ctx)
					c.Request.Header.Set("Content-Type", "application/json")
					account := &Account{ID: 1, Platform: platform, Type: accountType, Concurrency: 1, Credentials: map[string]any{"api_key": "test", "access_token": "test", "project_id": "project-1", "base_url": "https://images.example/v1", "pool_mode": true}}
					body := timeoutErrorBody(t, "request time out", nil)
					if platform == PlatformGemini && accountType == AccountTypeOAuth {
						body = append(append([]byte(`{"response":`), body...), '}')
					}
					var reader io.Reader = bytes.NewReader(body)
					if mode == "complete plaintext" {
						reader = strings.NewReader("request time out")
					}
					if mode == "valid JSON then read error" {
						reader = io.MultiReader(bytes.NewReader(body), asyncImageInterruptedErrorBody{})
					}
					if mode == "limit truncation" {
						reader = io.MultiReader(bytes.NewReader(body), strings.NewReader(strings.Repeat(" ", int(gatewayUpstreamErrorBodyReadLimit)+1024)))
					}
					status := 504
					if mode == "sync" {
						status = 418
					}
					response := &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(reader)}
					var err error
					if platform == PlatformOpenAI {
						svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: &httpUpstreamRecorder{resp: response}}
						parsed, parseErr := svc.ParseOpenAIImagesRequest(c, requestBody)
						require.NoError(t, parseErr)
						_, err = svc.ForwardImages(ctx, c, account, requestBody, parsed, "")
					} else {
						svc := &GeminiMessagesCompatService{tokenProvider: &GeminiTokenProvider{}, cfg: &config.Config{}, httpUpstream: &geminiCompatHTTPUpstreamStub{response: response}}
						_, err = svc.ForwardAsChatCompletions(ctx, c, account, requestBody)
					}
					require.Error(t, err)
					var failure *UpstreamFailoverError
					if mode == "complete JSON" || mode == "complete plaintext" {
						require.ErrorAs(t, err, &failure)
						require.True(t, failure.IsAsyncImageTimeoutRejection())
						require.Equal(t, 504, failure.StatusCode)
					} else if errors.As(err, &failure) {
						require.False(t, failure.IsAsyncImageTimeoutRejection())
					}
				})
			}
		}
	}
}
