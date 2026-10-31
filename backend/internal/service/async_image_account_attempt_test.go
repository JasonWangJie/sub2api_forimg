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
	require.NotEmpty(t, records[0].AttemptID)
	require.NotEqual(t, records[0].AttemptID, records[1].AttemptID)
	saved, _ := MergeAsyncImageAccountAttempts(nil, records)
	final, _ := MergeAsyncImageAccountAttempts(saved, records)
	var merged []AsyncImageAccountAttempt
	require.NoError(t, json.Unmarshal(final, &merged))
	require.Equal(t, records, merged, "two real calls stay distinct, replaying their capture stays idempotent")
}

func TestRecordAsyncImageAccountAttemptCorrelatesSelectionSendAndOutcome(t *testing.T) {
	capture := &AsyncImageAccountAttemptCapture{}
	ctx := WithAsyncImageAccountAttemptCapture(context.Background(), capture)
	var persisted []AsyncImageAccountAttempt
	capture.SetPersistor(func(_ context.Context, attempt AsyncImageAccountAttempt) {
		persisted = append(persisted, attempt)
	})
	for _, accountID := range []int64{7, 8} {
		RecordAsyncImageAccountAttempt(ctx, AsyncImageAccountAttempt{AccountID: accountID, Status: AsyncImageAccountAttemptSelected})
		RecordAsyncImageAccountAttempt(ctx, AsyncImageAccountAttempt{AccountID: accountID, Status: AsyncImageAccountAttemptInvoked})
		outcome := AsyncImageAccountAttemptFailed
		if accountID == 8 {
			outcome = AsyncImageAccountAttemptSucceeded
		}
		RecordAsyncImageAccountAttempt(ctx, AsyncImageAccountAttempt{AccountID: accountID, Status: outcome})
	}
	records := capture.Attempts()
	require.Equal(t, records, persisted, "correlation must be assigned before immediate persistence")
	require.Len(t, records, 6)
	seenEvents := map[string]bool{}
	for i, record := range records {
		require.NotEmpty(t, record.AttemptID)
		require.NotEmpty(t, record.EventID)
		require.False(t, seenEvents[record.EventID], "each phase needs its own event identity")
		seenEvents[record.EventID] = true
		require.Equal(t, records[(i/3)*3].AttemptID, record.AttemptID)
	}
	require.NotEqual(t, records[0].AttemptID, records[3].AttemptID)
	// Same-worker result continuation reuses the capture; a restarted worker
	// reads these saved IDs. Neither merge may assign fresh identities.
	saved, _ := MergeAsyncImageAccountAttempts(nil, records)
	final, _ := MergeAsyncImageAccountAttempts(saved, capture.Attempts())
	require.JSONEq(t, string(saved), string(final))
	var recovered []AsyncImageAccountAttempt
	require.NoError(t, json.Unmarshal(final, &recovered))
	require.Equal(t, records, recovered)
}

func TestRecordAsyncImageAccountAttemptCorrelatesRetriesAndPreSendFailures(t *testing.T) {
	capture := &AsyncImageAccountAttemptCapture{}
	ctx := WithAsyncImageAccountAttemptCapture(context.Background(), capture)
	// Interleave accounts to ensure another channel's selection cannot steal
	// the first channel's pending identity. Repeated sends without selected are
	// possible inside a gateway; failed selections need an identity too.
	sequence := []struct {
		accountID int64
		status    string
		group     int
	}{
		{7, AsyncImageAccountAttemptSelected, 1},
		{8, AsyncImageAccountAttemptSelected, 2},
		{7, AsyncImageAccountAttemptInvoked, 1},
		{7, AsyncImageAccountAttemptInvoked, 3},
		{7, AsyncImageAccountAttemptSucceeded, 3}, // internal retry only reports its final success
		{7, AsyncImageAccountAttemptFailed, 3},    // later diagnostic still belongs to that send
		{8, AsyncImageAccountAttemptFailed, 2},    // selected, but no HTTP send
		{7, AsyncImageAccountAttemptSelected, 4},
		{7, AsyncImageAccountAttemptFailed, 4}, // local admission failure
		{7, AsyncImageAccountAttemptInvoked, 5},
		{7, AsyncImageAccountAttemptSucceeded, 5},
		{7, AsyncImageAccountAttemptFailed, 5}, // result-processing diagnostic
		{7, AsyncImageAccountAttemptInvoked, 6},
		{7, AsyncImageAccountAttemptSucceeded, 6},
		{7, AsyncImageAccountAttemptSelected, 7},
		{7, AsyncImageAccountAttemptSelected, 8}, // another selection is distinct
		{7, AsyncImageAccountAttemptInvoked, 8},
		{7, AsyncImageAccountAttemptSucceeded, 8},
	}
	groups := map[int]string{}
	identities := map[string]int{}
	for _, step := range sequence {
		RecordAsyncImageAccountAttempt(ctx, AsyncImageAccountAttempt{AccountID: step.accountID, Status: step.status})
		records := capture.Attempts()
		record := records[len(records)-1]
		require.NotEmpty(t, record.AttemptID)
		if id, exists := groups[step.group]; exists {
			require.Equal(t, id, record.AttemptID, "phase %s for account %d", step.status, step.accountID)
		} else {
			require.Zero(t, identities[record.AttemptID], "a new send/selection must not merge into an earlier attempt")
			groups[step.group] = record.AttemptID
			identities[record.AttemptID] = step.group
		}
	}
}

func TestAsyncImageAccountAttemptIdentityIsOptionalAndPreserved(t *testing.T) {
	legacy := AsyncImageAccountAttempt{AccountID: 7, Status: AsyncImageAccountAttemptFailed, AttemptedAt: time.Now().UTC()}
	stored, err := json.Marshal([]AsyncImageAccountAttempt{legacy})
	require.NoError(t, err)
	final, _ := MergeAsyncImageAccountAttempts(stored, nil)
	require.NotContains(t, string(final), "attempt_id", "reading old history must not invent call boundaries")
	require.NotContains(t, string(final), "event_id")

	capture := &AsyncImageAccountAttemptCapture{}
	ctx := WithAsyncImageAccountAttemptCapture(context.Background(), capture)
	explicit := AsyncImageAccountAttempt{AccountID: 7, Status: AsyncImageAccountAttemptInvoked, AttemptID: "saved-attempt", EventID: "saved-event"}
	RecordAsyncImageAccountAttempt(ctx, explicit)
	RecordAsyncImageAccountAttempt(ctx, AsyncImageAccountAttempt{AccountID: 7, Status: AsyncImageAccountAttemptSucceeded})
	records := capture.Attempts()
	require.Equal(t, explicit.AttemptID, records[0].AttemptID)
	require.Equal(t, explicit.EventID, records[0].EventID)
	require.Equal(t, explicit.AttemptID, records[1].AttemptID)
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
