package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *asyncImageTaskRepository) GetAsyncImageRetryState(ctx context.Context, taskID string) (service.AsyncImageRetryState, error) {
	var state service.AsyncImageRetryState
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return state, err
	}
	defer func() { _ = tx.Rollback() }()
	var eventID sql.NullInt64
	var pending service.AsyncImageRetryPending
	err = tx.QueryRowContext(ctx, `SELECT event_id, status, due_at FROM async_image_retry_state WHERE task_id = $1`, taskID).
		Scan(&eventID, &pending.Status, &pending.DueAt)
	switch {
	case err == nil:
		if eventID.Valid {
			pending.Event, err = getAsyncImageRetryEvent(ctx, tx, eventID.Int64)
			if err != nil {
				return state, err
			}
			state.Pending = &pending
		} else if pending.Status == service.AsyncImageRetryStateManual {
			state.Pending = &pending
		}
	case !errors.Is(err, sql.ErrNoRows):
		return state, err
	}
	state.Usage, err = getAsyncImageRetryUsage(ctx, tx, taskID)
	if err != nil {
		return state, err
	}
	return state, tx.Commit()
}

func getAsyncImageRetryUsage(ctx context.Context, db asyncImageSQLExecutor, taskID string) (service.AsyncImageRetryUsage, error) {
	usage := service.AsyncImageRetryUsage{Rules: map[string]int{}, Steps: map[string]map[string]int{}}
	rows, err := db.QueryContext(ctx, `SELECT rule_id, step_id, used_count FROM async_image_retry_usage WHERE task_id = $1`, taskID)
	if err != nil {
		return usage, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var rule, step string
		var used int
		if err := rows.Scan(&rule, &step, &used); err != nil {
			return usage, err
		}
		usage.Rules[rule] += used
		if usage.Steps[rule] == nil {
			usage.Steps[rule] = make(map[string]int)
		}
		usage.Steps[rule][step] = used
	}
	return usage, rows.Err()
}

func (r *asyncImageTaskRepository) SaveAsyncImageRetryDecision(ctx context.Context, p service.AsyncImageRetrySaveParams) (service.AsyncImageRetryEvent, error) {
	event := p.Event
	fact := service.SanitizeAsyncImageRetryFact(event.Fact)
	if fact.TaskID == "" || fact.FailureID == "" || len(fact.FailureID) > 256 || p.ExpectedTaskVersion <= 0 || event.Decision.Revision < 0 {
		return event, service.ErrAsyncImageInvalidInput
	}
	if event.Decision.Revision == 0 && p.State == service.AsyncImageRetryStatePending &&
		(!fact.SafeToRetry || fact.Unknown || fact.HasOutput || event.Decision.BlockReason != "policy_unavailable") {
		return event, service.ErrAsyncImageInvalidInput
	}
	switch p.State {
	case "", service.AsyncImageRetryStatePending, service.AsyncImageRetryStateManual, service.AsyncImageRetryStateClear:
	default:
		return event, service.ErrAsyncImageInvalidInput
	}
	now := time.Now().UTC()
	if fact.OccurredAt.IsZero() {
		fact.OccurredAt = now
	}
	event.Fact, event.Fingerprint = fact, service.AsyncImageRetryFingerprint(fact)
	if p.DueAt.IsZero() {
		p.DueAt = now
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return event, err
	}
	defer func() { _ = tx.Rollback() }()
	if event.Decision.Revision > 0 {
		var revision int64
		if err := tx.QueryRowContext(ctx, `SELECT revision FROM async_image_retry_policy WHERE singleton FOR SHARE`).Scan(&revision); err != nil {
			return event, err
		}
		if revision != event.Decision.Revision {
			return event, service.ErrAsyncImageRetryRevisionConflict
		}
	}
	task, err := lockAsyncImageRetryTask(ctx, tx, fact.TaskID, p.ExpectedTaskVersion)
	if err != nil {
		return event, err
	}
	pending, err := getAsyncImageRetryStateRow(ctx, tx, fact.TaskID)
	if err != nil {
		return event, err
	}
	if p.ExpectedPendingEventID != 0 && pending.eventID != p.ExpectedPendingEventID {
		return event, service.ErrAsyncImageRetryActionChanged
	}
	version := task.version
	if p.Transition != nil {
		if p.Transition.TaskID != fact.TaskID || p.Transition.ExpectedVersion != task.version {
			return event, service.ErrAsyncImageInvalidTransition
		}
		version++
	}
	requiresPending := p.State == service.AsyncImageRetryStatePending
	event, err = saveAsyncImageRetryEvent(ctx, tx, event, version, task.status, requiresPending)
	if err != nil {
		return event, err
	}
	if p.State != "" && pending.status == service.AsyncImageRetryStatePending &&
		pending.eventID != event.ID && p.ExpectedPendingEventID == 0 {
		return event, service.ErrAsyncImageRetryActionChanged
	}
	if p.Transition != nil {
		if _, err := transitionAsyncImageTaskLocked(ctx, tx, task.status, task.version, *p.Transition); err != nil {
			return event, err
		}
	}
	if err := saveAsyncImageRetryState(ctx, tx, event, pending, p); err != nil {
		return event, err
	}
	return event, tx.Commit()
}

