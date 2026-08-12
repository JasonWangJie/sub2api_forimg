package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageQueueReliableLifecycle(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	queue := NewAsyncImageQueue(client)
	ctx := context.Background()
	taskID := "asyncimg_0123456789abcdef"

	require.NoError(t, queue.Enqueue(ctx, taskID))
	require.ErrorIs(t, queue.Enqueue(ctx, taskID), service.ErrAsyncImageAlreadyQueued)

	reserved, err := queue.Reserve(ctx, time.Second)
	require.NoError(t, err)
	require.Equal(t, taskID, reserved.TaskID)
	require.NotEmpty(t, reserved.LeaseToken)
	require.NoError(t, queue.Heartbeat(ctx, reserved))
	require.NoError(t, queue.RequeueAfter(ctx, reserved, 5*time.Second))

	moved, err := queue.MoveDueDelayedToReady(ctx, 10)
	require.NoError(t, err)
	require.Zero(t, moved)
	require.NoError(t, client.ZAdd(ctx, asyncImageDelayedKey, redis.Z{Score: float64(time.Now().Add(-time.Second).UnixMilli()), Member: taskID}).Err())
	moved, err = queue.MoveDueDelayedToReady(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, moved)

	reserved, err = queue.Reserve(ctx, time.Second)
	require.NoError(t, err)
	require.Equal(t, taskID, reserved.TaskID)
	require.NoError(t, queue.Ack(ctx, reserved))
	require.NoError(t, queue.Enqueue(ctx, taskID), "ack must release the inflight dedup key")
}

func TestAsyncImageQueueRejectsStaleLeaseOperations(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	queue := NewAsyncImageQueue(client)
	ctx := context.Background()
	taskID := "asyncimg_lease_owner"

	require.NoError(t, queue.Enqueue(ctx, taskID))
	oldLease, err := queue.Reserve(ctx, time.Second)
	require.NoError(t, err)

	client.ZAdd(ctx, asyncImageActiveKey, redis.Z{Score: float64(time.Now().Add(-time.Minute).UnixMilli()), Member: taskID})
	recovered, err := queue.RecoverStaleActive(ctx, time.Second, 10)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	newLease, err := queue.Reserve(ctx, time.Second)
	require.NoError(t, err)
	require.NotEqual(t, oldLease.LeaseToken, newLease.LeaseToken)

	require.ErrorIs(t, queue.Heartbeat(ctx, oldLease), service.ErrAsyncImageQueueLeaseLost)
	require.ErrorIs(t, queue.Ack(ctx, oldLease), service.ErrAsyncImageQueueLeaseLost)
	require.ErrorIs(t, queue.RequeueAfter(ctx, oldLease, time.Second), service.ErrAsyncImageQueueLeaseLost)
	require.NoError(t, queue.Heartbeat(ctx, newLease))
	require.NoError(t, queue.Ack(ctx, newLease))
}

func TestAsyncImageQueueRejectsBadPayloadAndReportsEmpty(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	queue := NewAsyncImageQueue(client)
	ctx := context.Background()

	require.ErrorIs(t, queue.Enqueue(ctx, "not-a-task"), service.ErrAsyncImageQueueBadPayload)
	_, err := queue.Reserve(ctx, time.Millisecond)
	require.True(t, errors.Is(err, service.ErrAsyncImageQueueEmpty))
}
