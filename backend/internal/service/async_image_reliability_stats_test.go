package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type reliabilityTaskRepo struct {
	asyncImageTaskRepositoryStub
	seen AsyncImageTaskFilter
}

func (r *reliabilityTaskRepo) AsyncImageReliabilityStats(_ context.Context, f AsyncImageTaskFilter) (*AsyncImageReliabilityStats, error) {
	r.seen = f
	return &AsyncImageReliabilityStats{HTTPInvocations: 7, UnknownEvents: 2}, nil
}

func TestAsyncImageReliabilityPreservesUserScopeAndExcludesExpiry(t *testing.T) {
	r := &reliabilityTaskRepo{asyncImageTaskRepositoryStub: asyncImageTaskRepositoryStub{statusCounts: map[string]int64{AsyncImageTaskStatusSucceeded: 8, AsyncImageTaskStatusFailed: 2, AsyncImageTaskStatusExpired: 30}}}
	stats, err := NewAsyncImageTaskService(r).StatsForUser(context.Background(), 42, AsyncImageTaskFilter{})
	require.NoError(t, err)
	require.Equal(t, int64(42), *r.seen.UserID)
	require.Equal(t, int64(2), stats.Failed)
	require.Equal(t, int64(30), stats.Expired)
	require.Equal(t, float64(80), stats.SuccessRate)
	require.Equal(t, int64(7), stats.Reliability.HTTPInvocations)
}
