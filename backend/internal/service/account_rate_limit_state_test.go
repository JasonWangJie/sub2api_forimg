//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type atomicRateLimitRepoStub struct {
	rateLimitClearRepoStub
	state                 *AccountRateLimitState
	clearErr, snapshotErr error
	calls                 int
	onClear               func()
}

func (r *atomicRateLimitRepoStub) GetAccountRateLimitState(context.Context, int64) (*AccountRateLimitState, error) {
	return r.state, nil
}
func (r *atomicRateLimitRepoStub) ClearAccountRateLimitState(_ context.Context, id int64, expected string) (*AccountRateLimitState, *AccountRateLimitState, error) {
	r.calls++
	if r.clearErr != nil {
		return nil, nil, r.clearErr
	}
	if expected != "" && expected != r.state.Version() {
		return nil, nil, ErrRateLimitStateChanged
	}
	if r.onClear != nil {
		r.onClear()
	}
	return r.state, &AccountRateLimitState{AccountID: id, Status: r.state.Status, Schedulable: r.state.Schedulable}, nil
}
func (r *atomicRateLimitRepoStub) SyncAccountRateLimitSnapshot(context.Context, int64) error {
	return r.snapshotErr
}
func TestAccountRateLimitPreviewCanonicalBoundedAndPrivate(t *testing.T) {
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	state := &AccountRateLimitState{AccountID: 42, Status: "active", RateLimitResetAt: &future, TempUnschedulableReason: ptrForRateLimit("secret upstream text")}
	models := map[string]any{}
	for i := 0; i < 120; i++ {
		models[time.Unix(int64(i), 0).Format(time.RFC3339)] = map[string]any{"rate_limit_reset_at": future}
	}
	state.ModelRateLimits, _ = json.Marshal(models)
	p := state.Preview(time.Now())
	require.Len(t, p.Restrictions, 100)
	require.Equal(t, 121, p.ActiveCount)
	require.True(t, p.Truncated)
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "secret upstream text")
	state.ModelRateLimits = json.RawMessage(`{"b":2,"a":1}`)
	v := state.Version()
	state.ModelRateLimits = json.RawMessage(`{ "a":1, "b":2 }`)
	require.Equal(t, v, state.Version())
	state.ModelRateLimits = json.RawMessage(`{"generation":9007199254740992}`)
	v = state.Version()
	state.ModelRateLimits = json.RawMessage(`{"generation":9007199254740993}`)
	require.NotEqual(t, v, state.Version())
	require.Equal(t, "model", safeRateLimitScope("https://example.test/?key=secret"))

	state.RateLimitResetAt = &past
	v = state.Version()
	past2 := past.Add(-time.Second)
	state.RateLimitResetAt = &past2
	require.NotEqual(t, v, state.Version(), "expired generation changes must invalidate preview")
}
func ptrForRateLimit(s string) *string { return &s }
func TestAccountRateLimitAtomicCacheWarningAndConflict(t *testing.T) {
	future := time.Now().Add(time.Hour)
	repo := &atomicRateLimitRepoStub{state: &AccountRateLimitState{AccountID: 42, Status: "error", Schedulable: false, RateLimitResetAt: &future}, snapshotErr: errors.New("cache")}
	cache := &manualTempCacheStub{tempUnschedCacheRecorder: tempUnschedCacheRecorder{deleteErr: errors.New("redis")}}
	blocker := &manualRuntimeBlockStub{}
	svc := NewRateLimitService(repo, nil, nil, nil, cache)
	svc.SetAccountRuntimeBlocker(blocker)
	result, err := svc.ClearAccountRateLimitPreview(context.Background(), 42, repo.state.Version())
	require.NoError(t, err)
	require.Equal(t, "warning", result.CacheSync.Status)
	require.ElementsMatch(t, []string{"temp_unsched", "scheduler_snapshot"}, result.CacheSync.FailedComponents)
	require.False(t, result.After.Schedulable)
	require.Equal(t, "error", result.After.Status)
	require.Equal(t, []int64{42}, blocker.clearedIDs)
	require.Zero(t, repo.clearRateLimitCalls, "legacy multi-step path must not execute for atomic repositories")
	repo.clearErr = ErrRateLimitStateChanged
	cache.deletedIDs = nil
	blocker.clearedIDs = nil
	_, err = svc.ClearAccountRateLimitPreview(context.Background(), 42, repo.state.Version())
	require.ErrorIs(t, err, ErrRateLimitStateChanged)
	require.Empty(t, cache.deletedIDs)
	require.Empty(t, blocker.clearedIDs)
	_, err = svc.ClearAccountRateLimitPreview(context.Background(), 42, "")
	require.Error(t, err)
}
func TestAccountRateLimitManualRecoveryCannotBypassPreview(t *testing.T) {
	future := time.Now().Add(time.Hour)
	repo := &atomicRateLimitRepoStub{state: &AccountRateLimitState{AccountID: 42, Status: "error", RateLimitResetAt: &future}}
	repo.getByIDAccount = &Account{ID: 42, Status: StatusError}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	err := svc.RecoverAccountErrorManually(context.Background(), 42)
	require.Error(t, err)
	require.Zero(t, repo.clearErrorCalls)
	require.Zero(t, repo.calls)
	repo.state.RateLimitResetAt = nil
	require.NoError(t, svc.RecoverAccountErrorManually(context.Background(), 42))
	require.Equal(t, 1, repo.clearErrorCalls)
	require.Zero(t, repo.calls, "error-only recovery never clears restrictions")
}

