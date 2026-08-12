package repository

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSessionLimitCacheSetWindowCostBatch(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := NewSessionLimitCache(rdb, 5)

	want := map[int64]float64{
		11: 1.25,
		22: 9.5,
	}
	require.NoError(t, cache.SetWindowCostBatch(context.Background(), want))

	got, err := cache.GetWindowCostBatch(context.Background(), []int64{11, 22, 33})
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Positive(t, server.TTL(windowCostKey(11)))
}
