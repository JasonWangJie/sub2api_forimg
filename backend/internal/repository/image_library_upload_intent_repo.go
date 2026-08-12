package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *imageLibraryRepository) PrepareLibraryUploadIntent(ctx context.Context, intent service.ImageLibraryUploadIntent) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("image library upload intent repository is not configured")
	}
	var id int64
	err := r.db.QueryRowContext(ctx, `
INSERT INTO image_library_upload_intents (
    provider,bucket,object_key,content_type,byte_size,checksum_sha256,expires_at
) VALUES ($1,$2,$3,$4,$5,$6,$7)
RETURNING id`, intent.Provider, intent.Bucket, intent.ObjectKey, intent.ContentType,
		intent.SizeBytes, intent.ChecksumSHA256, intent.ExpiresAt).Scan(&id)
	return id, err
}

func (r *imageLibraryRepository) CompleteLibraryUploadIntent(ctx context.Context, id int64) error {
	if r == nil || r.db == nil {
		return errors.New("image library upload intent repository is not configured")
	}
	result, err := r.db.ExecContext(ctx, `
DELETE FROM image_library_upload_intents
WHERE id=$1 AND cleanup_claimed_at IS NULL`, id)
	return requireImageLibraryUploadIntentMutation(result, err)
}

func (r *imageLibraryRepository) ClaimExpiredLibraryUploadIntents(
	ctx context.Context,
	before, staleBefore time.Time,
	limit int,
) ([]service.ImageLibraryUploadIntent, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("image library upload intent repository is not configured")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
WITH candidates AS (
    SELECT id FROM image_library_upload_intents
    WHERE expires_at <= $1
      AND (cleanup_claimed_at IS NULL OR cleanup_claimed_at <= $2)
    ORDER BY expires_at,id LIMIT $3 FOR UPDATE SKIP LOCKED
), claimed AS (
    UPDATE image_library_upload_intents i
    SET cleanup_claimed_at=NOW(),updated_at=NOW()
    FROM candidates c WHERE i.id=c.id
    RETURNING i.*
)
SELECT i.id,i.provider,i.bucket,i.object_key,i.content_type,i.byte_size,
       i.checksum_sha256,i.expires_at,i.cleanup_claimed_at,
       EXISTS (
           SELECT 1 FROM image_storage_objects o
           WHERE o.provider=i.provider AND o.bucket=i.bucket AND o.object_key=i.object_key
             AND o.state<>'deleted'
             AND (
                 EXISTS (SELECT 1 FROM image_library_items li WHERE li.storage_object_id=o.id AND li.purged_at IS NULL)
                 OR EXISTS (SELECT 1 FROM async_image_results ar WHERE ar.storage_object_id=o.id)
             )
       )
       OR EXISTS (
           SELECT 1 FROM async_image_results ar
           WHERE ar.provider=i.provider AND ar.bucket=i.bucket AND ar.object_key=i.object_key
       )
       OR EXISTS (
           SELECT 1 FROM async_image_input_objects ai
           WHERE ai.provider=i.provider AND ai.bucket=i.bucket AND ai.object_key=i.object_key
       )
       OR EXISTS (
           SELECT 1 FROM async_image_result_upload_intents ari
           WHERE ari.provider=i.provider AND ari.bucket=i.bucket AND ari.object_key=i.object_key
       )
       OR EXISTS (
           SELECT 1 FROM async_image_upload_reservations aur
           WHERE aur.intent_provider=i.provider AND aur.intent_bucket=i.bucket
             AND aur.intent_object_key=i.object_key
       ) AS referenced
FROM claimed i
ORDER BY i.expires_at,i.id`, before, staleBefore, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	intents := make([]service.ImageLibraryUploadIntent, 0)
	for rows.Next() {
		var intent service.ImageLibraryUploadIntent
		var claimedAt sql.NullTime
		if err := rows.Scan(
			&intent.ID, &intent.Provider, &intent.Bucket, &intent.ObjectKey,
			&intent.ContentType, &intent.SizeBytes, &intent.ChecksumSHA256,
			&intent.ExpiresAt, &claimedAt, &intent.Referenced,
		); err != nil {
			return nil, err
		}
		intent.CleanupClaimedAt = nullableTimePtr(claimedAt)
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}

func (r *imageLibraryRepository) CompleteLibraryUploadIntentDeletion(ctx context.Context, id int64, claimedAt time.Time) error {
	if r == nil || r.db == nil {
		return errors.New("image library upload intent repository is not configured")
	}
	result, err := r.db.ExecContext(ctx, `
DELETE FROM image_library_upload_intents WHERE id=$1 AND cleanup_claimed_at=$2`, id, claimedAt)
	return requireImageLibraryUploadIntentMutation(result, err)
}

func (r *imageLibraryRepository) ReleaseLibraryUploadIntentDeletion(ctx context.Context, id int64, claimedAt time.Time) error {
	if r == nil || r.db == nil {
		return errors.New("image library upload intent repository is not configured")
	}
	_, err := r.db.ExecContext(ctx, `
UPDATE image_library_upload_intents SET cleanup_claimed_at=NULL,updated_at=NOW()
WHERE id=$1 AND cleanup_claimed_at=$2`, id, claimedAt)
	return err
}

func lockImageLibraryUploadIntent(ctx context.Context, tx *sql.Tx, id int64, object service.ObjectRef) error {
	if id <= 0 {
		return errors.New("image library upload intent is required")
	}
	var stored service.ObjectRef
	var claimedAt sql.NullTime
	err := tx.QueryRowContext(ctx, `
SELECT provider,bucket,object_key,content_type,byte_size,checksum_sha256,cleanup_claimed_at
FROM image_library_upload_intents WHERE id=$1 FOR UPDATE`, id).Scan(
		&stored.Provider, &stored.Bucket, &stored.ObjectKey, &stored.ContentType,
		&stored.SizeBytes, &stored.ChecksumSHA256, &claimedAt,
	)
	if err == sql.ErrNoRows {
		return errors.New("image library upload intent was not found")
	}
	if err != nil {
		return err
	}
	if claimedAt.Valid {
		return errors.New("image library upload intent is being cleaned up")
	}
	if !sameStoredImageObject(stored, object) {
		return errors.New("image library upload intent does not match the uploaded object")
	}
	return nil
}

func deleteImageLibraryUploadIntentTx(ctx context.Context, tx *sql.Tx, id int64) error {
	result, err := tx.ExecContext(ctx, `DELETE FROM image_library_upload_intents WHERE id=$1`, id)
	return requireImageLibraryUploadIntentMutation(result, err)
}

func sameStoredImageObject(expected, actual service.ObjectRef) bool {
	return expected.Provider == actual.Provider && expected.Bucket == actual.Bucket &&
		expected.ObjectKey == actual.ObjectKey && expected.ContentType == actual.ContentType &&
		expected.SizeBytes == actual.SizeBytes && expected.ChecksumSHA256 == actual.ChecksumSHA256
}

func requireImageLibraryUploadIntentMutation(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("image library upload intent lease was lost")
	}
	return nil
}
