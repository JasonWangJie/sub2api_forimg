//go:build postgres

package repository

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const asyncImageRetryMigrationFile = "232_ZJ_async_image_retry_policy.sql"

func newAsyncImageRetryPostgresFixture(t *testing.T) (*asyncImageTaskRepository, context.Context) {
	t.Helper()
	admin := openAsyncImagePostgresTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	schema := "async_image_retry_" + uuid.NewString()
	quotedSchema := pq.QuoteIdentifier(schema)
	_, err := admin.ExecContext(ctx, `CREATE SCHEMA `+quotedSchema)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := admin.ExecContext(cleanup, `DROP SCHEMA `+quotedSchema+` CASCADE`)
		if err != nil {
			t.Errorf("remove isolated PostgreSQL retry fixture: %v", err)
		}
	})
	u, err := url.Parse(os.Getenv("SUB2API_TEST_POSTGRES_DSN"))
	require.NoError(t, err)
	query := u.Query()
	query.Set("search_path", quotedSchema)
	u.RawQuery = query.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(8)
	_, err = db.ExecContext(ctx, `
CREATE TABLE users (id BIGINT PRIMARY KEY);
CREATE TABLE api_keys (id BIGINT PRIMARY KEY);
CREATE TABLE groups (id BIGINT PRIMARY KEY);
CREATE TABLE accounts (id BIGINT PRIMARY KEY);
INSERT INTO users VALUES (1);
INSERT INTO api_keys VALUES (1);
INSERT INTO groups VALUES (1);
INSERT INTO accounts VALUES (42), (44);`)
	require.NoError(t, err)
	for _, filename := range []string{
		"185_ZJ_async_image_tasks.sql", "223_ZJ_async_image_reference_retry_state.sql",
		"224_ZJ_async_image_account_attempts.sql", "226_ZJ_async_image_reference_urls.sql",
		"227_ZJ_async_image_result_fetch.sql", asyncImageRetryMigrationFile,
	} {
		migration, err := migrations.FS.ReadFile(filename)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(migration))
		require.NoError(t, err, filename)
	}
	return &asyncImageTaskRepository{db: db, sql: db}, ctx
}

func insertAsyncImageRetryPostgresTask(t *testing.T, ctx context.Context, repo *asyncImageTaskRepository, id, status string) {
	t.Helper()
	_, err := repo.db.ExecContext(ctx, `
INSERT INTO async_image_tasks (task_id, user_id, api_key_id, group_id, protocol, platform, request_type,
 model, status, request_hash, request_payload)
VALUES ($1, 1, 1, 1, 'bb', 'openai', 'image_to_image', 'gpt-image-2', $2, 'fixture', '\x01'::bytea)`, id, status)
	require.NoError(t, err)
}

func TestAsyncImageRetryPolicyRevisionPostgres(t *testing.T) {
	repo, ctx := newAsyncImageRetryPostgresFixture(t)
	initial, err := repo.GetAsyncImageRetryPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), initial.Revision)
	require.Empty(t, initial.Rules)
	initial.PauseGenerationRetries = true
	results := make(chan error, 2)
	for _, actor := range []int64{100, 200} {
		go func(actor int64) {
			_, err := repo.SaveAsyncImageRetryPolicy(ctx, 1, initial, actor, nil)
			results <- err
		}(actor)
	}
	first, second := <-results, <-results
	if first != nil {
		first, second = second, first
	}
	require.NoError(t, first)
	require.ErrorIs(t, second, service.ErrAsyncImageRetryRevisionConflict)

	migration, err := migrations.FS.ReadFile(asyncImageRetryMigrationFile)
	require.NoError(t, err)
	_, err = repo.db.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	current, err := repo.GetAsyncImageRetryPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(2), current.Revision)
	require.True(t, current.PauseGenerationRetries)

	insertAsyncImageRetryPostgresTask(t, ctx, repo, "first-generation", service.AsyncImageTaskStatusInvoking)
	start := service.AsyncImageRetryStartParams{
		TaskID: "first-generation", ExpectedTaskVersion: 1, AccountID: 44, Generation: true, FirstGeneration: true,
	}
	require.NoError(t, repo.StartAsyncImageRetryAction(ctx, start))
	require.ErrorIs(t, repo.StartAsyncImageRetryAction(ctx, start), service.ErrAsyncImageRetryActionChanged)
	start.Revision, start.FirstGeneration = current.Revision, false
	require.ErrorIs(t, repo.StartAsyncImageRetryAction(ctx, start), service.ErrAsyncImageRetryGenerationPaused)

	target := int64(1)
	rollback, err := repo.SaveAsyncImageRetryPolicy(ctx, 2, current, 300, &target)
	require.NoError(t, err)
	require.Equal(t, int64(3), rollback.Revision)
	require.Equal(t, "rollback", rollback.Operation)
	require.Equal(t, &target, rollback.RollbackFrom)
	require.False(t, rollback.Policy.PauseGenerationRetries)
	page, total, err := repo.ListAsyncImageRetryVersions(ctx, 2, 0)
	require.NoError(t, err)
	require.Equal(t, int64(3), total)
	require.Len(t, page, 2)
	require.Equal(t, int64(3), page[0].Revision)
	require.Equal(t, int64(2), page[1].Revision)
}

