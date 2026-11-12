//go:build postgres

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func resultCleanupPostgresDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openAsyncImagePostgresTestDB(t)
	_, err := db.Exec(`CREATE TEMP TABLE users(id BIGINT PRIMARY KEY);
 INSERT INTO users VALUES(1);
 CREATE TEMP TABLE async_image_tasks(id BIGSERIAL PRIMARY KEY,task_id VARCHAR(64) UNIQUE NOT NULL,status TEXT NOT NULL,cleanup_claimed_at TIMESTAMPTZ,
 reference_image_urls JSONB NOT NULL DEFAULT '[]');
 CREATE TEMP TABLE image_storage_objects(id BIGSERIAL PRIMARY KEY,provider TEXT NOT NULL,bucket TEXT NOT NULL,object_key TEXT NOT NULL,
 content_type TEXT NOT NULL,byte_size BIGINT NOT NULL,checksum_sha256 TEXT NOT NULL,width INT,height INT,state TEXT NOT NULL DEFAULT 'active',
 deletion_claimed_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),UNIQUE(provider,bucket,object_key));
 CREATE TEMP TABLE async_image_results(id BIGSERIAL PRIMARY KEY,task_id VARCHAR(64) REFERENCES async_image_tasks(task_id) ON DELETE CASCADE,
 provider TEXT NOT NULL,bucket TEXT NOT NULL,object_key TEXT NOT NULL,content_type TEXT NOT NULL,byte_size BIGINT NOT NULL,checksum TEXT NOT NULL,
 width INT,height INT,storage_object_id BIGINT REFERENCES image_storage_objects(id),cleanup_claimed_at TIMESTAMPTZ,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
 CREATE INDEX ON async_image_results(storage_object_id);
 CREATE INDEX ON async_image_results(created_at,id) WHERE cleanup_claimed_at IS NULL;
 CREATE TEMP TABLE image_library_items(id BIGSERIAL PRIMARY KEY,storage_object_id BIGINT,deleted_at TIMESTAMPTZ);
 CREATE INDEX ON image_library_items(storage_object_id);
 CREATE TEMP TABLE image_plaza_publications(id BIGSERIAL PRIMARY KEY,library_item_id BIGINT,status TEXT,expires_at TIMESTAMPTZ);
 CREATE INDEX ON image_plaza_publications(library_item_id);
 CREATE TEMP TABLE image_library_cleanup_jobs(id BIGSERIAL PRIMARY KEY,requested_by BIGINT REFERENCES users(id),scope VARCHAR(32) NOT NULL,
 filters JSONB NOT NULL DEFAULT '{}',status VARCHAR(20) NOT NULL DEFAULT 'pending',lease_version BIGINT NOT NULL DEFAULT 0,
 scanned_count BIGINT NOT NULL DEFAULT 0,deleted_count BIGINT NOT NULL DEFAULT 0,deleted_bytes BIGINT NOT NULL DEFAULT 0,
 last_error TEXT,started_at TIMESTAMPTZ,finished_at TIMESTAMPTZ,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());`)
	require.NoError(t, err)
	migration, err := migrations.FS.ReadFile("228_ZJ_image_result_cleanup.sql")
	require.NoError(t, err)
	// Run the real migration twice; only table creation becomes connection-local.
	statement := strings.ReplaceAll(string(migration), "CREATE TABLE IF NOT EXISTS", "CREATE TEMP TABLE IF NOT EXISTS")
	_, err = db.Exec(statement)
	require.NoError(t, err)
	_, err = db.Exec(statement)
	require.NoError(t, err)
	indexes, err := migrations.FS.ReadFile("229_ZJ_image_result_cleanup_indexes_notx.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		for _, statement := range strings.Split(string(indexes), ";") {
			if strings.TrimSpace(statement) != "" {
				_, err = db.Exec(statement)
				require.NoError(t, err)
			}
		}
	}
	return db
}

