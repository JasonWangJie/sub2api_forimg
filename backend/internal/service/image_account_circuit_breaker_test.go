package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageCircuitScopeFromContext(t *testing.T) {
	scope, ok := ImageCircuitScopeFromContext(WithOpenAIImageGenerationIntent(context.Background()))
	require.True(t, ok)
	require.Equal(t, ImageCircuitScopeSync, scope)

	scope, ok = ImageCircuitScopeFromContext(WithGeminiAsyncImageGeneration(context.Background()))
	require.True(t, ok)
	require.Equal(t, ImageCircuitScopeAsync, scope)

	_, ok = ImageCircuitScopeFromContext(context.Background())
	require.False(t, ok)
}

func TestIsImageAccountCircuitFailure(t *testing.T) {
	require.True(t, IsImageAccountCircuitFailure(&UpstreamFailoverError{StatusCode: http.StatusTooManyRequests}))
	require.True(t, IsImageAccountCircuitFailure(&OpenAIImagesUpstreamError{StatusCode: http.StatusForbidden}))
	require.True(t, IsImageAccountCircuitFailure(&OpenAIImagesUpstreamError{StatusCode: http.StatusBadGateway}))
	require.False(t, IsImageAccountCircuitFailure(&OpenAIImagesUpstreamError{StatusCode: http.StatusBadRequest}))
	require.False(t, IsImageAccountCircuitFailure(&UpstreamFailoverError{StatusCode: http.StatusBadRequest}))
	require.False(t, IsImageAccountCircuitFailure(&UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, Scope: GatewayFailureScopeRequest}))
}