func TestAsyncImageRetryPolicyStartupMigrationPostgres(t *testing.T) {
	admin := openAsyncImagePostgresTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	name := "async_image_retry_" + uuid.NewString()
	quotedDatabase := pq.QuoteIdentifier(name)
	_, err := admin.ExecContext(ctx, `CREATE DATABASE `+quotedDatabase)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_, err := admin.ExecContext(cleanup, `DROP DATABASE `+quotedDatabase)
		if err != nil {
			t.Errorf("remove isolated PostgreSQL migration fixture: %v", err)
		}
	})
	u, err := url.Parse(os.Getenv("SUB2API_TEST_POSTGRES_DSN"))
	require.NoError(t, err)
	u.Path = "/" + name
	u.RawPath = ""
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, ApplyMigrations(ctx, db))
	require.NoError(t, ApplyMigrations(ctx, db))
	var migrationsRecorded int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE filename = $1`, asyncImageRetryMigrationFile).Scan(&migrationsRecorded))
	require.Equal(t, 1, migrationsRecorded)
	repo := NewAsyncImageTaskRepository(db).(service.AsyncImageRetryRepository)
	policy, err := repo.GetAsyncImageRetryPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), policy.Revision)
	require.Empty(t, policy.Rules)
}

func TestAsyncImageRetryExecutionPostgres(t *testing.T) {
	repo, ctx := newAsyncImageRetryPostgresFixture(t)
	const taskID = "generation"
	insertAsyncImageRetryPostgresTask(t, ctx, repo, taskID, service.AsyncImageTaskStatusInvoking)
	policy := service.AsyncImageRetryPolicy{Rules: []service.AsyncImageRetryRule{{
		ID: "rule-a", Name: "Reference retry", Enabled: true,
		Match:             service.AsyncImageRetryMatch{HTTPStatuses: []int{500}},
		Steps:             []service.AsyncImageRetryStep{{ID: "local-elsewhere", Action: "local_other_account", MaxAttempts: 1}},
		AllowedAccountIDs: []int64{44},
	}}}
	require.NoError(t, service.ValidateAsyncImageRetryPolicy(&policy))
	version, err := repo.SaveAsyncImageRetryPolicy(ctx, 1, policy, 100, nil)
	require.NoError(t, err)
	policy = version.Policy
	old := time.Now().UTC().Add(-8 * 24 * time.Hour)
	fact := service.AsyncImageRetryFact{
		TaskID: taskID, FailureID: "failure-1", Platform: service.PlatformOpenAI, Model: "gpt-image-2", GroupID: 1,
		Stage: "submit", HTTPStatus: 500, AccountID: 42, OccurredAt: old, HasReferences: true, SafeToRetry: true,
		Message: "download timeout for https://example.test/image?signature=sensitive attempt 123456789",
	}
	decision := service.EvaluateAsyncImageRetry(policy, fact, service.AsyncImageRetryUsage{})
	scheduled := service.AsyncImageRetrySaveParams{
		ExpectedTaskVersion: 1, State: service.AsyncImageRetryStatePending, DueAt: time.Now().UTC().Add(time.Minute),
		Event: service.AsyncImageRetryEvent{Fact: fact, Decision: decision},
		Transition: &service.AsyncImageTaskTransition{TaskID: taskID, ExpectedVersion: 1,
			FromStatuses: []string{service.AsyncImageTaskStatusInvoking}, ToStatus: service.AsyncImageTaskStatusQueued},
	}
	first, err := repo.SaveAsyncImageRetryDecision(ctx, scheduled)
	require.NoError(t, err)
	require.NotContains(t, first.Fact.Message, "sensitive")
	require.Contains(t, first.Fact.Message, "[url]")
	task, err := repo.GetAsyncImageTaskByTaskID(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, service.AsyncImageTaskStatusQueued, task.Status)
	require.Equal(t, int64(2), task.Version)
	var outboxCount int
	require.NoError(t, repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM async_image_outbox WHERE task_id = $1`, taskID).Scan(&outboxCount))
	require.Equal(t, 1, outboxCount)
	recovery, err := repo.ListRecoverableAsyncImageRetryAwareTasks(ctx, []string{task.Status}, time.Now().Add(time.Hour), 5, 5, 10)
	require.NoError(t, err)
	require.Empty(t, recovery, "PostgreSQL recovery cannot bypass a future retry")

	version, err = repo.SaveAsyncImageRetryPolicy(ctx, policy.Revision, policy, 100, nil)
	require.NoError(t, err)
	policy = version.Policy
	start := service.AsyncImageRetryStartParams{TaskID: taskID, ExpectedTaskVersion: 2, EventID: first.ID, Revision: first.Decision.Revision}
	require.ErrorIs(t, repo.StartAsyncImageRetryAction(ctx, start), service.ErrAsyncImageRetryRevisionConflict)
	scheduled.ExpectedTaskVersion, scheduled.ExpectedPendingEventID, scheduled.Transition = 2, first.ID, nil
	scheduled.DueAt = time.Now().UTC().Add(-time.Second)
	scheduled.Event.Decision = service.EvaluateAsyncImageRetry(policy, fact, service.AsyncImageRetryUsage{})
	second, err := repo.SaveAsyncImageRetryDecision(ctx, scheduled)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	_, err = repo.TransitionAsyncImageTask(ctx, service.AsyncImageTaskTransition{TaskID: taskID, ExpectedVersion: 2,
		FromStatuses: []string{service.AsyncImageTaskStatusQueued}, ToStatus: service.AsyncImageTaskStatusInvoking})
	require.NoError(t, err)
	start.EventID, start.ExpectedTaskVersion, start.Revision = second.ID, 3, policy.Revision
	require.NoError(t, repo.StartAsyncImageRetryAction(ctx, start))
	require.ErrorIs(t, repo.StartAsyncImageRetryAction(ctx, start), service.ErrAsyncImageRetryActionChanged)
	state, err := repo.GetAsyncImageRetryState(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, 1, state.Usage.Rules["rule-a"])
	require.Equal(t, 1, state.Usage.Steps["rule-a"]["local-elsewhere"])
	require.Nil(t, state.Pending.Event.GenerationStartedAt)
	start.Generation, start.AccountID = true, 42
	require.ErrorIs(t, repo.StartAsyncImageRetryAction(ctx, start), service.ErrAsyncImageRetryActionChanged)
	start.AccountID = 44
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- repo.StartAsyncImageRetryAction(ctx, start) }()
	}
	firstErr, secondErr := <-results, <-results
	if firstErr != nil {
		firstErr, secondErr = secondErr, firstErr
	}
	require.NoError(t, firstErr)
	require.ErrorIs(t, secondErr, service.ErrAsyncImageRetryActionChanged)
	state, err = repo.GetAsyncImageRetryState(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, 1, state.Usage.Rules["rule-a"], "preparation and its send consume a single step")
	require.NotNil(t, state.Pending.Event.GenerationStartedAt)

	_, err = repo.db.ExecContext(ctx, `UPDATE async_image_tasks SET status = 'succeeded' WHERE task_id = $1`, taskID)
	require.NoError(t, err)
	fact.FailureID, fact.OccurredAt = "failure-2", old.Add(time.Hour)
	fact.Message = "download timeout for https://another.test/object attempt 234567890"
	_, err = repo.SaveAsyncImageRetryDecision(ctx, service.AsyncImageRetrySaveParams{ExpectedTaskVersion: 3,
		Event: service.AsyncImageRetryEvent{Fact: fact, Decision: service.AsyncImageRetryDecision{Source: "none", Revision: policy.Revision}}})
	require.NoError(t, err)
	samples, total, err := repo.ListAsyncImageRetrySamples(ctx, service.AsyncImageRetryFilter{HTTPStatus: 500})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, samples, 1)
	sample := samples[0]
	require.Equal(t, int64(2), sample.Occurrences)
	require.Equal(t, int64(1), sample.Tasks)
	require.Equal(t, int64(1), sample.Executions)
	require.Equal(t, int64(1), sample.SucceededTasks)
	require.Equal(t, "none", sample.Latest.Decision.Source)
	events, total, err := repo.ListAsyncImageRetryEvents(ctx, service.AsyncImageRetryFilter{Source: "custom"}, sample.Fingerprint)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, int64(2), total)
	require.Equal(t, service.AsyncImageTaskStatusSucceeded, events[0].FinalStatus)
	deleted, err := repo.CleanupAsyncImageRetryEvents(ctx, time.Now().UTC().Add(-7*24*time.Hour), 50)
	require.NoError(t, err)
	require.Equal(t, int64(3), deleted)
	state, err = repo.GetAsyncImageRetryState(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, 1, state.Usage.Rules["rule-a"], "sample TTL is independent of task usage")
	_, err = repo.GetAsyncImageTaskByTaskID(ctx, taskID)
	require.NoError(t, err)
}

