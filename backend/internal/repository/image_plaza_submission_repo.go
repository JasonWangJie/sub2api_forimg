package repository

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

const submissionRequestSelect = `
SELECT r.id, r.request_id, r.user_id, r.status, r.title, r.private_prompt, r.public_title, r.public_prompt,
       r.share_prompt, r.platform, r.generation_mode, r.source_type, r.model, r.requested_size,
       r.aspect_ratio, r.quality, r.content_type, r.byte_size, r.checksum_sha256, r.client_blob_key,
       r.api_key_id, r.group_id, r.reviewer_user_id, r.review_reason, r.reviewed_at,
       r.library_item_id, i.asset_id, r.publication_public_id, r.expires_at, r.created_at, r.updated_at
FROM image_plaza_submission_requests r
LEFT JOIN image_library_items i ON i.id = r.library_item_id`

func scanSubmissionRequest(scanner interface {
	Scan(dest ...any) error
}) (*service.ImagePlazaSubmissionRequest, error) {
	var item service.ImagePlazaSubmissionRequest
	var publicPrompt, reviewReason, publicationID, assetID sql.NullString
	var apiKeyID, groupID, reviewerID, libraryItemID sql.NullInt64
	var reviewedAt sql.NullTime
	if err := scanner.Scan(
		&item.ID, &item.RequestID, &item.UserID, &item.Status, &item.Title, &item.PrivatePrompt,
		&item.PublicTitle, &publicPrompt, &item.SharePrompt, &item.Platform, &item.GenerationMode,
		&item.SourceType, &item.Model, &item.RequestedSize, &item.AspectRatio, &item.Quality,
		&item.ContentType, &item.ByteSize, &item.ChecksumSHA256, &item.ClientBlobKey,
		&apiKeyID, &groupID, &reviewerID, &reviewReason, &reviewedAt,
		&libraryItemID, &assetID, &publicationID, &item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return nil, err
	}
	item.PublicPrompt = nullableStringPtr(publicPrompt)
	item.APIKeyID = nullableInt64Ptr(apiKeyID)
	item.GroupID = nullableInt64Ptr(groupID)
	item.ReviewerUserID = nullableInt64Ptr(reviewerID)
	item.ReviewReason = nullableStringPtr(reviewReason)
	item.ReviewedAt = nullableTimePtr(reviewedAt)
	item.LibraryItemID = nullableInt64Ptr(libraryItemID)
	item.LibraryAssetID = nullableStringPtr(assetID)
	item.PublicationPublicID = nullableStringPtr(publicationID)
	item.ImageHeldClientSide = item.Status == service.ImageSubmissionPendingReview || item.Status == service.ImageSubmissionApprovedPendingSync
	return &item, nil
}

