package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestAccountRateLimitTempCacheConditionalDelete(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { client.Close() })
	cache := &tempUnschedCache{rdb: client}
	ctx := context.Background()
	id := int64(42)
	first := &service.TempUnschedState{UntilUnix: time.Now().Add(time.Hour).Unix(), TriggeredAtUnix: time.Now().Unix()}
	require.NoError(t, cache.SetTempUnsched(ctx, id, first))
	token, err := cache.SnapshotTempUnsched(ctx, id)
	require.NoError(t, err)
	newer := *first
	newer.UntilUnix += 3600
	require.NoError(t, cache.SetTempUnsched(ctx, id, &newer))
	cleared, err := cache.DeleteTempUnschedIfObserved(ctx, id, token)
	require.NoError(t, err)
	require.False(t, cleared)
	actual, err := cache.GetTempUnsched(ctx, id)
	require.NoError(t, err)
	require.Equal(t, newer.UntilUnix, actual.UntilUnix)
	current, err := cache.SnapshotTempUnsched(ctx, id)
	require.NoError(t, err)
	cleared, err = cache.DeleteTempUnschedIfObserved(ctx, id, current)
	require.NoError(t, err)
	require.True(t, cleared)
	// A fresh observation with an equal or shorter deadline still rotates
	// generation without shortening the existing protective deadline.
	require.NoError(t, cache.SetTempUnsched(ctx, id, &newer))
	for _, delta := range []int64{0, -60} {
		observed, err := cache.SnapshotTempUnsched(ctx, id)
		require.NoError(t, err)
		fresh := newer
		fresh.UntilUnix += delta
		require.NoError(t, cache.SetTempUnsched(ctx, id, &fresh))
		cleared, err = cache.DeleteTempUnschedIfObserved(ctx, id, observed)
		require.NoError(t, err)
		require.False(t, cleared)
		actual, err = cache.GetTempUnsched(ctx, id)
		require.NoError(t, err)
		require.Equal(t, newer.UntilUnix, actual.UntilUnix)
	}
	current, err = cache.SnapshotTempUnsched(ctx, id)
	require.NoError(t, err)
	require.NoError(t, cache.DeleteTempUnsched(ctx, id))
	// Same timestamps after delete/reinsert get a distinct generation (ABA safe).
	require.NoError(t, cache.SetTempUnsched(ctx, id, &newer))
	cleared, err = cache.DeleteTempUnschedIfObserved(ctx, id, current)
	require.NoError(t, err)
	require.False(t, cleared)
	// An absent observation must not delete a subsequently created marker.
	cleared, err = cache.DeleteTempUnschedIfObserved(ctx, id, "")
	require.NoError(t, err)
	require.False(t, cleared)
	// A sub-millisecond remaining TTL must not become a persistent key.
	server.SetTTL("temp_unsched:account:42", 500*time.Microsecond)
	require.NoError(t, cache.SetTempUnsched(ctx, id, &newer))
	require.Greater(t, server.TTL("temp_unsched:account:42"), time.Duration(0))
	require.LessOrEqual(t, server.TTL("temp_unsched:account:42"), time.Millisecond)
	// Older versions without a generation cannot safely be conditionally reset.
	require.NoError(t, client.Set(ctx, "temp_unsched:account:42", `{"until_unix":9999999999}`, time.Hour).Err())
	legacy, err := cache.SnapshotTempUnsched(ctx, id)
	require.NoError(t, err)
	cleared, err = cache.DeleteTempUnschedIfObserved(ctx, id, legacy)
	require.NoError(t, err)
	require.False(t, cleared)
}

type rateLimitInvalidationCache struct {
	service.SchedulerCache
	deleted []int64
	t       *testing.T
}

func (c *rateLimitInvalidationCache) DeleteAccount(_ context.Context, id int64) error {
	c.deleted = append(c.deleted, id)
	return nil
}
func (c *rateLimitInvalidationCache) SetAccount(context.Context, *service.Account) error {
	c.t.Fatal("must invalidate instead of writing a possibly stale account snapshot")
	return nil
}
func TestAccountRateLimitSnapshotInvalidatesWithoutStaleWrite(t *testing.T) {
	cache := &rateLimitInvalidationCache{t: t}
	repo := &accountRepository{schedulerCache: cache}
	require.NoError(t, repo.SyncAccountRateLimitSnapshot(context.Background(), 42))
	require.Equal(t, []int64{42}, cache.deleted)
}
