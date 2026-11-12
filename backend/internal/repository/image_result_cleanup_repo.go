package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.ImageResultCleanupRepository = (*imageLibraryRepository)(nil)

const resultCleanupJobColumns = `id,requested_by,scope,status,result_request,lease_version,result_lease_until,result_cursor,result_cursor_created_at,
 scanned_count,deleted_count,deleted_bytes,detached_count,protected_count,skipped_count,failed_count,
 cancel_requested,next_attempt_at,last_error,created_at,finished_at`

type resultCleanupScanner interface{ Scan(...any) error }

func scanResultCleanupJob(row resultCleanupScanner) (*service.ImageResultCleanupJob, error) {
	job := &service.ImageResultCleanupJob{}
	var raw []byte
	if err := row.Scan(&job.ID, &job.RequestedBy, &job.Scope, &job.Status, &raw, &job.LeaseVersion, &job.LeaseUntil, &job.Cursor, &job.CursorCreatedAt,
		&job.ScannedCount, &job.DeletedCount, &job.DeletedBytes, &job.DetachedCount, &job.ProtectedCount, &job.SkippedCount, &job.FailedCount,
		&job.CancelRequested, &job.NextAttemptAt, &job.LastError, &job.CreatedAt, &job.FinishedAt); err != nil {
		return nil, err
	}
	var request service.ImageResultCleanupRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	job.Before, job.Source, job.Target, job.PolicyRevision = request.Before, request.Source, request.Target, request.PolicyRevision
	job.PolicySnapshot = request.PolicySnapshot
	return job, nil
}

func (r *imageLibraryRepository) GetImageResultCleanupPolicy(ctx context.Context) (*service.ImageResultCleanupPolicy, error) {
	var raw []byte
	p := &service.ImageResultCleanupPolicy{}
	err := r.db.QueryRowContext(ctx, `SELECT config,revision,next_run_at,updated_by FROM image_result_cleanup_policy WHERE id=1`).Scan(&raw, &p.Revision, &p.NextRunAt, &p.UpdatedBy)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var config service.ImageResultCleanupPolicy
	if err = json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	config.Revision, config.NextRunAt, config.UpdatedBy = p.Revision, p.NextRunAt, p.UpdatedBy
	return &config, nil
}

func (r *imageLibraryRepository) SaveImageResultCleanupPolicy(ctx context.Context, p service.ImageResultCleanupPolicy, expected int64) (*service.ImageResultCleanupPolicy, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var revision int64
	if expected == 0 {
		err = r.db.QueryRowContext(ctx, `INSERT INTO image_result_cleanup_policy(id,config,revision,next_run_at,updated_by)
 VALUES(1,$1,1,$2,$3) ON CONFLICT(id) DO NOTHING RETURNING revision`, raw, p.NextRunAt, p.UpdatedBy).Scan(&revision)
	} else {
		err = r.db.QueryRowContext(ctx, `UPDATE image_result_cleanup_policy SET config=$1,revision=revision+1,next_run_at=$2,updated_by=$3,updated_at=NOW()
 WHERE id=1 AND revision=$4 RETURNING revision`, raw, p.NextRunAt, p.UpdatedBy, expected).Scan(&revision)
	}
	if err == sql.ErrNoRows {
		return nil, service.ErrImageResultCleanupConflict
	}
	if err != nil {
		return nil, err
	}
	p.Revision = revision
	return &p, nil
}

// The same predicate protects preview and claim. Unknown input history fails closed.
const resultCleanupDependencySQL = `EXISTS(SELECT 1 FROM async_image_task_result_references d JOIN async_image_tasks dt ON dt.task_id=d.task_id
 WHERE d.storage_object_id=o.id AND dt.status NOT IN ('succeeded','failed','expired'))
 OR EXISTS(SELECT 1 FROM async_image_tasks ut WHERE NOT ut.result_references_checked AND ut.status NOT IN ('succeeded','failed','expired'))`
const resultCleanupSharedSQL = `EXISTS(SELECT 1 FROM image_library_items li WHERE li.storage_object_id=o.id AND li.deleted_at IS NULL)
 OR EXISTS(SELECT 1 FROM image_plaza_publications p JOIN image_library_items li ON li.id=p.library_item_id
 WHERE li.storage_object_id=o.id AND p.status IN ('pending_review','published','admin_hidden') AND p.expires_at>NOW())
 OR EXISTS(SELECT 1 FROM async_image_results other WHERE other.id<>r.id AND (other.storage_object_id=o.id
 OR (other.provider=r.provider AND other.bucket=r.bucket AND other.object_key=r.object_key)))`

