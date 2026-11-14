//go:build postgres

package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageCleanupHistoryPostgresRetention(t *testing.T) {
	db := resultCleanupPostgresDB(t)
	repo := requireImageLibraryRepository(t, db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	before := now.Add(-24 * time.Hour)
	old := before.Add(-time.Hour)
	setup := `INSERT INTO image_storage_objects(id,provider,bucket,object_key,content_type,byte_size,checksum_sha256)
 SELECT i,'custom_s3','results','history/'||i,'image/png',10,repeat('b',64) FROM generate_series(1,3)i;
 INSERT INTO image_library_cleanup_jobs(id,scope,status,result_request,finished_at,updated_at,created_at)
 SELECT id,'async_results',status,'{}'::jsonb,$1,$1,$1 FROM (VALUES
 (1,'succeeded'),(2,'succeeded'),(3,'failed'),(4,'succeeded'),(5,'cancelled'),(6,'pending'),
 (7,'running'),(8,'paused'),(9,'failed'),(10,'cancelled'),(11,'failed'),(12,'succeeded'),(13,'succeeded'))v(id,status);
 UPDATE image_library_cleanup_jobs SET finished_at=$2 WHERE id=2;
 UPDATE image_library_cleanup_jobs SET finished_at=$3 WHERE id=3;
 UPDATE image_library_cleanup_jobs SET finished_at=NULL WHERE id IN (4,5,6,7,8);
 UPDATE image_library_cleanup_jobs SET result_lease_until=NOW()+INTERVAL '1 hour' WHERE id=11;
 INSERT INTO image_result_cleanup_objects(job_id,result_id,storage_object_id,object_ref,state)VALUES
 (1,101,1,'{}','deleted'),(9,109,2,'{}','retry'),(10,110,3,'{}','deleting');
 INSERT INTO image_result_cleanup_previews(token_hash,requested_by,request,expires_at,consumed_job_id)VALUES
 ('unexpired',1,'{}',NOW()+INTERVAL '5 minutes',12),('expired',1,'{}',NOW()-INTERVAL '1 minute',13);`
	_, err := db.Exec(strings.NewReplacer("$1", cleanupHistoryTestTimestamp(old), "$2", cleanupHistoryTestTimestamp(now), "$3", cleanupHistoryTestTimestamp(before)).Replace(setup))
	require.NoError(t, err)
	deleted, err := repo.PruneCleanupJobHistory(ctx, before, 2)
	require.NoError(t, err)
	require.EqualValues(t, 2, deleted, "the cleanup must respect its job batch bound")
	deleted, err = repo.PruneCleanupJobHistory(ctx, before, 500)
	require.NoError(t, err)
	require.EqualValues(t, 2, deleted)
	var ids []int64
	rows, err := db.Query(`SELECT id FROM image_library_cleanup_jobs ORDER BY id`)
	require.NoError(t, err)
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, []int64{2, 3, 6, 7, 8, 9, 10, 11, 12}, ids,
		"recent completion, exact cutoff, active jobs, unresolved tickets, valid lease and live preview must survive")
	var objects, tickets, previews int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM image_storage_objects`).Scan(&objects))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM image_result_cleanup_objects`).Scan(&tickets))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM image_result_cleanup_previews`).Scan(&previews))
	require.Equal(t, 3, objects, "pruning task records must never remove storage objects")
	require.Equal(t, 2, tickets, "only completed bookkeeping tickets may be removed")
	require.Equal(t, 1, previews, "only expired preview credentials may be removed")
	for _, resultOnly := range []bool{false, true} {
		var listed []int64
		if resultOnly {
			jobs, listErr := repo.ListImageResultCleanupJobs(ctx, 100)
			require.NoError(t, listErr)
			for _, job := range jobs {
				listed = append(listed, job.ID)
			}
		} else {
			jobs, listErr := repo.ListCleanupJobs(ctx, 100)
			require.NoError(t, listErr)
			for _, job := range jobs {
				listed = append(listed, job.ID)
			}
		}
		require.Equal(t, []int64{10, 9, 8, 7, 6}, listed, "both lists cap at five and retain old recoverable work")
	}
	_, err = db.Exec(`UPDATE image_result_cleanup_objects SET state='protected' WHERE job_id=9`)
	require.NoError(t, err)
	deleted, err = repo.PruneCleanupJobHistory(ctx, before, 500)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted, "resolved recovery work can expire on the next pass")
}

func TestImageCleanupHistoryPostgresParentChildProtection(t *testing.T) {
	db := resultCleanupPostgresDB(t)
	repo := requireImageLibraryRepository(t, db)
	before := time.Now().UTC().Add(-24 * time.Hour)
	setup := `INSERT INTO image_storage_objects(id,provider,bucket,object_key,content_type,byte_size,checksum_sha256)
 VALUES(1,'custom_s3','results','history/protected','image/png',10,repeat('b',64));
 INSERT INTO image_library_cleanup_jobs(id,scope,status,result_request,finished_at,updated_at,created_at)
 SELECT id,'async_results',status,'{}'::jsonb,$1,$1,$1 FROM (VALUES
 (20,'succeeded'),(21,'succeeded'),(22,'running'),(23,'succeeded'),
 (24,'pending'),(25,'failed'),(26,'succeeded'),(27,'failed'))v(id,status);
 UPDATE image_library_cleanup_jobs SET result_child_job_id=21 WHERE id=20;
 UPDATE image_library_cleanup_jobs SET result_child_job_id=23 WHERE id=22;
 UPDATE image_library_cleanup_jobs SET result_child_job_id=24 WHERE id=25;
 UPDATE image_library_cleanup_jobs SET result_child_job_id=27 WHERE id=26;
 INSERT INTO image_result_cleanup_objects(job_id,result_id,storage_object_id,object_ref,state)VALUES(27,127,1,'{}','retry');`
	_, err := db.Exec(strings.ReplaceAll(setup, "$1", cleanupHistoryTestTimestamp(before.Add(-time.Hour))))
	require.NoError(t, err)
	deleted, err := repo.PruneCleanupJobHistory(context.Background(), before, 500)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted, "delete a completed parent before considering its child")
	deleted, err = repo.PruneCleanupJobHistory(context.Background(), before, 500)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted, "the unreferenced completed child expires on the following pass")
	var remaining int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM image_library_cleanup_jobs WHERE id IN (22,23,24,25,26,27)`).Scan(&remaining))
	require.Equal(t, 6, remaining, "active parent, active child and unresolved child chains must survive")
}