func seedResultCleanup(t *testing.T, db *sql.DB, count int) {
	t.Helper()
	for _, statement := range strings.Split(`INSERT INTO async_image_tasks(task_id,status,result_references_checked) SELECT 'task-'||g,'succeeded',TRUE FROM generate_series(1,$1) g;
 INSERT INTO image_storage_objects(provider,bucket,object_key,content_type,byte_size,checksum_sha256,storage_identity)
 SELECT 'custom_s3','results','results/'||g||'.png','image/png',100,repeat('a',64),'target' FROM generate_series(1,$1) g;
 INSERT INTO async_image_results(task_id,provider,bucket,object_key,content_type,byte_size,checksum,storage_object_id,created_at)
 SELECT 'task-'||g,'custom_s3','results','results/'||g||'.png','image/png',100,repeat('a',64),g,NOW()-INTERVAL '2 days' FROM generate_series(1,$1) g;`, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		_, err := db.Exec(statement, count)
		require.NoError(t, err)
	}
}

func newResultCleanupTestJob(t *testing.T, repo *imageLibraryRepository) (*service.ImageResultCleanupJob, time.Time) {
	t.Helper()
	now := time.Now().UTC()
	request := service.ImageResultCleanupRequest{Before: now.Add(-time.Hour), Target: service.ImageResultCleanupTarget{Provider: "custom_s3", Bucket: "results", Identity: "target"}, Source: "manual"}
	hash := fmt.Sprintf("%064d", now.UnixNano())
	require.NoError(t, repo.SaveImageResultCleanupPreview(context.Background(), hash, 1, request, now.Add(time.Minute)))
	job, err := repo.CreateImageResultCleanupJobFromPreview(context.Background(), hash, 1, now)
	require.NoError(t, err)
	replayed, err := repo.CreateImageResultCleanupJobFromPreview(context.Background(), hash, 1, now)
	require.NoError(t, err)
	require.Equal(t, job.ID, replayed.ID)
	claimed, err := repo.ClaimImageResultCleanupJob(context.Background(), now, now.Add(time.Minute))
	require.NoError(t, err)
	require.NotNil(t, claimed)
	return claimed, now
}

