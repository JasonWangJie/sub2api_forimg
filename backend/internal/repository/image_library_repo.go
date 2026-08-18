package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

type imageLibraryRepository struct {
	db *sql.DB
}

func (r *asyncImageTaskRepository) HasLiveImageLibraryObjectReference(ctx context.Context, ref service.ObjectRef) (bool, error) {
	return r.HasLiveImageObjectReference(ctx, ref, 0, "")
}

// HasLiveImageObjectReference protects a durable object while any other
// async result, private library item, or active publication still points to it.
// The exclusions let retention remove all results owned by the row or task it
// has already claimed without treating those same rows as external owners.
func (r *asyncImageTaskRepository) HasLiveImageObjectReference(ctx context.Context, ref service.ObjectRef, excludedResultID int64, excludedTaskID string) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("async image repository is not configured")
	}
	var exists bool
	err := r.db.QueryRowContext(ctx, `
SELECT EXISTS(
 SELECT 1
 FROM image_storage_objects o
 WHERE o.provider=$1 AND o.bucket=$2 AND o.object_key=$3
   AND (
     EXISTS (SELECT 1 FROM image_library_items i WHERE i.storage_object_id=o.id AND i.deleted_at IS NULL)
     OR EXISTS (
       SELECT 1 FROM image_plaza_publications p
       JOIN image_library_items i ON i.id=p.library_item_id
       WHERE i.storage_object_id=o.id
         AND p.status IN ('pending_review','published','admin_hidden')
         AND p.expires_at>NOW()
     )
     OR EXISTS (
       SELECT 1 FROM async_image_results ar
       WHERE (ar.storage_object_id=o.id OR (ar.provider=o.provider AND ar.bucket=o.bucket AND ar.object_key=o.object_key))
         AND ($4::BIGINT<=0 OR ar.id<>$4)
         AND ($5::TEXT='' OR ar.task_id<>$5)
     )
   )
)`, ref.Provider, ref.Bucket, ref.ObjectKey, excludedResultID, excludedTaskID).Scan(&exists)
	return exists, err
}

func NewImageLibraryRepository(db *sql.DB) service.ImageLibraryRepository {
	return &imageLibraryRepository{db: db}
}

func (r *imageLibraryRepository) HasActiveImageStorageObjects(ctx context.Context) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("image library repository is not configured")
	}
	var exists bool
	err := r.db.QueryRowContext(ctx, `SELECT
  EXISTS(SELECT 1 FROM image_storage_objects WHERE state<>'deleted')
  OR EXISTS(SELECT 1 FROM async_image_input_objects)
  OR EXISTS(SELECT 1 FROM async_image_result_upload_intents)
  OR EXISTS(SELECT 1 FROM image_library_upload_intents)
  OR EXISTS(
      SELECT 1 FROM async_image_upload_reservations
      WHERE status='reserved' OR intent_object_key IS NOT NULL
  )`).Scan(&exists)
	return exists, err
}

const imageLibraryImportClaimTimeout = 2 * time.Minute

