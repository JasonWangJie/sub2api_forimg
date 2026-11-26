package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const asyncImageRetryEventColumns = `e.id, e.fact, e.decision, e.fingerprint, e.started_at,
e.generation_started_at, e.target_account_id, e.final_status, e.created_at`

func scanAsyncImageRetryEvent(row interface{ Scan(...any) error }, extra ...any) (service.AsyncImageRetryEvent, error) {
	var event service.AsyncImageRetryEvent
	var fact, decision []byte
	fields := []any{&event.ID, &fact, &decision, &event.Fingerprint, &event.StartedAt,
		&event.GenerationStartedAt, &event.TargetAccountID, &event.FinalStatus, &event.CreatedAt}
	if err := row.Scan(append(fields, extra...)...); err != nil {
		return event, err
	}
	if err := json.Unmarshal(fact, &event.Fact); err != nil {
		return event, fmt.Errorf("decode asynchronous image retry fact: %w", err)
	}
	if err := json.Unmarshal(decision, &event.Decision); err != nil {
		return event, fmt.Errorf("decode asynchronous image retry decision: %w", err)
	}
	return event, nil
}

func saveAsyncImageRetryEvent(ctx context.Context, tx *sql.Tx, event service.AsyncImageRetryEvent, version int64, taskStatus string, requiresPending bool) (service.AsyncImageRetryEvent, error) {
	fact, err := json.Marshal(event.Fact)
	if err != nil {
		return event, err
	}
	decision, err := json.Marshal(event.Decision)
	if err != nil {
		return event, err
	}
	var finalStatus string
	switch taskStatus {
	case service.AsyncImageTaskStatusSucceeded, service.AsyncImageTaskStatusFailed, service.AsyncImageTaskStatusExecutionUnknown,
		service.AsyncImageTaskStatusStorageFailed, service.AsyncImageTaskStatusBillingFailed, service.AsyncImageTaskStatusExpired:
		finalStatus = taskStatus
	}
	return scanAsyncImageRetryEvent(tx.QueryRowContext(ctx, `
INSERT INTO async_image_retry_events AS e (task_id, task_version, failure_id, revision, rule_id, step_id,
    fingerprint, fact, decision, requires_pending, occurred_at, final_status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10, $11, $12)
ON CONFLICT (task_id, failure_id, revision, rule_id, step_id) DO UPDATE SET
    decision = CASE WHEN e.generation_started_at IS NULL THEN EXCLUDED.decision ELSE e.decision END,
    requires_pending = e.requires_pending OR EXCLUDED.requires_pending
RETURNING `+asyncImageRetryEventColumns,
		event.Fact.TaskID, version, event.Fact.FailureID, event.Decision.Revision, event.Decision.RuleID, event.Decision.StepID,
		event.Fingerprint, fact, decision, requiresPending, event.Fact.OccurredAt, finalStatus))
}

func (r *asyncImageTaskRepository) GetAsyncImageRetryEvent(ctx context.Context, id int64) (service.AsyncImageRetryEvent, error) {
	return getAsyncImageRetryEvent(ctx, r.sql, id)
}