func TestAsyncImageRetryScheduleAtomicPostgres(t *testing.T) {
	repo, ctx := newAsyncImageRetryPostgresFixture(t)
	const taskID = "fixture-rollback"
	insertAsyncImageRetryPostgresTask(t, ctx, repo, taskID, service.AsyncImageTaskStatusInvoking)
	_, err := repo.db.ExecContext(ctx, `ALTER TABLE async_image_outbox ADD CHECK (task_id <> 'fixture-rollback')`)
	require.NoError(t, err)
	_, err = repo.SaveAsyncImageRetryDecision(ctx, service.AsyncImageRetrySaveParams{ExpectedTaskVersion: 1,
		State: service.AsyncImageRetryStatePending, Event: service.AsyncImageRetryEvent{
			Fact:     service.AsyncImageRetryFact{TaskID: taskID, FailureID: "rollback-1", Stage: "submit"},
			Decision: service.AsyncImageRetryDecision{Source: "builtin", Revision: 1, Action: "retry_prepare"},
		}, Transition: &service.AsyncImageTaskTransition{TaskID: taskID, ExpectedVersion: 1,
			FromStatuses: []string{service.AsyncImageTaskStatusInvoking}, ToStatus: service.AsyncImageTaskStatusQueued},
	})
	var dbError *pq.Error
	require.ErrorAs(t, err, &dbError)
	require.Equal(t, pq.ErrorCode("23514"), dbError.Code)
	task, err := repo.GetAsyncImageTaskByTaskID(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, service.AsyncImageTaskStatusInvoking, task.Status)
	require.Equal(t, int64(1), task.Version)
	events, total, err := repo.ListAsyncImageRetryEvents(ctx, service.AsyncImageRetryFilter{}, "")
	require.NoError(t, err)
	require.Empty(t, events)
	require.Zero(t, total)
	state, err := repo.GetAsyncImageRetryState(ctx, taskID)
	require.NoError(t, err)
	require.Nil(t, state.Pending)
	_, err = repo.GetAsyncImageRetryEvent(ctx, 1)
	require.ErrorIs(t, err, service.ErrAsyncImageRetryEventNotFound)
}

