package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const asyncImageWorkerOnePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADElEQVR42mNk+M/wHwAF/gL+eQ5S2QAAAABJRU5ErkJggg=="

func TestAsyncImageExecutionTimeoutDefaultsToTwentyMinutes(t *testing.T) {
	require.Equal(t, 20*time.Minute, asyncImageExecutionTimeout(service.AsyncImageRuntimeConfig{}))
	require.Equal(t, 20*time.Minute, asyncImageExecutionTimeout(service.AsyncImageRuntimeConfig{ExecutionTimeoutSeconds: 1200}))
	require.Equal(t, 5*time.Minute, asyncImageExecutionTimeout(service.AsyncImageRuntimeConfig{ExecutionTimeoutSeconds: 300}))
}

func TestAsyncImageRecentFailedAccountIDsCountsUniqueAccounts(t *testing.T) {
	task := &service.AsyncImageTask{
		AccountAttempts: json.RawMessage(`[
			{"account_id": 11, "status": "failed"},
			{"account_id": 12, "status": "failed"},
			{"account_id": 13, "status": "failed"},
			{"account_id": 11, "status": "failed"},
			{"account_id": 11, "status": "failed"}
		]`),
	}

	ids := asyncImageRecentFailedAccountIDs(task)
	require.Equal(t, map[int64]struct{}{
		11: {},
		12: {},
		13: {},
	}, ids)
}

func TestAsyncImageExplicitReferenceFetchFailure(t *testing.T) {
	msg := "上游生图失败（HTTP 400）：image_url fetch failed: Failed to perform, curl: (28) Connection timed out after 60002 milliseconds. See https://cdn.example/a.png first for more details."
	require.True(t, isAsyncImageExplicitReferenceFetchFailure(msg))
	require.True(t, isAsyncImageExplicitReferenceFetchFailure("failed to download image from CDN"))
	require.False(t, isAsyncImageExplicitReferenceFetchFailure("Internal error encountered"))
	require.False(t, isAsyncImageExplicitReferenceFetchFailure("All available accounts exhausted"))
}

func TestFormatAsyncImageUpstreamFailureUsesChinesePrefix(t *testing.T) {
	msg := formatAsyncImageUpstreamFailure(http.StatusBadRequest, []byte(`{"error":{"message":"prompt is required"}}`))
	require.Equal(t, "提示词无效（HTTP 400）：prompt is required", msg)

	generic := formatAsyncImageUpstreamFailure(http.StatusBadGateway, []byte(`{"error":{"message":"upstream overloaded"}}`))
	require.Equal(t, "上游生图失败（HTTP 502）：upstream overloaded", generic)

	empty := formatAsyncImageUpstreamFailure(0, nil)
	require.Equal(t, "上游生图失败：网关无有效响应（no upstream error body）", empty)
}

func TestShouldFallbackHybridReferenceTransportToLocal(t *testing.T) {
	msg := "上游生图失败（HTTP 400）：image_url fetch failed: curl: (28) Connection timed out after 60002 milliseconds"
	hybrid := service.AsyncImageReferenceTransportPassthroughFallbackLocal
	task := &service.AsyncImageTask{Platform: service.PlatformOpenAI, RequestType: service.AsyncImageRequestTypeImageToImage, ReferenceTransport: &hybrid}
	cfg := testAsyncImageRetryConfig()
	require.True(t, shouldRetryAsyncImageUpstreamReferenceFetch(task, cfg, http.StatusBadRequest, msg))
	require.True(t, shouldRetryAsyncImageUpstreamReferenceFetch(task, cfg, http.StatusBadRequest, "images/edits requires multipart/form-data"))

	passthrough := service.AsyncImageReferenceTransportPassthrough
	task.ReferenceTransport = &passthrough
	require.True(t, shouldRetryAsyncImageUpstreamReferenceFetch(task, cfg, http.StatusBadRequest, msg))
	require.False(t, shouldRetryAsyncImageUpstreamReferenceFetch(task, cfg, http.StatusBadRequest, "images/edits requires multipart/form-data"))

	local := service.AsyncImageReferenceTransportLocal
	task.ReferenceTransport = &local
	require.False(t, shouldRetryAsyncImageUpstreamReferenceFetch(task, cfg, http.StatusBadRequest, msg))

	gemini := service.AsyncImageReferenceTransportPassthroughFallbackLocal
	task.Platform = service.PlatformGemini
	task.ReferenceTransport = &gemini
	require.False(t, shouldRetryAsyncImageUpstreamReferenceFetch(task, cfg, http.StatusBadRequest, "images/edits requires multipart/form-data"))
}