type asyncImageRetryStateRow struct {
	eventID int64
	status  string
	dueAt   time.Time
	due     bool
}

func getAsyncImageRetryStateRow(ctx context.Context, db asyncImageSQLExecutor, taskID string) (asyncImageRetryStateRow, error) {
	var row asyncImageRetryStateRow
	err := db.QueryRowContext(ctx, `SELECT COALESCE(event_id, 0), status, due_at, due_at <= NOW() FROM async_image_retry_state WHERE task_id = $1`, taskID).
		Scan(&row.eventID, &row.status, &row.dueAt, &row.due)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return row, err
}

func saveAsyncImageRetryState(ctx context.Context, tx *sql.Tx, event service.AsyncImageRetryEvent, pending asyncImageRetryStateRow, p service.AsyncImageRetrySaveParams) error {
	switch p.State {
	case service.AsyncImageRetryStateClear:
		return clearAsyncImageRetryState(ctx, tx, event.Fact.TaskID)
	case service.AsyncImageRetryStatePending, service.AsyncImageRetryStateManual:
		if p.State == service.AsyncImageRetryStatePending && event.StartedAt != nil {
			return service.ErrAsyncImageRetryActionChanged
		}
		if p.State == pending.status && pending.eventID == event.ID && p.ExpectedPendingEventID == 0 {
			p.DueAt = pending.dueAt
		}
		_, err := tx.ExecContext(ctx, `
INSERT INTO async_image_retry_state (task_id, event_id, status, due_at) VALUES ($1, $2, $3, $4)
ON CONFLICT (task_id) DO UPDATE SET event_id = EXCLUDED.event_id, status = EXCLUDED.status, due_at = EXCLUDED.due_at`,
			event.Fact.TaskID, event.ID, p.State, p.DueAt)
		if err != nil || p.State != service.AsyncImageRetryStatePending {
			return err
		}
		return enqueueAsyncImageOutboxWithSQL(ctx, tx, service.AsyncImageOutboxEntry{
			TaskID: event.Fact.TaskID, EventType: "task_ready", AvailableAt: p.DueAt,
			DedupKey: fmt.Sprintf("%s:retry:%d:%d", event.Fact.TaskID, event.ID, p.DueAt.UnixMicro()),
		})
	default:
		return nil
	}
}