func (r *imageLibraryRepository) PreviewImageResultCleanup(ctx context.Context, request service.ImageResultCleanupRequest, limit int) (*service.ImageResultCleanupPreview, error) {
	if limit < 1 || limit > 10000 {
		limit = 1000
	}
	preview := &service.ImageResultCleanupPreview{Before: request.Before}
	err := r.db.QueryRowContext(ctx, `WITH candidates AS (
 SELECT r.id,r.byte_size,t.status,t.cleanup_claimed_at task_claim,r.cleanup_claimed_at result_claim,o.state,
 (`+resultCleanupDependencySQL+`) dependency,(`+resultCleanupSharedSQL+`) shared
 FROM async_image_results r JOIN async_image_tasks t ON t.task_id=r.task_id
 LEFT JOIN image_storage_objects o ON o.id=r.storage_object_id
 WHERE r.created_at<=$1 AND r.provider=$2 AND r.bucket=$3 AND r.cleanup_claimed_at IS NULL ORDER BY r.created_at,r.id LIMIT $4)
 SELECT COUNT(*),COUNT(*) FILTER(WHERE status IN ('succeeded','failed','expired') AND task_claim IS NULL AND result_claim IS NULL AND NOT dependency),
 COALESCE(SUM(byte_size) FILTER(WHERE status IN ('succeeded','failed','expired') AND task_claim IS NULL AND result_claim IS NULL AND NOT dependency AND NOT shared),0),
 COUNT(*) FILTER(WHERE shared OR dependency OR status NOT IN ('succeeded','failed','expired')) FROM candidates`, request.Before, request.Target.Provider, request.Target.Bucket, limit).
		Scan(&preview.ScannedCount, &preview.MatchedItems, &preview.MatchedBytes, &preview.ProtectedCount)
	preview.Truncated = preview.ScannedCount == int64(limit)
	return preview, err
}

func (r *imageLibraryRepository) SaveImageResultCleanupPreview(ctx context.Context, hash string, adminID int64, request service.ImageResultCleanupRequest, expiry time.Time) error {
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO image_result_cleanup_previews(token_hash,requested_by,request,expires_at) VALUES($1,$2,$3,$4)`, hash, adminID, raw, expiry)
	if err == nil {
		_, _ = r.db.ExecContext(ctx, `DELETE FROM image_result_cleanup_previews WHERE token_hash IN (SELECT token_hash FROM image_result_cleanup_previews WHERE expires_at<NOW()-INTERVAL '1 day' LIMIT 100)`)
	}
	return err
}

func lockResultCleanupQueue(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(8017722801)`)
	return err
}
func insertResultCleanupJob(ctx context.Context, tx *sql.Tx, adminID *int64, request service.ImageResultCleanupRequest) (*service.ImageResultCleanupJob, error) {
	var busy bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM image_library_cleanup_jobs WHERE result_request IS NOT NULL AND status IN ('pending','running','paused'))`).Scan(&busy); err != nil {
		return nil, err
	}
	if busy {
		return nil, service.ErrImageResultCleanupConflict
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	return scanResultCleanupJob(tx.QueryRowContext(ctx, `INSERT INTO image_library_cleanup_jobs(requested_by,scope,filters,result_request)
 VALUES($1,'async_results',jsonb_build_object('before',$2::timestamptz),$3) RETURNING `+resultCleanupJobColumns, adminID, request.Before, raw))
}

func (r *imageLibraryRepository) CreateImageResultCleanupJobFromPreview(ctx context.Context, hash string, adminID int64, now time.Time) (*service.ImageResultCleanupJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = lockResultCleanupQueue(ctx, tx); err != nil {
		return nil, err
	}
	var raw []byte
	var expiry time.Time
	var consumed *int64
	err = tx.QueryRowContext(ctx, `SELECT request,expires_at,consumed_job_id FROM image_result_cleanup_previews WHERE token_hash=$1 AND requested_by=$2 FOR UPDATE`, hash, adminID).Scan(&raw, &expiry, &consumed)
	if err == sql.ErrNoRows {
		return nil, service.ErrImageResultCleanupPreviewExpired
	}
	if err != nil {
		return nil, err
	}
	if consumed != nil {
		job, readErr := scanResultCleanupJob(tx.QueryRowContext(ctx, `SELECT `+resultCleanupJobColumns+` FROM image_library_cleanup_jobs WHERE id=$1`, *consumed))
		if readErr != nil {
			return nil, readErr
		}
		return job, tx.Commit()
	}
	if !expiry.After(now) {
		return nil, service.ErrImageResultCleanupPreviewExpired
	}
	var request service.ImageResultCleanupRequest
	if err = json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	job, err := insertResultCleanupJob(ctx, tx, &adminID, request)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_result_cleanup_previews SET consumed_job_id=$2 WHERE token_hash=$1`, hash, job.ID); err != nil {
		return nil, err
	}
	return job, tx.Commit()
}

