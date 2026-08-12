package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestBillingSettingInvalidationPublishesAndSubscribes(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = client.Close() }()

	invalidation := NewBillingSettingInvalidation(client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- invalidation.SubscribeBillingChargeMultiplier(ctx, func(value string) {
			received <- value
		})
	}()

	require.Eventually(t, func() bool {
		return server.PubSubNumSub(billingChargeMultiplierChannel)[billingChargeMultiplierChannel] == 1
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, invalidation.PublishBillingChargeMultiplier(context.Background(), "1.25"))
	select {
	case value := <-received:
		require.Equal(t, "1.25", value)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for billing setting invalidation")
	}
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}
