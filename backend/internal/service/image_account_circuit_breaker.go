package service

import (
	"context"
	"errors"
	"net/http"
)

const (
	ImageCircuitScopeSync  = "sync"
	ImageCircuitScopeAsync = "async"
)

// ImageAccountCircuitBreaker tracks consecutive account-level image failures.
// Implementations must be safe for concurrent requests and may fail open when
// their backing store is unavailable.
type ImageAccountCircuitBreaker interface {
	IsOpen(context.Context, int64, string) (bool, error)
	RecordFailure(context.Context, int64, string) (bool, error)
	RecordSuccess(context.Context, int64, string) error
}

type ImageCircuitBreakerSettingsProvider interface {
	RuntimeConfig(context.Context) (AsyncImageRuntimeConfig, error)
}

// ImageCircuitScopeFromContext identifies image requests without making
// ordinary text traffic participate in the account circuit.
func ImageCircuitScopeFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	if AsyncImageAccountAttemptCaptureFromContext(ctx) != nil || IsGeminiAsyncImageGeneration(ctx) {
		return ImageCircuitScopeAsync, true
	}
	if OpenAIImageGenerationIntentFromContext(ctx) {
		return ImageCircuitScopeSync, true
	}
	if GeminiImageGenerationIntentFromContext(ctx) {
		return ImageCircuitScopeSync, true
	}
	return "", false
}

func ReportImageAccountResult(ctx context.Context, breaker ImageAccountCircuitBreaker, accountID int64, success bool, err error) {
	if breaker == nil || accountID <= 0 {
		return
	}
	scope, ok := ImageCircuitScopeFromContext(ctx)
	if !ok {
		return
	}
	if success {
		_ = breaker.RecordSuccess(ctx, accountID, scope)
		return
	}
	if err == nil {
		return
	}
	if IsImageAccountCircuitFailure(err) {
		_, _ = breaker.RecordFailure(ctx, accountID, scope)
	}
}

// IsImageAccountCircuitFailure excludes request/content/storage errors from
// account health while retaining authentication, throttling and upstream
// availability failures.
func IsImageAccountCircuitFailure(err error) bool {
	if err == nil {
		return false
	}
	var upstream *UpstreamFailoverError
	if errors.As(err, &upstream) {
		if upstream.StatusCode == http.StatusRequestTimeout || upstream.StatusCode == http.StatusTooManyRequests ||
			upstream.StatusCode == http.StatusUnauthorized || upstream.StatusCode == http.StatusForbidden ||
			upstream.StatusCode >= 500 {
			return !upstream.RequestScopedTransient && upstream.Scope != GatewayFailureScopeRequest
		}
		return false
	}
	var images *OpenAIImagesUpstreamError
	if errors.As(err, &images) {
		return images.StatusCode == http.StatusRequestTimeout || images.StatusCode == http.StatusTooManyRequests ||
			images.StatusCode == http.StatusUnauthorized || images.StatusCode == http.StatusForbidden || images.StatusCode >= 500
	}
	return false
}