type manualTempCacheStub struct{ tempUnschedCacheRecorder }

func (c *manualTempCacheStub) SnapshotTempUnsched(context.Context, int64) (string, error) {
	return "observed", nil
}
func (c *manualTempCacheStub) DeleteTempUnschedIfObserved(ctx context.Context, id int64, _ string) (bool, error) {
	err := c.DeleteTempUnsched(ctx, id)
	return err == nil, err
}

type manualRuntimeBlockStub struct{ runtimeBlockRecorder }

func (b *manualRuntimeBlockStub) SnapshotAccountSchedulingBlock(int64) (uint64, time.Time) {
	return 1, time.Time{}
}
func (b *manualRuntimeBlockStub) ClearAccountSchedulingBlockIfObserved(id int64, _ uint64) bool {
	b.ClearAccountSchedulingBlock(id)
	return true
}
func TestAccountRateLimitManualClearKeepsNewRuntimeGeneration(t *testing.T) {
	gateway := &OpenAIGatewayService{}
	account := &Account{ID: 42, Platform: PlatformOpenAI}
	gateway.BlockAccountScheduling(account, time.Now().Add(time.Hour), "old")
	observed, _ := gateway.SnapshotAccountSchedulingBlock(42)
	gateway.BlockAccountScheduling(account, time.Now().Add(2*time.Hour), "new")
	require.False(t, gateway.ClearAccountSchedulingBlockIfObserved(42, observed))
	require.True(t, gateway.isOpenAIAccountRuntimeBlocked(account))
	current, _ := gateway.SnapshotAccountSchedulingBlock(42)
	require.True(t, gateway.ClearAccountSchedulingBlockIfObserved(42, current))
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
}

type manual403CounterStub struct {
	OpenAI403CounterCache
	resetCalls int
}

func (c *manual403CounterStub) SnapshotOpenAI403Count(context.Context, int64) (string, error) {
	return "5", nil
}
func (c *manual403CounterStub) ResetOpenAI403Count(context.Context, int64) error {
	c.resetCalls++
	return nil
}
func TestAccountRateLimitManualPostCommitPreservesNewAndUnknownRuntime(t *testing.T) {
	for _, scenario := range []string{"new_after_capture", "unknown_before_capture", "matching_old"} {
		t.Run(scenario, func(t *testing.T) {
			until := time.Now().Add(time.Hour)
			gateway := &OpenAIGatewayService{}
			account := &Account{ID: 42, Platform: PlatformOpenAI}
			runtimeUntil := until
			if scenario == "unknown_before_capture" {
				runtimeUntil = until.Add(time.Hour)
			}
			gateway.BlockAccountScheduling(account, runtimeUntil, "old")
			repo := &atomicRateLimitRepoStub{state: &AccountRateLimitState{AccountID: 42, RateLimitResetAt: &until}}
			if scenario == "new_after_capture" {
				repo.onClear = func() { gateway.BlockAccountScheduling(account, until.Add(time.Hour), "new") }
			}
			counter := &manual403CounterStub{}
			svc := NewRateLimitService(repo, nil, nil, nil, nil)
			svc.SetAccountRuntimeBlocker(gateway)
			svc.SetOpenAI403CounterCache(counter)
			result, err := svc.ClearAccountRateLimitPreview(context.Background(), 42, repo.state.Version())
			require.NoError(t, err)
			require.Equal(t, 1, repo.calls)
			require.Zero(t, counter.resetCalls)
			require.Contains(t, result.CacheSync.FailedComponents, "openai_403")
			if scenario == "matching_old" {
				require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
				require.NotContains(t, result.CacheSync.FailedComponents, "runtime_block")
			} else {
				require.True(t, gateway.isOpenAIAccountRuntimeBlocked(account))
				require.Contains(t, result.CacheSync.FailedComponents, "runtime_block")
			}
		})
	}
}