func (r *imageLibraryRepository) CreateSubmissionRequest(ctx context.Context, in service.CreateImagePlazaSubmissionParams) (*service.ImagePlazaSubmissionRequest, bool, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var existingID int64
	err = tx.QueryRowContext(ctx, `
SELECT id FROM image_plaza_submission_requests
WHERE user_id=$1 AND client_blob_key=$2 AND status IN ('pending_review','approved_pending_sync')
FOR UPDATE`, in.UserID, in.ClientBlobKey).Scan(&existingID)
	if err == nil {
		item, getErr := getSubmissionRequestByID(ctx, tx, existingID)
		return item, true, getErr
	}
	if err != sql.ErrNoRows {
		return nil, false, err
	}

	if in.RateLimit > 0 {
		var recent int
		if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM image_plaza_submission_requests
WHERE user_id=$1 AND created_at >= NOW()-INTERVAL '1 minute'`, in.UserID).Scan(&recent); err != nil {
			return nil, false, err
		}
		if recent >= in.RateLimit {
			return nil, false, apperrors.TooManyRequests("IMAGE_PUBLICATION_RATE_LIMIT", "too many image publication requests")
		}
	}

	var publicPrompt any
	if in.SharePrompt && in.PublicPrompt != nil {
		publicPrompt = *in.PublicPrompt
	}
	var id int64
	requestID := "imgsub_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	err = tx.QueryRowContext(ctx, `
INSERT INTO image_plaza_submission_requests (
 request_id, user_id, status, title, private_prompt, public_title, public_prompt, share_prompt,
 platform, generation_mode, source_type, model, requested_size, aspect_ratio, quality,
 content_type, byte_size, checksum_sha256, client_blob_key, api_key_id, group_id, expires_at
) VALUES ($1,$2,'pending_review',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
RETURNING id`, requestID, in.UserID, in.Title, in.PrivatePrompt, in.PublicTitle, publicPrompt, in.SharePrompt,
		in.Platform, in.GenerationMode, in.SourceType, in.Model, in.RequestedSize, in.AspectRatio, in.Quality,
		in.ContentType, in.ByteSize, in.ChecksumSHA256, in.ClientBlobKey, in.APIKeyID, in.GroupID, in.ExpiresAt,
	).Scan(&id)
	if err != nil {
		return nil, false, err
	}
	item, err := getSubmissionRequestByID(ctx, tx, id)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return item, false, nil
}

func (r *imageLibraryRepository) ListSubmissionRequestsForUser(ctx context.Context, userID int64, status string, cursor *service.ImageLibraryCursor, limit int) ([]service.ImagePlazaSubmissionRequest, error) {
	return r.listSubmissionRequests(ctx, &userID, status, cursor, limit)
}

func (r *imageLibraryRepository) ListSubmissionRequestsAdmin(ctx context.Context, status string, cursor *service.ImageLibraryCursor, limit int) ([]service.ImagePlazaSubmissionRequest, error) {
	return r.listSubmissionRequests(ctx, nil, status, cursor, limit)
}

func (r *imageLibraryRepository) listSubmissionRequests(ctx context.Context, userID *int64, status string, cursor *service.ImageLibraryCursor, limit int) ([]service.ImagePlazaSubmissionRequest, error) {
	limit = normalizeLibraryLimit(limit)
	args := make([]any, 0)
	where := []string{"TRUE"}
	if userID != nil {
		args = append(args, *userID)
		where = append(where, "r.user_id=$"+strconv.Itoa(len(args)))
	}
	if value := strings.TrimSpace(status); value != "" {
		args = append(args, value)
		where = append(where, "r.status=$"+strconv.Itoa(len(args)))
	}
	if cursor != nil {
		args = append(args, cursor.CreatedAt, cursor.ID)
		where = append(where, "(r.created_at,r.id)<($"+strconv.Itoa(len(args)-1)+",$"+strconv.Itoa(len(args))+")")
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, submissionRequestSelect+`
WHERE `+strings.Join(where, " AND ")+`
ORDER BY r.created_at DESC, r.id DESC
LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.ImagePlazaSubmissionRequest, 0, limit)
	for rows.Next() {
		item, scanErr := scanSubmissionRequest(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (r *imageLibraryRepository) GetSubmissionRequestForUser(ctx context.Context, userID int64, requestID string) (*service.ImagePlazaSubmissionRequest, error) {
	row := r.db.QueryRowContext(ctx, submissionRequestSelect+` WHERE r.request_id=$1 AND r.user_id=$2`, requestID, userID)
	item, err := scanSubmissionRequest(row)
	if err == sql.ErrNoRows {
		return nil, apperrors.NotFound("IMAGE_SUBMISSION_NOT_FOUND", "publication submission request not found")
	}
	return item, err
}

func (r *imageLibraryRepository) TransitionSubmissionRequest(ctx context.Context, adminUserID int64, requestID, action, reason string) (*service.ImagePlazaSubmissionRequest, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	var status string
	err = tx.QueryRowContext(ctx, `
SELECT id, status FROM image_plaza_submission_requests WHERE request_id=$1 FOR UPDATE`, requestID).Scan(&id, &status)
	if err == sql.ErrNoRows {
		return nil, apperrors.NotFound("IMAGE_SUBMISSION_NOT_FOUND", "publication submission request not found")
	}
	if err != nil {
		return nil, err
	}
	if status != service.ImageSubmissionPendingReview {
		return nil, apperrors.Conflict("INVALID_SUBMISSION_TRANSITION", "submission is not awaiting review")
	}
	toStatus := service.ImageSubmissionApprovedPendingSync
	if action == "reject" {
		toStatus = service.ImageSubmissionRejected
	}
	_, err = tx.ExecContext(ctx, `
UPDATE image_plaza_submission_requests
SET status=$2, reviewer_user_id=$3, review_reason=$4, reviewed_at=NOW(), updated_at=NOW()
WHERE id=$1`, id, toStatus, adminUserID, nullIfEmpty(reason))
	if err != nil {
		return nil, err
	}
	item, err := getSubmissionRequestByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return item, nil
}

func (r *imageLibraryRepository) WithdrawSubmissionRequest(ctx context.Context, userID int64, requestID string) error {
	result, err := r.db.ExecContext(ctx, `
UPDATE image_plaza_submission_requests
SET status='withdrawn', updated_at=NOW()
WHERE request_id=$1 AND user_id=$2 AND status IN ('pending_review','approved_pending_sync')`, requestID, userID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return apperrors.NotFound("IMAGE_SUBMISSION_NOT_FOUND", "publication submission request not found")
	}
	return nil
}

func (r *imageLibraryRepository) MarkSubmissionSynced(ctx context.Context, userID int64, requestID, libraryAssetID, publicationPublicID string) (*service.ImagePlazaSubmissionRequest, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var id, libraryItemID int64
	var status string
	err = tx.QueryRowContext(ctx, `
SELECT id, status FROM image_plaza_submission_requests
WHERE request_id=$1 AND user_id=$2 FOR UPDATE`, requestID, userID).Scan(&id, &status)
	if err == sql.ErrNoRows {
		return nil, apperrors.NotFound("IMAGE_SUBMISSION_NOT_FOUND", "publication submission request not found")
	}
	if err != nil {
		return nil, err
	}
	if status != service.ImageSubmissionApprovedPendingSync && status != service.ImageSubmissionSynced {
		return nil, apperrors.Conflict("SUBMISSION_NOT_READY_TO_SYNC", "only approved submissions waiting for sync can be uploaded")
	}
	err = tx.QueryRowContext(ctx, `
SELECT id FROM image_library_items WHERE asset_id=$1 AND user_id=$2 AND deleted_at IS NULL`, libraryAssetID, userID).Scan(&libraryItemID)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `
UPDATE image_plaza_submission_requests
SET status='synced', library_item_id=$2, publication_public_id=$3, updated_at=NOW()
WHERE id=$1`, id, libraryItemID, publicationPublicID)
	if err != nil {
		return nil, err
	}
	item, err := getSubmissionRequestByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return item, nil
}

func getSubmissionRequestByID(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (*service.ImagePlazaSubmissionRequest, error) {
	row := q.QueryRowContext(ctx, submissionRequestSelect+` WHERE r.id=$1`, id)
	item, err := scanSubmissionRequest(row)
	if err == sql.ErrNoRows {
		return nil, apperrors.NotFound("IMAGE_SUBMISSION_NOT_FOUND", "publication submission request not found")
	}
	return item, err
}

var _ service.ImagePlazaSubmissionRepository = (*imageLibraryRepository)(nil)