func cleanupHistoryTestTimestamp(t time.Time) string {
	return "'" + t.Format(time.RFC3339Nano) + "'::timestamptz"
}

func TestImageCleanupHistoryPostgresSkipsConcurrentWork(t *testing.T) {
	db := resultCleanupConcurrentPostgresDB(t)
	repo := requireImageLibraryRepository(t, db)
	before := time.Now().UTC().Add(-24 * time.Hour)
	_, err := db.Exec(`INSERT INTO image_library_cleanup_jobs(id,scope,status,finished_at,updated_at,created_at)
 SELECT i,'expired','succeeded',NOW()-INTERVAL '2 days',NOW()-INTERVAL '2 days',NOW()-INTERVAL '2 days' FROM generate_series(31,33)i;
 INSERT INTO image_result_cleanup_previews(token_hash,requested_by,request,expires_at,consumed_job_id)
 VALUES('locked-preview',1,'{}',NOW()-INTERVAL '1 minute',33)`)
	require.NoError(t, err)
	jobLock, err := db.Begin()
	require.NoError(t, err)
	defer jobLock.Rollback()
	_, err = jobLock.Exec(`SELECT id FROM image_library_cleanup_jobs WHERE id=31 FOR UPDATE`)
	require.NoError(t, err)
	previewLock, err := db.Begin()
	require.NoError(t, err)
	defer previewLock.Rollback()
	_, err = previewLock.Exec(`SELECT token_hash FROM image_result_cleanup_previews WHERE token_hash='locked-preview' FOR UPDATE`)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	deleted, err := repo.PruneCleanupJobHistory(ctx, before, 500)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted, "skip the locked job and the job whose expired preview cannot yet be removed")
	require.NoError(t, jobLock.Rollback())
	require.NoError(t, previewLock.Rollback())
	queueLock, err := db.Begin()
	require.NoError(t, err)
	defer queueLock.Rollback()
	_, err = queueLock.Exec(`SELECT pg_advisory_xact_lock(8017722801)`)
	require.NoError(t, err)
	deleted, err = repo.PruneCleanupJobHistory(ctx, before, 500)
	require.NoError(t, err)
	require.Zero(t, deleted, "a queue operation wins admission immediately")
	require.NoError(t, queueLock.Rollback())
	deleted, err = repo.PruneCleanupJobHistory(context.Background(), before, 500)
	require.NoError(t, err)
	require.EqualValues(t, 2, deleted, "records become eligible after concurrent work releases its locks")
}

func TestImageCleanupHistoryPostgresBoundsTicketHistory(t *testing.T) {
	db := resultCleanupPostgresDB(t)
	repo := requireImageLibraryRepository(t, db)
	_, err := db.Exec(`INSERT INTO image_storage_objects(id,provider,bucket,object_key,content_type,byte_size,checksum_sha256)
 VALUES(1,'custom_s3','results','history/many-tickets','image/png',10,repeat('b',64));
 INSERT INTO image_library_cleanup_jobs(id,scope,status,result_request,finished_at)
 VALUES(1,'async_results','succeeded','{}',NOW()-INTERVAL '2 days');
 INSERT INTO image_result_cleanup_objects(job_id,result_id,storage_object_id,object_ref,state)
 SELECT 1,i,1,'{}','deleted' FROM generate_series(1,6001)i`)
	require.NoError(t, err)
	before := time.Now().UTC().Add(-24 * time.Hour)
	deleted, err := repo.PruneCleanupJobHistory(context.Background(), before, 500)
	require.NoError(t, err)
	require.Zero(t, deleted, "retain the job while its completed ticket batch is not drained")
	var jobs, tickets int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM image_library_cleanup_jobs`).Scan(&jobs))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM image_result_cleanup_objects`).Scan(&tickets))
	require.Equal(t, 1, jobs)
	require.Equal(t, 1001, tickets, "each pass removes at most 5000 tickets")
	deleted, err = repo.PruneCleanupJobHistory(context.Background(), before, 500)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM image_result_cleanup_objects`).Scan(&tickets))
	require.Zero(t, tickets)
	var objects int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM image_storage_objects`).Scan(&objects))
	require.Equal(t, 1, objects, "history pruning cannot remove storage identity records")
}