func TestInlineAsyncOpenAIReferencesRewritesSupportedURLShapes(t *testing.T) {
	var root any
	require.NoError(t, json.Unmarshal([]byte(`{
  "image_url":"https://images.example/string.png",
  "content":[{"type":"input_image","image_url":{"url":"https://images.example/object.png"}}],
  "image_urls":["https://images.example/list.png", {"url":"https://images.example/list-object.png"}]
}`), &root))
	imageData, err := base64.StdEncoding.DecodeString(asyncImageWorkerOnePixelPNG)
	require.NoError(t, err)
	requests := make([]string, 0, 4)
	downloader := service.AsyncImageReferenceDownloader{
		BoundLoader: func(_ context.Context, rawURL string) (*service.AsyncImageReference, bool, error) {
			requests = append(requests, rawURL)
			return &service.AsyncImageReference{MIMEType: "image/png", Data: imageData, Width: 1, Height: 1}, true, nil
		},
	}

	changed, err := inlineAsyncOpenAIReferences(context.Background(), &root, downloader)
	require.NoError(t, err)
	require.True(t, changed)
	require.ElementsMatch(t, []string{
		"https://images.example/string.png",
		"https://images.example/object.png",
		"https://images.example/list.png",
		"https://images.example/list-object.png",
	}, requests)

	body, err := json.Marshal(root)
	require.NoError(t, err)
	var rewritten map[string]any
	require.NoError(t, json.Unmarshal(body, &rewritten))
	require.Contains(t, rewritten["image_url"].(string), "data:image/png;base64,")
	content := rewritten["content"].([]any)[0].(map[string]any)
	require.Contains(t, content["image_url"].(map[string]any)["url"].(string), "data:image/png;base64,")
	urls := rewritten["image_urls"].([]any)
	require.Contains(t, urls[0].(string), "data:image/png;base64,")
	require.Contains(t, urls[1].(map[string]any)["url"].(string), "data:image/png;base64,")
}

func TestInlineAsyncOpenAIReferencesValidatesDataURIsLocally(t *testing.T) {
	original := "data:image/png;base64," + durableAsyncImageOnePixelPNG
	root := any(map[string]any{"image_url": original})
	called := false
	downloader := service.AsyncImageReferenceDownloader{
		BoundLoader: func(context.Context, string) (*service.AsyncImageReference, bool, error) {
			called = true
			return nil, false, nil
		},
	}

	changed, err := inlineAsyncOpenAIReferences(context.Background(), &root, downloader)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, called)
	require.Equal(t, original, root.(map[string]any)["image_url"])
}

func TestPrepareAsyncOpenAIReferenceImagesLeavesMultipartPayloadUntouched(t *testing.T) {
	body := []byte("--boundary\r\nContent-Disposition: form-data; name=\"image\"\r\n\r\nbytes\r\n--boundary--\r\n")
	prepared, err := (&DurableAsyncImageHandler{}).prepareAsyncOpenAIReferenceImages(context.Background(), body, service.AsyncImageRuntimeConfig{})
	require.NoError(t, err)
	require.Equal(t, body, prepared)
}

func TestPrepareAsyncOpenAIReferenceImagesPassthroughKeepsHTTPSURL(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2","image_url":"https://1.1.1.1/ref.png"}`)
	prepared, local, err := (&DurableAsyncImageHandler{}).prepareAsyncOpenAIReferenceImagesForTransport(
		context.Background(), body, service.AsyncImageRuntimeConfig{MaxReferenceImages: 8}, false,
	)
	require.NoError(t, err)
	require.False(t, local)
	require.JSONEq(t, string(body), string(prepared))
}

