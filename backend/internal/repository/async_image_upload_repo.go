package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const asyncImageUploadAdvisoryLockBase int64 = 187_000_000_000
const maxAsyncImageInputURLAliases = 128

const asyncImageUploadReservationColumns = `
id, reservation_id, user_id, api_key_id, idempotency_key, request_hash,
byte_size, status, input_object_id, failure_reason, lease_expires_at,
reserved_at, completed_at, failed_at, created_at, updated_at,
intent_provider, intent_bucket, intent_object_key, intent_content_type,
intent_byte_size, intent_checksum, cleanup_claimed_at, cleanup_delete_count,
last_deleted_at, idempotency_expires_at`

func (r *asyncImageTaskRepository) AdmitAsyncImageUpload(ctx context.Context, params service.AdmitAsyncImageUploadParams) (_ *service.AsyncImageUploadAdmission, retErr error) {
	if r == nil || r.db == nil {
		return nil, errors.New("async image upload repository is not configured")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, asyncImageUploadAdvisoryLockBase+params.APIKeyID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `
WITH stale AS (
    SELECT id FROM async_image_upload_attempts
    WHERE attempted_at < $1::timestamptz - INTERVAL '5 minutes'
    ORDER BY attempted_at,id LIMIT 200 FOR UPDATE SKIP LOCKED
)
DELETE FROM async_image_upload_attempts a USING stale
WHERE a.id=stale.id`, params.Now); err != nil {
		return nil, err
	}
	var recentAttempts int
	if err = tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM async_image_upload_attempts
WHERE api_key_id=$1 AND attempted_at > $2::timestamptz - INTERVAL '1 minute'`, params.APIKeyID, params.Now).Scan(&recentAttempts); err != nil {
		return nil, err
	}
	if recentAttempts >= params.UploadPerMinute {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return nil, service.ErrAsyncImageUploadRateLimited
	}
	if _, err = tx.ExecContext(ctx, `
INSERT INTO async_image_upload_attempts(admission_id,user_id,api_key_id,attempted_at)
VALUES($1,$2,$3,$4)`, params.AdmissionID, params.UserID, params.APIKeyID, params.Now); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &service.AsyncImageUploadAdmission{AdmissionID: params.AdmissionID, AttemptedAt: params.Now}, nil
}

func (r *asyncImageTaskRepository) ReserveAsyncImageUpload(ctx context.Context, params service.ReserveAsyncImageUploadParams) (_ *service.AsyncImageUploadReservationResult, retErr error) {
	if r == nil || r.db == nil {
		return nil, errors.New("async image upload repository is not configured")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			_ = tx.Rollback()
		}
	}()

	// Reserve uses the same per-key lock as Admit. The admission row proves the
	// rolling limit was consumed, while this transaction serializes active-byte
	// accounting across processes without Redis or process-local state.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, asyncImageUploadAdvisoryLockBase+params.APIKeyID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE async_image_upload_reservations
SET status='failed', input_object_id=NULL, failure_reason='reservation_expired',
    lease_expires_at=NULL, completed_at=NULL, failed_at=$2, updated_at=$2
WHERE api_key_id=$1 AND status='reserved' AND intent_object_key IS NULL
  AND lease_expires_at <= $2`, params.APIKeyID, params.Now); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `
WITH stale AS (
    SELECT id FROM async_image_upload_reservations
    WHERE cleanup_claimed_at IS NULL AND intent_object_key IS NULL AND (
        (status='failed' AND updated_at < $1::timestamptz - INTERVAL '24 hours')
        OR (status='completed' AND input_object_id IS NULL AND idempotency_expires_at < $1::timestamptz)
    )
    ORDER BY updated_at,id LIMIT 200 FOR UPDATE SKIP LOCKED
)
DELETE FROM async_image_upload_reservations r USING stale
WHERE r.id=stale.id`, params.Now); err != nil {
		return nil, err
	}
	var admissionID string
	if err = tx.QueryRowContext(ctx, `
UPDATE async_image_upload_attempts SET consumed_at=$4
WHERE admission_id=$1 AND user_id=$2 AND api_key_id=$3
  AND consumed_at IS NULL AND attempted_at > $4::timestamptz - INTERVAL '5 minutes'
RETURNING admission_id`, params.AdmissionID, params.UserID, params.APIKeyID, params.Now).Scan(&admissionID); err == sql.ErrNoRows {
		return nil, service.ErrAsyncImageUploadReservationInvalid
	} else if err != nil {
		return nil, err
	}

	var existing *service.AsyncImageUploadReservation
	if params.IdempotencyKey != nil {
		reservation, queryErr := queryAsyncImageUploadReservation(ctx, tx, `api_key_id=$1 AND idempotency_key=$2 FOR UPDATE`, params.APIKeyID, *params.IdempotencyKey)
		switch {
		case queryErr == nil:
			existing = reservation
			if reservation.RequestHash != params.RequestHash {
				return nil, service.ErrAsyncImageUploadIdempotencyConflict
			}
			if reservation.CleanupClaimedAt != nil {
				return nil, service.ErrAsyncImageUploadResultUnavailable
			}
			if reservation.ObjectIntent != nil {
				return nil, service.ErrAsyncImageUploadInProgress
			}
			if reservation.Status == service.AsyncImageUploadStatusCompleted && reservation.InputObjectID != nil {
				object, objectErr := getLiveAsyncImageInputObject(ctx, tx, *reservation.InputObjectID, params.UserID, params.APIKeyID, params.Now)
				if objectErr == nil {
					if _, err = tx.ExecContext(ctx, `
UPDATE async_image_upload_attempts SET reservation_id=$2
WHERE admission_id=$1 AND consumed_at=$3`, params.AdmissionID, reservation.ReservationID, params.Now); err != nil {
						return nil, err
					}
					if err = tx.Commit(); err != nil {
						return nil, err
					}
					return &service.AsyncImageUploadReservationResult{Reservation: reservation, InputObject: object, Reused: true}, nil
				}
				if objectErr != sql.ErrNoRows {
					return nil, objectErr
				}
				return nil, service.ErrAsyncImageUploadResultUnavailable
			}
			if reservation.Status == service.AsyncImageUploadStatusCompleted {
				return nil, service.ErrAsyncImageUploadResultUnavailable
			}
			if reservation.Status == service.AsyncImageUploadStatusReserved {
				return nil, service.ErrAsyncImageUploadInProgress
			}
		case queryErr == sql.ErrNoRows:
		case queryErr != nil:
			return nil, queryErr
		}
	}

	var storedBytes, reservedBytes int64
	if err = tx.QueryRowContext(ctx, `
SELECT
  COALESCE((
    SELECT SUM(byte_size) FROM async_image_input_objects
    WHERE api_key_id=$1 AND cleanup_claimed_at IS NULL AND expires_at > $2
  ),0),
  COALESCE((
    SELECT SUM(CASE
        WHEN intent_object_key IS NOT NULL THEN intent_byte_size
        ELSE byte_size
    END) FROM async_image_upload_reservations
    WHERE api_key_id=$1 AND (
        (status='reserved' AND lease_expires_at > $2)
        OR (status='failed' AND intent_object_key IS NOT NULL)
        OR (status='reserved' AND intent_object_key IS NOT NULL)
    )
  ),0)`, params.APIKeyID, params.Now).Scan(&storedBytes, &reservedBytes); err != nil {
		return nil, err
	}
	usedBytes := storedBytes + reservedBytes
	if params.ByteSize > params.MaxInputBytesPerKey || usedBytes > params.MaxInputBytesPerKey-params.ByteSize {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return nil, service.ErrAsyncImageUploadQuotaExceeded
	}

	reservation := &service.AsyncImageUploadReservation{}
	if existing != nil {
		err = scanAsyncImageUploadReservation(tx.QueryRowContext(ctx, `
UPDATE async_image_upload_reservations SET
    user_id=$2, request_hash=$3, byte_size=$4, status='reserved', input_object_id=NULL,
    failure_reason=NULL, lease_expires_at=$5, reserved_at=$6,
    completed_at=NULL, failed_at=NULL, updated_at=$6,
    intent_provider=NULL,intent_bucket=NULL,intent_object_key=NULL,intent_content_type=NULL,
    intent_byte_size=NULL,intent_checksum=NULL,cleanup_claimed_at=NULL,
    cleanup_delete_count=0,last_deleted_at=NULL,idempotency_expires_at=NULL
WHERE id=$1
RETURNING `+asyncImageUploadReservationColumns,
			existing.ID, params.UserID, params.RequestHash, params.ByteSize, params.LeaseExpiresAt, params.Now), reservation)
	} else {
		err = scanAsyncImageUploadReservation(tx.QueryRowContext(ctx, `
INSERT INTO async_image_upload_reservations (
    reservation_id,user_id,api_key_id,idempotency_key,request_hash,byte_size,
    status,lease_expires_at,reserved_at
) VALUES ($1,$2,$3,$4,$5,$6,'reserved',$7,$8)
RETURNING `+asyncImageUploadReservationColumns,
			params.ReservationID, params.UserID, params.APIKeyID, params.IdempotencyKey,
			params.RequestHash, params.ByteSize, params.LeaseExpiresAt, params.Now), reservation)
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE async_image_upload_attempts SET reservation_id=$2
WHERE admission_id=$1 AND consumed_at=$3`, params.AdmissionID, reservation.ReservationID, params.Now); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &service.AsyncImageUploadReservationResult{Reservation: reservation}, nil
}

func (r *asyncImageTaskRepository) SetAsyncImageUploadObjectIntent(ctx context.Context, params service.SetAsyncImageUploadObjectIntentParams) error {
	if r == nil || r.sql == nil {
		return errors.New("async image upload repository is not configured")
	}
	ref := params.ObjectRef
	result, err := r.sql.ExecContext(ctx, `
UPDATE async_image_upload_reservations SET
    intent_provider=$5,intent_bucket=$6,intent_object_key=$7,intent_content_type=$8,
    intent_byte_size=$9,intent_checksum=$10,cleanup_delete_count=0,last_deleted_at=NULL,updated_at=NOW()
WHERE reservation_id=$1 AND user_id=$2 AND api_key_id=$3 AND request_hash=$4
  AND status='reserved' AND lease_expires_at>NOW() AND cleanup_claimed_at IS NULL`,
		strings.TrimSpace(params.ReservationID), params.UserID, params.APIKeyID,
		strings.ToLower(strings.TrimSpace(params.RequestHash)), ref.Provider, ref.Bucket,
		ref.ObjectKey, ref.ContentType, ref.SizeBytes, ref.ChecksumSHA256)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return service.ErrAsyncImageUploadReservationInvalid
	}
	return nil
}

func (r *asyncImageTaskRepository) CompleteAsyncImageUpload(ctx context.Context, params service.CompleteAsyncImageUploadParams) (_ *service.AsyncImageInputObject, retErr error) {
	if r == nil || r.db == nil {
		return nil, errors.New("async image upload repository is not configured")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			_ = tx.Rollback()
		}
	}()

	reservation, err := queryAsyncImageUploadReservation(ctx, tx, `reservation_id=$1 FOR UPDATE`, strings.TrimSpace(params.ReservationID))
	if err == sql.ErrNoRows {
		return nil, service.ErrAsyncImageUploadReservationInvalid
	}
	if err != nil {
		return nil, err
	}
	if reservation.UserID != params.UserID || reservation.APIKeyID != params.APIKeyID || reservation.RequestHash != strings.ToLower(strings.TrimSpace(params.RequestHash)) {
		return nil, service.ErrAsyncImageUploadReservationInvalid
	}
	if reservation.Status == service.AsyncImageUploadStatusCompleted && reservation.InputObjectID != nil {
		object, objectErr := getLiveAsyncImageInputObject(ctx, tx, *reservation.InputObjectID, params.UserID, params.APIKeyID, time.Now().UTC())
		if objectErr != nil {
			return nil, service.ErrAsyncImageUploadReservationInvalid
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return object, nil
	}
	if reservation.Status != service.AsyncImageUploadStatusReserved || reservation.LeaseExpiresAt == nil ||
		!reservation.LeaseExpiresAt.After(time.Now().UTC()) || reservation.ByteSize != params.ObjectRef.SizeBytes ||
		reservation.ObjectIntent == nil || !sameAsyncImageObjectIdentity(*reservation.ObjectIntent, params.ObjectRef) {
		return nil, service.ErrAsyncImageUploadReservationInvalid
	}

	object := &service.AsyncImageInputObject{}
	var width, height sql.NullInt64
	var filename sql.NullString
	err = tx.QueryRowContext(ctx, `
INSERT INTO async_image_input_objects (
    upload_id, user_id, api_key_id, provider, bucket, object_key,
    content_type, byte_size, checksum, width, height, url_hash, filename, expires_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12, $13, $14
)
RETURNING id, upload_id, user_id, api_key_id, provider, bucket, object_key,
          content_type, byte_size, checksum, width, height, url_hash, filename,
          expires_at, cleanup_claimed_at, created_at`,
		reservation.ReservationID, params.UserID, params.APIKeyID,
		params.ObjectRef.Provider, params.ObjectRef.Bucket, params.ObjectRef.ObjectKey,
		params.ObjectRef.ContentType, params.ObjectRef.SizeBytes, params.ObjectRef.ChecksumSHA256,
		nullablePositiveInt(params.ObjectRef.Width), nullablePositiveInt(params.ObjectRef.Height),
		strings.TrimSpace(params.URLHash), nullableTrimmedString(params.Filename), params.ExpiresAt,
	).Scan(
		&object.ID, &object.UploadID, &object.UserID, &object.APIKeyID,
		&object.ObjectRef.Provider, &object.ObjectRef.Bucket, &object.ObjectRef.ObjectKey,
		&object.ObjectRef.ContentType, &object.ObjectRef.SizeBytes, &object.ObjectRef.ChecksumSHA256,
		&width, &height, &object.URLHash, &filename,
		&object.ExpiresAt, &object.CleanupClaimedAt, &object.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	object.ObjectRef.Width = nullIntValue(width)
	object.ObjectRef.Height = nullIntValue(height)
	object.Filename = filename.String

	result, err := tx.ExecContext(ctx, `
UPDATE async_image_upload_reservations SET
    status='completed', input_object_id=$2, failure_reason=NULL,
    lease_expires_at=NULL, completed_at=NOW(), failed_at=NULL, updated_at=NOW(),
    intent_provider=NULL,intent_bucket=NULL,intent_object_key=NULL,intent_content_type=NULL,
    intent_byte_size=NULL,intent_checksum=NULL,cleanup_claimed_at=NULL,
    cleanup_delete_count=0,last_deleted_at=NULL,
    idempotency_expires_at=$3::timestamptz + INTERVAL '24 hours'
WHERE id=$1 AND status='reserved'`, reservation.ID, object.ID, params.ExpiresAt)
	if err != nil {
		return nil, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if updated != 1 {
		return nil, service.ErrAsyncImageUploadReservationInvalid
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return object, nil
}

func (r *asyncImageTaskRepository) FailAsyncImageUpload(ctx context.Context, reservationID, requestHash, reason string) (bool, error) {
	if r == nil || r.sql == nil {
		return false, errors.New("async image upload repository is not configured")
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > 64 {
		reason = reason[:64]
	}
	result, err := r.sql.ExecContext(ctx, `
UPDATE async_image_upload_reservations SET
    status='failed', input_object_id=NULL, failure_reason=$3,
    lease_expires_at=NULL, completed_at=NULL, failed_at=NOW(), updated_at=NOW()
WHERE reservation_id=$1 AND request_hash=$2 AND status='reserved'`,
		strings.TrimSpace(reservationID), strings.ToLower(strings.TrimSpace(requestHash)), nullableTrimmedString(reason))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (r *asyncImageTaskRepository) RegisterAsyncImageInputURLAlias(ctx context.Context, params service.RegisterAsyncImageInputURLAliasParams) (retErr error) {
	if r == nil || r.db == nil {
		return errors.New("async image upload repository is not configured")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			_ = tx.Rollback()
		}
	}()
	var objectID int64
	if err = tx.QueryRowContext(ctx, `
SELECT id FROM async_image_input_objects
WHERE id=$1 AND user_id=$2 AND api_key_id=$3
  AND cleanup_claimed_at IS NULL AND expires_at > NOW()
FOR UPDATE`, params.InputObjectID, params.UserID, params.APIKeyID).Scan(&objectID); err == sql.ErrNoRows {
		return service.ErrAsyncImageUploadReservationInvalid
	} else if err != nil {
		return err
	}
	hash := strings.TrimSpace(params.URLHash)
	var existingObjectID int64
	err = tx.QueryRowContext(ctx, `
SELECT input_object_id FROM async_image_input_url_aliases WHERE url_hash=$1`, hash).Scan(&existingObjectID)
	if err == nil {
		if existingObjectID != objectID {
			return service.ErrAsyncImageUploadReservationInvalid
		}
		if _, err = tx.ExecContext(ctx, `
UPDATE async_image_input_url_aliases SET expires_at=GREATEST(expires_at,$2)
WHERE url_hash=$1 AND input_object_id=$3`, hash, params.ExpiresAt, objectID); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err != sql.ErrNoRows {
		return err
	}
	var aliasCount int
	if err = tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM async_image_input_url_aliases
WHERE input_object_id=$1`, objectID).Scan(&aliasCount); err != nil {
		return err
	}
	if aliasCount >= maxAsyncImageInputURLAliases {
		return service.ErrAsyncImageUploadAliasLimit
	}
	if _, err = tx.ExecContext(ctx, `
INSERT INTO async_image_input_url_aliases(url_hash,input_object_id,expires_at)
VALUES($1,$2,$3)`, hash, objectID, params.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *asyncImageTaskRepository) DeleteExpiredAsyncImageUploadAdmissionState(ctx context.Context, before time.Time, limit int) (int64, error) {
	if r == nil || r.sql == nil {
		return 0, errors.New("async image upload repository is not configured")
	}
	limit = normalizeAsyncImageCleanupLimit(limit)
	var deleted int64
	err := r.sql.QueryRowContext(ctx, `
WITH stale_attempts AS (
    SELECT id FROM async_image_upload_attempts
    WHERE attempted_at < $1::timestamptz - INTERVAL '5 minutes'
    ORDER BY attempted_at,id LIMIT $2 FOR UPDATE SKIP LOCKED
), deleted_attempts AS (
    DELETE FROM async_image_upload_attempts a USING stale_attempts s
    WHERE a.id=s.id RETURNING a.id
), stale_reservations AS (
    SELECT id FROM async_image_upload_reservations
    WHERE cleanup_claimed_at IS NULL AND intent_object_key IS NULL AND (
        (status='failed' AND updated_at < $1::timestamptz - INTERVAL '24 hours')
        OR (status='completed' AND input_object_id IS NULL AND idempotency_expires_at < $1::timestamptz)
    )
    ORDER BY updated_at,id LIMIT $2 FOR UPDATE SKIP LOCKED
), deleted_reservations AS (
    DELETE FROM async_image_upload_reservations r USING stale_reservations s
    WHERE r.id=s.id RETURNING r.id
)
SELECT
  (SELECT COUNT(*) FROM deleted_attempts) +
  (SELECT COUNT(*) FROM deleted_reservations)`, before, limit).Scan(&deleted)
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

func (r *asyncImageTaskRepository) ClaimAsyncImageUploadCleanupIntents(ctx context.Context, before, staleBefore time.Time, limit int) ([]service.AsyncImageUploadCleanupIntent, error) {
	if r == nil || r.sql == nil {
		return nil, errors.New("async image upload repository is not configured")
	}
	limit = normalizeAsyncImageCleanupLimit(limit)
	rows, err := r.sql.QueryContext(ctx, `
WITH candidates AS (
    SELECT id FROM async_image_upload_reservations
    WHERE intent_object_key IS NOT NULL
      AND (cleanup_claimed_at IS NULL OR cleanup_claimed_at <= $2::timestamptz)
      AND (cleanup_delete_count=0 OR last_deleted_at <= $1::timestamptz - INTERVAL '10 minutes')
      AND (
          (status='failed' AND updated_at <= $1::timestamptz - INTERVAL '10 minutes')
          OR (status='reserved' AND lease_expires_at <= $2::timestamptz)
      )
    ORDER BY updated_at,id LIMIT $3 FOR UPDATE SKIP LOCKED
)
UPDATE async_image_upload_reservations r SET
    status='failed',input_object_id=NULL,failure_reason=COALESCE(failure_reason,'reservation_expired'),
    lease_expires_at=NULL,completed_at=NULL,failed_at=COALESCE(failed_at,NOW()),
    cleanup_claimed_at=NOW(),updated_at=NOW()
FROM candidates c WHERE r.id=c.id
RETURNING r.reservation_id,r.intent_provider,r.intent_bucket,r.intent_object_key,
          r.intent_content_type,r.intent_byte_size,r.intent_checksum,r.cleanup_claimed_at`,
		before, staleBefore, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	intents := make([]service.AsyncImageUploadCleanupIntent, 0)
	for rows.Next() {
		var intent service.AsyncImageUploadCleanupIntent
		if err := rows.Scan(
			&intent.ReservationID, &intent.ObjectRef.Provider, &intent.ObjectRef.Bucket,
			&intent.ObjectRef.ObjectKey, &intent.ObjectRef.ContentType, &intent.ObjectRef.SizeBytes,
			&intent.ObjectRef.ChecksumSHA256, &intent.CleanupClaimedAt,
		); err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}

func (r *asyncImageTaskRepository) CompleteAsyncImageUploadIntentDeletion(ctx context.Context, reservationID string, claimedAt time.Time) (_ bool, retErr error) {
	if r == nil || r.db == nil {
		return false, errors.New("async image upload repository is not configured")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() {
		if retErr != nil {
			_ = tx.Rollback()
		}
	}()
	var deleteCount int
	err = tx.QueryRowContext(ctx, `
SELECT cleanup_delete_count FROM async_image_upload_reservations
WHERE reservation_id=$1 AND cleanup_claimed_at=$2 AND status='failed'
FOR UPDATE`, reservationID, claimedAt).Scan(&deleteCount)
	if err == sql.ErrNoRows {
		return false, service.ErrAsyncImageInvalidTransition
	}
	if err != nil {
		return false, err
	}
	if deleteCount == 0 {
		result, updateErr := tx.ExecContext(ctx, `
UPDATE async_image_upload_reservations SET
    cleanup_delete_count=1,last_deleted_at=NOW(),cleanup_claimed_at=NULL,updated_at=NOW()
WHERE reservation_id=$1 AND cleanup_claimed_at=$2 AND status='failed' AND cleanup_delete_count=0`,
			reservationID, claimedAt)
		if err = requireAsyncImageCleanupDelete(result, updateErr); err != nil {
			return false, err
		}
		if err = tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	result, deleteErr := tx.ExecContext(ctx, `
DELETE FROM async_image_upload_reservations
WHERE reservation_id=$1 AND cleanup_claimed_at=$2 AND status='failed'
  AND cleanup_delete_count=1 AND last_deleted_at <= NOW() - INTERVAL '10 minutes'`,
		reservationID, claimedAt)
	if err = requireAsyncImageCleanupDelete(result, deleteErr); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *asyncImageTaskRepository) ReleaseAsyncImageUploadIntentDeletion(ctx context.Context, reservationID string, claimedAt time.Time) error {
	if r == nil || r.sql == nil {
		return errors.New("async image upload repository is not configured")
	}
	_, err := r.sql.ExecContext(ctx, `
UPDATE async_image_upload_reservations SET cleanup_claimed_at=NULL,updated_at=NOW()
WHERE reservation_id=$1 AND cleanup_claimed_at=$2 AND status='failed'`, reservationID, claimedAt)
	return err
}

func queryAsyncImageUploadReservation(ctx context.Context, tx *sql.Tx, where string, args ...any) (*service.AsyncImageUploadReservation, error) {
	reservation := &service.AsyncImageUploadReservation{}
	err := scanAsyncImageUploadReservation(tx.QueryRowContext(ctx, `SELECT `+asyncImageUploadReservationColumns+` FROM async_image_upload_reservations WHERE `+where, args...), reservation)
	if err != nil {
		return nil, err
	}
	return reservation, nil
}

func scanAsyncImageUploadReservation(scanner interface{ Scan(dest ...any) error }, reservation *service.AsyncImageUploadReservation) error {
	var idempotencyKey, failureReason sql.NullString
	var inputObjectID sql.NullInt64
	var leaseExpiresAt, completedAt, failedAt, cleanupClaimedAt, lastDeletedAt, idempotencyExpiresAt sql.NullTime
	var intentProvider, intentBucket, intentObjectKey, intentContentType, intentChecksum sql.NullString
	var intentByteSize sql.NullInt64
	err := scanner.Scan(
		&reservation.ID, &reservation.ReservationID, &reservation.UserID, &reservation.APIKeyID,
		&idempotencyKey, &reservation.RequestHash, &reservation.ByteSize, &reservation.Status,
		&inputObjectID, &failureReason, &leaseExpiresAt, &reservation.ReservedAt,
		&completedAt, &failedAt, &reservation.CreatedAt, &reservation.UpdatedAt,
		&intentProvider, &intentBucket, &intentObjectKey, &intentContentType,
		&intentByteSize, &intentChecksum, &cleanupClaimedAt, &reservation.CleanupDeleteCount,
		&lastDeletedAt, &idempotencyExpiresAt,
	)
	if err != nil {
		return err
	}
	reservation.IdempotencyKey = nullableStringPtr(idempotencyKey)
	reservation.InputObjectID = nullableInt64Ptr(inputObjectID)
	reservation.FailureReason = nullableStringPtr(failureReason)
	reservation.LeaseExpiresAt = nullableTimePtr(leaseExpiresAt)
	reservation.CompletedAt = nullableTimePtr(completedAt)
	reservation.FailedAt = nullableTimePtr(failedAt)
	reservation.CleanupClaimedAt = nullableTimePtr(cleanupClaimedAt)
	reservation.LastDeletedAt = nullableTimePtr(lastDeletedAt)
	reservation.IdempotencyExpiresAt = nullableTimePtr(idempotencyExpiresAt)
	if intentProvider.Valid && intentBucket.Valid && intentObjectKey.Valid {
		reservation.ObjectIntent = &service.ObjectRef{
			Provider: intentProvider.String, Bucket: intentBucket.String, ObjectKey: intentObjectKey.String,
			ContentType: intentContentType.String, SizeBytes: intentByteSize.Int64,
			ChecksumSHA256: intentChecksum.String,
		}
	}
	return nil
}

func sameAsyncImageObjectIdentity(expected, actual service.ObjectRef) bool {
	return expected.Provider == actual.Provider && expected.Bucket == actual.Bucket &&
		expected.ObjectKey == actual.ObjectKey && expected.ContentType == actual.ContentType &&
		expected.SizeBytes == actual.SizeBytes && expected.ChecksumSHA256 == actual.ChecksumSHA256
}

func getLiveAsyncImageInputObject(ctx context.Context, tx *sql.Tx, objectID, userID, apiKeyID int64, now time.Time) (*service.AsyncImageInputObject, error) {
	returnObject, err := scanAsyncImageInputObject(tx.QueryRowContext(ctx, `
SELECT id, upload_id, user_id, api_key_id, provider, bucket, object_key,
       content_type, byte_size, checksum, width, height, url_hash, filename,
       expires_at, cleanup_claimed_at, created_at
FROM async_image_input_objects
WHERE id=$1 AND user_id=$2 AND api_key_id=$3
  AND cleanup_claimed_at IS NULL AND expires_at > $4`, objectID, userID, apiKeyID, now))
	if err != nil {
		return nil, err
	}
	return &returnObject, nil
}

var _ service.AsyncImageUploadReservationRepository = (*asyncImageTaskRepository)(nil)
var _ service.AsyncImageUploadIntentRetentionRepository = (*asyncImageTaskRepository)(nil)
