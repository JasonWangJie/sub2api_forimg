package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type imageCircuitSettingsStub struct {
	cfg service.AsyncImageRuntimeConfig
}

func (s imageCircuitSettingsStub) RuntimeConfig(context.Context) (service.AsyncImageRuntimeConfig, error) {
	return s.cfg, nil
}

func newImageCircuitBreakerForTest(t *testing.T, cfg service.AsyncImageRuntimeConfig) (*imageAccountCircuitBreaker, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	breaker := NewImageAccountCircuitBreaker(redis.NewClient(&redis.Options{Addr: server.Addr()}))
	ConfigureImageAccountCircuitBreaker(breaker, imageCircuitSettingsStub{cfg: cfg})
	return breaker.(*imageAccountCircuitBreaker), server
}

func TestImageAccountCircuitBreaker_ConsecutiveFailuresAndSuccessReset(t *testing.T) {
	ctx := context.Background()
	breaker, server := newImageCircuitBreakerForTest(t, service.AsyncImageRuntimeConfig{
		ImageCircuitBreakerEnabled: true, ImageCircuitBreakerFailureThreshold: 5, ImageCircuitBreakerCooldownSeconds: 60,
	})

	for i := 0; i < 4; i++ {
		opened, err := breaker.RecordFailure(ctx, 101, service.ImageCircuitScopeSync)
		require.NoError(t, err)
		require.False(t, opened)
	}
	open, err := breaker.IsOpen(ctx, 101, service.ImageCircuitScopeSync)
	require.NoError(t, err)
	require.False(t, open)

	require.NoError(t, breaker.RecordSuccess(ctx, 101, service.ImageCircuitScopeSync))
	for i := 0; i < 4; i++ {
		opened, err := breaker.RecordFailure(ctx, 101, service.ImageCircuitScopeSync)
		require.NoError(t, err)
		require.False(t, opened)
	}
	opened, err := breaker.RecordFailure(ctx, 101, service.ImageCircuitScopeSync)
	require.NoError(t, err)
	require.True(t, opened)

	open, err = breaker.IsOpen(ctx, 101, service.ImageCircuitScopeSync)
	require.NoError(t, err)
	require.True(t, open)
	server.FastForward(61 * 1e9)
	open, err = breaker.IsOpen(ctx, 101, service.ImageCircuitScopeSync)
	require.NoError(t, err)
	require.False(t, open)
}

func TestImageAccountCircuitBreaker_ScopesAndDisabledSetting(t *testing.T) {
	ctx := context.Background()
	breaker, _ := newImageCircuitBreakerForTest(t, service.AsyncImageRuntimeConfig{
		ImageCircuitBreakerEnabled: true, ImageCircuitBreakerFailureThreshold: 1, ImageCircuitBreakerCooldownSeconds: 60,
	})
	opened, err := breaker.RecordFailure(ctx, 202, service.ImageCircuitScopeAsync)
	require.NoError(t, err)
	require.True(t, opened)
	open, err := breaker.IsOpen(ctx, 202, service.ImageCircuitScopeSync)
	require.NoError(t, err)
	require.False(t, open)

	disabled, _ := newImageCircuitBreakerForTest(t, service.AsyncImageRuntimeConfig{
		ImageCircuitBreakerEnabled: false, ImageCircuitBreakerFailureThreshold: 1, ImageCircuitBreakerCooldownSeconds: 60,
	})
	opened, err = disabled.RecordFailure(ctx, 303, service.ImageCircuitScopeSync)
	require.NoError(t, err)
	require.False(t, opened)
}
