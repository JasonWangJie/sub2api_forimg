package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageCircuitRouteAndSharedGroupIsolation(t *testing.T) {
	ctx := WithGeminiAsyncImageGeneration(context.Background())
	first, err := WithImageAccountPoolRoute(ctx, "gemini-3-pro-image-preview", "2K")
	require.NoError(t, err)
	second, err := WithImageAccountPoolRoute(ctx, "gemini-3-pro-image-preview", "4K")
	require.NoError(t, err)
	scopeA, _ := ImageCircuitScopeFromContext(first)
	scopeB, _ := ImageCircuitScopeFromContext(second)
	require.NotEqual(t, scopeA, scopeB)
	accountA := &Account{ID: 12, Extra: map[string]any{"image_health_group": "same-vendor"}}
	accountB := &Account{ID: 33, Extra: map[string]any{"image_health_group": "same-vendor"}}
	idA, keyA, _ := ImageCircuitIdentity(first, accountA)
	idB, keyB, _ := ImageCircuitIdentity(first, accountB)
	require.Equal(t, idA, idB)
	require.Equal(t, keyA, keyB)
	require.NotContains(t, keyA, "same-vendor")
	_, keyC, _ := ImageCircuitIdentity(second, accountB)
	require.NotEqual(t, keyA, keyC)
	_, syncKey, _ := ImageCircuitIdentity(WithOpenAIImageGenerationIntent(context.Background()), accountB)
	require.NotEqual(t, keyA, syncKey)
}

func TestImageInputCapabilitiesAdmission(t *testing.T) {
	account := &Account{ID: 1, Extra: map[string]any{"image_input_capabilities": map[string]any{
		"transports": []any{"local"}, "max_images": 2, "max_image_bytes": 1000, "max_total_bytes": 1500, "max_image_pixels": 100, "max_total_pixels": 150,
	}}}
	base := ImageInputCapabilities{Transport: "local", Count: 2, MaxBytes: 800, TotalBytes: 1200, MaxPixels: 80, TotalPixels: 120}
	require.True(t, ImageAccountSupportsInput(WithImageInputCapabilities(context.Background(), base), account))
	for _, change := range []func(*ImageInputCapabilities){
		func(x *ImageInputCapabilities) { x.Transport = "passthrough" }, func(x *ImageInputCapabilities) { x.Count = 3 },
		func(x *ImageInputCapabilities) { x.MaxBytes = 1001 }, func(x *ImageInputCapabilities) { x.TotalBytes = 1501 },
		func(x *ImageInputCapabilities) { x.MaxPixels = 101 }, func(x *ImageInputCapabilities) { x.TotalPixels = 151 },
	} {
		input := base
		change(&input)
		ctx := WithImageInputCapabilities(context.Background(), input)
		require.False(t, ImageAccountSupportsInput(ctx, account))
		require.True(t, IsImageAccountCircuitOpen(ctx, nil, account))
	}
	unknown := ImageInputCapabilities{Transport: "local", Count: 1}
	require.True(t, ImageAccountSupportsInput(WithImageInputCapabilities(context.Background(), unknown), account))
	require.True(t, ImageAccountSupportsInput(WithImageInputCapabilities(context.Background(), base), &Account{ID: 2}))
	require.True(t, ImageAccountSupportsInput(WithImageInputCapabilities(context.Background(), ImageInputCapabilities{}), account))
}

func TestImageInputCapabilitiesValidation(t *testing.T) {
	require.NoError(t, ValidateImageAccountExtra(map[string]any{"image_health_group": "vendor", "image_input_capabilities": map[string]any{"transports": []string{"local"}, "max_image_bytes": int64(20 << 20)}}))
	for _, raw := range []any{"invalid", []any{}, map[string]any{"max_images": -1}, map[string]any{"max_image_pixels": 1.5}, map[string]any{"transports": []string{"ftp"}}, map[string]any{"max_images": "10"}, map[string]any{"unknown": 1}} {
		require.Error(t, ValidateImageAccountExtra(map[string]any{"image_input_capabilities": raw}))
	}
	require.Error(t, ValidateImageAccountExtra(map[string]any{"image_health_group": 123}))
	require.Error(t, ValidateImageAccountExtra(map[string]any{"image_health_group": " spaced "}))
}

func TestAsyncImageActualAttemptBudgetConcurrent(t *testing.T) {
	capture := &AsyncImageAccountAttemptCapture{}
	ctx := WithAsyncImageAttemptBudget(WithAsyncImageAccountAttemptCapture(context.Background(), capture), 3)
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := BeginAsyncImageUpstreamAttempt(ctx, 7)
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, ErrAsyncImageAttemptBudgetExhausted) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(3), accepted.Load())
	require.Equal(t, 3, AsyncImageUpstreamAttemptCount(ctx))
	require.Len(t, capture.Attempts(), 3)
	for _, a := range capture.Attempts() {
		require.Equal(t, AsyncImageAccountAttemptInvoked, a.Status)
	}
	cancelled, cancel := context.WithCancel(WithAsyncImageAttemptBudget(context.Background(), 2))
	cancel()
	require.ErrorIs(t, BeginAsyncImageUpstreamAttempt(cancelled, 7), context.Canceled)
	require.Zero(t, AsyncImageUpstreamAttemptCount(cancelled))
	require.NoError(t, BeginAsyncImageUpstreamAttempt(context.Background(), 7))
}

func TestImageHealthDefiniteAvailabilityFailures(t *testing.T) {
	for _, reason := range []GatewayFailureReason{AsyncImageServiceTimeout400Reason, AsyncImageInternalError422Reason, AsyncImageCapacityRejectionReason} {
		status := 400
		if reason == AsyncImageInternalError422Reason {
			status = 422
		}
		require.True(t, IsImageAccountCircuitFailure(&UpstreamFailoverError{StatusCode: status, Reason: reason, Scope: GatewayFailureScopeAccount}))
	}
	require.False(t, IsImageAccountCircuitFailure(&UpstreamFailoverError{StatusCode: 400, Reason: AsyncImageReferenceLimitReason, Scope: GatewayFailureScopeAccount}))
}
