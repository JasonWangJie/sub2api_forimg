package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestImageHealthHalfOpenFailureReopensBeforeRelease(t *testing.T) {
	ctx := context.Background()
	b, server := newImageCircuitBreakerForTest(t, service.AsyncImageRuntimeConfig{
		ImageCircuitBreakerEnabled: true, ImageCircuitBreakerFailureThreshold: 5, ImageCircuitBreakerCooldownSeconds: 60,
	})
	for i := 0; i < 5; i++ {
		_, err := b.RecordFailure(ctx, 1, "async")
		require.NoError(t, err)
	}
	server.FastForward(61 * time.Second)
	token, allowed, err := b.AcquireProbe(ctx, 1, "async")
	require.NoError(t, err)
	require.True(t, allowed)
	require.NotEmpty(t, token)
	require.NoError(t, b.FinishProbeResult(ctx, 1, "async", token, false, true))
	_, allowed, err = b.AcquireProbe(ctx, 1, "async")
	require.NoError(t, err)
	require.False(t, allowed, "one health failure after half-open must immediately cool down, even with threshold five")
	failures, key := imageCircuitKeys(1, "async")
	require.False(t, server.Exists(key+":probe"), "completed probe must not block until its lease expires")
	require.False(t, server.Exists(failures), "ordinary result reporting owns the single failure sample")
	server.FastForward(61 * time.Second)
	token, allowed, err = b.AcquireProbe(ctx, 1, "async")
	require.NoError(t, err)
	require.True(t, allowed)
	require.NoError(t, b.FinishProbeResult(ctx, 1, "async", token, false, false))
	nextToken, allowed, err := b.AcquireProbe(ctx, 1, "async")
	require.NoError(t, err)
	require.True(t, allowed, "content, cancellation and budget rejection release without health cooldown")
	require.NotEmpty(t, nextToken, "a non-health rejection does not prove channel recovery")
	require.NoError(t, b.FinishProbeResult(ctx, 1, "async", token, false, true))
	require.False(t, server.Exists(key), "a stale owner cannot reopen or release a newer probe")
	require.NoError(t, b.FinishProbeResult(ctx, 1, "async", nextToken, true, false))
	token, allowed, err = b.AcquireProbe(ctx, 1, "async")
	require.NoError(t, err)
	require.True(t, allowed)
	require.Empty(t, token, "successful final result restores normal admission")
}
