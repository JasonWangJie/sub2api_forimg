package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestImageHealthProbeRenewalPreservesOwnerBeyondOriginalTTL(t *testing.T) {
	ctx := context.Background()
	b, server := newImageCircuitBreakerForTest(t, service.AsyncImageRuntimeConfig{
		ImageCircuitBreakerEnabled: true, ImageCircuitBreakerFailureThreshold: 1,
		ImageCircuitBreakerCooldownSeconds: 60, AccountAttemptTimeoutSeconds: 30,
	})
	_, err := b.RecordFailure(ctx, 1, "sync")
	require.NoError(t, err)
	server.FastForward(61 * time.Second)
	token, allowed, err := b.AcquireProbe(ctx, 1, "sync")
	require.NoError(t, err)
	require.True(t, allowed)
	server.FastForward(50 * time.Second)
	renewed, err := b.RenewProbe(ctx, 1, "sync", token)
	require.NoError(t, err)
	require.True(t, renewed)
	server.FastForward(50 * time.Second)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, e := b.AcquireProbe(ctx, 1, "sync")
			if e != nil || ok {
				t.Errorf("renewed probe admitted a competing invocation: allowed=%v err=%v", ok, e)
			}
		}()
	}
	wg.Wait()
	renewed, err = b.RenewProbe(ctx, 1, "sync", "wrong-owner")
	require.NoError(t, err)
	require.False(t, renewed)
	server.FastForward(11 * time.Second)
	nextToken, allowed, err := b.AcquireProbe(ctx, 1, "sync")
	require.NoError(t, err)
	require.True(t, allowed, "wrong owner must not extend the lease; process exit still recovers by TTL")
	require.NotEqual(t, token, nextToken)
	renewed, err = b.RenewProbe(ctx, 1, "sync", token)
	require.NoError(t, err)
	require.False(t, renewed, "expired owner cannot renew a replacement token")
	require.NoError(t, b.FinishProbe(ctx, 1, "sync", nextToken, true))
	renewed, err = b.RenewProbe(ctx, 1, "sync", nextToken)
	require.NoError(t, err)
	require.False(t, renewed, "completed probe cannot be recreated by a late renewal")
}
