package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *asyncImageTaskRepository) ClaimExpiredAsyncImageResultUploadIntents(
	ctx context.Context,
	before, staleBefore time.Time,
	limit int,
) ([]service.AsyncImageResultUploadIntent, error) {
	if r == nil || r.sql == nil {
		return nil, errors.New("async image result upload intent repository is not configured")
	}
	limit = normalizeAsyncImageCleanupLimit(limit)
	rows, err := r.sql.QueryContext(ctx, `
WITH candidates AS (
    SELECT id FROM async_image_result_upload_intents
    WHERE expires_at <= $1
      AND (cleanup_claimed_at IS NULL OR cleanup_claimed_at <= $2)
    ORDER BY expires_at,id LIMIT $3 FOR UPDATE SKIP LOCKED
)
UPDATE async_image_result_upload_intents i
SET cleanup_claimed_at=NOW(),updated_at=NOW()
FROM candidates c WHERE i.id=c.id
RETURNING i.id,i.task_id,i.image_index,i.provider,i.bucket,i.object_key,
          i.content_type,i.byte_size,i.checksum_sha256,i.expires_at,i.cleanup_claimed_at`,
		before, staleBefore, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	intents := make([]service.AsyncImageResultUploadIntent, 0)
	for rows.Next() {
		var intent service.AsyncImageResultUploadIntent
		var claimedAt sql.NullTime
		if err := rows.Scan(
			&intent.ID, &intent.TaskID, &intent.ImageIndex,
			&intent.ObjectRef.Provider, &intent.ObjectRef.Bucket, &intent.ObjectRef.ObjectKey,
			&intent.ObjectRef.ContentType, &intent.ObjectRef.SizeBytes,
			&intent.ObjectRef.ChecksumSHA256, &intent.ExpiresAt, &claimedAt,
		); err != nil {
			return nil, err
		}
		intent.CleanupClaimedAt = nullableTime(claimedAt)
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}

func (r *asyncImageTaskRepository) CompleteAsyncImageResultUploadIntentDeletion(ctx context.Context, id int64, claimedAt time.Time) error {
	return requireAsyncImageCleanupDelete(r.sql.ExecContext(ctx, `
DELETE FROM async_image_result_upload_intents WHERE id=$1 AND cleanup_claimed_at=$2`, id, claimedAt))
}

func (r *asyncImageTaskRepository) ReleaseAsyncImageResultUploadIntentDeletion(ctx context.Context, id int64, claimedAt time.Time) error {
	_, err := r.sql.ExecContext(ctx, `
UPDATE async_image_result_upload_intents SET cleanup_claimed_at=NULL,updated_at=NOW()
WHERE id=$1 AND cleanup_claimed_at=$2`, id, claimedAt)
	return err
}

var _ service.AsyncImageResultUploadIntentRetentionRepository = (*asyncImageTaskRepository)(nil)
