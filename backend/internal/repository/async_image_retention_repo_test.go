package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageInputObjectRepositoryPersistsOwnerAndStableRef(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	expiresAt := now.Add(24 * time.Hour)
	mock.ExpectQuery("(?s)INSERT INTO async_image_input_objects .*RETURNING").
		WithArgs(
			"asyncimg_upload", int64(10), int64(20), service.ImageStorageProviderAliyun,
			"images", "inputs/20/upload.png", "image/png", int64(12), "checksum",
			640, 480, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"reference.png", expiresAt,
		).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "upload_id", "user_id", "api_key_id", "provider", "bucket", "object_key",
			"content_type", "byte_size", "checksum", "width", "height", "url_hash", "filename",
			"expires_at", "cleanup_claimed_at", "created_at",
		}).AddRow(
			int64(1), "asyncimg_upload", int64(10), int64(20), service.ImageStorageProviderAliyun,
			"images", "inputs/20/upload.png", "image/png", int64(12), "checksum", 640, 480,
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "reference.png",
			expiresAt, nil, now,
		))

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	object, err := repo.RegisterAsyncImageInputObject(context.Background(), service.RegisterAsyncImageInputObjectParams{
		UploadID: "asyncimg_upload", UserID: 10, APIKeyID: 20,
		ObjectRef: service.ObjectRef{
			Provider: service.ImageStorageProviderAliyun, Bucket: "images", ObjectKey: "inputs/20/upload.png",
			ContentType: "image/png", SizeBytes: 12, ChecksumSHA256: "checksum", Width: 640, Height: 480,
		},
		URLHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Filename: "reference.png", ExpiresAt: expiresAt,
	})
	require.NoError(t, err)
	require.Equal(t, int64(20), object.APIKeyID)
	require.Equal(t, "inputs/20/upload.png", object.ObjectRef.ObjectKey)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompleteAsyncImageResultDeletionMarksUnreferencedStorageObjectDeleted(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	claimedAt := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("(?s)DELETE FROM async_image_results.*RETURNING r.storage_object_id").
		WithArgs(int64(41), claimedAt).
		WillReturnRows(sqlmock.NewRows([]string{"storage_object_id", "provider", "bucket", "object_key"}).
			AddRow(int64(77), "aliyun", "images", "results/41.png"))
	mock.ExpectExec("(?s)UPDATE image_storage_objects.*state='deleted'.*NOT EXISTS.*async_image_results.*NOT EXISTS.*image_library_items.*NOT EXISTS.*image_plaza_publications").
		WithArgs(int64(77)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	err = repo.CompleteAsyncImageResultDeletion(context.Background(), 41, claimedAt)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompleteAsyncImageResultDeletionRollsBackWhenObjectStateCannotPersist(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	claimedAt := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("(?s)DELETE FROM async_image_results.*RETURNING r.storage_object_id").
		WithArgs(int64(41), claimedAt).
		WillReturnRows(sqlmock.NewRows([]string{"storage_object_id", "provider", "bucket", "object_key"}).
			AddRow(int64(77), "aliyun", "images", "results/41.png"))
	mock.ExpectExec("(?s)UPDATE image_storage_objects.*state='deleted'").
		WithArgs(int64(77)).
		WillReturnError(errors.New("database unavailable"))
	mock.ExpectRollback()

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	err = repo.CompleteAsyncImageResultDeletion(context.Background(), 41, claimedAt)
	require.EqualError(t, err, "database unavailable")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompleteAsyncImageResultDeletionSupportsLegacyResultWithoutObjectID(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	claimedAt := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("(?s)DELETE FROM async_image_results.*RETURNING r.storage_object_id").
		WithArgs(int64(41), claimedAt).
		WillReturnRows(sqlmock.NewRows([]string{"storage_object_id", "provider", "bucket", "object_key"}).
			AddRow(nil, "qiniu", "legacy", "results/41.webp"))
	mock.ExpectExec("(?s)UPDATE image_storage_objects.*o.provider=\\$1 AND o.bucket=\\$2 AND o.object_key=\\$3").
		WithArgs("qiniu", "legacy", "results/41.webp").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	err = repo.CompleteAsyncImageResultDeletion(context.Background(), 41, claimedAt)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompleteAsyncImageResultDeletionRejectsLostClaim(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	claimedAt := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("(?s)DELETE FROM async_image_results.*RETURNING r.storage_object_id").
		WithArgs(int64(41), claimedAt).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	err = repo.CompleteAsyncImageResultDeletion(context.Background(), 41, claimedAt)
	require.ErrorIs(t, err, service.ErrAsyncImageInvalidTransition)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHasLiveImageObjectReferenceExcludesClaimedResultAndTask(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("(?s)SELECT EXISTS.*image_library_items.*image_plaza_publications.*async_image_results").
		WithArgs("tencent", "images", "results/shared.png", int64(41), "asyncimg_task").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	exists, err := repo.HasLiveImageObjectReference(context.Background(), service.ObjectRef{
		Provider: "tencent", Bucket: "images", ObjectKey: "results/shared.png",
	}, 41, "asyncimg_task")
	require.NoError(t, err)
	require.True(t, exists)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompleteAsyncImageTaskDeletionMarksResultObjectsDeleted(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	claimedAt := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("(?s)SELECT storage_object_id,provider,bucket,object_key.*FOR UPDATE").
		WithArgs("asyncimg_old").
		WillReturnRows(sqlmock.NewRows([]string{"storage_object_id", "provider", "bucket", "object_key"}).
			AddRow(int64(77), "aliyun", "images", "results/old.png"))
	mock.ExpectExec("(?s)DELETE FROM async_image_tasks.*cleanup_claimed_at").
		WithArgs("asyncimg_old", claimedAt).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("(?s)UPDATE image_storage_objects.*state='deleted'.*NOT EXISTS.*async_image_results").
		WithArgs(int64(77)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	err = repo.CompleteAsyncImageTaskDeletion(context.Background(), "asyncimg_old", claimedAt)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClaimExpiredInputsExcludesActiveTaskReferences(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	mock.ExpectQuery("(?s)NOT EXISTS.*async_image_task_inputs.*t.status IN.*FOR UPDATE OF i SKIP LOCKED.*UPDATE async_image_input_objects").
		WithArgs(now, now.Add(-30*time.Minute), 25).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "upload_id", "user_id", "api_key_id", "provider", "bucket", "object_key",
			"content_type", "byte_size", "checksum", "width", "height", "url_hash", "filename",
			"expires_at", "cleanup_claimed_at", "created_at",
		}))

	repo := NewAsyncImageTaskRepository(db).(*asyncImageTaskRepository)
	objects, err := repo.ClaimExpiredAsyncImageInputObjects(context.Background(), now, now.Add(-30*time.Minute), 25)
	require.NoError(t, err)
	require.Empty(t, objects)
	require.NoError(t, mock.ExpectationsWereMet())
}