func clearAsyncImageRetryState(ctx context.Context, db asyncImageSQLExecutor, taskID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM async_image_retry_state WHERE task_id = $1`, taskID)
	return err
}

type asyncImageRetryTaskLock struct {
	status      string
	version     int64
	submittedAt time.Time
	now         time.Time
	hasOutput   bool
}

func lockAsyncImageRetryTask(ctx context.Context, db asyncImageSQLExecutor, taskID string, expectedVersion int64) (asyncImageRetryTaskLock, error) {
	var task asyncImageRetryTaskLock
	err := db.QueryRowContext(ctx, `
SELECT status, version, submitted_at, NOW(), (upstream_succeeded_at IS NOT NULL OR billing_payload IS NOT NULL)
FROM async_image_tasks WHERE task_id = $1 FOR UPDATE`, taskID).
		Scan(&task.status, &task.version, &task.submittedAt, &task.now, &task.hasOutput)
	if errors.Is(err, sql.ErrNoRows) {
		return task, service.ErrAsyncImageTaskNotFound
	}
	if err == nil && expectedVersion != task.version {
		err = service.ErrAsyncImageRetryActionChanged
	}
	return task, err
}

func (r *asyncImageTaskRepository) StartAsyncImageRetryAction(ctx context.Context, p service.AsyncImageRetryStartParams) error {
	initialDispatch := p.FirstGeneration && p.EventID == 0
	if p.TaskID == "" || p.ExpectedTaskVersion <= 0 || p.EventID < 0 || (p.Revision <= 0 && !initialDispatch) ||
		(p.Generation && p.AccountID <= 0) || (!p.Generation && (p.EventID == 0 || p.FirstGeneration)) {
		return service.ErrAsyncImageInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var policy service.AsyncImageRetryPolicy
	if !initialDispatch {
		policy, err = getAsyncImageRetryPolicy(ctx, tx, " FOR SHARE OF policy_row")
		if err != nil {
			return err
		}
		if policy.Revision != p.Revision {
			return service.ErrAsyncImageRetryRevisionConflict
		}
		if p.Generation && !p.FirstGeneration && policy.PauseGenerationRetries {
			return service.ErrAsyncImageRetryGenerationPaused
		}
	}
	task, err := lockAsyncImageRetryTask(ctx, tx, p.TaskID, p.ExpectedTaskVersion)
	if err != nil {
		return err
	}
	if p.FirstGeneration {
		if err := requireAsyncImageFirstDispatch(ctx, tx, p.TaskID); err != nil {
			return err
		}
	}
	if p.EventID == 0 {
		if err := startAsyncImageUnboundDispatch(ctx, tx, p, task); err != nil {
			return err
		}
		return tx.Commit()
	}
	var requiresPending bool
	var eventVersion int64
	row := tx.QueryRowContext(ctx, `SELECT `+asyncImageRetryEventColumns+`, e.requires_pending, e.task_version
FROM async_image_retry_events e WHERE e.id = $1 AND e.task_id = $2 FOR UPDATE`, p.EventID, p.TaskID)
	event, err := scanAsyncImageRetryEvent(row, &requiresPending, &eventVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrAsyncImageRetryActionChanged
	}
	if err != nil {
		return err
	}
	if event.Decision.Revision != p.Revision || event.GenerationStartedAt != nil || (event.StartedAt != nil && !p.Generation) {
		return service.ErrAsyncImageRetryActionChanged
	}
	pending, err := getAsyncImageRetryStateRow(ctx, tx, p.TaskID)
	if err != nil {
		return err
	}
	if requiresPending {
		if pending.eventID != p.EventID || pending.status == service.AsyncImageRetryStateManual || !pending.due {
			return service.ErrAsyncImageRetryActionChanged
		}
	} else if eventVersion != task.version || pending.status == service.AsyncImageRetryStateManual || pending.status == service.AsyncImageRetryStatePending {
		return service.ErrAsyncImageRetryActionChanged
	}
	if p.Generation && !canStartAsyncImageRetryGeneration(p, event, task) {
		return service.ErrAsyncImageRetryActionChanged
	}
	if event.StartedAt == nil && event.Decision.Source == "custom" && event.Decision.StepID != "" {
		if err := useAsyncImageRetryStep(ctx, tx, policy, event); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE async_image_retry_events SET started_at = COALESCE(started_at, NOW()),
    generation_started_at = CASE WHEN $2 THEN NOW() ELSE generation_started_at END,
    target_account_id = COALESCE(NULLIF($3::bigint, 0), target_account_id)
WHERE id = $1`, event.ID, p.Generation, p.AccountID); err != nil {
		return err
	}
	if requiresPending {
		if _, err := tx.ExecContext(ctx, `UPDATE async_image_retry_state SET status = 'started' WHERE task_id = $1 AND event_id = $2`, p.TaskID, p.EventID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func requireAsyncImageFirstDispatch(ctx context.Context, db asyncImageSQLExecutor, taskID string) error {
	var previous bool
	err := db.QueryRowContext(ctx, `
SELECT EXISTS (SELECT 1 FROM async_image_retry_task_dispatches WHERE task_id = $1)
    OR EXISTS (SELECT 1 FROM async_image_retry_events WHERE task_id = $1 AND generation_started_at IS NOT NULL)
    OR EXISTS (
        SELECT 1 FROM async_image_tasks task, LATERAL jsonb_array_elements(task.account_attempts) attempt
        WHERE task.task_id = $1 AND (
            attempt->>'status' IN ('invocation_started', 'succeeded')
            OR COALESCE(attempt->>'upstream_request_id', '') <> ''
            OR COALESCE(attempt->>'status_code', '0') <> '0'
        )
    )`, taskID).Scan(&previous)
	if err == nil && previous {
		return service.ErrAsyncImageRetryActionChanged
	}
	return err
}

func startAsyncImageUnboundDispatch(ctx context.Context, tx *sql.Tx, p service.AsyncImageRetryStartParams, task asyncImageRetryTaskLock) error {
	pending, err := getAsyncImageRetryStateRow(ctx, tx, p.TaskID)
	if err != nil {
		return err
	}
	if pending.status != "" || !canStartAsyncImageRetryGeneration(p, service.AsyncImageRetryEvent{}, task) {
		return service.ErrAsyncImageRetryActionChanged
	}
	var revision any
	if !p.FirstGeneration {
		revision = p.Revision
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO async_image_retry_task_dispatches (task_id, task_version, revision, account_id)
VALUES ($1, $2, $3, $4) ON CONFLICT (task_id, task_version) DO NOTHING`, p.TaskID, task.version, revision, p.AccountID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return service.ErrAsyncImageRetryActionChanged
	}
	return err
}

func canStartAsyncImageRetryGeneration(p service.AsyncImageRetryStartParams, event service.AsyncImageRetryEvent, task asyncImageRetryTaskLock) bool {
	if task.status != service.AsyncImageTaskStatusInvoking || task.hasOutput || event.Fact.HasOutput || event.Fact.Unknown ||
		(p.FirstGeneration && event.Fact.Dispatched) ||
		!task.now.Before(task.submittedAt.Add(service.AsyncImageGenerationTimeBudget)) {
		return false
	}
	decision := event.Decision
	if decision.Source != "custom" {
		return true
	}
	if decision.Action == "retry_prepare" {
		return event.Fact.Stage == "prepare" && event.Fact.SafeToRetry
	}
	return event.Fact.SafeToRetry && service.IsAsyncImageGenerationRetry(decision.Action) &&
		service.AsyncImageRetryAccountAllowed(decision, event.Fact, p.AccountID)
}

func useAsyncImageRetryStep(ctx context.Context, tx *sql.Tx, policy service.AsyncImageRetryPolicy, event service.AsyncImageRetryEvent) error {
	usage, err := getAsyncImageRetryUsage(ctx, tx, event.Fact.TaskID)
	if err != nil {
		return err
	}
	decision := service.EvaluateAsyncImageRetry(policy, event.Fact, usage)
	if decision.RuleID != event.Decision.RuleID || decision.StepID != event.Decision.StepID {
		return service.ErrAsyncImageRetryActionChanged
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO async_image_retry_usage (task_id, rule_id, step_id, used_count) VALUES ($1, $2, $3, 1)
ON CONFLICT (task_id, rule_id, step_id) DO UPDATE SET used_count = async_image_retry_usage.used_count + 1`,
		event.Fact.TaskID, decision.RuleID, decision.StepID)
	return err
}

func (r *asyncImageTaskRepository) CanResumeAsyncImageHeldDownload(ctx context.Context, taskID string) (bool, error) {
	return canResumeAsyncImageHeldDownload(ctx, r.sql, taskID)
}

func canResumeAsyncImageHeldDownload(ctx context.Context, db asyncImageSQLExecutor, taskID string) (bool, error) {
	var canResume bool
	err := db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM async_image_tasks task
    JOIN async_image_retry_state state USING (task_id)
    JOIN async_image_retry_events event ON event.id = state.event_id
    JOIN async_image_result_fetches result_source ON result_source.task_id = task.task_id
    WHERE task.task_id = $1 AND task.status = 'upstream_succeeded' AND state.status = 'manual'
      AND event.fact->>'stage' = 'download' AND event.fact->>'has_output' = 'true'
      AND event.fact->>'safe_to_retry' = 'true'
      AND result_source.expires_at > clock_timestamp() AND octet_length(result_source.encrypted_payload) > 0
)`, taskID).Scan(&canResume)
	return canResume, err
}

// ResumeAsyncImageRetryPostProcessing releases a rule's hold only for the
// existing, explicitly requested post-processing recovery transition.
func (r *asyncImageTaskRepository) ResumeAsyncImageRetryPostProcessing(ctx context.Context, transition service.AsyncImageTaskTransition, outbox service.AsyncImageOutboxEntry) (*service.AsyncImageTask, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	task, err := lockAsyncImageRetryTask(ctx, tx, transition.TaskID, transition.ExpectedVersion)
	if errors.Is(err, service.ErrAsyncImageRetryActionChanged) {
		return nil, service.ErrAsyncImageInvalidTransition
	}
	if err != nil {
		return nil, err
	}
	var updated *service.AsyncImageTask
	if task.status == service.AsyncImageTaskStatusUpstreamSucceeded {
		canResume, err := canResumeAsyncImageHeldDownload(ctx, tx, transition.TaskID)
		if err != nil {
			return nil, err
		}
		if !canResume || transition.ToStatus != task.status {
			return nil, service.ErrAsyncImagePostProcessingResumeNotAllowed
		}
		updated, err = updateAsyncImageTaskTransition(ctx, tx, task.version, transition)
		if err != nil {
			return nil, err
		}
		if err := appendAsyncImageEventWithSQL(ctx, tx, service.AsyncImageEvent{
			TaskID: transition.TaskID, EventType: transition.EventType, FromStatus: &task.status,
			ToStatus: &task.status, Payload: normalizeJSONPayload(transition.EventPayload),
		}); err != nil {
			return nil, err
		}
	} else {
		updated, err = transitionAsyncImageTaskLocked(ctx, tx, task.status, task.version, transition)
		if err != nil {
			return nil, err
		}
	}
	if err := clearAsyncImageRetryState(ctx, tx, transition.TaskID); err != nil {
		return nil, err
	}
	if err := enqueueAsyncImageOutboxWithSQL(ctx, tx, outbox); err != nil {
		return nil, err
	}
	return updated, tx.Commit()
}

// Keep held tasks available for review while removing expired result sources.
func (r *asyncImageTaskRepository) CleanupExpiredAsyncImageHeldDownloads(ctx context.Context, before time.Time, limit int) (int64, error) {
	if before.IsZero() {
		return 0, service.ErrAsyncImageInvalidInput
	}
	result, err := r.sql.ExecContext(ctx, `
WITH expired AS (
    SELECT task.task_id FROM async_image_tasks task
    JOIN async_image_retry_state state USING (task_id)
    JOIN async_image_result_fetches result_source USING (task_id)
    WHERE task.status = 'upstream_succeeded' AND state.status = 'manual' AND result_source.expires_at <= $1
    ORDER BY result_source.expires_at, task.task_id LIMIT $2 FOR UPDATE OF task SKIP LOCKED
)
DELETE FROM async_image_result_fetches WHERE task_id IN (SELECT task_id FROM expired)`, before, min(max(limit, 1), 1000))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