func (r *imageLibraryRepository) EnqueueScheduledImageResultCleanup(ctx context.Context, request service.ImageResultCleanupRequest, now, next time.Time) (*service.ImageResultCleanupJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = lockResultCleanupQueue(ctx, tx); err != nil {
		return nil, err
	}
	var due bool
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(next_run_at<=$2,FALSE) AND COALESCE((config->>'enabled')::boolean,FALSE)
 FROM image_result_cleanup_policy WHERE id=1 AND revision=$1 FOR UPDATE`, request.PolicyRevision, now).Scan(&due)
	if err == sql.ErrNoRows {
		return nil, service.ErrImageResultCleanupConflict
	}
	if err != nil {
		return nil, err
	}
	if !due {
		return nil, nil
	}
	job, err := insertResultCleanupJob(ctx, tx, nil, request)
	if err != nil && !errors.Is(err, service.ErrImageResultCleanupConflict) {
		return nil, err
	}
	if _, updateErr := tx.ExecContext(ctx, `UPDATE image_result_cleanup_policy SET next_run_at=$1 WHERE id=1`, next); updateErr != nil {
		return nil, updateErr
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return nil, commitErr
	}
	return job, nil
}

func (r *imageLibraryRepository) ListImageResultCleanupJobs(ctx context.Context, limit int) ([]service.ImageResultCleanupJob, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+resultCleanupJobColumns+` FROM image_library_cleanup_jobs WHERE result_request IS NOT NULL ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]service.ImageResultCleanupJob, 0)
	for rows.Next() {
		job, err := scanResultCleanupJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, *job)
	}
	return jobs, rows.Err()
}
func (r *imageLibraryRepository) GetImageResultCleanupJob(ctx context.Context, id int64) (*service.ImageResultCleanupJob, error) {
	job, err := scanResultCleanupJob(r.db.QueryRowContext(ctx, `SELECT `+resultCleanupJobColumns+` FROM image_library_cleanup_jobs WHERE id=$1 AND result_request IS NOT NULL`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return job, err
}

func (r *imageLibraryRepository) ControlImageResultCleanupJob(ctx context.Context, id int64, action string) (*service.ImageResultCleanupJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = lockResultCleanupQueue(ctx, tx); err != nil {
		return nil, err
	}
	var result sql.Result
	switch action {
	case "cancel":
		result, err = tx.ExecContext(ctx, `UPDATE image_library_cleanup_jobs SET cancel_requested=TRUE,
 status=CASE WHEN status='running' AND result_lease_until>NOW() THEN status ELSE 'cancelled' END,updated_at=NOW()
 WHERE id=$1 AND result_request IS NOT NULL AND status IN ('pending','running','paused')`, id)
	case "resume":
		result, err = tx.ExecContext(ctx, `UPDATE image_library_cleanup_jobs SET cancel_requested=FALSE,status='pending',last_error=NULL,finished_at=NULL,next_attempt_at=NULL,updated_at=NOW()
 WHERE id=$1 AND result_request IS NOT NULL AND status IN ('cancelled','paused','failed') AND NOT EXISTS(
 SELECT 1 FROM image_library_cleanup_jobs j WHERE j.id<>$1 AND j.result_request IS NOT NULL AND j.status IN ('pending','running','paused'))`, id)
	default:
		return nil, fmt.Errorf("invalid cleanup action %q", action)
	}
	if err = requireImageLibraryLease(result, err); err != nil {
		return nil, err
	}
	job, err := scanResultCleanupJob(tx.QueryRowContext(ctx, `SELECT `+resultCleanupJobColumns+` FROM image_library_cleanup_jobs WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	return job, tx.Commit()
}

func (r *imageLibraryRepository) ClaimImageResultCleanupJob(ctx context.Context, now, until time.Time) (*service.ImageResultCleanupJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = lockResultCleanupQueue(ctx, tx); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE image_library_cleanup_jobs SET status='cancelled',result_lease_until=NULL,finished_at=$1
 WHERE result_request IS NOT NULL AND cancel_requested AND (status='pending' OR (status='running' AND result_lease_until<=$1))`, now)
	if err != nil {
		return nil, err
	}
	job, err := scanResultCleanupJob(tx.QueryRowContext(ctx, `UPDATE image_library_cleanup_jobs SET status='running',lease_version=lease_version+1,
 result_lease_until=$2,started_at=COALESCE(started_at,$1),updated_at=$1 WHERE id=(
 SELECT id FROM image_library_cleanup_jobs WHERE result_request IS NOT NULL AND NOT cancel_requested
 AND (status='pending' OR(status='running' AND result_lease_until<=$1)) AND(next_attempt_at IS NULL OR next_attempt_at<=$1)
 AND NOT EXISTS(SELECT 1 FROM image_library_cleanup_jobs active WHERE active.result_request IS NOT NULL AND active.status='running' AND active.result_lease_until>$1)
 ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED) RETURNING `+resultCleanupJobColumns, now, until))
	if err == sql.ErrNoRows {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	return job, tx.Commit()
}

func (r *imageLibraryRepository) HeartbeatImageResultCleanupJob(ctx context.Context, id, lease int64, until time.Time) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE image_library_cleanup_jobs SET result_lease_until=$3,updated_at=NOW()
 WHERE id=$1 AND lease_version=$2 AND status='running' AND NOT cancel_requested AND result_lease_until>NOW()`, id, lease, until)
	return imageLibraryLeaseAlive(result, err)
}

func lockResultCleanupJob(ctx context.Context, tx *sql.Tx, id, lease int64, allowCancelled bool) (*service.ImageResultCleanupJob, error) {
	job, err := scanResultCleanupJob(tx.QueryRowContext(ctx, `SELECT `+resultCleanupJobColumns+` FROM image_library_cleanup_jobs WHERE id=$1 AND lease_version=$2
 AND status='running' AND result_lease_until>NOW() FOR UPDATE`, id, lease))
	if err == sql.ErrNoRows {
		return nil, service.ErrImageLibraryLeaseLost
	}
	if err != nil {
		return nil, err
	}
	if job.CancelRequested && !allowCancelled {
		return nil, service.ErrImageLibraryLeaseLost
	}
	return job, nil
}

func scanResultCleanupObject(row resultCleanupScanner) (*service.ImageResultCleanupObject, error) {
	t := &service.ImageResultCleanupObject{Action: "delete"}
	var raw []byte
	var identity string
	err := row.Scan(&t.ID, &t.JobID, &t.ResultID, &t.ObjectID, &raw, &identity, &t.Attempts)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &t.Object); err != nil {
		return nil, err
	}
	t.Object.StorageIdentity = identity
	return t, nil
}

const resultCleanupObjectColumns = `id,job_id,result_id,storage_object_id,object_ref,verified_identity,attempts`

// One transaction claims a single bounded unit. Remote I/O never holds its locks.
func (r *imageLibraryRepository) ClaimImageResultCleanupObject(ctx context.Context, jobID, lease int64, now time.Time) (*service.ImageResultCleanupObject, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	job, err := lockResultCleanupJob(ctx, tx, jobID, lease, false)
	if err != nil {
		return nil, err
	}
	// An uncertain delete always resumes from the durable ticket, never the cursor.
	ticket, err := scanResultCleanupObject(tx.QueryRowContext(ctx, `UPDATE image_result_cleanup_objects SET attempts=attempts+1,state='deleting',updated_at=$2
 WHERE id=(SELECT id FROM image_result_cleanup_objects WHERE job_id=$1 AND state IN ('deleting','retry')
 AND(next_attempt_at IS NULL OR next_attempt_at<=$2) ORDER BY id LIMIT 1 FOR UPDATE) RETURNING `+resultCleanupObjectColumns, jobID, now))
	if err == nil {
		return ticket, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	var resultID int64
	var createdAt time.Time
	var objectID *int64
	var ref service.ObjectRef
	var status string
	var taskClaim, resultClaim *time.Time
	err = tx.QueryRowContext(ctx, `SELECT r.id,r.created_at,r.storage_object_id,r.provider,r.bucket,r.object_key,r.content_type,r.byte_size,r.checksum,
 COALESCE(r.width,0),COALESCE(r.height,0),t.status,t.cleanup_claimed_at,r.cleanup_claimed_at
 FROM async_image_results r JOIN async_image_tasks t ON t.task_id=r.task_id
 WHERE (r.created_at,r.id)>($1,$2) AND r.created_at<=$3 AND r.provider=$4 AND r.bucket=$5 AND r.cleanup_claimed_at IS NULL ORDER BY r.created_at,r.id LIMIT 1 FOR UPDATE OF t,r SKIP LOCKED`,
		job.CursorCreatedAt, job.Cursor, job.Before, job.Target.Provider, job.Target.Bucket).Scan(&resultID, &createdAt, &objectID, &ref.Provider, &ref.Bucket, &ref.ObjectKey, &ref.ContentType, &ref.SizeBytes, &ref.ChecksumSHA256, &ref.Width, &ref.Height, &status, &taskClaim, &resultClaim)
	if err == sql.ErrNoRows {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_library_cleanup_jobs SET result_cursor=$2,result_cursor_created_at=$4,scanned_count=scanned_count+1,updated_at=$3 WHERE id=$1`, jobID, resultID, now, createdAt); err != nil {
		return nil, err
	}
	skip := func(action, reason string) (*service.ImageResultCleanupObject, error) {
		column := "skipped_count"
		if action == "protected" {
			column = "protected_count"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE image_library_cleanup_jobs SET `+column+`=`+column+`+1,last_error=$2,
 last_skip_reason=CASE WHEN $3::text='skipped' THEN $2::text ELSE last_skip_reason END WHERE id=$1`, jobID, reason, action); err != nil {
			return nil, err
		}
		return &service.ImageResultCleanupObject{JobID: jobID, ResultID: resultID, Object: ref, Action: action}, tx.Commit()
	}
	if (status != "succeeded" && status != "failed" && status != "expired") || taskClaim != nil || resultClaim != nil {
		return skip("protected", "任务仍在执行、恢复或清理中，已保留结果")
	}
	// Backfill only metadata already owned by this exact result; never infer a remote endpoint.
	if objectID == nil {
		var id int64
		err = tx.QueryRowContext(ctx, `INSERT INTO image_storage_objects(provider,bucket,object_key,content_type,byte_size,checksum_sha256,width,height)
 VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,0),NULLIF($8,0)) ON CONFLICT(provider,bucket,object_key) DO UPDATE SET updated_at=image_storage_objects.updated_at RETURNING id`,
			ref.Provider, ref.Bucket, ref.ObjectKey, ref.ContentType, ref.SizeBytes, ref.ChecksumSHA256, ref.Width, ref.Height).Scan(&id)
		if err != nil {
			return nil, err
		}
		objectID = &id
		if _, err = tx.ExecContext(ctx, `UPDATE async_image_results SET storage_object_id=$2 WHERE id=$1`, resultID, id); err != nil {
			return nil, err
		}
	}
	var objectState string
	var objectProvider, objectBucket, objectKey, objectChecksum string
	var objectBytes int64
	err = tx.QueryRowContext(ctx, `SELECT state,storage_identity,provider,bucket,object_key,byte_size,checksum_sha256 FROM image_storage_objects WHERE id=$1 FOR UPDATE`, *objectID).
		Scan(&objectState, &ref.StorageIdentity, &objectProvider, &objectBucket, &objectKey, &objectBytes, &objectChecksum)
	if err != nil {
		return nil, err
	}
	if objectState != "active" || objectProvider != ref.Provider || objectBucket != ref.Bucket || objectKey != ref.ObjectKey || objectBytes != ref.SizeBytes || objectChecksum != ref.ChecksumSHA256 || (ref.StorageIdentity != "" && ref.StorageIdentity != job.Target.Identity) {
		return skip("skipped", "对象归属或元数据无法确认，已跳过并保留结果")
	}
	var dependency, shared bool
	err = tx.QueryRowContext(ctx, `SELECT (`+resultCleanupDependencySQL+`),(`+resultCleanupSharedSQL+`)
 FROM async_image_results r JOIN image_storage_objects o ON o.id=r.storage_object_id WHERE r.id=$1`, resultID).Scan(&dependency, &shared)
	if err != nil {
		return nil, err
	}
	if dependency {
		return skip("protected", "存在活动任务引用或待完成的引用检查，已保留结果")
	}
	if shared {
		if _, err = tx.ExecContext(ctx, `DELETE FROM async_image_results WHERE id=$1`, resultID); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE image_library_cleanup_jobs SET detached_count=detached_count+1 WHERE id=$1`, jobID); err != nil {
			return nil, err
		}
		return skip("protected", "已清理结果引用，图库、广场或其他结果仍使用此对象")
	}
	raw, err := json.Marshal(ref)
	if err != nil {
		return nil, err
	}
	ticket, err = scanResultCleanupObject(tx.QueryRowContext(ctx, `INSERT INTO image_result_cleanup_objects(job_id,result_id,storage_object_id,object_ref,verified_identity,attempts)
 VALUES($1,$2,$3,$4,$5,1) RETURNING `+resultCleanupObjectColumns, jobID, resultID, *objectID, raw, ref.StorageIdentity))
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_storage_objects SET state='deleting',deletion_claimed_at=$2,updated_at=$2 WHERE id=$1`, *objectID, now); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE async_image_results SET cleanup_claimed_at=$2 WHERE id=$1`, resultID, now); err != nil {
		return nil, err
	}
	return ticket, tx.Commit()
}