func TestImageResultCleanupPostgresRecoveryAndReferences(t *testing.T) {
	db := resultCleanupPostgresDB(t)
	seedResultCleanup(t, db, 8)
	repo := &imageLibraryRepository{db: db}
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO image_library_items(storage_object_id) VALUES(1);
 INSERT INTO async_image_tasks(task_id,status,result_references_checked,reference_image_urls)
 VALUES('dependent','invoking',FALSE,'["https://images.invalid/results/2.png"]');
 UPDATE async_image_tasks SET status='invoking' WHERE task_id='task-3';
 UPDATE image_storage_objects SET storage_identity='' WHERE id=4;
 UPDATE image_storage_objects SET storage_identity='other-target' WHERE id=6;`)
	require.NoError(t, err)
	backfilled, err := repo.BackfillAsyncImageResultReferences(ctx, service.ImageResultReferenceConfig{
		Backend: "oss", Provider: "custom_s3", Bucket: "results", StorageIdentity: "target", URLBases: []string{"https://images.invalid"}, ResultPrefix: "results/",
	}, 50)
	require.NoError(t, err)
	require.True(t, backfilled)
	job, now := newResultCleanupTestJob(t, repo)
	other, err := repo.ClaimImageResultCleanupJob(ctx, now, now.Add(time.Minute))
	require.NoError(t, err)
	require.Nil(t, other, "only one live result job lease")
	for _, id := range []int64{1, 2, 3} {
		ticket, err := repo.ClaimImageResultCleanupObject(ctx, job.ID, job.LeaseVersion, now)
		require.NoError(t, err)
		require.Equal(t, id, ticket.ResultID)
		require.Equal(t, "protected", ticket.Action)
	}
	var remaining int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM async_image_results WHERE id IN (1,2,3)`).Scan(&remaining))
	require.Equal(t, 2, remaining, "active dependencies retain their result; library detaches only")
	ticket, err := repo.ClaimImageResultCleanupObject(ctx, job.ID, job.LeaseVersion, now)
	require.NoError(t, err)
	require.Equal(t, int64(4), ticket.ResultID)
	require.Empty(t, ticket.Object.StorageIdentity)
	require.ErrorIs(t, repo.CompleteImageResultCleanupObject(ctx, job.ID, job.LeaseVersion, ticket.ID, "deleted", nil, ""), service.ErrImageResultCleanupConflict)
	require.NoError(t, repo.VerifyImageResultCleanupObject(ctx, job.ID, job.LeaseVersion, ticket.ID, "target"))
	retryAt := now.Add(-time.Second)
	require.NoError(t, repo.CompleteImageResultCleanupObject(ctx, job.ID, job.LeaseVersion, ticket.ID, "retry", &retryAt, "timeout"))
	require.NoError(t, repo.ReleaseImageResultCleanupJob(ctx, job.ID, job.LeaseVersion, "pending", nil, ""))
	job, err = repo.ClaimImageResultCleanupJob(ctx, time.Now().UTC(), time.Now().UTC().Add(time.Minute))
	require.NoError(t, err)
	resumed, err := repo.ClaimImageResultCleanupObject(ctx, job.ID, job.LeaseVersion, now)
	require.NoError(t, err)
	require.Equal(t, ticket.ID, resumed.ID)
	require.Equal(t, "target", resumed.Object.StorageIdentity)
	require.NoError(t, repo.CompleteImageResultCleanupObject(ctx, job.ID, job.LeaseVersion, resumed.ID, "missing", nil, ""))
	var state string
	require.NoError(t, db.QueryRow(`SELECT state FROM image_storage_objects WHERE id=4`).Scan(&state))
	require.Equal(t, "deleted", state)
	ticket, err = repo.ClaimImageResultCleanupObject(ctx, job.ID, job.LeaseVersion, now)
	require.NoError(t, err)
	require.Equal(t, int64(5), ticket.ResultID)
	// Old stale recovery is forbidden from consuming the new protocol's active ticket.
	_, err = db.Exec(`UPDATE image_storage_objects SET deletion_claimed_at=NOW()-INTERVAL '1 hour' WHERE id=5`)
	require.NoError(t, err)
	stale, err := repo.ClaimStaleCleanupObjects(ctx, time.Now().Add(-time.Minute), 100)
	require.NoError(t, err)
	require.Empty(t, stale)
	require.NoError(t, repo.CompleteImageResultCleanupObject(ctx, job.ID, job.LeaseVersion, ticket.ID, "deleted", nil, ""))
	ticket, err = repo.ClaimImageResultCleanupObject(ctx, job.ID, job.LeaseVersion, now)
	require.NoError(t, err)
	require.Equal(t, int64(6), ticket.ResultID)
	require.Equal(t, "skipped", ticket.Action)
	ticket, err = repo.ClaimImageResultCleanupObject(ctx, job.ID, job.LeaseVersion, now)
	require.NoError(t, err)
	require.Equal(t, int64(7), ticket.ResultID)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.ErrorIs(t, bindResultObjectReferences(ctx, tx, "dependent", []service.ObjectRef{ticket.Object}), service.ErrAsyncImageInvalidInput)
	require.NoError(t, tx.Rollback())
	require.NoError(t, repo.CompleteImageResultCleanupObject(ctx, job.ID, job.LeaseVersion, ticket.ID, "skipped", nil, "历史对象校验值或大小不一致"))
	_, err = repo.ControlImageResultCleanupJob(ctx, job.ID, "cancel")
	require.NoError(t, err)
	alive, err := repo.HeartbeatImageResultCleanupJob(ctx, job.ID, job.LeaseVersion, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.False(t, alive)
	require.NoError(t, repo.ReleaseImageResultCleanupJob(ctx, job.ID, job.LeaseVersion, "pending", nil, ""))
	cancelled, err := repo.GetImageResultCleanupJob(ctx, job.ID)
	require.NoError(t, err)
	require.NotNil(t, cancelled.LastError)
	require.Equal(t, "历史对象校验值或大小不一致", *cancelled.LastError, "empty release reason must retain the visible skip reason")
	job, err = repo.ControlImageResultCleanupJob(ctx, job.ID, "resume")
	require.NoError(t, err)
	require.Equal(t, "pending", job.Status)
	job, err = repo.GetImageResultCleanupJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, int64(3), job.ProtectedCount)
	require.Equal(t, int64(3), job.DetachedCount)
	require.Equal(t, int64(1), job.DeletedCount)
	require.Equal(t, int64(100), job.DeletedBytes)
}

