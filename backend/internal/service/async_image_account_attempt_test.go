package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMergeAsyncImageAccountAttemptsKeepsHistoryAndDistinctIDs(t *testing.T) {
	existing, _ := json.Marshal([]AsyncImageAccountAttempt{{AccountID: 7, AccountName: "old", Status: AsyncImageAccountAttemptFailed}})
	merged, ids := MergeAsyncImageAccountAttempts(existing, []AsyncImageAccountAttempt{
		{AccountID: 2, AccountName: "new", Status: AsyncImageAccountAttemptSelected, AttemptedAt: time.Now().UTC()},
		{AccountID: 7, AccountName: "old", Status: AsyncImageAccountAttemptFailed},
	})
	var attempts []AsyncImageAccountAttempt
	var attemptedIDs []int64
	require.NoError(t, json.Unmarshal(merged, &attempts))
	require.NoError(t, json.Unmarshal(ids, &attemptedIDs))
	require.Len(t, attempts, 3)
	require.Equal(t, []int64{2, 7}, attemptedIDs)
}

func TestAsyncImageAttemptContextCopiesExcludedAccounts(t *testing.T) {
	ctx := WithAsyncImageExcludedAccountIDs(context.Background(), map[int64]struct{}{7: {}})
	ids := AsyncImageExcludedAccountIDs(ctx)
	ids[9] = struct{}{}
	require.Equal(t, map[int64]struct{}{7: {}}, AsyncImageExcludedAccountIDs(ctx))
}

func TestAsyncImageAccountAttemptTimeoutContext(t *testing.T) {
	ctx := WithAsyncImageAccountAttemptTimeout(context.Background(), 5*time.Minute)
	got, ok := AsyncImageAccountAttemptTimeout(ctx)
	require.True(t, ok)
	require.Equal(t, 5*time.Minute, got)
	_, ok = AsyncImageAccountAttemptTimeout(context.Background())
	require.False(t, ok)
}

func TestRecordAsyncImageAccountAttemptPersistsAfterParentCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	capture := &AsyncImageAccountAttemptCapture{}
	persisted := false
	capture.SetPersistor(func(ctx context.Context, _ AsyncImageAccountAttempt) {
		persisted = true
		require.NoError(t, ctx.Err(), "durable attempt persistence must not inherit request cancellation")
	})
	parent = WithAsyncImageAccountAttemptCapture(parent, capture)
	cancel()

	RecordAsyncImageAccountAttempt(parent, AsyncImageAccountAttempt{AccountID: 7, Status: AsyncImageAccountAttemptSelected})
	require.True(t, persisted)
}