func (r *imageLibraryRepository) VerifyImageResultCleanupObject(ctx context.Context, jobID, lease, ticketID int64, identity string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := lockResultCleanupJob(ctx, tx, jobID, lease, true)
	if err != nil {
		return err
	}
	if identity == "" || identity != job.Target.Identity {
		return service.ErrImageResultCleanupConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE image_result_cleanup_objects SET verified_identity=$4,updated_at=NOW()
 WHERE id=$1 AND job_id=$2 AND state='deleting' AND(verified_identity='' OR verified_identity=$4) AND $3>0`, ticketID, jobID, lease, identity)
	if err = requireImageLibraryLease(result, err); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *imageLibraryRepository) CompleteImageResultCleanupObject(ctx context.Context, jobID, lease, ticketID int64, outcome string, retryAt *time.Time, message string) error {
	if outcome != "deleted" && outcome != "missing" && outcome != "retry" && outcome != "skipped" {
		return fmt.Errorf("invalid cleanup outcome %q", outcome)
	}
	if outcome == "retry" && retryAt == nil {
		return errors.New("cleanup retry requires next attempt time")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := lockResultCleanupJob(ctx, tx, jobID, lease, true)
	if err != nil {
		return err
	}
	ticket, err := scanResultCleanupObject(tx.QueryRowContext(ctx, `SELECT `+resultCleanupObjectColumns+` FROM image_result_cleanup_objects
 WHERE id=$1 AND job_id=$2 AND state IN ('deleting','retry') FOR UPDATE`, ticketID, jobID))
	if err == sql.ErrNoRows {
		return service.ErrImageResultCleanupConflict
	}
	if err != nil {
		return err
	}
	if outcome == "deleted" || outcome == "missing" {
		if ticket.Object.StorageIdentity == "" || ticket.Object.StorageIdentity != job.Target.Identity {
			return service.ErrImageResultCleanupConflict
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM async_image_results WHERE id=$1 AND storage_object_id=$2 AND cleanup_claimed_at IS NOT NULL`, ticket.ResultID, ticket.ObjectID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE image_storage_objects SET state='deleted',deleted_at=NOW(),deletion_claimed_at=NULL,updated_at=NOW() WHERE id=$1 AND state='deleting'`, ticket.ObjectID); err != nil {
			return err
		}
		count := int64(0)
		bytes := int64(0)
		if outcome == "deleted" {
			count = 1
			bytes = ticket.Object.SizeBytes
		}
		if _, err = tx.ExecContext(ctx, `UPDATE image_library_cleanup_jobs SET deleted_count=deleted_count+$2,deleted_bytes=deleted_bytes+$3,detached_count=detached_count+1 WHERE id=$1`, jobID, count, bytes); err != nil {
			return err
		}
	} else if outcome == "skipped" {
		if _, err = tx.ExecContext(ctx, `UPDATE image_storage_objects SET state='active',deletion_claimed_at=NULL,updated_at=NOW() WHERE id=$1 AND state='deleting'`, ticket.ObjectID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE async_image_results SET cleanup_claimed_at=NULL WHERE id=$1`, ticket.ResultID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE image_library_cleanup_jobs SET skipped_count=skipped_count+1,last_error=$2,last_skip_reason=$2 WHERE id=$1`, jobID, nullIfEmpty(message)); err != nil {
			return err
		}
	} else {
		if _, err = tx.ExecContext(ctx, `UPDATE image_library_cleanup_jobs SET failed_count=failed_count+1 WHERE id=$1`, jobID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_result_cleanup_objects SET state=$3,next_attempt_at=$4,last_error=$5,updated_at=NOW() WHERE id=$1 AND job_id=$2`, ticketID, jobID, outcome, retryAt, nullIfEmpty(message)); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *imageLibraryRepository) ReleaseImageResultCleanupJob(ctx context.Context, jobID, lease int64, status string, next *time.Time, message string) error {
	if status != "pending" && status != "paused" && status != "succeeded" && status != "failed" && status != "cancelled" {
		return fmt.Errorf("invalid result cleanup status %q", status)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := lockResultCleanupJob(ctx, tx, jobID, lease, true)
	if err != nil {
		return err
	}
	if job.CancelRequested {
		status = "cancelled"
	}
	if status == "succeeded" {
		var count int64
		var retry *time.Time
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),MIN(next_attempt_at) FROM image_result_cleanup_objects WHERE job_id=$1 AND state IN ('deleting','retry')`, jobID).Scan(&count, &retry); err != nil {
			return err
		}
		if count > 0 {
			status = "pending"
			next = retry
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE image_library_cleanup_jobs SET status=$3::text,result_lease_until=NULL,next_attempt_at=$4,
 last_error=CASE WHEN $5::text IS NULL AND skipped_count>0 THEN COALESCE(last_skip_reason,last_error)
 WHEN $5::text IS NULL AND protected_count>0 THEN last_error ELSE $5::text END,updated_at=NOW(),
 finished_at=CASE WHEN $3::text IN ('succeeded','failed','cancelled') THEN NOW() ELSE NULL END WHERE id=$1 AND lease_version=$2`, jobID, lease, status, next, nullIfEmpty(message))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *imageLibraryRepository) PrepareLegacyImageResultCleanup(ctx context.Context, jobID, lease int64, request service.ImageResultCleanupRequest) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err = lockResultCleanupQueue(ctx, tx); err != nil {
		return false, err
	}
	var child *int64
	var requestedBy *int64
	err = tx.QueryRowContext(ctx, `SELECT result_child_job_id,requested_by FROM image_library_cleanup_jobs WHERE id=$1 AND lease_version=$2 AND status='running' AND result_request IS NULL FOR UPDATE`, jobID, lease).Scan(&child, &requestedBy)
	if err == sql.ErrNoRows {
		return false, service.ErrImageLibraryLeaseLost
	}
	if err != nil {
		return false, err
	}
	if child != nil {
		var state string
		err = tx.QueryRowContext(ctx, `SELECT status FROM image_library_cleanup_jobs WHERE id=$1`, *child).Scan(&state)
		if err != nil {
			return false, err
		}
		if state == "failed" || state == "cancelled" || state == "paused" {
			return false, fmt.Errorf("result cleanup child %d is %s", *child, state)
		}
		return state == "succeeded", tx.Commit()
	}
	request.Source = "legacy"
	job, err := insertResultCleanupJob(ctx, tx, requestedBy, request)
	if errors.Is(err, service.ErrImageResultCleanupConflict) {
		return false, tx.Commit()
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_library_cleanup_jobs SET result_child_job_id=$2 WHERE id=$1`, jobID, job.ID); err != nil {
		return false, err
	}
	return false, tx.Commit()
}
