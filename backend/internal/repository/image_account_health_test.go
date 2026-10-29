package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestImageHealthRollingFailureRateAndLateSuccess(t *testing.T) {
	ctx := context.Background()
	b, _ := newImageCircuitBreakerForTest(t, service.AsyncImageRuntimeConfig{ImageCircuitBreakerEnabled: true, ImageCircuitBreakerFailureThreshold: 6, ImageCircuitBreakerCooldownSeconds: 60})
	// Interleaved successes must not conceal a 50% failure rate, even though no
	// consecutive streak reaches the configured threshold.
	for i := 0; i < 6; i++ {
		require.NoError(t, b.RecordSuccess(ctx, 1, "async:route:A"))
		_, err := b.RecordFailure(ctx, 1, "async:route:A")
		require.NoError(t, err)
	}
	open, err := b.IsOpen(ctx, 1, "async:route:A")
	require.NoError(t, err)
	require.True(t, open)
	require.NoError(t, b.RecordSuccess(ctx, 1, "async:route:B"))
	require.NoError(t, b.RecordSuccess(ctx, 1, "async:route:A"))
	open, err = b.IsOpen(ctx, 1, "async:route:A")
	require.NoError(t, err)
	require.True(t, open, "late success must not clear a newer open circuit")
	open, err = b.IsOpen(ctx, 1, "async:route:B")
	require.NoError(t, err)
	require.False(t, open)
}

func TestImageHealthHalfOpenOnlyOneProbeAndOwnership(t *testing.T) {
	ctx := context.Background()
	b, server := newImageCircuitBreakerForTest(t, service.AsyncImageRuntimeConfig{ImageCircuitBreakerEnabled: true, ImageCircuitBreakerFailureThreshold: 1, ImageCircuitBreakerCooldownSeconds: 60, AccountAttemptTimeoutSeconds: 30})
	_, err := b.RecordFailure(ctx, 1, "async:route:A")
	require.NoError(t, err)
	_, allowed, err := b.AcquireProbe(ctx, 1, "async:route:A")
	require.NoError(t, err)
	require.False(t, allowed)
	server.FastForward(61 * time.Second)
	var admitted atomic.Int32
	var token string
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, ok, e := b.AcquireProbe(ctx, 1, "async:route:A")
			if e != nil {
				t.Error(e)
			}
			if ok {
				admitted.Add(1)
				mu.Lock()
				token = got
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), admitted.Load())
	require.NotEmpty(t, token)
	require.NoError(t, b.FinishProbe(ctx, 1, "async:route:A", "wrong-owner", true))
	_, allowed, err = b.AcquireProbe(ctx, 1, "async:route:A")
	require.NoError(t, err)
	require.False(t, allowed)
	require.NoError(t, b.FinishProbe(ctx, 1, "async:route:A", token, true))
	for i := 0; i < 3; i++ {
		got, ok, e := b.AcquireProbe(ctx, 1, "async:route:A")
		require.NoError(t, e)
		require.True(t, ok)
		require.Empty(t, got)
	}
}

func TestImageCapabilityCacheExpiresAndDoesNotCrossRoutes(t *testing.T) {
	ctx := context.Background()
	b, server := newImageCircuitBreakerForTest(t, service.AsyncImageRuntimeConfig{ImageCircuitBreakerEnabled: true, ImageCircuitBreakerFailureThreshold: 5, ImageCircuitBreakerCooldownSeconds: 60})
	require.NoError(t, b.RecordCapabilityFailure(ctx, 1, "async:route:2K"))
	blocked, err := b.IsOpen(ctx, 1, "async:route:2K")
	require.NoError(t, err)
	require.True(t, blocked)
	blocked, err = b.IsOpen(ctx, 1, "async:route:4K")
	require.NoError(t, err)
	require.False(t, blocked)
	server.FastForward(301 * time.Second)
	blocked, err = b.IsOpen(ctx, 1, "async:route:2K")
	require.NoError(t, err)
	require.False(t, blocked)
}
