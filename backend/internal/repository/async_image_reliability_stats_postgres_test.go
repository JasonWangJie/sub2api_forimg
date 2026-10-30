//go:build postgres

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageReliabilityStatsDuplicateAttemptsPostgres(t *testing.T) {
	db := openAsyncImagePostgresTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `SET LOCAL search_path=pg_temp;
CREATE TEMP TABLE async_image_tasks (
 task_id text PRIMARY KEY, user_id bigint NOT NULL, platform text NOT NULL,
 status text NOT NULL, error_code text, billing_status text NOT NULL,
 actual_cost numeric NOT NULL, account_attempts jsonb, attempted_account_ids jsonb NOT NULL,
 reference_transport text NOT NULL, reference_retry_count integer NOT NULL, retry_count integer NOT NULL,
 submitted_at timestamptz NOT NULL, started_at timestamptz, upstream_succeeded_at timestamptz, finished_at timestamptz
) ON COMMIT DROP;
CREATE TEMP TABLE async_image_events (
 task_id text NOT NULL, event_type text NOT NULL, to_status text, created_at timestamptz NOT NULL
) ON COMMIT DROP;
INSERT INTO async_image_tasks VALUES
 ('target',42,'openai','succeeded',NULL,'succeeded',0.09,'[]','[7]','passthrough',0,0,
  '2026-10-30T05:31:29Z','2026-10-30T05:31:30Z','2026-10-30T05:32:23Z','2026-10-30T05:32:24Z'),
 ('other-user',43,'openai','succeeded',NULL,'succeeded',100,'[]','[7]','passthrough',0,0,
  '2026-10-30T05:31:29Z','2026-10-30T05:31:30Z','2026-10-30T05:32:23Z','2026-10-30T05:32:24Z');
INSERT INTO async_image_events VALUES
 ('target','invocation_started','invoking','2026-10-30T05:31:30Z'),
 ('target','upstream_succeeded','upstream_succeeded','2026-10-30T05:32:23Z'),
 ('target','upstream_succeeded','upstream_succeeded','2026-10-30T05:32:23Z'),
 ('other-user','result_fetch_retry',NULL,'2026-10-30T05:32:23Z');`)
	require.NoError(t, err)
	repo := &asyncImageTaskRepository{db: db, sql: tx}
	uid := int64(42)
	const firstTime = "2026-10-30T05:31:30.411942319Z"
	captured := func(timestamp any, omitTimestamp bool) []map[string]any {
		var attempts []map[string]any
		for _, status := range []string{"selected", "invocation_started", "succeeded"} {
			attempt := map[string]any{"account_id": 7, "status": status}
			if !omitTimestamp {
				attempt["attempted_at"] = timestamp
			}
			attempts = append(attempts, attempt)
		}
		return attempts
	}
	duplicate := func(attempts []map[string]any) []map[string]any {
		return append(append([]map[string]any{}, attempts...), attempts...)
	}
	for _, tc := range []struct {
		name     string
		attempts []map[string]any
		calls    int64
		selected int64
		first    int64
		tracked  int64
	}{
		{name: "duplicated result-fetch capture is one call", attempts: duplicate(captured(firstTime, false)), calls: 1, selected: 1, first: 1, tracked: 1},
		{name: "same account retried one nanosecond later", attempts: append(captured(firstTime, false), captured("2026-10-30T05:31:30.411942320Z", false)...), calls: 2, selected: 2, tracked: 1},
		{name: "different complete records at the same time", attempts: []map[string]any{
			{"account_id": 7, "status": "invocation_started", "attempted_at": firstTime, "upstream_request_id": "request-1"},
			{"account_id": 7, "status": "invocation_started", "attempted_at": firstTime, "upstream_request_id": "request-2"},
		}, calls: 2, tracked: 1},
		{name: "unknown metadata remains part of the identity", attempts: []map[string]any{
			{"account_id": 7, "status": "invocation_started", "attempted_at": firstTime, "future_field": "first"},
			{"account_id": 7, "status": "invocation_started", "attempted_at": firstTime, "future_field": "second"},
		}, calls: 2, tracked: 1},
		{name: "valid offset timestamp", attempts: duplicate(captured("2026-10-30T13:31:30.411942319+08:00", false)), calls: 1, selected: 1, first: 1, tracked: 1},
		{name: "nonzero nanosecond after year one", attempts: duplicate(captured("0001-01-01T00:00:00.000000001Z", false)), calls: 1, selected: 1, first: 1, tracked: 1},
		{name: "valid leap century", attempts: duplicate(captured("2000-02-29T05:31:30Z", false)), calls: 1, selected: 1, first: 1, tracked: 1},
		{name: "missing timestamps remain independent", attempts: duplicate(captured(nil, true)), calls: 2, selected: 2, tracked: 1},
		{name: "empty timestamps remain independent", attempts: duplicate(captured("", false)), calls: 2, selected: 2, tracked: 1},
		{name: "null timestamps remain independent", attempts: duplicate(captured(nil, false)), calls: 2, selected: 2, tracked: 1},
		{name: "numeric timestamps remain independent", attempts: duplicate(captured(123, false)), calls: 2, selected: 2, tracked: 1},
		{name: "zero timestamps remain independent", attempts: duplicate(captured("0001-01-01T00:00:00Z", false)), calls: 2, selected: 2, tracked: 1},
		{name: "zero instant with offset remains independent", attempts: duplicate(captured("0001-01-01T08:00:00+08:00", false)), calls: 2, selected: 2, tracked: 1},
		{name: "malformed timestamps remain independent", attempts: duplicate(captured("not-a-time", false)), calls: 2, selected: 2, tracked: 1},
		{name: "invalid calendar date remains independent", attempts: duplicate(captured("2026-02-30T05:31:30Z", false)), calls: 2, selected: 2, tracked: 1},
		{name: "invalid leap century remains independent", attempts: duplicate(captured("1900-02-29T05:31:30Z", false)), calls: 2, selected: 2, tracked: 1},
		{name: "non RFC3339 date remains independent", attempts: duplicate(captured("2026-10-30 05:31:30+00", false)), calls: 2, selected: 2, tracked: 1},
		{name: "empty history has no measured calls", attempts: []map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := json.Marshal(tc.attempts)
			require.NoError(t, err)
			_, err = tx.ExecContext(ctx, `UPDATE async_image_tasks SET account_attempts=$1::jsonb WHERE task_id='target'`, string(payload))
			require.NoError(t, err)
			stats, err := repo.AsyncImageReliabilityStats(ctx, service.AsyncImageTaskFilter{UserID: &uid, Platform: "openai"})
			require.NoError(t, err)
			require.Equal(t, tc.calls, stats.HTTPInvocations)
			require.Equal(t, tc.selected, stats.AccountSelections)
			require.Equal(t, tc.first, stats.FirstAttemptSucceeded)
			require.Equal(t, tc.tracked, stats.InstrumentedTasks)
			require.Equal(t, int64(1), stats.Total)
			require.InDelta(t, 0.09, stats.ActualCost, 0.000001)
			require.Equal(t, int64(0), stats.ResultFetchRetriedTasks)
			require.InDelta(t, 55000, *stats.P95DurationMS, 0.001)
		})
	}
}
