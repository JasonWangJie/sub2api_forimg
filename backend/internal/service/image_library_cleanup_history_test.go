package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type cleanupHistoryRepoStub struct {
	ImageLibraryRepository
	ImageResultCleanupRepository
	calls     int
	before    time.Time
	batchSize int
	deadline  time.Time
	err       error
	listLimit int
}

func (r *cleanupHistoryRepoStub) PruneCleanupJobHistory(ctx context.Context, before time.Time, limit int) (int64, error) {
	r.calls++
	r.before, r.batchSize = before, limit
	r.deadline, _ = ctx.Deadline()
	return 1, r.err
}

func (r *cleanupHistoryRepoStub) ListCleanupJobs(_ context.Context, limit int) ([]ImageLibraryCleanupJob, error) {
	r.listLimit = limit
	return nil, nil
}

func (r *cleanupHistoryRepoStub) ListImageResultCleanupJobs(_ context.Context, limit int) ([]ImageResultCleanupJob, error) {
	r.listLimit = limit
	return nil, nil
}

func TestImageCleanupHistoryRunsWithoutStorageAndThrottles(t *testing.T) {
	repo := &cleanupHistoryRepoStub{}
	s := NewImageLibraryMaintenanceService(&ImageLibraryService{repo: repo}, nil)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.FixedZone("Beijing", 8*60*60))
	started := time.Now()
	require.NoError(t, s.processCleanupHistory(context.Background(), now))
	require.Equal(t, now.UTC().Add(-24*time.Hour), repo.before)
	require.Equal(t, 500, repo.batchSize)
	require.WithinDuration(t, started.Add(2*time.Second), repo.deadline, time.Second)
	require.NoError(t, s.processCleanupHistory(context.Background(), now.Add(59*time.Second)))
	require.Equal(t, 1, repo.calls)
	require.NoError(t, s.processCleanupHistory(context.Background(), now.Add(time.Minute)))
	require.Equal(t, 2, repo.calls)
}

func TestImageCleanupHistoryRetriesErrorsOnNextInterval(t *testing.T) {
	repo := &cleanupHistoryRepoStub{err: errors.New("database temporarily unavailable")}
	s := NewImageLibraryMaintenanceService(&ImageLibraryService{repo: repo}, nil)
	now := time.Now()
	require.ErrorIs(t, s.processCleanupHistory(context.Background(), now), repo.err)
	require.NoError(t, s.processCleanupHistory(context.Background(), now.Add(time.Second)))
	require.Equal(t, 1, repo.calls)
	repo.err = nil
	require.NoError(t, s.processCleanupHistory(context.Background(), now.Add(time.Minute)))
	require.Equal(t, 2, repo.calls)
}

func TestImageCleanupHistoryListsAreCappedAtFive(t *testing.T) {
	repo := &cleanupHistoryRepoStub{}
	library := &ImageLibraryService{repo: repo}
	results := &ImageResultCleanupService{repo: repo}
	for _, limit := range []int{-1, 0, 1, 5, 6, 30, 100} {
		want := limit
		if want < 1 || want > 5 {
			want = 5
		}
		_, err := library.AdminListCleanupJobs(context.Background(), limit)
		require.NoError(t, err)
		require.Equal(t, want, repo.listLimit)
		_, err = results.List(context.Background(), limit)
		require.NoError(t, err)
		require.Equal(t, want, repo.listLimit)
	}
}