func TestImageResultCleanupPostgresPolicyScheduleAndPreview(t *testing.T) {
	db := resultCleanupPostgresDB(t)
	repo := &imageLibraryRepository{db: db}
	ctx := context.Background()
	now := time.Now().UTC()
	p, err := repo.GetImageResultCleanupPolicy(ctx)
	require.NoError(t, err)
	require.Nil(t, p)
	p, err = repo.SaveImageResultCleanupPolicy(ctx, service.ImageResultCleanupPolicy{Enabled: true, NextRunAt: &now}, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), p.Revision)
	_, err = repo.SaveImageResultCleanupPolicy(ctx, *p, 0)
	require.ErrorIs(t, err, service.ErrImageResultCleanupConflict)
	request := service.ImageResultCleanupRequest{Before: now.Add(-time.Hour), Target: service.ImageResultCleanupTarget{Provider: "custom_s3", Bucket: "results", Identity: "target"}, Source: "scheduled", PolicyRevision: p.Revision}
	job, err := repo.EnqueueScheduledImageResultCleanup(ctx, request, now.Add(time.Second), now.Add(time.Hour))
	require.NoError(t, err)
	require.NotNil(t, job)
	duplicate, err := repo.EnqueueScheduledImageResultCleanup(ctx, request, now.Add(time.Second), now.Add(time.Hour))
	require.NoError(t, err)
	require.Nil(t, duplicate)
	p, err = repo.GetImageResultCleanupPolicy(ctx)
	require.NoError(t, err)
	require.True(t, p.NextRunAt.After(now))
	_, err = repo.CreateImageResultCleanupJobFromPreview(ctx, strings.Repeat("0", 64), 1, now)
	require.ErrorIs(t, err, service.ErrImageResultCleanupPreviewExpired)
	_, err = db.Exec(`UPDATE image_library_cleanup_jobs SET status='succeeded' WHERE id=$1`, job.ID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO image_library_cleanup_jobs(scope,status,lease_version)VALUES('all','running',3)`)
	require.NoError(t, err)
	var parentID int64
	require.NoError(t, db.QueryRow(`SELECT MAX(id) FROM image_library_cleanup_jobs`).Scan(&parentID))
	done, err := repo.PrepareLegacyImageResultCleanup(ctx, parentID, 3, request)
	require.NoError(t, err)
	require.False(t, done)
	_, err = db.Exec(`UPDATE image_library_cleanup_jobs SET status='succeeded' WHERE id=(SELECT result_child_job_id FROM image_library_cleanup_jobs WHERE id=$1)`, parentID)
	require.NoError(t, err)
	done, err = repo.PrepareLegacyImageResultCleanup(ctx, parentID, 3, request)
	require.NoError(t, err)
	require.True(t, done)
}

func TestImageResultCleanupPostgresHundredThousandBounded(t *testing.T) {
	db := resultCleanupPostgresDB(t)
	seedResultCleanup(t, db, 100000)
	repo := &imageLibraryRepository{db: db}
	ctx := context.Background()
	_, err := db.Exec(`ANALYZE async_image_results; ANALYZE async_image_tasks; ANALYZE image_storage_objects;`)
	require.NoError(t, err)
	job, now := newResultCleanupTestJob(t, repo)
	started := time.Now()
	preview, err := repo.PreviewImageResultCleanup(ctx, service.ImageResultCleanupRequest{Before: job.Before, Target: job.Target}, 1000)
	require.NoError(t, err)
	require.True(t, preview.Truncated)
	require.Equal(t, int64(1000), preview.ScannedCount)
	previewElapsed := time.Since(started)
	started = time.Now()
	// Simulate new generation completions while remote deletes happen outside DB
	// transactions. Even a single-connection pool must remain available to intake.
	type generationResult struct {
		elapsed time.Duration
		err     error
	}
	generated := make(chan generationResult, 20)
	go func() {
		for i := 0; i < 20; i++ {
			begin := time.Now()
			_, insertErr := db.Exec(`WITH t AS(INSERT INTO async_image_tasks(task_id,status,result_references_checked)
 VALUES('new-'||$1::text,'succeeded',TRUE) RETURNING task_id),
 o AS(INSERT INTO image_storage_objects(provider,bucket,object_key,content_type,byte_size,checksum_sha256,storage_identity)
 VALUES('custom_s3','results','new/'||$1::text,'image/png',10,repeat('b',64),'target') RETURNING id,provider,bucket,object_key,content_type,byte_size,checksum_sha256)
 INSERT INTO async_image_results(task_id,provider,bucket,object_key,content_type,byte_size,checksum,storage_object_id)
 SELECT t.task_id,o.provider,o.bucket,o.object_key,o.content_type,o.byte_size,o.checksum_sha256,o.id FROM t,o`, i)
			generated <- generationResult{time.Since(begin), insertErr}
		}
	}()
	for i := 0; i < 50; i++ {
		ticket, err := repo.ClaimImageResultCleanupObject(ctx, job.ID, job.LeaseVersion, now)
		require.NoError(t, err)
		require.Equal(t, "delete", ticket.Action)
		// A small mock-storage delay makes transaction isolation observable.
		time.Sleep(time.Millisecond)
		require.NoError(t, repo.CompleteImageResultCleanupObject(ctx, job.ID, job.LeaseVersion, ticket.ID, "deleted", nil, ""))
	}
	elapsed := time.Since(started)
	var worstGeneration time.Duration
	for i := 0; i < 20; i++ {
		result := <-generated
		require.NoError(t, result.err)
		if result.elapsed > worstGeneration {
			worstGeneration = result.elapsed
		}
	}
	t.Logf("100000 metadata rows: bounded 1000-row preview=%s; 50 claim/complete transactions=%s; 20 concurrent generation completions worst=%s", previewElapsed, elapsed, worstGeneration)
	require.Less(t, previewElapsed, 10*time.Second)
	require.Less(t, elapsed, 20*time.Second)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM async_image_results`).Scan(&count))
	require.Equal(t, 99970, count)
	var plan []byte
	err = db.QueryRow(`EXPLAIN(FORMAT JSON)SELECT r.id FROM async_image_results r WHERE (r.created_at,r.id)>(NOW()-INTERVAL '3 days',50000) AND r.created_at<NOW()-INTERVAL '1 day' AND r.cleanup_claimed_at IS NULL ORDER BY r.created_at,r.id LIMIT 1`).Scan(&plan)
	require.NoError(t, err)
	var parsed any
	require.NoError(t, json.Unmarshal(plan, &parsed))
	require.Contains(t, string(plan), "Index")
	require.NotContains(t, string(plan), "Seq Scan")
	// Reverse the age distribution: most IDs are new and only a tiny old tail
	// qualifies. The date-first seek must not scan through the new generation rows.
	_, err = db.Exec(`UPDATE async_image_results SET created_at=CASE WHEN id BETWEEN 99991 AND 100000 THEN NOW()-INTERVAL '2 days' ELSE NOW() END;
 ANALYZE async_image_results;`)
	require.NoError(t, err)
	err = db.QueryRow(`EXPLAIN(ANALYZE,BUFFERS,FORMAT JSON)
 SELECT r.id FROM async_image_results r JOIN async_image_tasks t ON t.task_id=r.task_id
 WHERE(r.created_at,r.id)>('1970-01-01'::timestamptz,0) AND r.created_at<NOW()-INTERVAL '1 day'
 AND r.provider='custom_s3' AND r.bucket='results' AND r.cleanup_claimed_at IS NULL
 ORDER BY r.created_at,r.id LIMIT 1 FOR UPDATE OF t,r SKIP LOCKED`).Scan(&plan)
	require.NoError(t, err)
	require.Contains(t, string(plan), "Index Scan")
	require.NotContains(t, string(plan), "Seq Scan")
	t.Logf("Sparse old tail EXPLAIN uses indexed date seek and indexed task lookup (%d plan bytes)", len(plan))
}
