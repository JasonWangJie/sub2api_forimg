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
