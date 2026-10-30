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

func TestMergeAsyncImageAccountAttemptsIsIdempotentAcrossResultRecovery(t *testing.T) {
	now := time.Date(2026, 10, 30, 5, 31, 30, 411942319, time.UTC)
	capture := []AsyncImageAccountAttempt{
		{AccountID: 38, AccountName: "image-account", Status: AsyncImageAccountAttemptSelected, AttemptedAt: now.Add(-time.Millisecond)},
		{AccountID: 38, Status: AsyncImageAccountAttemptInvoked, AttemptedAt: now},
		{AccountID: 38, AccountName: "image-account", Status: AsyncImageAccountAttemptSucceeded, AttemptedAt: now.Add(time.Minute)},
	}
	// Result URL persistence and finalization both see the same context capture.
	snapshot, ids := MergeAsyncImageAccountAttempts(nil, capture)
	final, finalIDs := MergeAsyncImageAccountAttempts(snapshot, capture)
	require.JSONEq(t, string(snapshot), string(final))
	require.JSONEq(t, string(ids), string(finalIDs))
	// Historical duplicated rows are normalized on the next merge as well.
	duplicated, err := json.Marshal(append(append([]AsyncImageAccountAttempt{}, capture...), capture...))
	require.NoError(t, err)
	normalized, _ := MergeAsyncImageAccountAttempts(duplicated, capture)
	require.JSONEq(t, string(snapshot), string(normalized))
}

func TestDeduplicateAsyncImageAccountAttemptsPreservesRealRetriesAndDiagnostics(t *testing.T) {
	now := time.Date(2026, 10, 30, 5, 31, 30, 411942319, time.UTC)
	first := AsyncImageAccountAttempt{AccountID: 7, Status: AsyncImageAccountAttemptInvoked, AttemptedAt: now}
	retry := first
	retry.AttemptedAt = now.Add(time.Nanosecond)
	diagnostic := first
	diagnostic.UpstreamRequestID = "distinct-provider-request"
	legacy := AsyncImageAccountAttempt{AccountID: 7, Status: AsyncImageAccountAttemptInvoked}
	input := []AsyncImageAccountAttempt{first, first, retry, diagnostic, legacy, legacy}
	before := append([]AsyncImageAccountAttempt{}, input...)
	got := DeduplicateAsyncImageAccountAttempts(input)
	require.Equal(t, []AsyncImageAccountAttempt{first, retry, diagnostic, legacy, legacy}, got)
	require.Equal(t, before, input, "normalizing a read must not mutate persisted history")
	require.Empty(t, DeduplicateAsyncImageAccountAttempts(nil))
}

func TestRecordAsyncImageAccountAttemptDistinguishesCallsAtTheSameClockTime(t *testing.T) {
	capture := &AsyncImageAccountAttemptCapture{}
	ctx := WithAsyncImageAccountAttemptCapture(context.Background(), capture)
	attempt := AsyncImageAccountAttempt{AccountID: 7, Status: AsyncImageAccountAttemptInvoked, AttemptedAt: time.Date(2026, 10, 30, 5, 31, 30, 0, time.UTC)}
	RecordAsyncImageAccountAttempt(ctx, attempt)
	RecordAsyncImageAccountAttempt(ctx, attempt)
	records := capture.Attempts()
	require.Len(t, records, 2)
	require.NotEmpty(t, records[0].EventID)
	require.NotEqual(t, records[0].EventID, records[1].EventID)
	saved, _ := MergeAsyncImageAccountAttempts(nil, records)
	final, _ := MergeAsyncImageAccountAttempts(saved, records)
	var merged []AsyncImageAccountAttempt
	require.NoError(t, json.Unmarshal(final, &merged))
	require.Equal(t, records, merged, "two real calls stay distinct, replaying their capture stays idempotent")
}

func TestAsyncImageAccountAttemptJSONPreservesProviderDiagnostics(t *testing.T) {
	attempt := AsyncImageAccountAttempt{
		AccountID: 7, Status: AsyncImageAccountAttemptFailed, StatusCode: 400,
		ProviderErrorCode: "INVALID_ARGUMENT", UpstreamRequestID: "req-1",
		Error: "image: at most 8 images are allowed", AttemptedAt: time.Now().UTC(),
	}
	encoded, err := json.Marshal(attempt)
	require.NoError(t, err)

	var decoded AsyncImageAccountAttempt
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, attempt.ProviderErrorCode, decoded.ProviderErrorCode)
	require.Equal(t, attempt.UpstreamRequestID, decoded.UpstreamRequestID)
	require.Equal(t, attempt.Error, decoded.Error)
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
