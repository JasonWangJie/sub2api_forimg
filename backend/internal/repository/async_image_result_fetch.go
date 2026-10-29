package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *asyncImageTaskRepository) SaveAsyncImageResultFetch(ctx context.Context, tr service.AsyncImageTaskTransition, encrypted []byte, expires time.Time) (*service.AsyncImageTask, error) {
	if len(encrypted) == 0 || expires.IsZero() || tr.ToStatus != service.AsyncImageTaskStatusUpstreamSucceeded {
		return nil, service.ErrAsyncImageInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var status string
	var version int64
	if err = tx.QueryRowContext(ctx, `SELECT status, version FROM async_image_tasks WHERE task_id=$1 FOR UPDATE`, tr.TaskID).Scan(&status, &version); err != nil {
		return nil, err
	}
	if status != service.AsyncImageTaskStatusInvoking || version != tr.ExpectedVersion {
		return nil, service.ErrAsyncImageInvalidTransition
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO async_image_result_fetches(task_id,encrypted_payload,expires_at) VALUES($1,$2,$3)`, tr.TaskID, encrypted, expires); err != nil {
		return nil, err
	}
	task, err := updateAsyncImageTaskTransition(ctx, tx, version, tr)
	if err != nil {
		return nil, err
	}
	if err = appendAsyncImageEventWithSQL(ctx, tx, service.AsyncImageEvent{TaskID: tr.TaskID, EventType: "result_fetch_pending", FromStatus: &status, ToStatus: &tr.ToStatus, Payload: json.RawMessage(`{"stage":"result_fetch"}`)}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return task, nil
}

func (r *asyncImageTaskRepository) GetAsyncImageResultFetch(ctx context.Context, taskID string) (*service.AsyncImageResultFetch, error) {
	var v service.AsyncImageResultFetch
	var attempts []byte
	err := r.sql.QueryRowContext(ctx, `SELECT encrypted_payload,attempts,expires_at FROM async_image_result_fetches WHERE task_id=$1`, taskID).Scan(&v.EncryptedPayload, &attempts, &v.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(attempts, &v.Attempts)
	return &v, err
}

// Reserve before the HTTP GET. A crash cannot reset the per-image budget.
// Index -1 is reserved for final billing-command preparation, not an HTTP GET.
func (r *asyncImageTaskRepository) BeginAsyncImageResultFetchAttempt(ctx context.Context, taskID string, index, max int) (int, error) {
	if index < -1 || max < 1 {
		return 0, service.ErrAsyncImageInvalidInput
	}
	var n int
	err := r.sql.QueryRowContext(ctx, `UPDATE async_image_result_fetches AS f SET attempts=jsonb_set(attempts,ARRAY[$2],to_jsonb(COALESCE((attempts->>$2)::int,0)+1))
 WHERE task_id=$1 AND expires_at>NOW() AND COALESCE((attempts->>$2)::int,0)<$3
 AND EXISTS(SELECT 1 FROM async_image_tasks t WHERE t.task_id=f.task_id AND t.status IN ('upstream_succeeded','uploading','storage_failed'))
 RETURNING (attempts->>$2)::int`, taskID, strconv.Itoa(index), max).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, service.ErrAsyncImageInvalidTransition
	}
	return n, err
}

func (r *asyncImageTaskRepository) SaveAsyncImageFetchedObject(ctx context.Context, o service.AsyncImageStagingObject) error {
	if err := validateAsyncImageStagingObjects(o.TaskID, []service.AsyncImageStagingObject{o}); err != nil {
		return err
	}
	result, err := r.sql.ExecContext(ctx, `INSERT INTO async_image_staging_objects(task_id,image_index,content,content_type,byte_size,checksum,width,height,expires_at)
 SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9 WHERE EXISTS(SELECT 1 FROM async_image_tasks WHERE task_id=$1 AND status IN ('upstream_succeeded','uploading','storage_failed'))
 ON CONFLICT(task_id,image_index) DO NOTHING`, o.TaskID, o.ImageIndex, o.Content, o.ContentType, o.ByteSize, o.Checksum, o.Width, o.Height, o.ExpiresAt)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return service.ErrAsyncImageInvalidTransition
	}
	return err
}