func TestAsyncImageTypedRetryPolicies(t *testing.T) {
	message := "upstream image generation failed: All available accounts exhausted"
	cfg := testAsyncImageRetryConfig()
	task := &service.AsyncImageTask{RetryCount: 2}
	require.True(t, shouldRetryAsyncImageCapacity(task, cfg, http.StatusBadGateway, message))
	require.False(t, shouldRetryAsyncImageCapacity(task, cfg, http.StatusBadRequest, "prompt is required"))
	task.CapacityRetryCount = cfg.CapacityMaxRetries
	require.False(t, shouldRetryAsyncImageCapacity(task, cfg, http.StatusBadGateway, message))

	referenceTask := &service.AsyncImageTask{RetryCount: 5}
	require.True(t, shouldRetryAsyncImageReferenceFetch(referenceTask, cfg, context.DeadlineExceeded))
	referenceTask.ReferenceRetryCount = cfg.ReferenceFetchMaxRetries
	require.False(t, shouldRetryAsyncImageReferenceFetch(referenceTask, cfg, context.DeadlineExceeded))

	totalLimited := &service.AsyncImageTask{RetryCount: cfg.TotalMaxRetries}
	require.False(t, shouldRetryAsyncImageReferenceFetch(totalLimited, cfg, context.DeadlineExceeded))
}

func TestAsyncImageTransientRetryPolicy(t *testing.T) {
	cfg := testAsyncImageRetryConfig()
	task := &service.AsyncImageTask{}
	require.True(t, shouldRetryAsyncImageUpstreamTransient(task, cfg, http.StatusBadGateway, "上游生图失败（HTTP 502）：Upstream service temporarily unavailable"))
	require.True(t, shouldRetryAsyncImageUpstreamTransient(task, cfg, http.StatusBadGateway, "Internal error encountered."))
	require.True(t, shouldRetryAsyncImageUpstreamTransient(task, cfg, http.StatusServiceUnavailable, "gateway timeout"))
	require.False(t, shouldRetryAsyncImageUpstreamTransient(task, cfg, http.StatusBadRequest, "images/edits requires multipart/form-data"))
	require.False(t, shouldRetryAsyncImageUpstreamTransient(task, cfg, http.StatusBadGateway, "invalid api key"))
	task.UpstreamRetryCount = cfg.UpstreamTransientMaxRetries
	require.False(t, shouldRetryAsyncImageUpstreamTransient(task, cfg, http.StatusBadGateway, "Upstream service temporarily unavailable"))
	require.True(t, isAsyncImageAmbiguousUpstreamFailure(http.StatusBadGateway, "upstream request failed: unexpected EOF"))
	require.False(t, isAsyncImageAmbiguousUpstreamFailure(http.StatusBadGateway, "Internal error encountered"))
}

func TestAsyncImageRetryDelayUsesJitterAndRetryAfter(t *testing.T) {
	original := asyncImageRetryRandom
	asyncImageRetryRandom = func() float64 { return 0.5 }
	t.Cleanup(func() { asyncImageRetryRandom = original })

	require.Equal(t, 15*time.Second, asyncImageRetryDelay(1, 15, 60, 20, 900, 0))
	require.Equal(t, 30*time.Second, asyncImageRetryDelay(2, 15, 60, 20, 900, 0))
	require.Equal(t, 66*time.Second, asyncImageRetryDelay(3, 15, 60, 20, 900, 60*time.Second))
	require.Equal(t, 900*time.Second, asyncImageRetryDelay(1, 15, 60, 20, 900, 20*time.Minute))
}

func testAsyncImageRetryConfig() service.AsyncImageRuntimeConfig {
	return service.AsyncImageRuntimeConfig{
		OpenAIReferenceTransportMode: service.AsyncImageReferenceTransportPassthroughFallbackLocal,
		GeminiReferenceTransportMode: service.AsyncImageReferenceTransportPassthrough,
		ReferenceFetchMaxRetries:     2, UpstreamTransientMaxRetries: 3, CapacityMaxRetries: 5,
		TotalMaxRetries: 16, RetryJitterPercent: 20, RetryAfterMaxSeconds: 900,
		ReferenceFetchRetryBaseSeconds: 15, ReferenceFetchRetryMaxSeconds: 60,
		UpstreamTransientRetryBaseSeconds: 15, UpstreamTransientRetryMaxSeconds: 60,
		CapacityRetryBaseSeconds: 30, CapacityRetryMaxSeconds: 300,
	}
}

func TestBuildAsyncOpenAIEditMultipartFromJSONDataURI(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2","prompt":"edit","image_urls":["data:image/png;base64,` + asyncImageWorkerOnePixelPNG + `"],"size":"1024x1024"}`)
	converted, contentType, err := buildAsyncOpenAIEditMultipart(body, "application/json")
	require.NoError(t, err)
	mediaType, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	reader := multipart.NewReader(bytes.NewReader(converted), params["boundary"])
	fields := map[string]string{}
	imageBytes := 0
	for {
		part, nextErr := reader.NextPart()
		if nextErr != nil {
			break
		}
		data, readErr := io.ReadAll(part)
		require.NoError(t, readErr)
		if part.FormName() == "image" {
			imageBytes += len(data)
		} else {
			fields[part.FormName()] = string(data)
		}
	}
	require.Equal(t, "gpt-image-2", fields["model"])
	require.Equal(t, "edit", fields["prompt"])
	require.Equal(t, "1024x1024", fields["size"])
	require.Greater(t, imageBytes, 0)
}