func TestAsyncImageRetryManualResumePostgres(t *testing.T) {
	repo, ctx := newAsyncImageRetryPostgresFixture(t)
	const taskID = "manual-recovery"
	insertAsyncImageRetryPostgresTask(t, ctx, repo, taskID, service.AsyncImageTaskStatusStorageFailed)
	event, err := repo.SaveAsyncImageRetryDecision(ctx, service.AsyncImageRetrySaveParams{
		ExpectedTaskVersion: 1, State: service.AsyncImageRetryStateManual,
		Event: service.AsyncImageRetryEvent{
			Fact:     service.AsyncImageRetryFact{TaskID: taskID, FailureID: "manual-1", Stage: "storage", HasOutput: true},
			Decision: service.AsyncImageRetryDecision{Source: "none", Revision: 1, Action: "manual_review"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, service.AsyncImageTaskStatusStorageFailed, event.FinalStatus)
	recovery, err := repo.ListRecoverableAsyncImageRetryAwareTasks(ctx, []string{service.AsyncImageTaskStatusStorageFailed}, time.Now().Add(time.Hour), 5, 5, 10)
	require.NoError(t, err)
	require.Empty(t, recovery)
	task, err := repo.ResumeAsyncImageRetryPostProcessing(ctx, service.AsyncImageTaskTransition{
		TaskID: taskID, ExpectedVersion: 1, FromStatuses: []string{service.AsyncImageTaskStatusStorageFailed},
		ToStatus: service.AsyncImageTaskStatusUploading,
	}, service.AsyncImageOutboxEntry{TaskID: taskID, EventType: service.AsyncImageOutboxEventPostProcessingResume, DedupKey: "manual-resume-1"})
	require.NoError(t, err)
	require.Equal(t, service.AsyncImageTaskStatusUploading, task.Status)
	state, err := repo.GetAsyncImageRetryState(ctx, taskID)
	require.NoError(t, err)
	require.Nil(t, state.Pending)
	resumed, err := repo.GetAsyncImageRetryEvent(ctx, event.ID)
	require.NoError(t, err)
	require.Empty(t, resumed.FinalStatus)
	_, err = repo.db.ExecContext(ctx, `UPDATE async_image_tasks SET status = 'succeeded' WHERE task_id = 'manual-recovery';
DELETE FROM async_image_tasks WHERE task_id = 'manual-recovery';`)
	require.NoError(t, err)
	retained, err := repo.GetAsyncImageRetryEvent(ctx, event.ID)
	require.NoError(t, err)
	require.Equal(t, service.AsyncImageTaskStatusSucceeded, retained.FinalStatus)
}

func TestAsyncImageRetryHeldDownloadResumePostgres(t *testing.T) {
	repo, ctx := newAsyncImageRetryPostgresFixture(t)
	const readyID, expiredID, permanentID, exhaustedID = "download-ready", "download-expired", "download-permanent", "download-exhausted"
	for _, fixture := range []struct {
		id          string
		safeToRetry bool
		attempts    string
	}{
		{readyID, true, `{"0": 2}`},
		{expiredID, true, `{"0": 2}`},
		{permanentID, false, `{"0": 1}`},
		{exhaustedID, false, `{"0": 3}`},
	} {
		id := fixture.id
		insertAsyncImageRetryPostgresTask(t, ctx, repo, id, service.AsyncImageTaskStatusUpstreamSucceeded)
		canResume, err := service.CanResumeAsyncImageHeldDownload(ctx, repo, id)
		require.NoError(t, err)
		require.False(t, canResume)
		_, err = repo.SaveAsyncImageRetryDecision(ctx, service.AsyncImageRetrySaveParams{
			ExpectedTaskVersion: 1, State: service.AsyncImageRetryStateManual,
			Event: service.AsyncImageRetryEvent{
				Fact:     service.AsyncImageRetryFact{TaskID: id, FailureID: "download-1", Stage: "download", HasOutput: true, SafeToRetry: fixture.safeToRetry},
				Decision: service.AsyncImageRetryDecision{Source: "none", Revision: 1, Action: "manual_review"},
			},
		})
		require.NoError(t, err)
		expiry := time.Now().Add(time.Hour)
		if id == expiredID {
			expiry = time.Now().Add(-time.Second)
		}
		_, err = repo.db.ExecContext(ctx, `
INSERT INTO async_image_result_fetches (task_id, encrypted_payload, attempts, expires_at)
VALUES ($1, '\x01'::bytea, $2::jsonb, $3)`, id, fixture.attempts, expiry)
		require.NoError(t, err)
	}
	canResume, err := service.CanResumeAsyncImageHeldDownload(ctx, repo, readyID)
	require.NoError(t, err)
	require.True(t, canResume)
	for _, id := range []string{expiredID, permanentID, exhaustedID} {
		canResume, err := service.CanResumeAsyncImageHeldDownload(ctx, repo, id)
		require.NoError(t, err)
		require.False(t, canResume, id)
		_, err = repo.ResumeAsyncImageRetryPostProcessing(ctx, service.AsyncImageTaskTransition{
			TaskID: id, ExpectedVersion: 1, FromStatuses: []string{service.AsyncImageTaskStatusUpstreamSucceeded},
			ToStatus: service.AsyncImageTaskStatusUpstreamSucceeded,
		}, service.AsyncImageOutboxEntry{TaskID: id, EventType: service.AsyncImageOutboxEventPostProcessingResume, DedupKey: "must-not-publish:" + id})
		require.ErrorIs(t, err, service.ErrAsyncImagePostProcessingResumeNotAllowed, id)
		state, err := repo.GetAsyncImageRetryState(ctx, id)
		require.NoError(t, err)
		require.Equal(t, service.AsyncImageRetryStateManual, state.Pending.Status, id)
	}
	deleted, err := repo.CleanupExpiredAsyncImageHeldDownloads(ctx, time.Now().UTC(), 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)
	expired, err := repo.GetAsyncImageResultFetch(ctx, expiredID)
	require.NoError(t, err)
	require.Nil(t, expired)
	state, err := repo.GetAsyncImageRetryState(ctx, expiredID)
	require.NoError(t, err)
	require.Equal(t, service.AsyncImageRetryStateManual, state.Pending.Status)
	resumed, err := service.NewAsyncImageTaskService(repo).ResumePostProcessing(ctx, readyID)
	require.NoError(t, err)
	require.Equal(t, service.AsyncImageTaskStatusUpstreamSucceeded, resumed.Task.Status)
	require.Equal(t, int64(2), resumed.Task.Version)
	state, err = repo.GetAsyncImageRetryState(ctx, readyID)
	require.NoError(t, err)
	require.Nil(t, state.Pending)
	_, err = repo.SaveAsyncImageRetryDecision(ctx, service.AsyncImageRetrySaveParams{
		ExpectedTaskVersion: 2, State: service.AsyncImageRetryStateManual,
		Event: service.AsyncImageRetryEvent{
			Fact:     service.AsyncImageRetryFact{TaskID: readyID, FailureID: "download-2", Stage: "download", HasOutput: true, SafeToRetry: true},
			Decision: service.AsyncImageRetryDecision{Source: "none", Revision: 1, Action: "manual_review"},
		},
	})
	require.NoError(t, err)
	resumed, err = service.NewAsyncImageTaskService(repo).ResumePostProcessing(ctx, readyID)
	require.NoError(t, err)
	require.Equal(t, service.AsyncImageTaskStatusUpstreamSucceeded, resumed.Task.Status)
	require.Equal(t, int64(3), resumed.Task.Version)
	next, err := repo.BeginAsyncImageResultFetchAttempt(ctx, readyID, 0, 3)
	require.NoError(t, err)
	require.Equal(t, 3, next)
	_, err = repo.BeginAsyncImageResultFetchAttempt(ctx, readyID, 0, 3)
	require.ErrorIs(t, err, service.ErrAsyncImageInvalidTransition)
	var count int
	require.NoError(t, repo.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM async_image_outbox WHERE event_type = 'post_processing_resume' AND task_id = $1`, readyID).Scan(&count))
	require.Equal(t, 2, count)
	require.NoError(t, repo.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM async_image_outbox WHERE dedup_key LIKE 'must-not-publish:%'`).Scan(&count))
	require.Zero(t, count)
}
