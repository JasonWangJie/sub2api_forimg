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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const imageHealthTestPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADElEQVR42mNk+M/wHwAF/gL+eQ5S2QAAAABJRU5ErkJggg=="

type imageProbeHeldBody struct {
	io.Reader
	probe *imageProbeResultRecorder
}

func (r imageProbeHeldBody) Read(p []byte) (int, error) {
	require.False(r.probe.t, r.probe.finished, "probe must remain held while upstream body is being parsed")
	return r.Reader.Read(p)
}
func (imageProbeHeldBody) Close() error { return nil }

func TestImageHealthOpenAIProbeWaitsForParsedResult(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		probe := &imageProbeResultRecorder{imageHealthContextRecorder: imageHealthContextRecorder{t: t}}
		body := []byte(`{"model":"gpt-image-2","prompt":"draw","size":"1024x1024"}`)
		response := `{"data":[{"b64_json":"` + imageHealthTestPNG + `"}]}`
		if malformed {
			response = `{not valid JSON`
		}
		ctx := WithOpenAIImageGenerationIntent(context.Background())
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body)).WithContext(ctx)
		c.Request.Header.Set("Content-Type", "application/json")
		upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: imageProbeHeldBody{strings.NewReader(response), probe}}}
		svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, imageCircuitBreaker: probe}
		parsed, err := svc.ParseOpenAIImagesRequest(c, body)
		require.NoError(t, err)
		result, err := svc.ForwardImages(ctx, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://example.org"}}, body, parsed, "")
		require.NoError(t, err)
		require.Equal(t, 1, result.ImageCount, "legacy response/accounting fallback must remain unchanged")
		require.Equal(t, 1, probe.acquireCount)
		require.Equal(t, 1, probe.finishCount)
		require.Equal(t, !malformed, probe.success)
		require.False(t, probe.healthFailure, "a malformed 200 is not an account availability failure")
	}
}

func TestImageHealthGeminiTextResponseDoesNotRecoverImageProbe(t *testing.T) {
	probe := &imageProbeResultRecorder{imageHealthContextRecorder: imageHealthContextRecorder{t: t}}
	ctx := WithGeminiImageGenerationIntent(context.Background())
	body := []byte(`{"model":"gemini-3-pro-image-preview","messages":[{"role":"user","content":"draw"}]}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)).WithContext(ctx)
	upstream := &geminiCompatHTTPUpstreamStub{response: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: imageProbeHeldBody{
		strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"No image was produced"}]},"finishReason":"STOP"}]}`), probe,
	}}}
	svc := &GeminiMessagesCompatService{cfg: &config.Config{}, httpUpstream: upstream, imageCircuitBreaker: probe}
	result, err := svc.ForwardAsChatCompletions(ctx, c, &Account{ID: 1, Platform: PlatformGemini, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://example.org"}}, body)
	require.NoError(t, err)
	require.Equal(t, 1, result.ImageCount, "legacy model-name billing heuristic remains unchanged")
	require.Equal(t, 1, probe.finishCount)
	require.False(t, probe.success, "text content cannot prove recovery of image generation")
}

func TestImageHealthGeminiSameAccountRetriesHoldOneProbe(t *testing.T) {
	probe := &imageProbeResultRecorder{imageHealthContextRecorder: imageHealthContextRecorder{t: t}}
	ctx := WithGeminiImageGenerationIntent(context.Background())
	ctx = WithGeminiImageResponseCapture(ctx, &GeminiImageResponseCapture{})
	body := []byte(`{"model":"gemini-3-pro-image-preview","messages":[{"role":"user","content":"draw"}]}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)).WithContext(ctx)
	call := 0
	upstream := &geminiCompatHTTPUpstreamStub{responseFactory: func() *http.Response {
		call++
		require.False(t, probe.finished, "an internal retry must retain its half-open ownership")
		status := 503
		response := `{"error":{"message":"unavailable"}}`
		if call > 1 {
			status = 200
			response = `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + imageHealthTestPNG + `"}}]}}]}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: imageProbeHeldBody{strings.NewReader(response), probe}}
	}}
	svc := &GeminiMessagesCompatService{cfg: &config.Config{}, httpUpstream: upstream, imageCircuitBreaker: probe}
	result, err := svc.ForwardAsChatCompletions(ctx, c, &Account{ID: 1, Platform: PlatformGemini, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://example.org"}}, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 2, call)
	require.Equal(t, 1, probe.acquireCount)
	require.Equal(t, 1, probe.finishCount)
	require.True(t, probe.success)
}
