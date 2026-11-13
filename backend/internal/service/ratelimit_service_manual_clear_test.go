//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// A manual cooldown reset must not double as an account-enable operation.
type manualRateLimitClearRepo struct {
	rateLimitClearRepoStub
	t *testing.T
}

func (r *manualRateLimitClearRepo) SetSchedulable(context.Context, int64, bool) error {
	r.t.Fatal("clearing cooldown must not change the scheduling switch")
	return nil
}

func (r *manualRateLimitClearRepo) ClearError(context.Context, int64) error {
	r.t.Fatal("clearing cooldown must not clear the account error status")
	return nil
}

func (r *manualRateLimitClearRepo) Update(context.Context, *Account) error {
	r.t.Fatal("clearing cooldown must not rewrite account configuration")
	return nil
}

func (r *manualRateLimitClearRepo) BulkUpdate(context.Context, []int64, AccountBulkUpdate) (int64, error) {
	r.t.Fatal("clearing cooldown must not rewrite account configuration")
	return 0, nil
}

func TestRateLimitService_ManualClearPreservesDisabledAccountAndClearsRuntimeBlock(t *testing.T) {
	repo := &manualRateLimitClearRepo{t: t}
	repo.getByIDAccount = &Account{ID: 42, Schedulable: false, Status: StatusError}
	cache := &tempUnschedCacheRecorder{}
	counter := &openAI403CounterResetStub{}
	blocker := &runtimeBlockRecorder{}
	svc := NewRateLimitService(repo, nil, nil, nil, cache)
	svc.SetOpenAI403CounterCache(counter)
	svc.SetAccountRuntimeBlocker(blocker)

	require.NoError(t, svc.ClearRateLimit(context.Background(), 42))
	require.False(t, repo.getByIDAccount.Schedulable)
	require.Equal(t, StatusError, repo.getByIDAccount.Status)
	require.Equal(t, []int64{42}, cache.deletedIDs)
	require.Equal(t, []int64{42}, counter.resetCalls)
	require.Equal(t, []int64{42}, blocker.clearedIDs)
}