func TestBuildAsyncOpenAIEditMultipartLeavesExistingMultipartUntouched(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader("body"))
	request.Header.Set("Content-Type", "multipart/form-data; boundary=abc")
	converted, contentType, err := buildAsyncOpenAIEditMultipart([]byte("body"), request.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, []byte("body"), converted)
	require.Equal(t, request.Header.Get("Content-Type"), contentType)
}

func TestAsyncImageInvocationTimedOutUsesStartedAtWallClock(t *testing.T) {
	now := time.Date(2026, 7, 23, 15, 0, 0, 0, time.UTC)
	started := now.Add(-21 * time.Minute)
	task := &service.AsyncImageTask{
		Status:    service.AsyncImageTaskStatusInvoking,
		StartedAt: &started,
		CreatedAt: now.Add(-30 * time.Minute),
	}
	require.True(t, asyncImageInvocationTimedOut(task, 20*time.Minute, now))
	require.False(t, asyncImageInvocationTimedOut(task, 30*time.Minute, now))

	fresh := now.Add(-5 * time.Minute)
	task.StartedAt = &fresh
	require.False(t, asyncImageInvocationTimedOut(task, 20*time.Minute, now))

	task.StartedAt = nil
	task.CreatedAt = now.Add(-25 * time.Minute)
	require.True(t, asyncImageInvocationTimedOut(task, 20*time.Minute, now))
}

func TestApplyCapturedGeminiImageDimensionsUsesRequestedTierForBilling(t *testing.T) {
	requested := "0.5K"
	result := &service.ForwardResult{ImageSize: service.ImageBillingSize2K}

	applyCapturedGeminiImageDimensions(result, []asyncImageCapturedOutput{{Width: 512, Height: 512}}, &requested)

	require.Equal(t, service.ImageBillingSize1K, result.ImageSize)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, "0.5K", result.ImageInputSize)
	require.Equal(t, "512x512", result.ImageOutputSize)
	require.Equal(t, service.ImageSizeSourceInput, result.ImageSizeSource)
	require.Equal(t, map[string]int{service.ImageBillingSize1K: 1}, result.ImageSizeBreakdown)
}

func TestApplyCapturedOpenAIImageDimensionsUsesRequestedTierOverPixels(t *testing.T) {
	requested := "1K"
	result := &service.OpenAIForwardResult{ImageSize: service.ImageBillingSize2K}

	applyCapturedOpenAIImageDimensions(result, []asyncImageCapturedOutput{
		{Width: 1536, Height: 1024},
	}, &requested)

	require.Equal(t, service.ImageBillingSize1K, result.ImageSize)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, "1K", result.ImageInputSize)
	require.Equal(t, "1536x1024", result.ImageOutputSize)
	require.Equal(t, service.ImageSizeSourceInput, result.ImageSizeSource)
	require.Equal(t, map[string]int{service.ImageBillingSize1K: 1}, result.ImageSizeBreakdown)
}

func TestApplyCapturedOpenAIImageDimensionsUsesLargestActualTier(t *testing.T) {
	requested := "1024x1024"
	result := &service.OpenAIForwardResult{ImageSize: service.ImageBillingSize1K}

	applyCapturedOpenAIImageDimensions(result, []asyncImageCapturedOutput{
		{Width: 1024, Height: 1024},
		{Width: 3840, Height: 2160},
	}, &requested)

	require.Equal(t, service.ImageBillingSize4K, result.ImageSize)
	require.Equal(t, 2, result.ImageCount)
	require.Equal(t, "1024x1024", result.ImageInputSize)
	require.Equal(t, "1024x1024", result.ImageOutputSize)
	require.Equal(t, service.ImageSizeSourceOutput, result.ImageSizeSource)
	require.Equal(t, map[string]int{
		service.ImageBillingSize1K: 1,
		service.ImageBillingSize4K: 1,
	}, result.ImageSizeBreakdown)
}