func getAsyncImageRetryEvent(ctx context.Context, db asyncImageSQLExecutor, id int64) (service.AsyncImageRetryEvent, error) {
	event, err := scanAsyncImageRetryEvent(db.QueryRowContext(ctx,
		`SELECT `+asyncImageRetryEventColumns+` FROM async_image_retry_events e WHERE e.id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return event, service.ErrAsyncImageRetryEventNotFound
	}
	return event, err
}

func asyncImageRetryFilterSQL(filter service.AsyncImageRetryFilter, fingerprint string) (string, []any) {
	conditions := make([]string, 0, 11)
	args := make([]any, 0, 13)
	add := func(expression string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf("%s $%d", expression, len(args)))
	}
	if filter.From != nil {
		add("e.occurred_at >=", *filter.From)
	}
	if filter.To != nil {
		add("e.occurred_at <", *filter.To)
	}
	for _, field := range []struct{ expr, value string }{
		{"e.fact->>'platform' =", filter.Platform}, {"e.fact->>'model' =", filter.Model},
		{"e.fact->>'stage' =", filter.Stage}, {"e.fact->>'category' =", filter.Category},
		{"e.fact->>'error_code' =", filter.ErrorCode}, {"e.decision->>'source' =", filter.Source},
		{"e.fingerprint =", fingerprint},
	} {
		if field.value != "" {
			add(field.expr, field.value)
		}
	}
	for _, field := range []struct {
		expr  string
		value int64
	}{
		{"(e.fact->>'group_id')::bigint =", filter.GroupID},
		{"(e.fact->>'account_id')::bigint =", filter.AccountID},
		{"(e.fact->>'http_status')::bigint =", int64(filter.HTTPStatus)},
	} {
		if field.value > 0 {
			add(field.expr, field.value)
		}
	}
	if len(args) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

func (r *asyncImageTaskRepository) ListAsyncImageRetryEvents(ctx context.Context, filter service.AsyncImageRetryFilter, fingerprint string) ([]service.AsyncImageRetryEvent, int64, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	where, args := asyncImageRetryFilterSQL(filter, fingerprint)
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM async_image_retry_events e`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit, offset := asyncImageRetryPage(filter.Limit, filter.Offset)
	pageSQL := fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	rows, err := tx.QueryContext(ctx, `SELECT `+asyncImageRetryEventColumns+` FROM async_image_retry_events e`+where+
		` ORDER BY e.occurred_at DESC, e.id DESC`+pageSQL, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	events := make([]service.AsyncImageRetryEvent, 0, limit)
	for rows.Next() {
		event, err := scanAsyncImageRetryEvent(rows)
		if err != nil {
			return nil, 0, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return events, total, tx.Commit()
}

func (r *asyncImageTaskRepository) ListAsyncImageRetrySamples(ctx context.Context, filter service.AsyncImageRetryFilter) ([]service.AsyncImageRetrySample, int64, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	where, args := asyncImageRetryFilterSQL(filter, "")
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT e.fingerprint) FROM async_image_retry_events e`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit, offset := asyncImageRetryPage(filter.Limit, filter.Offset)
	pageSQL := fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	rows, err := tx.QueryContext(ctx, `
WITH filtered AS MATERIALIZED (SELECT e.* FROM async_image_retry_events e`+where+`),
stats AS (
    SELECT fingerprint, COUNT(DISTINCT (task_id, failure_id)) AS occurrences, COUNT(DISTINCT task_id) AS tasks,
           COUNT(*) FILTER (WHERE started_at IS NOT NULL) AS executions,
           COUNT(DISTINCT task_id) FILTER (WHERE started_at IS NOT NULL AND final_status = 'succeeded') AS succeeded_tasks,
           MAX(occurred_at) AS last_seen_at
    FROM filtered GROUP BY fingerprint
    ORDER BY last_seen_at DESC, fingerprint`+pageSQL+`
), latest AS (
    SELECT DISTINCT ON (filtered.fingerprint) filtered.* FROM filtered JOIN stats USING (fingerprint)
    ORDER BY filtered.fingerprint, filtered.occurred_at DESC, filtered.id DESC
)
SELECT `+asyncImageRetryEventColumns+`, stats.occurrences, stats.tasks, stats.executions, stats.succeeded_tasks, stats.last_seen_at
FROM stats JOIN latest e USING (fingerprint)
ORDER BY stats.last_seen_at DESC, stats.fingerprint`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	samples := make([]service.AsyncImageRetrySample, 0, limit)
	for rows.Next() {
		var sample service.AsyncImageRetrySample
		sample.Latest, err = scanAsyncImageRetryEvent(rows, &sample.Occurrences, &sample.Tasks, &sample.Executions, &sample.SucceededTasks, &sample.LastSeenAt)
		if err != nil {
			return nil, 0, err
		}
		sample.Fingerprint = sample.Latest.Fingerprint
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return samples, total, tx.Commit()
}

func (r *asyncImageTaskRepository) CleanupAsyncImageRetryEvents(ctx context.Context, before time.Time, limit int) (int64, error) {
	if before.IsZero() {
		return 0, service.ErrAsyncImageInvalidInput
	}
	limit = min(max(limit, 1), 1000)
	result, err := r.sql.ExecContext(ctx, `
WITH expired AS (
    SELECT e.id FROM async_image_retry_events e WHERE e.occurred_at < $1
      AND NOT EXISTS (
          SELECT 1 FROM async_image_retry_state state JOIN async_image_tasks task ON task.task_id = state.task_id
          WHERE state.event_id = e.id AND (state.status = 'pending' OR (state.status = 'started' AND task.status IN ('queued', 'invoking')))
      )
    ORDER BY e.occurred_at, e.id LIMIT $2 FOR UPDATE OF e SKIP LOCKED
)
DELETE FROM async_image_retry_events WHERE id IN (SELECT id FROM expired)`, before, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
