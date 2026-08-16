package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageExecutionTimeoutDefaultsToTwentyMinutes(t *testing.T) {
	require.Equal(t, 20*time.Minute, asyncImageExecutionTimeout(service.AsyncImageRuntimeConfig{}))
	require.Equal(t, 20*time.Minute, asyncImageExecutionTimeout(service.AsyncImageRuntimeConfig{ExecutionTimeoutSeconds: 1200}))
	require.Equal(t, 5*time.Minute, asyncImageExecutionTimeout(service.AsyncImageRuntimeConfig{ExecutionTimeoutSeconds: 300}))
}

func TestIsOpenAIImageURLFetchTimeoutFailure(t *testing.T) {
	msg := "upstream image generation failed (HTTP 400): image_url fetch failed: Failed to perform, curl: (28) Connection timed out after 60002 milliseconds. See https://cdn.example/a.png first for more details."
	require.True(t, isOpenAIImageURLFetchTimeoutFailure(http.StatusBadRequest, msg))
	require.True(t, isOpenAIImageURLFetchTimeoutFailure(http.StatusBadRequest, "image_url fetch failed: connection timed out after 60001 milliseconds"))
	require.False(t, isOpenAIImageURLFetchTimeoutFailure(http.StatusBadGateway, msg))
	require.False(t, isOpenAIImageURLFetchTimeoutFailure(http.StatusBadRequest, "image_url fetch failed: HTTP 403 Forbidden"))
	require.False(t, isOpenAIImageURLFetchTimeoutFailure(http.StatusBadRequest, "All available accounts exhausted"))
}

func TestShouldRetryOpenAIImageURLFetchTimeoutOnce(t *testing.T) {
	msg := "upstream image generation failed (HTTP 400): image_url fetch failed: curl: (28) Connection timed out after 60002 milliseconds"
	task := &service.AsyncImageTask{Platform: service.PlatformOpenAI}
	require.True(t, shouldRetryOpenAIImageURLFetchTimeout(task, http.StatusBadRequest, msg))

	code := asyncImageOpenAIImageURLFetchTimeoutCode
	task.ErrorCode = &code
	require.False(t, shouldRetryOpenAIImageURLFetchTimeout(task, http.StatusBadRequest, msg), "already scheduled retry must not retry again")

	gemini := &service.AsyncImageTask{Platform: service.PlatformGemini}
	require.False(t, shouldRetryOpenAIImageURLFetchTimeout(gemini, http.StatusBadRequest, msg))
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