func (r *imageLibraryRepository) PreflightImport(ctx context.Context, in service.ImageLibraryImportPreflightParams) (*service.ImageLibraryItem, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, errors.New("image library repository is not configured")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockImageLibraryUser(ctx, tx, in.UserID); err != nil {
		return nil, false, err
	}

	if in.IdempotencyKey != nil {
		var existingID int64
		var existingHash string
		err = tx.QueryRowContext(ctx, `
SELECT id,COALESCE(request_hash,'') FROM image_library_items
WHERE user_id=$1 AND idempotency_key=$2 AND deleted_at IS NULL`, in.UserID, *in.IdempotencyKey).Scan(&existingID, &existingHash)
		if err == nil {
			if existingHash != in.RequestHash {
				return nil, false, apperrors.Conflict("IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used with different image metadata")
			}
			item, _, getErr := getLibraryItemForUserByID(ctx, tx, in.UserID, existingID)
			return item, true, getErr
		}
		if err != sql.ErrNoRows {
			return nil, false, err
		}

		var attemptHash, state string
		var updatedAt time.Time
		err = tx.QueryRowContext(ctx, `
SELECT request_hash,state,updated_at FROM image_library_import_attempts
WHERE user_id=$1 AND idempotency_key=$2 FOR UPDATE`, in.UserID, *in.IdempotencyKey).Scan(&attemptHash, &state, &updatedAt)
		switch err {
		case nil:
			if attemptHash != in.RequestHash {
				return nil, false, apperrors.Conflict("IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used with a different request")
			}
			if in.ContinueAttempt {
				if state != "processing" {
					return nil, false, service.ErrImageImportInProgress
				}
			} else {
				if state == "completed" {
					return nil, false, apperrors.Conflict("IDEMPOTENCY_RESULT_UNAVAILABLE", "the prior image import result is no longer available")
				}
				if state == "processing" && updatedAt.After(time.Now().UTC().Add(-imageLibraryImportClaimTimeout)) {
					return nil, false, service.ErrImageImportInProgress
				}
				_, err = tx.ExecContext(ctx, `
UPDATE image_library_import_attempts
SET state='processing',library_item_id=NULL,attempted_at=NOW(),updated_at=NOW()
WHERE user_id=$1 AND idempotency_key=$2`, in.UserID, *in.IdempotencyKey)
				if err != nil {
					return nil, false, err
				}
			}
		case sql.ErrNoRows:
			_, err = tx.ExecContext(ctx, `
INSERT INTO image_library_import_attempts(user_id,idempotency_key,request_hash)
VALUES($1,$2,$3)`, in.UserID, *in.IdempotencyKey, in.RequestHash)
			if err != nil {
				return nil, false, err
			}
		default:
			return nil, false, err
		}
	} else if in.RecordAttempt {
		_, err = tx.ExecContext(ctx, `
INSERT INTO image_library_import_attempts(user_id,idempotency_key,request_hash)
VALUES($1,NULL,NULL)`, in.UserID)
		if err != nil {
			return nil, false, err
		}
	}

	if in.RecordAttempt {
		if _, err = tx.ExecContext(ctx, `
DELETE FROM image_library_import_attempts
WHERE user_id=$1 AND idempotency_key IS NULL AND attempted_at<NOW()-INTERVAL '5 minutes'`, in.UserID); err != nil {
			return nil, false, err
		}
		if in.RateLimit > 0 {
			var recent int
			if err = tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM image_library_import_attempts
WHERE user_id=$1 AND attempted_at>=NOW()-INTERVAL '1 minute'`, in.UserID).Scan(&recent); err != nil {
				return nil, false, err
			}
			if recent > in.RateLimit {
				return nil, false, finishImageImportPreflightError(ctx, tx, in, apperrors.TooManyRequests("IMAGE_LIBRARY_RATE_LIMIT", "too many image archive requests"))
			}
		}
	}
	if err = enforceImageLibraryQuota(ctx, tx, in.UserID, in.MaxItems, in.MaxBytes, in.IncomingBytes, in.IncomingBytes <= 0); err != nil {
		return nil, false, finishImageImportPreflightError(ctx, tx, in, err)
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

func finishImageImportPreflightError(ctx context.Context, tx *sql.Tx, in service.ImageLibraryImportPreflightParams, policyErr error) error {
	if in.IdempotencyKey != nil {
		if _, err := tx.ExecContext(ctx, `
UPDATE image_library_import_attempts SET state='failed',updated_at=NOW()
WHERE user_id=$1 AND idempotency_key=$2 AND request_hash=$3`, in.UserID, *in.IdempotencyKey, in.RequestHash); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return policyErr
}

func (r *imageLibraryRepository) ReleaseImportAttempt(ctx context.Context, userID int64, idempotencyKey *string, requestHash string) error {
	if idempotencyKey == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
UPDATE image_library_import_attempts SET state='failed',updated_at=NOW()
WHERE user_id=$1 AND idempotency_key=$2 AND request_hash=$3 AND state='processing'`, userID, *idempotencyKey, requestHash)
	return err
}

const libraryItemSelect = `
SELECT i.id, i.asset_id, i.user_id, i.api_key_id, i.group_id, i.storage_object_id,
       i.platform, i.generation_mode, i.source_type, i.source_task_id,
       i.source_result_index, i.model, i.requested_size, i.actual_size,
       i.aspect_ratio, i.quality, i.title, i.private_prompt, i.visibility,
       i.archive_status, i.archive_error, i.expires_at, i.created_at, i.updated_at,
       o.provider, o.bucket, o.object_key, o.content_type, o.byte_size,
       o.checksum_sha256, o.width, o.height,
       p.id, p.public_id, p.status, p.public_title, p.public_prompt, p.share_prompt,
       p.moderation_status, p.review_reason, p.published_at, p.reviewed_at,
       p.expires_at, p.created_at, p.updated_at
FROM image_library_items i
JOIN image_storage_objects o ON o.id = i.storage_object_id AND o.state = 'active'
LEFT JOIN LATERAL (
 SELECT p0.* FROM image_plaza_publications p0
 WHERE p0.library_item_id=i.id
 ORDER BY CASE WHEN p0.status IN ('pending_review','published','admin_hidden') THEN 0 ELSE 1 END,
          p0.created_at DESC,p0.id DESC LIMIT 1
) p ON TRUE`

type sqlRowScanner interface {
	Scan(dest ...any) error
}

func scanObjectRef(row sqlRowScanner) (*service.ObjectRef, error) {
	var ref service.ObjectRef
	var width, height sql.NullInt64
	if err := row.Scan(&ref.Provider, &ref.Bucket, &ref.ObjectKey, &ref.ContentType,
		&ref.SizeBytes, &ref.ChecksumSHA256, &width, &height); err != nil {
		return nil, err
	}
	if width.Valid {
		ref.Width = int(width.Int64)
	}
	if height.Valid {
		ref.Height = int(height.Int64)
	}
	return &ref, nil
}

func scanLibraryItem(row sqlRowScanner) (*service.ImageLibraryItem, *service.ObjectRef, error) {
	var item service.ImageLibraryItem
	var apiKeyID, groupID, sourceIndex sql.NullInt64
	var sourceTask, archiveErr sql.NullString
	var provider, bucket, objectKey, contentType, checksum string
	var objectWidth, objectHeight sql.NullInt64
	var publicationID sql.NullInt64
	var publicationPublicID sql.NullString
	var publicationStatus, publicTitle, publicPrompt, moderationStatus, reviewReason sql.NullString
	var sharePrompt sql.NullBool
	var publishedAt, reviewedAt, publicationExpiresAt, publicationCreatedAt, publicationUpdatedAt sql.NullTime
	err := row.Scan(
		&item.ID, &item.AssetID, &item.UserID, &apiKeyID, &groupID, &item.StorageObjectID,
		&item.Platform, &item.GenerationMode, &item.SourceType, &sourceTask,
		&sourceIndex, &item.Model, &item.RequestedSize, &item.ActualSize,
		&item.AspectRatio, &item.Quality, &item.Title, &item.PrivatePrompt,
		&item.Visibility, &item.ArchiveStatus, &archiveErr, &item.ExpiresAt,
		&item.CreatedAt, &item.UpdatedAt, &provider, &bucket, &objectKey,
		&contentType, &item.ByteSize, &checksum, &objectWidth, &objectHeight,
		&publicationID, &publicationPublicID, &publicationStatus, &publicTitle, &publicPrompt,
		&sharePrompt, &moderationStatus, &reviewReason, &publishedAt,
		&reviewedAt, &publicationExpiresAt, &publicationCreatedAt, &publicationUpdatedAt,
	)
	if err != nil {
		return nil, nil, err
	}
	item.APIKeyID = nullableInt64Ptr(apiKeyID)
	item.GroupID = nullableInt64Ptr(groupID)
	item.SourceTaskID = nullableStringPtr(sourceTask)
	item.SourceResultIndex = nullableLibraryIntPtr(sourceIndex)
	item.ArchiveError = nullableStringPtr(archiveErr)
	if objectWidth.Valid {
		item.Width = int(objectWidth.Int64)
	}
	if objectHeight.Valid {
		item.Height = int(objectHeight.Int64)
	}
	if publicationID.Valid {
		publication := &service.ImagePublication{
			ID: publicationID.Int64, PublicID: publicationPublicID.String, LibraryItemID: item.ID, AssetID: item.AssetID, UserID: item.UserID,
			Status: publicationStatus.String, PublicTitle: publicTitle.String,
			PublicPrompt: nullableStringPtr(publicPrompt), SharePrompt: sharePrompt.Bool,
			ModerationStatus: moderationStatus.String, ReviewReason: nullableStringPtr(reviewReason),
			PublishedAt: nullableTimePtr(publishedAt), ReviewedAt: nullableTimePtr(reviewedAt),
			ExpiresAt: publicationExpiresAt.Time, CreatedAt: publicationCreatedAt.Time,
			UpdatedAt: publicationUpdatedAt.Time,
		}
		item.Publication = publication
	}
	ref := &service.ObjectRef{
		Provider: provider, Bucket: bucket, ObjectKey: objectKey, ContentType: contentType,
		SizeBytes: item.ByteSize, ChecksumSHA256: checksum, Width: item.Width, Height: item.Height,
	}
	item.Object = ref
	return &item, ref, nil
}

func (r *imageLibraryRepository) CreateAsset(ctx context.Context, in service.CreateImageLibraryAssetParams) (*service.ImageLibraryItem, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, errors.New("image library repository is not configured")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockImageLibraryUser(ctx, tx, in.UserID); err != nil {
		return nil, false, err
	}
	if err := lockImageLibraryUploadIntent(ctx, tx, in.UploadIntentID, in.Object); err != nil {
		return nil, false, err
	}
	if err := resolveImageLibraryProvenance(ctx, tx, &in); err != nil {
		return nil, false, err
	}
	if in.IdempotencyKey != nil {
		var existingID int64
		var existingHash string
		err = tx.QueryRowContext(ctx, `SELECT id, COALESCE(request_hash, '') FROM image_library_items WHERE user_id=$1 AND idempotency_key=$2`, in.UserID, *in.IdempotencyKey).Scan(&existingID, &existingHash)
		if err == nil {
			if existingHash != in.RequestHash {
				return nil, false, apperrors.Conflict("IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used with different image metadata")
			}
			item, _, getErr := getLibraryItemForUserByID(ctx, tx, in.UserID, existingID)
			return item, true, getErr
		}
		if err != sql.ErrNoRows {
			return nil, false, err
		}
	}
	if err := enforceImageLibraryLimits(ctx, tx, in.UserID, in.MaxItems, in.MaxBytes, in.Object.SizeBytes, in.RateLimit); err != nil {
		return nil, false, err
	}
	objectID, err := upsertImageStorageObject(ctx, tx, in.Object)
	if err != nil {
		return nil, false, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `
INSERT INTO image_library_items (
    asset_id, user_id, api_key_id, group_id, storage_object_id, platform, generation_mode,
    source_type, source_task_id, source_result_index, model, requested_size,
    actual_size, aspect_ratio, quality, title, private_prompt, visibility,
    idempotency_key, request_hash, expires_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,'private',$18,$19,$20)
RETURNING id`, "img_"+uuid.NewString(), in.UserID, in.APIKeyID, in.GroupID, objectID, in.Platform,
		in.GenerationMode, in.SourceType, in.SourceTaskID, in.SourceResultIndex,
		in.Model, in.RequestedSize, in.ActualSize, in.AspectRatio, in.Quality,
		in.Title, in.PrivatePrompt, in.IdempotencyKey, in.RequestHash, in.ExpiresAt,
	).Scan(&id)
	if err != nil {
		if isImageLibraryUniqueViolation(err) && in.IdempotencyKey != nil {
			return nil, false, apperrors.Conflict("IDEMPOTENCY_CONFLICT", "Idempotency-Key is already in use")
		}
		return nil, false, err
	}
	if in.IdempotencyKey != nil {
		result, updateErr := tx.ExecContext(ctx, `
UPDATE image_library_import_attempts
SET state='completed',library_item_id=$4,updated_at=NOW()
WHERE user_id=$1 AND idempotency_key=$2 AND request_hash=$3 AND state='processing'`,
			in.UserID, *in.IdempotencyKey, in.RequestHash, id)
		if updateErr != nil {
			return nil, false, updateErr
		}
		if changed, rowsErr := result.RowsAffected(); rowsErr != nil || changed != 1 {
			if rowsErr != nil {
				return nil, false, rowsErr
			}
			return nil, false, service.ErrImageImportInProgress
		}
	}
	if err := appendLibraryEvent(ctx, tx, id, nil, &in.UserID, "library.created", "", "private", json.RawMessage(`{}`)); err != nil {
		return nil, false, err
	}
	if err := appendLibraryOutbox(ctx, tx, "library_item", id, "library.created", fmt.Sprintf("library:%d:created", id)); err != nil {
		return nil, false, err
	}
	item, _, err := getLibraryItemForUserByID(ctx, tx, in.UserID, id)
	if err != nil {
		return nil, false, err
	}
	if err := deleteImageLibraryUploadIntentTx(ctx, tx, in.UploadIntentID); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return item, false, nil
}

func (r *imageLibraryRepository) PrepareAssetFromTask(ctx context.Context, userID int64, taskID string, imageIndex int) (*service.ImageLibraryItem, *service.ObjectRef, bool, error) {
	var existingID int64
	err := r.db.QueryRowContext(ctx, `
SELECT id FROM image_library_items
WHERE user_id=$1 AND source_task_id=$2 AND source_result_index=$3 AND deleted_at IS NULL`, userID, taskID, imageIndex).Scan(&existingID)
	if err == nil {
		item, _, getErr := getLibraryItemForUserByID(ctx, r.db, userID, existingID)
		return item, nil, true, getErr
	}
	if err != sql.ErrNoRows {
		return nil, nil, false, err
	}

	var ref service.ObjectRef
	var resultWidth, resultHeight sql.NullInt64
	err = r.db.QueryRowContext(ctx, `
SELECT r.provider,r.bucket,r.object_key,r.content_type,r.byte_size,r.checksum,r.width,r.height
FROM async_image_tasks t
JOIN async_image_results r ON r.task_id=t.task_id AND r.image_index=$3
WHERE t.task_id=$1 AND t.user_id=$2 AND t.status='succeeded'
  AND t.billing_status IN ('succeeded','not_billable')
  AND t.cleanup_claimed_at IS NULL AND r.cleanup_claimed_at IS NULL
  AND r.library_validation_status<>'quarantined'`, taskID, userID, imageIndex).Scan(
		&ref.Provider, &ref.Bucket, &ref.ObjectKey, &ref.ContentType,
		&ref.SizeBytes, &ref.ChecksumSHA256, &resultWidth, &resultHeight,
	)
	if err == sql.ErrNoRows {
		return nil, nil, false, apperrors.NotFound("ASYNC_IMAGE_RESULT_NOT_ARCHIVABLE", "successful, settled, and non-quarantined task result not found")
	}
	if err != nil {
		return nil, nil, false, err
	}
	if resultWidth.Valid {
		ref.Width = int(resultWidth.Int64)
	}
	if resultHeight.Valid {
		ref.Height = int(resultHeight.Int64)
	}
	return nil, &ref, false, nil
}

func (r *imageLibraryRepository) CreateAssetFromTask(ctx context.Context, userID int64, taskID string, imageIndex int, validated *service.ObjectRef, title string, expiresAt time.Time, maxItems int, maxBytes int64) (*service.ImageLibraryItem, bool, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockImageLibraryUser(ctx, tx, userID); err != nil {
		return nil, false, err
	}
	var existingID int64
	err = tx.QueryRowContext(ctx, `
SELECT id FROM image_library_items
WHERE user_id=$1 AND source_task_id=$2 AND source_result_index=$3 AND deleted_at IS NULL`, userID, taskID, imageIndex).Scan(&existingID)
	if err == nil {
		item, _, getErr := getLibraryItemForUserByID(ctx, tx, userID, existingID)
		return item, true, getErr
	}
	if err != sql.ErrNoRows {
		return nil, false, err
	}

	var taskUserID, apiKeyID, groupID int64
	var platform, model, requestedSize, actualSize, aspectRatio string
	var prompt sql.NullString
	var ref service.ObjectRef
	var resultWidth, resultHeight sql.NullInt64
	err = tx.QueryRowContext(ctx, `
SELECT t.user_id, t.api_key_id, t.group_id, t.platform, t.model,
       COALESCE(t.requested_image_size,''), COALESCE(t.actual_image_size,''),
       COALESCE(t.aspect_ratio,''), t.prompt_preview,
       r.provider, r.bucket, r.object_key, r.content_type, r.byte_size,
       r.checksum, r.width, r.height
FROM async_image_tasks t
JOIN async_image_results r ON r.task_id=t.task_id AND r.image_index=$3
WHERE t.task_id=$1 AND t.user_id=$2 AND t.status='succeeded'
  AND t.billing_status IN ('succeeded','not_billable')
  AND t.cleanup_claimed_at IS NULL
  AND r.cleanup_claimed_at IS NULL
  AND r.library_validation_status<>'quarantined'
FOR UPDATE OF t,r`, taskID, userID, imageIndex).Scan(
		&taskUserID, &apiKeyID, &groupID, &platform, &model, &requestedSize,
		&actualSize, &aspectRatio, &prompt, &ref.Provider, &ref.Bucket,
		&ref.ObjectKey, &ref.ContentType, &ref.SizeBytes, &ref.ChecksumSHA256,
		&resultWidth, &resultHeight,
	)
	if err == sql.ErrNoRows {
		return nil, false, apperrors.NotFound("ASYNC_IMAGE_RESULT_NOT_ARCHIVABLE", "successful and settled task result not found")
	}
	if err != nil {
		return nil, false, err
	}
	if resultWidth.Valid {
		ref.Width = int(resultWidth.Int64)
	}
	if resultHeight.Valid {
		ref.Height = int(resultHeight.Int64)
	}
	if validated != nil {
		if validated.Provider != ref.Provider || validated.Bucket != ref.Bucket || validated.ObjectKey != ref.ObjectKey {
			return nil, false, apperrors.Conflict("ASYNC_IMAGE_RESULT_CHANGED", "asynchronous image result changed during archive validation")
		}
		ref.ContentType = validated.ContentType
		ref.SizeBytes = validated.SizeBytes
		ref.ChecksumSHA256 = validated.ChecksumSHA256
		ref.Width = validated.Width
		ref.Height = validated.Height
	}
	if err := enforceImageLibraryLimits(ctx, tx, userID, maxItems, maxBytes, ref.SizeBytes, 0); err != nil {
		return nil, false, err
	}
	objectID, err := upsertImageStorageObject(ctx, tx, ref)
	if err != nil {
		return nil, false, err
	}
	if title == "" {
		title = truncateRepositoryText(prompt.String, 200)
	}
	if title == "" {
		title = "Async image"
	}
	var id int64
	err = tx.QueryRowContext(ctx, `
INSERT INTO image_library_items (
 asset_id,user_id,api_key_id,group_id,storage_object_id,platform,generation_mode,source_type,
 source_task_id,source_result_index,model,requested_size,actual_size,aspect_ratio,
 title,private_prompt,visibility,expires_at
) VALUES ($1,$2,$3,$4,$5,$6,'async','async_task',$7,$8,$9,$10,$11,$12,$13,$14,'private',$15)
RETURNING id`, "img_"+uuid.NewString(), userID, apiKeyID, groupID, objectID, platform, taskID, imageIndex,
		model, requestedSize, actualSize, aspectRatio, title, prompt.String, expiresAt).Scan(&id)
	if err != nil {
		return nil, false, err
	}
	_, _ = tx.ExecContext(ctx, `
UPDATE async_image_results SET storage_object_id=$1,content_type=$4,byte_size=$5,checksum=$6,width=$7,height=$8,
 library_validation_status='passed',library_validation_error=NULL,library_validated_at=NOW()
WHERE task_id=$2 AND image_index=$3`, objectID, taskID, imageIndex, ref.ContentType,
		ref.SizeBytes, ref.ChecksumSHA256, nullPositiveInt(ref.Width), nullPositiveInt(ref.Height))
	if err := appendLibraryEvent(ctx, tx, id, nil, &userID, "library.archived_from_task", "", "private", json.RawMessage(`{}`)); err != nil {
		return nil, false, err
	}
	item, _, err := getLibraryItemForUserByID(ctx, tx, userID, id)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return item, false, nil
}

func (r *imageLibraryRepository) QuarantineAssetFromTask(ctx context.Context, userID int64, taskID string, imageIndex int, ref service.ObjectRef, reason string) error {
	if r == nil || r.db == nil {
		return errors.New("image library repository is not configured")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockImageLibraryUser(ctx, tx, userID); err != nil {
		return err
	}
	var provider, bucket, objectKey string
	err = tx.QueryRowContext(ctx, `
SELECT r.provider,r.bucket,r.object_key
FROM async_image_tasks t
JOIN async_image_results r ON r.task_id=t.task_id AND r.image_index=$3
WHERE t.task_id=$1 AND t.user_id=$2 AND t.status='succeeded'
  AND t.billing_status IN ('succeeded','not_billable')
FOR UPDATE OF t,r`, taskID, userID, imageIndex).Scan(&provider, &bucket, &objectKey)
	if err == sql.ErrNoRows {
		return apperrors.NotFound("ASYNC_IMAGE_RESULT_NOT_ARCHIVABLE", "successful and settled task result not found")
	}
	if err != nil {
		return err
	}
	if provider != ref.Provider || bucket != ref.Bucket || objectKey != ref.ObjectKey {
		return apperrors.Conflict("ASYNC_IMAGE_RESULT_CHANGED", "asynchronous image result changed during archive validation")
	}
	result, err := tx.ExecContext(ctx, `
UPDATE async_image_results
SET library_validation_status='quarantined',library_validation_error=$4,library_validated_at=NOW()
WHERE task_id=$1 AND image_index=$2 AND provider=$3`, taskID, imageIndex, ref.Provider, truncateRepositoryText(reason, 1000))
	if err != nil {
		return err
	}
	if changed, rowsErr := result.RowsAffected(); rowsErr != nil || changed != 1 {
		if rowsErr != nil {
			return rowsErr
		}
		return apperrors.NotFound("ASYNC_IMAGE_RESULT_NOT_ARCHIVABLE", "asynchronous image result not found")
	}
	return tx.Commit()
}

func (r *imageLibraryRepository) ListForUser(ctx context.Context, in service.ImageLibraryListParams) ([]service.ImageLibraryItem, error) {
	in.Limit = normalizeLibraryLimit(in.Limit)
	args := []any{in.UserID}
	where := []string{"i.user_id=$1", "i.deleted_at IS NULL"}
	appendLibraryFilters(&where, &args, in)
	query := libraryItemSelect + " WHERE " + strings.Join(where, " AND ") + fmt.Sprintf(" ORDER BY i.created_at DESC, i.id DESC LIMIT $%d", len(args)+1)
	args = append(args, in.Limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.ImageLibraryItem, 0, in.Limit)
	for rows.Next() {
		item, _, scanErr := scanLibraryItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (r *imageLibraryRepository) GetForUser(ctx context.Context, userID int64, assetID string) (*service.ImageLibraryItem, *service.ObjectRef, error) {
	item, ref, err := scanLibraryItem(r.db.QueryRowContext(ctx, libraryItemSelect+` WHERE i.asset_id=$1 AND i.user_id=$2 AND i.deleted_at IS NULL`, assetID, userID))
	if err == sql.ErrNoRows {
		return nil, nil, service.ErrImageLibraryNotFound
	}
	return item, ref, err
}

func (r *imageLibraryRepository) GetObjectAdmin(ctx context.Context, assetID string) (*service.ObjectRef, error) {
	ref, err := scanObjectRef(r.db.QueryRowContext(ctx, `
SELECT o.provider,o.bucket,o.object_key,o.content_type,o.byte_size,o.checksum_sha256,o.width,o.height
FROM image_library_items i JOIN image_storage_objects o ON o.id=i.storage_object_id AND o.state='active'
WHERE i.asset_id=$1 AND i.deleted_at IS NULL`, assetID))
	if err == sql.ErrNoRows {
		return nil, service.ErrImageLibraryNotFound
	}
	return ref, err
}

func getLibraryItemForUserByID(ctx context.Context, q queryRower, userID, id int64) (*service.ImageLibraryItem, *service.ObjectRef, error) {
	item, ref, err := scanLibraryItem(q.QueryRowContext(ctx, libraryItemSelect+` WHERE i.id=$1 AND i.user_id=$2 AND i.deleted_at IS NULL`, id, userID))
	if err == sql.ErrNoRows {
		return nil, nil, service.ErrImageLibraryNotFound
	}
	return item, ref, err
}

func (r *imageLibraryRepository) UpdateForUser(ctx context.Context, in service.UpdateImageLibraryItemParams) (*service.ImageLibraryItem, error) {
	if in.Title == nil && in.PrivatePrompt == nil {
		item, _, err := r.GetForUser(ctx, in.UserID, in.AssetID)
		return item, err
	}
	res, err := r.db.ExecContext(ctx, `
UPDATE image_library_items SET
 title=CASE WHEN $3::boolean THEN $4 ELSE title END,
 private_prompt=CASE WHEN $5::boolean THEN $6 ELSE private_prompt END,
 updated_at=NOW()
WHERE asset_id=$1 AND user_id=$2 AND deleted_at IS NULL`, in.AssetID, in.UserID,
		in.Title != nil, nullableStringValue(in.Title), in.PrivatePrompt != nil, nullableStringValue(in.PrivatePrompt))
	if err != nil {
		return nil, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return nil, service.ErrImageLibraryNotFound
	}
	item, _, err := r.GetForUser(ctx, in.UserID, in.AssetID)
	return item, err
}

func (r *imageLibraryRepository) DeleteForUser(ctx context.Context, userID int64, assetID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	err = tx.QueryRowContext(ctx, `
SELECT id FROM image_library_items
WHERE asset_id=$1 AND user_id=$2 AND deleted_at IS NULL
FOR UPDATE`, assetID, userID).Scan(&id)
	if err != nil {
		if err == sql.ErrNoRows {
			return service.ErrImageLibraryNotFound
		}
		return err
	}
	if err := rejectAdminHiddenLibraryDeletion(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE image_library_items SET deleted_at=NOW(),visibility='private',updated_at=NOW()
WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID); err != nil {
		return err
	}
	if err := finalizeImageLibraryDeletion(ctx, tx, id, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *imageLibraryRepository) DeleteLegacyPlazaForUser(ctx context.Context, userID int64, identifier, legacyIdempotencyKey string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	err = tx.QueryRowContext(ctx, `
SELECT i.id
FROM image_library_items i
LEFT JOIN image_plaza_publications p ON p.library_item_id=i.id
WHERE i.user_id=$1 AND i.deleted_at IS NULL
  AND (i.asset_id=$2 OR p.public_id=$2 OR ($3<>'' AND i.idempotency_key=$3))
ORDER BY i.id LIMIT 1
FOR UPDATE OF i`, userID, identifier, legacyIdempotencyKey).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := rejectAdminHiddenLibraryDeletion(ctx, tx, id); err != nil {
		return true, err
	}
	result, err := tx.ExecContext(ctx, `
UPDATE image_library_items
SET deleted_at=NOW(),visibility='private',updated_at=NOW()
WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		return false, nil
	}
	if err := finalizeImageLibraryDeletion(ctx, tx, id, userID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func rejectAdminHiddenLibraryDeletion(ctx context.Context, tx *sql.Tx, itemID int64) error {
	var hidden bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
 SELECT 1 FROM image_plaza_publications
 WHERE library_item_id=$1 AND status='admin_hidden'
)`, itemID).Scan(&hidden); err != nil {
		return err
	}
	if hidden {
		return apperrors.Conflict(
			"ADMIN_HIDDEN_PUBLICATION_LOCKED",
			"an administrator-hidden publication must be resolved before its library asset can be deleted",
		)
	}
	return nil
}

func finalizeImageLibraryDeletion(ctx context.Context, tx *sql.Tx, id, userID int64) error {
	if _, err := tx.ExecContext(ctx, `
UPDATE image_plaza_publications
SET status='withdrawn',withdrawn_at=COALESCE(withdrawn_at,NOW()),updated_at=NOW()
WHERE library_item_id=$1 AND status IN ('pending_review','published')`, id); err != nil {
		return err
	}
	if err := appendLibraryEvent(ctx, tx, id, nil, &userID, "library.deleted", "", "deleted", json.RawMessage(`{}`)); err != nil {
		return err
	}
	return appendLibraryOutbox(ctx, tx, "library_item", id, "library.cleanup_requested", fmt.Sprintf("library:%d:cleanup", id))
}

func (r *imageLibraryRepository) CreatePublication(ctx context.Context, in service.CreateImagePublicationParams) (*service.ImagePublication, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockImageLibraryUser(ctx, tx, in.UserID); err != nil {
		return nil, err
	}
	var assetPK int64
	var title, prompt string
	var assetExpires time.Time
	err = tx.QueryRowContext(ctx, `SELECT id,title,private_prompt,expires_at FROM image_library_items WHERE asset_id=$1 AND user_id=$2 AND deleted_at IS NULL FOR UPDATE`, in.AssetID, in.UserID).Scan(&assetPK, &title, &prompt, &assetExpires)
	if err == sql.ErrNoRows {
		return nil, service.ErrImageLibraryNotFound
	}
	if err != nil {
		return nil, err
	}
	if in.RateLimit > 0 {
		var recent int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_plaza_publications WHERE user_id=$1 AND created_at >= NOW()-INTERVAL '1 minute'`, in.UserID).Scan(&recent); err != nil {
			return nil, err
		}
		if recent >= in.RateLimit {
			return nil, apperrors.TooManyRequests("IMAGE_PUBLICATION_RATE_LIMIT", "too many image publication requests")
		}
	}
	if in.PublicTitle == "" {
		in.PublicTitle = title
	}
	var publicPrompt any
	if in.SharePrompt {
		if in.PublicPrompt != nil {
			publicPrompt = *in.PublicPrompt
		} else {
			publicPrompt = prompt
		}
	}
	var id int64
	var activeStatus string
	initialStatus := strings.TrimSpace(in.InitialStatus)
	if initialStatus == "" {
		initialStatus = service.ImagePublicationPending
	}
	if initialStatus != service.ImagePublicationPending && initialStatus != service.ImagePublicationPublished {
		return nil, apperrors.BadRequest("INVALID_PUBLICATION_STATUS", "unsupported initial publication status")
	}
	moderation := "pending"
	if initialStatus == service.ImagePublicationPublished {
		moderation = "approved"
	}
	err = tx.QueryRowContext(ctx, `SELECT id,status FROM image_plaza_publications WHERE library_item_id=$1 AND status IN ('pending_review','published','admin_hidden') FOR UPDATE`, assetPK).Scan(&id, &activeStatus)
	switch err {
	case nil:
		if activeStatus != service.ImagePublicationPending {
			return nil, apperrors.Conflict("ACTIVE_PUBLICATION_EXISTS", "withdraw the current publication before submitting again")
		}
		_, err = tx.ExecContext(ctx, `UPDATE image_plaza_publications SET public_title=$2,public_prompt=$3,share_prompt=$4,expires_at=GREATEST(expires_at,$5),updated_at=NOW() WHERE id=$1`, id, in.PublicTitle, publicPrompt, in.SharePrompt, in.ExpiresAt)
	case sql.ErrNoRows:
		err = tx.QueryRowContext(ctx, `
INSERT INTO image_plaza_publications (
 public_id,library_item_id,user_id,status,public_title,public_prompt,share_prompt,
 moderation_status,published_at,expires_at
) VALUES ($1,$2,$3,$4::text,$5,$6,$7,$8,CASE WHEN $4::text='published' THEN NOW() ELSE NULL END,$9)
RETURNING id`, "imgpub_"+uuid.NewString(), assetPK, in.UserID, initialStatus, in.PublicTitle, publicPrompt, in.SharePrompt, moderation, in.ExpiresAt).Scan(&id)
	}
	if err != nil {
		return nil, err
	}
	visibility := service.ImageLibraryVisibilityPrivate
	if initialStatus == service.ImagePublicationPublished {
		visibility = service.ImageLibraryVisibilityPublic
	}
	_, err = tx.ExecContext(ctx, `UPDATE image_library_items SET visibility=$2,expires_at=GREATEST(expires_at,$3),updated_at=NOW() WHERE id=$1`, assetPK, visibility, in.ExpiresAt)
	if err != nil {
		return nil, err
	}
	eventTo := initialStatus
	if err := appendLibraryEvent(ctx, tx, assetPK, &id, &in.UserID, "publication.submitted", "", eventTo, json.RawMessage(`{}`)); err != nil {
		return nil, err
	}
	publication, err := getPublication(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return publication, nil
}

func (r *imageLibraryRepository) WithdrawPublication(ctx context.Context, userID int64, assetID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var assetPK, publicationID int64
	var oldStatus string
	err = tx.QueryRowContext(ctx, `SELECT id FROM image_library_items WHERE asset_id=$1 AND user_id=$2 AND deleted_at IS NULL`, assetID, userID).Scan(&assetPK)
	if err == sql.ErrNoRows {
		return service.ErrImageLibraryNotFound
	}
	if err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT id,status FROM image_plaza_publications WHERE library_item_id=$1 AND user_id=$2 AND status IN ('pending_review','published') FOR UPDATE`, assetPK, userID).Scan(&publicationID, &oldStatus)
	if err == sql.ErrNoRows {
		return service.ErrImagePublicationNotFound
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE image_plaza_publications SET status='withdrawn',withdrawn_at=NOW(),updated_at=NOW() WHERE id=$1`, publicationID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE image_library_items SET visibility='private',updated_at=NOW() WHERE id=$1 AND user_id=$2`, assetPK, userID)
	if err != nil {
		return err
	}
	if err := appendLibraryEvent(ctx, tx, assetPK, &publicationID, &userID, "publication.withdrawn", oldStatus, service.ImagePublicationWithdrawn, json.RawMessage(`{}`)); err != nil {
		return err
	}
	// Realtime 投稿撤回待审：对象仅为投稿而上传，撤回后删除图库条目并清理 OSS。
	if oldStatus == service.ImagePublicationPending {
		var sourceType string
		if err := tx.QueryRowContext(ctx, `SELECT source_type FROM image_library_items WHERE id=$1`, assetPK).Scan(&sourceType); err != nil {
			return err
		}
		if sourceType == "realtime_import" {
			if _, err := tx.ExecContext(ctx, `
UPDATE image_library_items SET deleted_at=NOW(),visibility='private',updated_at=NOW()
WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, assetPK, userID); err != nil {
				return err
			}
			if err := finalizeImageLibraryDeletion(ctx, tx, assetPK, userID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (r *imageLibraryRepository) ListPublished(ctx context.Context, viewerUserID int64, in service.ImagePublicationListParams) (*service.PublicImagePlazaListResult, error) {
	in.Limit = normalizeLibraryLimit(in.Limit)
	baseWhere := []string{"p.status='published'", "p.expires_at>NOW()", "i.deleted_at IS NULL", "o.state='active'"}
	countWhere := append([]string(nil), baseWhere...)
	countArgs := make([]any, 0)
	appendPublicationFilters(&countWhere, &countArgs, in, "")
	var total int64
	if err := r.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM image_plaza_publications p
JOIN image_library_items i ON i.id=p.library_item_id
JOIN image_storage_objects o ON o.id=i.storage_object_id
WHERE `+strings.Join(countWhere, " AND "), countArgs...).Scan(&total); err != nil {
		return nil, err
	}

	args := []any{viewerUserID}
	where := append([]string(nil), baseWhere...)
	appendPublicationFilters(&where, &args, in, "p.published_at")
	direction, _ := imagePublicationSortSQL(in.Sort)
	query := `
SELECT p.id,p.public_id,i.id,i.asset_id,p.user_id,p.public_title,
       CASE WHEN p.share_prompt THEN p.public_prompt ELSE NULL END,
       i.platform,i.model,COALESCE(NULLIF(i.actual_size,''),i.requested_size),
       i.aspect_ratio,o.width,o.height,o.content_type,p.published_at,
       (p.user_id=$1),p.expires_at
FROM image_plaza_publications p
JOIN image_library_items i ON i.id=p.library_item_id
JOIN image_storage_objects o ON o.id=i.storage_object_id
WHERE ` + strings.Join(where, " AND ") + fmt.Sprintf(" ORDER BY p.published_at %s,p.id %s LIMIT $%d", direction, direction, len(args)+1)
	args = append(args, in.Limit)
	if in.Cursor == nil && in.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", len(args)+1)
		args = append(args, in.Offset)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.PublicImagePlazaItem, 0, in.Limit)
	for rows.Next() {
		var item service.PublicImagePlazaItem
		var prompt sql.NullString
		var width, height sql.NullInt64
		if err := rows.Scan(&item.PublicationPK, &item.PublicationID, &item.AssetPK, &item.AssetID, &item.UserID, &item.Title,
			&prompt, &item.Platform, &item.Model, &item.Size, &item.AspectRatio,
			&width, &height, &item.ContentType, &item.PublishedAt, &item.IsOwner,
			&item.ExpiresAt); err != nil {
			return nil, err
		}
		item.Prompt = nullableStringPtr(prompt)
		if width.Valid {
			item.Width = int(width.Int64)
		}
		if height.Valid {
			item.Height = int(height.Int64)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &service.PublicImagePlazaListResult{Items: items, Total: total}, nil
}

func (r *imageLibraryRepository) GetPublishedObject(ctx context.Context, publicationID string) (*service.ObjectRef, error) {
	ref, err := scanObjectRef(r.db.QueryRowContext(ctx, `
SELECT o.provider,o.bucket,o.object_key,o.content_type,o.byte_size,o.checksum_sha256,o.width,o.height
FROM image_plaza_publications p
JOIN image_library_items i ON i.id=p.library_item_id AND i.deleted_at IS NULL
JOIN image_storage_objects o ON o.id=i.storage_object_id AND o.state='active'
WHERE p.public_id=$1 AND p.status='published' AND p.expires_at>NOW()`, publicationID))
	if err == sql.ErrNoRows {
		return nil, service.ErrImagePublicationNotFound
	}
	return ref, err
}

func (r *imageLibraryRepository) GetPublicationObjectAdmin(ctx context.Context, publicationID string) (*service.ObjectRef, error) {
	ref, err := scanObjectRef(r.db.QueryRowContext(ctx, `
SELECT o.provider,o.bucket,o.object_key,o.content_type,o.byte_size,o.checksum_sha256,o.width,o.height
FROM image_plaza_publications p
JOIN image_library_items i ON i.id=p.library_item_id AND i.deleted_at IS NULL
JOIN image_storage_objects o ON o.id=i.storage_object_id AND o.state='active'
WHERE p.public_id=$1`, publicationID))
	if err == sql.ErrNoRows {
		return nil, service.ErrImagePublicationNotFound
	}
	return ref, err
}

func (r *imageLibraryRepository) CreateReport(ctx context.Context, reporterUserID int64, publicationID string, reason, details string) (*service.ImagePlazaReport, error) {
	var ownerID int64
	var publicationPK int64
	err := r.db.QueryRowContext(ctx, `SELECT id,user_id FROM image_plaza_publications WHERE public_id=$1 AND status='published' AND expires_at>NOW()`, publicationID).Scan(&publicationPK, &ownerID)
	if err == sql.ErrNoRows {
		return nil, service.ErrImagePublicationNotFound
	}
	if err != nil {
		return nil, err
	}
	if ownerID == reporterUserID {
		return nil, apperrors.BadRequest("CANNOT_REPORT_OWN_IMAGE", "you cannot report your own image")
	}
	var report service.ImagePlazaReport
	err = r.db.QueryRowContext(ctx, `
INSERT INTO image_plaza_reports(publication_id,reporter_user_id,reason,details)
VALUES($1,$2,$3,$4)
ON CONFLICT (publication_id,reporter_user_id) WHERE status='open'
DO UPDATE SET reason=EXCLUDED.reason,details=EXCLUDED.details,updated_at=NOW()
RETURNING id,publication_id,reporter_user_id,reason,details,status,resolution,resolved_at,created_at`,
		publicationPK, reporterUserID, reason, details).Scan(&report.ID, &publicationPK,
		&report.ReporterUserID, &report.Reason, &report.Details, &report.Status,
		&report.Resolution, &report.ResolvedAt, &report.CreatedAt)
	report.PublicationID = publicationID
	return &report, err
}

func (r *imageLibraryRepository) ListPublicationsAdmin(ctx context.Context, in service.ImagePublicationListParams) ([]service.AdminImagePlazaPublication, error) {
	in.Limit = normalizeLibraryLimit(in.Limit)
	args := make([]any, 0)
	where := []string{"i.deleted_at IS NULL", "o.state='active'"}
	appendPublicationFilters(&where, &args, in, "p.created_at")
	if in.Status != "" {
		args = append(args, in.Status)
		where = append(where, fmt.Sprintf("p.status=$%d", len(args)))
	}
	direction, _ := imagePublicationSortSQL(in.Sort)
	query := `
SELECT p.id,p.public_id,i.id,i.asset_id,p.user_id,p.public_title,
       CASE WHEN p.share_prompt THEN p.public_prompt ELSE NULL END,
       i.platform,i.model,COALESCE(NULLIF(i.actual_size,''),i.requested_size),
       i.aspect_ratio,o.width,o.height,o.content_type,COALESCE(p.published_at,p.created_at),false,
       p.expires_at,p.created_at,p.status,p.moderation_status,p.review_reason
FROM image_plaza_publications p
JOIN image_library_items i ON i.id=p.library_item_id
JOIN image_storage_objects o ON o.id=i.storage_object_id
WHERE ` + strings.Join(where, " AND ") + fmt.Sprintf(" ORDER BY p.created_at %s,p.id %s LIMIT $%d", direction, direction, len(args)+1)
	args = append(args, in.Limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.AdminImagePlazaPublication, 0, in.Limit)
	for rows.Next() {
		var item service.AdminImagePlazaPublication
		public := &item.PublicImagePlazaItem
		var prompt sql.NullString
		var width, height sql.NullInt64
		if err := rows.Scan(&public.PublicationPK, &public.PublicationID, &public.AssetPK, &public.AssetID, &public.UserID, &public.Title,
			&prompt, &public.Platform, &public.Model, &public.Size, &public.AspectRatio,
			&width, &height, &public.ContentType, &public.PublishedAt, &public.IsOwner,
			&public.ExpiresAt, &item.CreatedAt, &item.Status, &item.ModerationStatus, &item.ReviewReason); err != nil {
			return nil, err
		}
		public.Prompt = nullableStringPtr(prompt)
		if width.Valid {
			public.Width = int(width.Int64)
		}
		if height.Valid {
			public.Height = int(height.Int64)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *imageLibraryRepository) TransitionPublication(ctx context.Context, adminUserID int64, publicationID string, action, reason string, retentionUntil time.Time) (*service.ImagePublication, error) {
	var toStatus, moderation string
	switch action {
	case "approve", "restore":
		toStatus, moderation = service.ImagePublicationPublished, "approved"
	case "reject":
		toStatus, moderation = service.ImagePublicationRejected, "rejected"
	case "hide":
		toStatus, moderation = service.ImagePublicationHidden, "approved"
	default:
		return nil, apperrors.BadRequest("INVALID_PUBLICATION_ACTION", "unsupported publication action")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var assetID int64
	var oldStatus string
	var publicationPK int64
	err = tx.QueryRowContext(ctx, `SELECT id,library_item_id,status FROM image_plaza_publications WHERE public_id=$1 FOR UPDATE`, publicationID).Scan(&publicationPK, &assetID, &oldStatus)
	if err == sql.ErrNoRows {
		return nil, service.ErrImagePublicationNotFound
	}
	if err != nil {
		return nil, err
	}
	if (action == "approve" && oldStatus != service.ImagePublicationPending) ||
		(action == "restore" && oldStatus != service.ImagePublicationHidden) ||
		(action == "reject" && oldStatus != service.ImagePublicationPending) ||
		(action == "hide" && oldStatus != service.ImagePublicationPublished) {
		return nil, apperrors.Conflict("INVALID_PUBLICATION_TRANSITION", "publication status no longer permits this action")
	}
	_, err = tx.ExecContext(ctx, `
UPDATE image_plaza_publications SET status=$2::text,moderation_status=$3,
 reviewer_user_id=$4,review_reason=$5,reviewed_at=NOW(),
 published_at=CASE WHEN $2::text='published' THEN NOW() ELSE published_at END,
 hidden_at=CASE WHEN $2::text='admin_hidden' THEN NOW() ELSE NULL END,
 expires_at=CASE WHEN $2::text='published' THEN GREATEST(expires_at,$6) ELSE expires_at END,
 updated_at=NOW() WHERE id=$1`, publicationPK, toStatus, moderation, adminUserID,
		nullIfEmpty(reason), retentionUntil)
	if err != nil {
		return nil, err
	}
	visibility := service.ImageLibraryVisibilityPrivate
	if toStatus == service.ImagePublicationPublished {
		visibility = service.ImageLibraryVisibilityPublic
	}
	_, err = tx.ExecContext(ctx, `UPDATE image_library_items SET visibility=$2::text,expires_at=CASE WHEN $2::text='public' THEN GREATEST(expires_at,$3) ELSE expires_at END,updated_at=NOW() WHERE id=$1`, assetID, visibility, retentionUntil)
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]string{"reason": reason})
	if err := appendLibraryEvent(ctx, tx, assetID, &publicationPK, &adminUserID, "publication."+action, oldStatus, toStatus, payload); err != nil {
		return nil, err
	}

	// Realtime 投稿：对象存储仅在投稿时写入。审核拒绝后软删图库条目并触发 OSS 清理。
	// 异步归档（async_task）仍保留私有图库副本，拒绝投稿不删 OSS。
	if action == "reject" {
		var sourceType string
		if err := tx.QueryRowContext(ctx, `SELECT source_type FROM image_library_items WHERE id=$1`, assetID).Scan(&sourceType); err != nil {
			return nil, err
		}
		if sourceType == "realtime_import" {
			var ownerID int64
			if err := tx.QueryRowContext(ctx, `SELECT user_id FROM image_library_items WHERE id=$1`, assetID).Scan(&ownerID); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `
UPDATE image_library_items SET deleted_at=NOW(),visibility='private',updated_at=NOW()
WHERE id=$1 AND deleted_at IS NULL`, assetID); err != nil {
				return nil, err
			}
			if err := finalizeImageLibraryDeletion(ctx, tx, assetID, ownerID); err != nil {
				return nil, err
			}
		}
	}

	publication, err := getPublication(ctx, tx, publicationPK)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return publication, nil
}

func (r *imageLibraryRepository) ListReportsAdmin(ctx context.Context, status string, cursor *service.ImageLibraryCursor, limit int) ([]service.ImagePlazaReport, error) {
	limit = normalizeLibraryLimit(limit)
	args := make([]any, 0)
	where := []string{"TRUE"}
	if status != "" {
		args = append(args, status)
		where = append(where, fmt.Sprintf("r.status=$%d", len(args)))
	}
	if cursor != nil {
		args = append(args, cursor.CreatedAt, cursor.ID)
		where = append(where, fmt.Sprintf("(r.created_at,r.id)<($%d,$%d)", len(args)-1, len(args)))
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, `
SELECT r.id,p.public_id,r.reporter_user_id,r.reason,r.details,r.status,r.resolution,r.resolved_at,r.created_at
FROM image_plaza_reports r JOIN image_plaza_publications p ON p.id=r.publication_id WHERE `+strings.Join(where, " AND ")+fmt.Sprintf(" ORDER BY r.created_at DESC,r.id DESC LIMIT $%d", len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	reports := make([]service.ImagePlazaReport, 0, limit)
	for rows.Next() {
		var report service.ImagePlazaReport
		if err := rows.Scan(&report.ID, &report.PublicationID, &report.ReporterUserID,
			&report.Reason, &report.Details, &report.Status, &report.Resolution,
			&report.ResolvedAt, &report.CreatedAt); err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	return reports, rows.Err()
}

func (r *imageLibraryRepository) ResolveReport(ctx context.Context, adminUserID, reportID int64, status, resolution string) (*service.ImagePlazaReport, error) {
	if status != "resolved" && status != "dismissed" {
		return nil, apperrors.BadRequest("INVALID_REPORT_STATUS", "report status must be resolved or dismissed")
	}
	var report service.ImagePlazaReport
	var publicationPK int64
	err := r.db.QueryRowContext(ctx, `
UPDATE image_plaza_reports SET status=$2,resolution=$3,resolved_by=$4,resolved_at=NOW(),updated_at=NOW()
WHERE id=$1 AND status='open'
RETURNING id,publication_id,reporter_user_id,reason,details,status,resolution,resolved_at,created_at`,
		reportID, status, resolution, adminUserID).Scan(&report.ID, &publicationPK,
		&report.ReporterUserID, &report.Reason, &report.Details, &report.Status,
		&report.Resolution, &report.ResolvedAt, &report.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, apperrors.NotFound("IMAGE_REPORT_NOT_FOUND", "open report not found")
	}
	if err != nil {
		return &report, err
	}
	err = r.db.QueryRowContext(ctx, `SELECT public_id FROM image_plaza_publications WHERE id=$1`, publicationPK).Scan(&report.PublicationID)
	return &report, err
}

func (r *imageLibraryRepository) ListLibraryAdmin(ctx context.Context, in service.ImageLibraryListParams) ([]service.ImageLibraryItem, error) {
	in.Limit = normalizeLibraryLimit(in.Limit)
	args := make([]any, 0)
	where := []string{"i.deleted_at IS NULL"}
	if in.UserID > 0 {
		args = append(args, in.UserID)
		where = append(where, fmt.Sprintf("i.user_id=$%d", len(args)))
	}
	appendLibraryFilters(&where, &args, in)
	args = append(args, in.Limit)
	rows, err := r.db.QueryContext(ctx, libraryItemSelect+" WHERE "+strings.Join(where, " AND ")+fmt.Sprintf(" ORDER BY i.created_at DESC,i.id DESC LIMIT $%d", len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.ImageLibraryItem, 0, in.Limit)
	for rows.Next() {
		item, _, scanErr := scanLibraryItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (r *imageLibraryRepository) Stats(ctx context.Context) (*service.AdminImageLibraryStats, error) {
	stats := &service.AdminImageLibraryStats{}
	err := r.db.QueryRowContext(ctx, `
SELECT
 (SELECT COUNT(*) FROM image_library_items WHERE deleted_at IS NULL),
 (SELECT COUNT(*) FROM image_storage_objects WHERE state='active'),
 (SELECT COALESCE(SUM(byte_size),0) FROM image_storage_objects WHERE state='active'),
 (SELECT COUNT(*) FROM image_plaza_publications WHERE status='pending_review'),
 (SELECT COUNT(*) FROM image_plaza_publications WHERE status='published' AND expires_at>NOW()),
 (SELECT COUNT(*) FROM image_plaza_reports WHERE status='open')`).Scan(
		&stats.ItemCount, &stats.ObjectCount, &stats.TotalBytes, &stats.PendingReview,
		&stats.Published, &stats.OpenReports)
	return stats, err
}

func (r *imageLibraryRepository) CreateCleanupJob(ctx context.Context, adminUserID int64, scope string, filters json.RawMessage) (*service.ImageLibraryCleanupJob, error) {
	if !validImageLibraryCleanupScope(scope) {
		return nil, apperrors.BadRequest("INVALID_CLEANUP_SCOPE", "cleanup scope must be expired, deleted, user, async_results, or all")
	}
	if len(filters) == 0 || !json.Valid(filters) {
		filters = json.RawMessage(`{}`)
	}
	job := &service.ImageLibraryCleanupJob{}
	err := r.db.QueryRowContext(ctx, `
INSERT INTO image_library_cleanup_jobs(requested_by,scope,filters)
VALUES($1,$2,$3) RETURNING id,requested_by,scope,filters,status,scanned_count,
deleted_count,deleted_bytes,last_error,created_at`, adminUserID, scope, filters).Scan(
		&job.ID, &job.RequestedBy, &job.Scope, &job.Filters, &job.Status,
		&job.ScannedCount, &job.DeletedCount, &job.DeletedBytes, &job.LastError,
		&job.CreatedAt)
	return job, err
}

func (r *imageLibraryRepository) ListCleanupJobs(ctx context.Context, limit int) ([]service.ImageLibraryCleanupJob, error) {
	limit = normalizeLibraryLimit(limit)
	rows, err := r.db.QueryContext(ctx, `SELECT id,requested_by,scope,filters,status,scanned_count,deleted_count,deleted_bytes,last_error,created_at FROM image_library_cleanup_jobs ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]service.ImageLibraryCleanupJob, 0, limit)
	for rows.Next() {
		var job service.ImageLibraryCleanupJob
		if err := rows.Scan(&job.ID, &job.RequestedBy, &job.Scope, &job.Filters,
			&job.Status, &job.ScannedCount, &job.DeletedCount, &job.DeletedBytes,
			&job.LastError, &job.CreatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (r *imageLibraryRepository) PreviewCleanup(ctx context.Context, scope string, filters json.RawMessage) (*service.ImageLibraryCleanupPreview, error) {
	preview := &service.ImageLibraryCleanupPreview{}
	switch scope {
	case "async_results":
		return r.previewAsyncResultsCleanup(ctx, filters)
	case "all":
		libWhere, libArgs, err := imageLibraryCleanupWhere("all", filters)
		if err != nil {
			return nil, err
		}
		err = r.db.QueryRowContext(ctx, `
SELECT COUNT(*),COALESCE(SUM(byte_size),0) FROM (
 SELECT i.id,o.byte_size FROM image_library_items i
 JOIN image_storage_objects o ON o.id=i.storage_object_id
 WHERE i.purged_at IS NULL AND `+libWhere+`) matched`, libArgs...).Scan(&preview.MatchedItems, &preview.MatchedBytes)
		if err != nil {
			return nil, err
		}
		asyncPreview, err := r.previewAsyncResultsCleanup(ctx, filters)
		if err != nil {
			return nil, err
		}
		preview.MatchedItems += asyncPreview.MatchedItems
		preview.MatchedBytes += asyncPreview.MatchedBytes

		orphanItems, orphanBytes, err := r.previewOrphanStorageObjects(ctx, filters)
		if err != nil {
			return nil, err
		}
		preview.MatchedItems += orphanItems
		preview.MatchedBytes += orphanBytes

		inputItems, inputBytes, err := r.previewAsyncInputObjects(ctx, filters)
		if err != nil {
			return nil, err
		}
		preview.MatchedItems += inputItems
		preview.MatchedBytes += inputBytes
		return preview, nil
	default:
		where, args, err := imageLibraryCleanupWhere(scope, filters)
		if err != nil {
			return nil, err
		}
		err = r.db.QueryRowContext(ctx, `
SELECT COUNT(*),COALESCE(SUM(byte_size),0) FROM (
 SELECT i.id,o.byte_size FROM image_library_items i
 JOIN image_storage_objects o ON o.id=i.storage_object_id
 WHERE i.purged_at IS NULL AND `+where+`) matched`, args...).Scan(&preview.MatchedItems, &preview.MatchedBytes)
		return preview, err
	}
}

func (r *imageLibraryRepository) previewAsyncResultsCleanup(ctx context.Context, filters json.RawMessage) (*service.ImageLibraryCleanupPreview, error) {
	where, args, err := asyncResultsCleanupWhere(filters)
	if err != nil {
		return nil, err
	}
	preview := &service.ImageLibraryCleanupPreview{}
	err = r.db.QueryRowContext(ctx, `
SELECT COUNT(*),COALESCE(SUM(COALESCE(o.byte_size,r.byte_size)),0)
FROM async_image_results r
LEFT JOIN image_storage_objects o ON o.id=r.storage_object_id
WHERE `+where, args...).Scan(&preview.MatchedItems, &preview.MatchedBytes)
	return preview, err
}

func (r *imageLibraryRepository) previewOrphanStorageObjects(ctx context.Context, filters json.RawMessage) (int64, int64, error) {
	where, args, err := orphanStorageCleanupWhere(filters)
	if err != nil {
		return 0, 0, err
	}
	var items, bytes int64
	err = r.db.QueryRowContext(ctx, `
SELECT COUNT(*),COALESCE(SUM(o.byte_size),0)
FROM image_storage_objects o
WHERE o.state IN ('active','deleting') AND `+where, args...).Scan(&items, &bytes)
	return items, bytes, err
}

func (r *imageLibraryRepository) previewAsyncInputObjects(ctx context.Context, filters json.RawMessage) (int64, int64, error) {
	where, args, err := asyncInputCleanupWhere(filters)
	if err != nil {
		return 0, 0, err
	}
	var items, bytes int64
	err = r.db.QueryRowContext(ctx, `
SELECT COUNT(*),COALESCE(SUM(byte_size),0)
FROM async_image_input_objects
WHERE `+where, args...).Scan(&items, &bytes)
	return items, bytes, err
}

func (r *imageLibraryRepository) EnsureExpiredCleanupJob(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO image_library_cleanup_jobs(requested_by,scope,filters)
SELECT NULL,'expired','{}'::jsonb
WHERE EXISTS (SELECT 1 FROM image_library_items WHERE deleted_at IS NULL AND purged_at IS NULL AND expires_at<=NOW())
  AND NOT EXISTS (SELECT 1 FROM image_library_cleanup_jobs WHERE scope='expired' AND status IN ('pending','running'))`)
	return err
}

func (r *imageLibraryRepository) ClaimCleanupJob(ctx context.Context, staleBefore time.Time) (*service.ImageLibraryCleanupJob, error) {
	job := &service.ImageLibraryCleanupJob{}
	err := r.db.QueryRowContext(ctx, `
WITH candidate AS (
 SELECT id FROM image_library_cleanup_jobs
 WHERE status='pending' OR (status='running' AND updated_at<=$1)
 ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED
)
UPDATE image_library_cleanup_jobs j
SET status='running',started_at=COALESCE(started_at,NOW()),last_error=NULL,
    lease_version=lease_version+1,updated_at=NOW()
FROM candidate c WHERE j.id=c.id
RETURNING j.id,j.requested_by,j.scope,j.filters,j.status,j.lease_version,j.scanned_count,
          j.deleted_count,j.deleted_bytes,j.last_error,j.created_at`, staleBefore).Scan(
		&job.ID, &job.RequestedBy, &job.Scope, &job.Filters, &job.Status, &job.LeaseVersion,
		&job.ScannedCount, &job.DeletedCount, &job.DeletedBytes, &job.LastError, &job.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return job, err
}

func (r *imageLibraryRepository) HeartbeatCleanupJob(ctx context.Context, jobID, leaseVersion int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
UPDATE image_library_cleanup_jobs SET updated_at=NOW()
WHERE id=$1 AND lease_version=$2 AND status='running'`, jobID, leaseVersion)
	return imageLibraryLeaseAlive(result, err)
}

func (r *imageLibraryRepository) PrepareCleanupBatch(ctx context.Context, jobID, leaseVersion int64, scope string, filters json.RawMessage, limit int) (*service.ImageLibraryCleanupBatch, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if scope == "async_results" || scope == "all" {
		batch, err := r.prepareAsyncResultsCleanupBatch(ctx, jobID, leaseVersion, filters, limit)
		if err != nil {
			return nil, err
		}
		if batch.MatchedItems > 0 || len(batch.Objects) > 0 {
			return batch, nil
		}
		if scope == "async_results" {
			return &service.ImageLibraryCleanupBatch{Done: true}, nil
		}
		batch, err = r.prepareLibraryItemsCleanupBatch(ctx, jobID, leaseVersion, "all", filters, limit)
		if err != nil {
			return nil, err
		}
		if batch.MatchedItems > 0 || len(batch.Objects) > 0 {
			return batch, nil
		}
		batch, err = r.prepareOrphanStorageCleanupBatch(ctx, jobID, leaseVersion, filters, limit)
		if err != nil {
			return nil, err
		}
		if batch.MatchedItems > 0 || len(batch.Objects) > 0 {
			return batch, nil
		}
		return r.prepareStorageResidueCleanupBatch(ctx, jobID, leaseVersion, filters, limit)
	}
	return r.prepareLibraryItemsCleanupBatch(ctx, jobID, leaseVersion, scope, filters, limit)
}

func (r *imageLibraryRepository) prepareLibraryItemsCleanupBatch(ctx context.Context, jobID, leaseVersion int64, scope string, filters json.RawMessage, limit int) (*service.ImageLibraryCleanupBatch, error) {
	where, args, err := imageLibraryCleanupWhere(scope, filters)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var leaseAlive bool
	if err := tx.QueryRowContext(ctx, `
SELECT TRUE FROM image_library_cleanup_jobs
WHERE id=$1 AND lease_version=$2 AND status='running'
FOR UPDATE`, jobID, leaseVersion).Scan(&leaseAlive); err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrImageLibraryLeaseLost
		}
		return nil, err
	}
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, `
SELECT i.id,i.storage_object_id FROM image_library_items i
WHERE i.purged_at IS NULL AND `+where+fmt.Sprintf(" ORDER BY i.id LIMIT $%d FOR UPDATE SKIP LOCKED", len(args)), args...)
	if err != nil {
		return nil, err
	}
	itemIDs := make([]int64, 0, limit)
	objectIDs := make([]int64, 0, limit)
	for rows.Next() {
		var itemID, objectID int64
		if err := rows.Scan(&itemID, &objectID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		itemIDs = append(itemIDs, itemID)
		objectIDs = append(objectIDs, objectID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(itemIDs) == 0 {
		return &service.ImageLibraryCleanupBatch{Done: true}, tx.Commit()
	}
	if scope == "expired" {
		publicationRows, queryErr := tx.QueryContext(ctx, `
SELECT id,library_item_id,status FROM image_plaza_publications
WHERE library_item_id=ANY($1) AND status IN ('pending_review','published','admin_hidden')
FOR UPDATE`, pq.Array(itemIDs))
		if queryErr != nil {
			return nil, queryErr
		}
		type expiringPublication struct {
			id, itemID int64
			status     string
		}
		expiring := make([]expiringPublication, 0)
		for publicationRows.Next() {
			var publication expiringPublication
			if scanErr := publicationRows.Scan(&publication.id, &publication.itemID, &publication.status); scanErr != nil {
				_ = publicationRows.Close()
				return nil, scanErr
			}
			expiring = append(expiring, publication)
		}
		if closeErr := publicationRows.Close(); closeErr != nil {
			return nil, closeErr
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE image_plaza_publications SET status='expired',updated_at=NOW()
WHERE library_item_id=ANY($1) AND status IN ('pending_review','published','admin_hidden')`, pq.Array(itemIDs)); err != nil {
			return nil, err
		}
		for _, publication := range expiring {
			if err := appendLibraryEvent(ctx, tx, publication.itemID, &publication.id, nil, "publication.expired", publication.status, service.ImagePublicationExpired, json.RawMessage(`{}`)); err != nil {
				return nil, err
			}
		}
	} else if _, err := tx.ExecContext(ctx, `
UPDATE image_plaza_publications SET status='withdrawn',withdrawn_at=COALESCE(withdrawn_at,NOW()),updated_at=NOW()
WHERE library_item_id=ANY($1) AND status IN ('pending_review','published','admin_hidden')`, pq.Array(itemIDs)); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE image_library_items SET deleted_at=COALESCE(deleted_at,NOW()),purged_at=NOW(),visibility='private',updated_at=NOW()
WHERE id=ANY($1)`, pq.Array(itemIDs)); err != nil {
		return nil, err
	}
	objectRows, err := tx.QueryContext(ctx, `
UPDATE image_storage_objects o SET state='deleting',deletion_claimed_at=NOW(),updated_at=NOW()
WHERE o.id=ANY($1) AND o.state='active'
  AND NOT EXISTS (SELECT 1 FROM image_library_items i WHERE i.storage_object_id=o.id AND i.deleted_at IS NULL)
  AND NOT EXISTS (
    SELECT 1 FROM image_plaza_publications p JOIN image_library_items i ON i.id=p.library_item_id
    WHERE i.storage_object_id=o.id AND p.status IN ('pending_review','published','admin_hidden') AND p.expires_at>NOW()
  )
  AND NOT EXISTS (
    SELECT 1 FROM async_image_results ar
    WHERE ar.storage_object_id=o.id OR (ar.provider=o.provider AND ar.bucket=o.bucket AND ar.object_key=o.object_key)
  )
RETURNING o.provider,o.bucket,o.object_key,o.content_type,o.byte_size,o.checksum_sha256,o.width,o.height`, pq.Array(objectIDs))
	if err != nil {
		return nil, err
	}
	objects := make([]service.ObjectRef, 0)
	for objectRows.Next() {
		ref, scanErr := scanObjectRef(objectRows)
		if scanErr != nil {
			_ = objectRows.Close()
			return nil, scanErr
		}
		objects = append(objects, *ref)
	}
	if err := objectRows.Close(); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `
UPDATE image_library_cleanup_jobs
SET scanned_count=scanned_count+$3,deleted_count=deleted_count+$3,updated_at=NOW()
WHERE id=$1 AND lease_version=$2 AND status='running'`, jobID, leaseVersion, len(itemIDs))
	if err := requireImageLibraryLease(result, err); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &service.ImageLibraryCleanupBatch{MatchedItems: int64(len(itemIDs)), Objects: objects, Done: len(itemIDs) < limit}, nil
}

func (r *imageLibraryRepository) ClaimStaleCleanupObjects(ctx context.Context, staleBefore time.Time, limit int) ([]service.ObjectRef, error) {
	limit = normalizeLibraryLimit(limit)
	rows, err := r.db.QueryContext(ctx, `
WITH reactivated AS (
 UPDATE image_storage_objects o
 SET state='active',deletion_claimed_at=NULL,updated_at=NOW()
 WHERE o.state='deleting'
   AND (o.deletion_claimed_at IS NULL OR o.deletion_claimed_at<=$1)
   AND (
     EXISTS (SELECT 1 FROM image_library_items i WHERE i.storage_object_id=o.id AND i.deleted_at IS NULL)
     OR EXISTS (
       SELECT 1 FROM image_plaza_publications p
       JOIN image_library_items i ON i.id=p.library_item_id
       WHERE i.storage_object_id=o.id
         AND p.status IN ('pending_review','published','admin_hidden')
         AND p.expires_at>NOW()
     )
     OR EXISTS (SELECT 1 FROM async_image_results ar WHERE ar.storage_object_id=o.id OR (ar.provider=o.provider AND ar.bucket=o.bucket AND ar.object_key=o.object_key))
   )
 RETURNING o.id
), candidates AS (
 SELECT id FROM image_storage_objects
 WHERE state='deleting' AND (deletion_claimed_at IS NULL OR deletion_claimed_at<=$1)
   AND NOT EXISTS (SELECT 1 FROM reactivated r WHERE r.id=image_storage_objects.id)
 ORDER BY id LIMIT $2 FOR UPDATE SKIP LOCKED
)
UPDATE image_storage_objects o SET deletion_claimed_at=NOW(),updated_at=NOW()
FROM candidates c WHERE o.id=c.id
RETURNING o.provider,o.bucket,o.object_key,o.content_type,o.byte_size,o.checksum_sha256,o.width,o.height`, staleBefore, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	objects := make([]service.ObjectRef, 0, limit)
	for rows.Next() {
		ref, scanErr := scanObjectRef(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		objects = append(objects, *ref)
	}
	return objects, rows.Err()
}

func (r *imageLibraryRepository) CompleteCleanupObject(ctx context.Context, jobID, leaseVersion int64, ref service.ObjectRef) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
UPDATE image_storage_objects SET state='deleted',deleted_at=NOW(),deletion_claimed_at=NULL,updated_at=NOW()
WHERE provider=$1 AND bucket=$2 AND object_key=$3 AND state='deleting'`, ref.Provider, ref.Bucket, ref.ObjectKey)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected > 0 && jobID > 0 {
		result, err := tx.ExecContext(ctx, `
UPDATE image_library_cleanup_jobs SET deleted_bytes=deleted_bytes+$3,updated_at=NOW()
WHERE id=$1 AND lease_version=$2 AND status='running'`, jobID, leaseVersion, ref.SizeBytes)
		if err := requireImageLibraryLease(result, err); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *imageLibraryRepository) ReleaseCleanupObject(ctx context.Context, ref service.ObjectRef) error {
	_, err := r.db.ExecContext(ctx, `
UPDATE image_storage_objects SET state='active',deletion_claimed_at=NULL,updated_at=NOW()
WHERE provider=$1 AND bucket=$2 AND object_key=$3 AND state='deleting'`, ref.Provider, ref.Bucket, ref.ObjectKey)
	return err
}

func (r *imageLibraryRepository) FinishCleanupJob(ctx context.Context, jobID, leaseVersion int64, status, message string) error {
	if status != "succeeded" && status != "failed" {
		return apperrors.BadRequest("INVALID_CLEANUP_STATUS", "cleanup status must be succeeded or failed")
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE image_library_cleanup_jobs SET status=$3,last_error=$4,finished_at=NOW(),updated_at=NOW()
WHERE id=$1 AND lease_version=$2 AND status='running'`, jobID, leaseVersion, status, nullIfEmpty(message))
	return requireImageLibraryLease(result, err)
}

func (r *imageLibraryRepository) ClaimLibraryOutbox(ctx context.Context, limit int, staleBefore time.Time) ([]service.ImageLibraryOutboxEntry, error) {
	limit = normalizeLibraryLimit(limit)
	rows, err := r.db.QueryContext(ctx, `
WITH candidates AS (
 SELECT id FROM image_library_outbox
 WHERE completed_at IS NULL AND available_at<=NOW() AND (claimed_at IS NULL OR claimed_at<=$1)
 ORDER BY id LIMIT $2 FOR UPDATE SKIP LOCKED
)
UPDATE image_library_outbox o SET claimed_at=NOW(),attempts=attempts+1,updated_at=NOW()
FROM candidates c WHERE o.id=c.id
RETURNING o.id,o.aggregate_type,o.aggregate_id,o.event_type,o.attempts`, staleBefore, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	entries := make([]service.ImageLibraryOutboxEntry, 0, limit)
	for rows.Next() {
		var entry service.ImageLibraryOutboxEntry
		if err := rows.Scan(&entry.ID, &entry.AggregateType, &entry.AggregateID, &entry.EventType, &entry.Attempts); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (r *imageLibraryRepository) HeartbeatLibraryOutbox(ctx context.Context, id int64, attempts int) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
UPDATE image_library_outbox SET claimed_at=NOW(),updated_at=NOW()
WHERE id=$1 AND attempts=$2 AND completed_at IS NULL AND claimed_at IS NOT NULL`, id, attempts)
	return imageLibraryLeaseAlive(result, err)
}

func (r *imageLibraryRepository) PrepareOutboxCleanup(ctx context.Context, itemID int64) ([]service.ObjectRef, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var objectID int64
	err = tx.QueryRowContext(ctx, `SELECT storage_object_id FROM image_library_items WHERE id=$1 FOR UPDATE`, itemID).Scan(&objectID)
	if err == sql.ErrNoRows {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE image_library_items SET deleted_at=COALESCE(deleted_at,NOW()),purged_at=NOW(),visibility='private',updated_at=NOW() WHERE id=$1`, itemID); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
UPDATE image_storage_objects o SET state='deleting',deletion_claimed_at=NOW(),updated_at=NOW()
WHERE o.id=$1 AND o.state='active'
  AND NOT EXISTS (SELECT 1 FROM image_library_items i WHERE i.storage_object_id=o.id AND i.deleted_at IS NULL)
  AND NOT EXISTS (
    SELECT 1 FROM image_plaza_publications p
    JOIN image_library_items i ON i.id=p.library_item_id
    WHERE i.storage_object_id=o.id AND p.status IN ('pending_review','published','admin_hidden')
  )
  AND NOT EXISTS (SELECT 1 FROM async_image_results ar WHERE ar.storage_object_id=o.id OR (ar.provider=o.provider AND ar.bucket=o.bucket AND ar.object_key=o.object_key))
RETURNING o.provider,o.bucket,o.object_key,o.content_type,o.byte_size,o.checksum_sha256,o.width,o.height`, objectID)
	if err != nil {
		return nil, err
	}
	objects := make([]service.ObjectRef, 0, 1)
	for rows.Next() {
		ref, scanErr := scanObjectRef(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		objects = append(objects, *ref)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return objects, tx.Commit()
}

func (r *imageLibraryRepository) CompleteLibraryOutbox(ctx context.Context, id int64, attempts int) error {
	result, err := r.db.ExecContext(ctx, `
UPDATE image_library_outbox SET completed_at=NOW(),claimed_at=NULL,last_error=NULL,updated_at=NOW()
WHERE id=$1 AND attempts=$2 AND completed_at IS NULL`, id, attempts)
	return requireImageLibraryLease(result, err)
}

func (r *imageLibraryRepository) RetryLibraryOutbox(ctx context.Context, id int64, attempts int, availableAt time.Time, message string) error {
	result, err := r.db.ExecContext(ctx, `
UPDATE image_library_outbox SET claimed_at=NULL,available_at=$3,last_error=$4,updated_at=NOW()
WHERE id=$1 AND attempts=$2 AND completed_at IS NULL`, id, attempts, availableAt, truncateRepositoryText(message, 2000))
	return requireImageLibraryLease(result, err)
}

func (r *imageLibraryRepository) GetMigrationState(ctx context.Context, key string) (*service.ImageLibraryMigrationState, error) {
	state := &service.ImageLibraryMigrationState{}
	err := r.db.QueryRowContext(ctx, `SELECT migration_key,status,lease_version,last_legacy_id,migrated_count,quarantined_count,last_error,started_at,finished_at,updated_at FROM image_library_migration_state WHERE migration_key=$1`, key).Scan(
		&state.MigrationKey, &state.Status, &state.LeaseVersion, &state.LastLegacyID, &state.MigratedCount,
		&state.QuarantinedCount, &state.LastError, &state.StartedAt, &state.FinishedAt, &state.UpdatedAt)
	return state, err
}

func (r *imageLibraryRepository) ClaimMigration(ctx context.Context, key string, staleBefore time.Time) (*service.ImageLibraryMigrationState, error) {
	state := &service.ImageLibraryMigrationState{}
	err := r.db.QueryRowContext(ctx, `
UPDATE image_library_migration_state SET status='running',started_at=COALESCE(started_at,NOW()),
 lease_version=lease_version+1,last_error=NULL,updated_at=NOW()
WHERE migration_key=$1 AND (status IN ('pending','failed') OR (status='running' AND updated_at<=$2))
RETURNING migration_key,status,lease_version,last_legacy_id,migrated_count,quarantined_count,last_error,started_at,finished_at,updated_at`, key, staleBefore).Scan(
		&state.MigrationKey, &state.Status, &state.LeaseVersion, &state.LastLegacyID, &state.MigratedCount,
		&state.QuarantinedCount, &state.LastError, &state.StartedAt, &state.FinishedAt, &state.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return state, err
}

func (r *imageLibraryRepository) HeartbeatMigration(ctx context.Context, key string, leaseVersion int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
UPDATE image_library_migration_state SET updated_at=NOW()
WHERE migration_key=$1 AND lease_version=$2 AND status='running'`, key, leaseVersion)
	return imageLibraryLeaseAlive(result, err)
}

func (r *imageLibraryRepository) ListLegacyPlazaItems(ctx context.Context, afterID int64, limit int) ([]service.LegacyImagePlazaItem, error) {
	limit = normalizeLibraryLimit(limit)
	rows, err := r.db.QueryContext(ctx, `
SELECT id,user_id,prompt,title,model,size,quality,storage_path,content_type
FROM image_plaza_items WHERE id>$1 ORDER BY id LIMIT $2`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.LegacyImagePlazaItem, 0, limit)
	for rows.Next() {
		var item service.LegacyImagePlazaItem
		if err := rows.Scan(&item.ID, &item.UserID, &item.Prompt, &item.Title, &item.Model,
			&item.Size, &item.Quality, &item.StoragePath, &item.ContentType); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *imageLibraryRepository) AdvanceMigration(ctx context.Context, key string, leaseVersion, legacyID, migrated, quarantined int64) error {
	result, err := r.db.ExecContext(ctx, `
UPDATE image_library_migration_state SET last_legacy_id=GREATEST(last_legacy_id,$3),
 migrated_count=migrated_count+$4,quarantined_count=quarantined_count+$5,updated_at=NOW()
WHERE migration_key=$1 AND lease_version=$2 AND status='running'`, key, leaseVersion, legacyID, migrated, quarantined)
	return requireImageLibraryLease(result, err)
}

func (r *imageLibraryRepository) FinishMigration(ctx context.Context, key string, leaseVersion int64, status, message string) error {
	if status != "succeeded" && status != "failed" {
		return apperrors.BadRequest("INVALID_MIGRATION_STATUS", "migration status must be succeeded or failed")
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE image_library_migration_state SET status=$3::text,last_error=$4,
 finished_at=CASE WHEN $3::text='succeeded' THEN NOW() ELSE finished_at END,updated_at=NOW()
WHERE migration_key=$1 AND lease_version=$2 AND status='running'`, key, leaseVersion, status, nullIfEmpty(message))
	return requireImageLibraryLease(result, err)
}

func imageLibraryLeaseAlive(result sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

func requireImageLibraryLease(result sql.Result, err error) error {
	alive, err := imageLibraryLeaseAlive(result, err)
	if err != nil {
		return err
	}
	if !alive {
		return service.ErrImageLibraryLeaseLost
	}
	return nil
}

type imageLibraryCleanupFilters struct {
	Before *time.Time `json:"before"`
	UserID int64      `json:"user_id"`
}

func validImageLibraryCleanupScope(scope string) bool {
	switch scope {
	case "expired", "deleted", "user", "async_results", "all":
		return true
	default:
		return false
	}
}

func parseImageLibraryCleanupFilters(filters json.RawMessage) (imageLibraryCleanupFilters, error) {
	parsed := imageLibraryCleanupFilters{}
	if len(filters) > 0 && string(filters) != "null" {
		if err := json.Unmarshal(filters, &parsed); err != nil {
			return parsed, apperrors.BadRequest("INVALID_CLEANUP_FILTERS", "cleanup filters are invalid")
		}
	}
	return parsed, nil
}

func asyncResultsCleanupWhere(filters json.RawMessage) (string, []any, error) {
	parsed, err := parseImageLibraryCleanupFilters(filters)
	if err != nil {
		return "", nil, err
	}
	if parsed.Before != nil && !parsed.Before.IsZero() {
		return "r.created_at<=$1", []any{parsed.Before.UTC()}, nil
	}
	return "TRUE", nil, nil
}

func asyncInputCleanupWhere(filters json.RawMessage) (string, []any, error) {
	parsed, err := parseImageLibraryCleanupFilters(filters)
	if err != nil {
		return "", nil, err
	}
	if parsed.Before != nil && !parsed.Before.IsZero() {
		return "created_at<=$1", []any{parsed.Before.UTC()}, nil
	}
	return "TRUE", nil, nil
}

func orphanStorageCleanupWhere(filters json.RawMessage) (string, []any, error) {
	parsed, err := parseImageLibraryCleanupFilters(filters)
	if err != nil {
		return "", nil, err
	}
	clauses := []string{
		`NOT EXISTS (SELECT 1 FROM image_library_items i WHERE i.storage_object_id=o.id AND i.deleted_at IS NULL)`,
		`NOT EXISTS (
      SELECT 1 FROM image_plaza_publications p
      JOIN image_library_items i ON i.id=p.library_item_id
      WHERE i.storage_object_id=o.id
        AND p.status IN ('pending_review','published','admin_hidden')
        AND p.expires_at>NOW()
    )`,
		`NOT EXISTS (
      SELECT 1 FROM async_image_results ar
      WHERE ar.storage_object_id=o.id OR (ar.provider=o.provider AND ar.bucket=o.bucket AND ar.object_key=o.object_key)
    )`,
	}
	args := make([]any, 0, 1)
	if parsed.Before != nil && !parsed.Before.IsZero() {
		args = append(args, parsed.Before.UTC())
		clauses = append(clauses, fmt.Sprintf("o.created_at<=$%d", len(args)))
	}
	return strings.Join(clauses, " AND "), args, nil
}

func (r *imageLibraryRepository) prepareAsyncResultsCleanupBatch(ctx context.Context, jobID, leaseVersion int64, filters json.RawMessage, limit int) (*service.ImageLibraryCleanupBatch, error) {
	where, args, err := asyncResultsCleanupWhere(filters)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var leaseAlive bool
	if err := tx.QueryRowContext(ctx, `
SELECT TRUE FROM image_library_cleanup_jobs
WHERE id=$1 AND lease_version=$2 AND status='running'
FOR UPDATE`, jobID, leaseVersion).Scan(&leaseAlive); err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrImageLibraryLeaseLost
		}
		return nil, err
	}
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, `
SELECT r.id,r.storage_object_id,r.provider,r.bucket,r.object_key,r.content_type,r.byte_size,r.checksum,r.width,r.height
FROM async_image_results r
WHERE `+where+fmt.Sprintf(" ORDER BY r.id LIMIT $%d FOR UPDATE OF r SKIP LOCKED", len(args)), args...)
	if err != nil {
		return nil, err
	}
	type resultRow struct {
		id       int64
		objectID sql.NullInt64
		ref      service.ObjectRef
	}
	claimed := make([]resultRow, 0, limit)
	for rows.Next() {
		var row resultRow
		var width, height sql.NullInt64
		if err := rows.Scan(
			&row.id, &row.objectID, &row.ref.Provider, &row.ref.Bucket, &row.ref.ObjectKey,
			&row.ref.ContentType, &row.ref.SizeBytes, &row.ref.ChecksumSHA256, &width, &height,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if width.Valid {
			row.ref.Width = int(width.Int64)
		}
		if height.Valid {
			row.ref.Height = int(height.Int64)
		}
		claimed = append(claimed, row)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(claimed) == 0 {
		return &service.ImageLibraryCleanupBatch{Done: true}, tx.Commit()
	}
	resultIDs := make([]int64, 0, len(claimed))
	for _, row := range claimed {
		resultIDs = append(resultIDs, row.id)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM async_image_results WHERE id=ANY($1)`, pq.Array(resultIDs)); err != nil {
		return nil, err
	}
	objects := make([]service.ObjectRef, 0, len(claimed))
	seen := make(map[string]struct{}, len(claimed))
	for _, row := range claimed {
		key := row.ref.Provider + "\x00" + row.ref.Bucket + "\x00" + row.ref.ObjectKey
		if _, ok := seen[key]; ok {
			continue
		}
		objectRows, queryErr := tx.QueryContext(ctx, `
UPDATE image_storage_objects o SET state='deleting',deletion_claimed_at=NOW(),updated_at=NOW()
WHERE o.state IN ('active','deleting')
  AND (
    ($1::BIGINT>0 AND o.id=$1)
    OR (o.provider=$2 AND o.bucket=$3 AND o.object_key=$4)
  )
  AND NOT EXISTS (SELECT 1 FROM image_library_items i WHERE i.storage_object_id=o.id AND i.deleted_at IS NULL)
  AND NOT EXISTS (
    SELECT 1 FROM image_plaza_publications p JOIN image_library_items i ON i.id=p.library_item_id
    WHERE i.storage_object_id=o.id AND p.status IN ('pending_review','published','admin_hidden') AND p.expires_at>NOW()
  )
  AND NOT EXISTS (
    SELECT 1 FROM async_image_results ar
    WHERE ar.storage_object_id=o.id OR (ar.provider=o.provider AND ar.bucket=o.bucket AND ar.object_key=o.object_key)
  )
RETURNING o.provider,o.bucket,o.object_key,o.content_type,o.byte_size,o.checksum_sha256,o.width,o.height`,
			nullInt64OrZero(row.objectID), row.ref.Provider, row.ref.Bucket, row.ref.ObjectKey)
		if queryErr != nil {
			return nil, queryErr
		}
		claimedObject := false
		for objectRows.Next() {
			ref, scanErr := scanObjectRef(objectRows)
			if scanErr != nil {
				_ = objectRows.Close()
				return nil, scanErr
			}
			objects = append(objects, *ref)
			claimedObject = true
		}
		if closeErr := objectRows.Close(); closeErr != nil {
			return nil, closeErr
		}
		if !claimedObject && row.ref.ObjectKey != "" {
			// Object metadata may only live on the result row (legacy / local path).
			objects = append(objects, row.ref)
		}
		seen[key] = struct{}{}
	}
	result, err := tx.ExecContext(ctx, `
UPDATE image_library_cleanup_jobs
SET scanned_count=scanned_count+$3,deleted_count=deleted_count+$3,updated_at=NOW()
WHERE id=$1 AND lease_version=$2 AND status='running'`, jobID, leaseVersion, len(claimed))
	if err := requireImageLibraryLease(result, err); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &service.ImageLibraryCleanupBatch{
		MatchedItems: int64(len(claimed)),
		Objects:      objects,
		Done:         len(claimed) < limit,
	}, nil
}

func nullInt64OrZero(value sql.NullInt64) int64 {
	if value.Valid {
		return value.Int64
	}
	return 0
}

func (r *imageLibraryRepository) prepareOrphanStorageCleanupBatch(ctx context.Context, jobID, leaseVersion int64, filters json.RawMessage, limit int) (*service.ImageLibraryCleanupBatch, error) {
	where, args, err := orphanStorageCleanupWhere(filters)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var leaseAlive bool
	if err := tx.QueryRowContext(ctx, `
SELECT TRUE FROM image_library_cleanup_jobs
WHERE id=$1 AND lease_version=$2 AND status='running'
FOR UPDATE`, jobID, leaseVersion).Scan(&leaseAlive); err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrImageLibraryLeaseLost
		}
		return nil, err
	}
	args = append(args, limit)
	objectRows, err := tx.QueryContext(ctx, `
WITH candidates AS (
 SELECT o.id FROM image_storage_objects o
 WHERE o.state IN ('active','deleting') AND `+where+fmt.Sprintf(`
 ORDER BY o.id LIMIT $%d FOR UPDATE OF o SKIP LOCKED
)
UPDATE image_storage_objects o SET state='deleting',deletion_claimed_at=NOW(),updated_at=NOW()
FROM candidates c WHERE o.id=c.id
RETURNING o.provider,o.bucket,o.object_key,o.content_type,o.byte_size,o.checksum_sha256,o.width,o.height`, len(args)), args...)
	if err != nil {
		return nil, err
	}
	objects := make([]service.ObjectRef, 0, limit)
	for objectRows.Next() {
		ref, scanErr := scanObjectRef(objectRows)
		if scanErr != nil {
			_ = objectRows.Close()
			return nil, scanErr
		}
		objects = append(objects, *ref)
	}
	if err := objectRows.Close(); err != nil {
		return nil, err
	}
	if len(objects) == 0 {
		return &service.ImageLibraryCleanupBatch{Done: true}, tx.Commit()
	}
	result, err := tx.ExecContext(ctx, `
UPDATE image_library_cleanup_jobs
SET scanned_count=scanned_count+$3,deleted_count=deleted_count+$3,updated_at=NOW()
WHERE id=$1 AND lease_version=$2 AND status='running'`, jobID, leaseVersion, len(objects))
	if err := requireImageLibraryLease(result, err); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &service.ImageLibraryCleanupBatch{
		MatchedItems: int64(len(objects)),
		Objects:      objects,
		Done:         len(objects) < limit,
	}, nil
}

func (r *imageLibraryRepository) prepareStorageResidueCleanupBatch(ctx context.Context, jobID, leaseVersion int64, filters json.RawMessage, limit int) (*service.ImageLibraryCleanupBatch, error) {
	inputWhere, inputArgs, err := asyncInputCleanupWhere(filters)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var leaseAlive bool
	if err := tx.QueryRowContext(ctx, `
SELECT TRUE FROM image_library_cleanup_jobs
WHERE id=$1 AND lease_version=$2 AND status='running'
FOR UPDATE`, jobID, leaseVersion).Scan(&leaseAlive); err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrImageLibraryLeaseLost
		}
		return nil, err
	}

	inputArgs = append(inputArgs, limit)
	inputRows, err := tx.QueryContext(ctx, `
SELECT id,provider,bucket,object_key,content_type,byte_size,checksum,width,height
FROM async_image_input_objects
WHERE `+inputWhere+fmt.Sprintf(" ORDER BY id LIMIT $%d FOR UPDATE SKIP LOCKED", len(inputArgs)), inputArgs...)
	if err != nil {
		return nil, err
	}
	type inputRow struct {
		id  int64
		ref service.ObjectRef
	}
	inputs := make([]inputRow, 0, limit)
	for inputRows.Next() {
		var row inputRow
		var width, height sql.NullInt64
		if err := inputRows.Scan(
			&row.id, &row.ref.Provider, &row.ref.Bucket, &row.ref.ObjectKey,
			&row.ref.ContentType, &row.ref.SizeBytes, &row.ref.ChecksumSHA256, &width, &height,
		); err != nil {
			_ = inputRows.Close()
			return nil, err
		}
		if width.Valid {
			row.ref.Width = int(width.Int64)
		}
		if height.Valid {
			row.ref.Height = int(height.Int64)
		}
		inputs = append(inputs, row)
	}
	if err := inputRows.Close(); err != nil {
		return nil, err
	}

	objects := make([]service.ObjectRef, 0, len(inputs))
	if len(inputs) > 0 {
		ids := make([]int64, 0, len(inputs))
		for _, row := range inputs {
			ids = append(ids, row.id)
			if row.ref.ObjectKey != "" {
				objects = append(objects, row.ref)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM async_image_input_objects WHERE id=ANY($1)`, pq.Array(ids)); err != nil {
			return nil, err
		}
	}

	// Wipe leftover upload intents / reservations that keep storage "in use".
	if _, err := tx.ExecContext(ctx, `DELETE FROM async_image_result_upload_intents`); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM image_library_upload_intents`); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM async_image_upload_reservations
WHERE status='reserved' OR intent_object_key IS NOT NULL`); err != nil {
		return nil, err
	}

	matched := int64(len(inputs))
	if matched == 0 && len(objects) == 0 {
		// Still count one synthetic pass so the job can finish after residue wipe.
		matched = 0
	}
	result, err := tx.ExecContext(ctx, `
UPDATE image_library_cleanup_jobs
SET scanned_count=scanned_count+$3,deleted_count=deleted_count+$3,updated_at=NOW()
WHERE id=$1 AND lease_version=$2 AND status='running'`, jobID, leaseVersion, matched)
	if err := requireImageLibraryLease(result, err); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &service.ImageLibraryCleanupBatch{
		MatchedItems: matched,
		Objects:      objects,
		Done:         len(inputs) < limit,
	}, nil
}

func imageLibraryCleanupWhere(scope string, filters json.RawMessage) (string, []any, error) {
	parsed, err := parseImageLibraryCleanupFilters(filters)
	if err != nil {
		return "", nil, err
	}
	args := make([]any, 0, 2)
	cutoff := "NOW()"
	if parsed.Before != nil && !parsed.Before.IsZero() {
		args = append(args, parsed.Before.UTC())
		cutoff = fmt.Sprintf("$%d", len(args))
	}
	switch scope {
	case "expired":
		return "i.deleted_at IS NULL AND i.expires_at<=" + cutoff, args, nil
	case "deleted":
		return "i.deleted_at IS NOT NULL AND i.deleted_at<=" + cutoff, args, nil
	case "user":
		if parsed.UserID <= 0 {
			return "", nil, apperrors.BadRequest("INVALID_CLEANUP_FILTERS", "user cleanup requires a positive filters.user_id")
		}
		args = append(args, parsed.UserID)
		return fmt.Sprintf("i.user_id=$%d", len(args)), args, nil
	case "all":
		if parsed.Before != nil && !parsed.Before.IsZero() {
			return "i.created_at<=" + cutoff, args, nil
		}
		return "TRUE", args, nil
	default:
		return "", nil, apperrors.BadRequest("INVALID_CLEANUP_SCOPE", "cleanup scope must be expired, deleted, user, async_results, or all")
	}
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func lockImageLibraryUser(ctx context.Context, tx *sql.Tx, userID int64) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(186_000_000_000)+userID)
	return err
}

func resolveImageLibraryProvenance(ctx context.Context, tx *sql.Tx, in *service.CreateImageLibraryAssetParams) error {
	if in.APIKeyID == nil {
		in.GroupID = nil
		in.Platform = ""
		return nil
	}
	var groupID sql.NullInt64
	var platform sql.NullString
	err := tx.QueryRowContext(ctx, `
SELECT k.group_id,g.platform FROM api_keys k
LEFT JOIN groups g ON g.id=k.group_id
WHERE k.id=$1 AND k.user_id=$2 AND k.status='active' AND k.deleted_at IS NULL`, *in.APIKeyID, in.UserID).Scan(&groupID, &platform)
	if err == sql.ErrNoRows {
		return apperrors.NotFound("API_KEY_NOT_FOUND", "active API key not found")
	}
	if err != nil {
		return err
	}
	in.GroupID = nullableInt64Ptr(groupID)
	in.Platform = strings.ToLower(strings.TrimSpace(platform.String))
	return nil
}

func enforceImageLibraryLimits(ctx context.Context, tx *sql.Tx, userID int64, maxItems int, maxBytes, incomingBytes int64, rateLimit int) error {
	if rateLimit > 0 {
		var recent int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_library_items WHERE user_id=$1 AND created_at>=NOW()-INTERVAL '1 minute'`, userID).Scan(&recent); err != nil {
			return err
		}
		if recent >= rateLimit {
			return apperrors.TooManyRequests("IMAGE_LIBRARY_RATE_LIMIT", "too many image archive requests")
		}
	}
	return enforceImageLibraryQuota(ctx, tx, userID, maxItems, maxBytes, incomingBytes, false)
}

func enforceImageLibraryQuota(ctx context.Context, tx *sql.Tx, userID int64, maxItems int, maxBytes, incomingBytes int64, rejectAtCapacity bool) error {
	var itemCount int
	var usedBytes int64
	err := tx.QueryRowContext(ctx, `
SELECT
 (SELECT COUNT(*) FROM image_library_items WHERE user_id=$1 AND deleted_at IS NULL),
 (SELECT COALESCE(SUM(byte_size),0) FROM (
    SELECT DISTINCT i.storage_object_id,o.byte_size
    FROM image_library_items i
    JOIN image_storage_objects o ON o.id=i.storage_object_id
    WHERE i.user_id=$1 AND i.deleted_at IS NULL
  ) unique_objects)`, userID).Scan(&itemCount, &usedBytes)
	if err != nil {
		return err
	}
	if maxItems > 0 && itemCount >= maxItems {
		return apperrors.Conflict("IMAGE_LIBRARY_ITEM_QUOTA", "image library item quota exceeded")
	}
	if rejectAtCapacity && maxBytes > 0 && usedBytes >= maxBytes {
		return apperrors.Conflict("IMAGE_LIBRARY_BYTE_QUOTA", "image library storage quota exceeded")
	}
	if maxBytes > 0 && (incomingBytes > maxBytes || usedBytes > maxBytes-incomingBytes) {
		return apperrors.Conflict("IMAGE_LIBRARY_BYTE_QUOTA", "image library storage quota exceeded")
	}
	return nil
}

func upsertImageStorageObject(ctx context.Context, tx *sql.Tx, ref service.ObjectRef) (int64, error) {
	if ref.SizeBytes < 0 || strings.TrimSpace(ref.ChecksumSHA256) == "" {
		return 0, apperrors.BadRequest("INVALID_STORAGE_OBJECT", "storage object metadata is incomplete")
	}
	var id int64
	err := tx.QueryRowContext(ctx, `
INSERT INTO image_storage_objects(provider,bucket,object_key,content_type,byte_size,checksum_sha256,width,height)
VALUES($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT(provider,bucket,object_key) DO UPDATE SET
 content_type=EXCLUDED.content_type,byte_size=EXCLUDED.byte_size,
 checksum_sha256=EXCLUDED.checksum_sha256,width=EXCLUDED.width,height=EXCLUDED.height,
 state='active',deleted_at=NULL,updated_at=NOW()
RETURNING id`, ref.Provider, ref.Bucket, ref.ObjectKey, ref.ContentType,
		ref.SizeBytes, ref.ChecksumSHA256, nullPositiveInt(ref.Width), nullPositiveInt(ref.Height)).Scan(&id)
	return id, err
}

func appendLibraryEvent(ctx context.Context, tx *sql.Tx, assetID int64, publicationID *int64, actorID *int64, eventType, fromStatus, toStatus string, payload json.RawMessage) error {
	if len(payload) == 0 || !json.Valid(payload) {
		payload = json.RawMessage(`{}`)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO image_library_events(library_item_id,publication_id,actor_user_id,event_type,from_status,to_status,payload) VALUES($1,$2,$3,$4,$5,$6,$7)`, assetID, publicationID, actorID, eventType, nullIfEmpty(fromStatus), nullIfEmpty(toStatus), payload)
	return err
}

func appendLibraryOutbox(ctx context.Context, tx *sql.Tx, aggregateType string, aggregateID int64, eventType, dedupKey string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO image_library_outbox(aggregate_type,aggregate_id,event_type,dedup_key) VALUES($1,$2,$3,$4) ON CONFLICT(dedup_key) DO NOTHING`, aggregateType, aggregateID, eventType, dedupKey)
	return err
}

func getPublication(ctx context.Context, q queryRower, id int64) (*service.ImagePublication, error) {
	var p service.ImagePublication
	err := q.QueryRowContext(ctx, `
SELECT p.id,p.public_id,p.library_item_id,i.asset_id,p.user_id,p.status,p.public_title,p.public_prompt,p.share_prompt,
 p.moderation_status,p.review_reason,p.published_at,p.reviewed_at,p.expires_at,p.created_at,p.updated_at
FROM image_plaza_publications p JOIN image_library_items i ON i.id=p.library_item_id WHERE p.id=$1`, id).Scan(&p.ID, &p.PublicID, &p.LibraryItemID, &p.AssetID,
		&p.UserID, &p.Status, &p.PublicTitle, &p.PublicPrompt, &p.SharePrompt,
		&p.ModerationStatus, &p.ReviewReason, &p.PublishedAt, &p.ReviewedAt,
		&p.ExpiresAt, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, service.ErrImagePublicationNotFound
	}
	return &p, err
}

func appendLibraryFilters(where *[]string, args *[]any, in service.ImageLibraryListParams) {
	if value := strings.TrimSpace(in.Visibility); value != "" {
		*args = append(*args, value)
		*where = append(*where, fmt.Sprintf("i.visibility=$%d", len(*args)))
	}
	if value := strings.TrimSpace(in.SourceType); value != "" {
		*args = append(*args, value)
		*where = append(*where, fmt.Sprintf("i.source_type=$%d", len(*args)))
	}
	if value := strings.TrimSpace(in.Platform); value != "" {
		*args = append(*args, value)
		*where = append(*where, fmt.Sprintf("i.platform=$%d", len(*args)))
	}
	if value := strings.TrimSpace(in.Status); value != "" {
		*args = append(*args, value)
		*where = append(*where, fmt.Sprintf("p.status=$%d", len(*args)))
	}
	if value := strings.TrimSpace(in.Query); value != "" {
		*args = append(*args, "%"+value+"%")
		*where = append(*where, fmt.Sprintf("(i.title ILIKE $%d OR i.model ILIKE $%d)", len(*args), len(*args)))
	}
	if in.Cursor != nil {
		*args = append(*args, in.Cursor.CreatedAt, in.Cursor.ID)
		*where = append(*where, fmt.Sprintf("(i.created_at,i.id)<($%d,$%d)", len(*args)-1, len(*args)))
	}
}

func appendPublicationFilters(where *[]string, args *[]any, in service.ImagePublicationListParams, cursorColumn string) {
	if in.UserID != nil {
		*args = append(*args, *in.UserID)
		*where = append(*where, fmt.Sprintf("p.user_id=$%d", len(*args)))
	}
	if value := strings.TrimSpace(in.Platform); value != "" {
		*args = append(*args, value)
		*where = append(*where, fmt.Sprintf("i.platform=$%d", len(*args)))
	}
	if value := strings.TrimSpace(in.Model); value != "" {
		*args = append(*args, value)
		*where = append(*where, fmt.Sprintf("i.model=$%d", len(*args)))
	}
	if value := strings.TrimSpace(in.AspectRatio); value != "" {
		*args = append(*args, value)
		*where = append(*where, fmt.Sprintf("i.aspect_ratio=$%d", len(*args)))
	}
	if value := strings.TrimSpace(in.Query); value != "" {
		*args = append(*args, "%"+value+"%")
		*where = append(*where, fmt.Sprintf("(p.public_title ILIKE $%d OR (p.share_prompt AND p.public_prompt ILIKE $%d) OR i.model ILIKE $%d)", len(*args), len(*args), len(*args)))
	}
	if in.Cursor != nil && cursorColumn != "" {
		*args = append(*args, in.Cursor.CreatedAt, in.Cursor.ID)
		_, comparator := imagePublicationSortSQL(in.Sort)
		*where = append(*where, fmt.Sprintf("(%s,p.id)%s($%d,$%d)", cursorColumn, comparator, len(*args)-1, len(*args)))
	}
}

func imagePublicationSortSQL(value string) (direction, comparator string) {
	if strings.EqualFold(strings.TrimSpace(value), "oldest") {
		return "ASC", ">"
	}
	return "DESC", "<"
}

func normalizeLibraryLimit(limit int) int {
	if limit <= 0 {
		return 30
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func nullableInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func nullableLibraryIntPtr(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	value := int(v.Int64)
	return &value
}

func nullableStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func nullableTimePtr(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}

func nullableStringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func nullPositiveInt(v int) any {
	if v <= 0 {
		return nil
	}
	return v
}

func nullIfEmpty(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func isImageLibraryUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

func truncateRepositoryText(value string, max int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > max {
		runes = runes[:max]
	}
	return string(runes)
}
