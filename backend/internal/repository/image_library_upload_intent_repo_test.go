package repository

import (
	"context"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPrepareLibraryUploadIntentPersistsFinalIdentityBeforeUpload(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	intent := service.ImageLibraryUploadIntent{
		ObjectRef: service.ObjectRef{
			Provider: "custom_s3", Bucket: "library", ObjectKey: "prefix/42/image.png",
			ContentType: "image/png", SizeBytes: 1234,
			ChecksumSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		ExpiresAt: expiresAt,
	}
	mock.ExpectQuery(`(?s)INSERT INTO image_library_upload_intents.*RETURNING id`).
		WithArgs(intent.Provider, intent.Bucket, intent.ObjectKey, intent.ContentType, intent.SizeBytes, intent.ChecksumSHA256, expiresAt).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(17)))

	repo := requireImageLibraryRepository(t, db)
	id, err := repo.PrepareLibraryUploadIntent(context.Background(), intent)
	require.NoError(t, err)
	require.Equal(t, int64(17), id)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClaimExpiredLibraryUploadIntentsReturnsReferenceDecision(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	claimedAt := now.Add(-time.Minute)
	mock.ExpectQuery(`(?s)image_library_upload_intents.*FOR UPDATE SKIP LOCKED.*image_storage_objects.*image_library_items.*async_image_results.*async_image_input_objects.*async_image_result_upload_intents.*async_image_upload_reservations`).
		WithArgs(now, sqlmock.AnyArg(), 100).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "provider", "bucket", "object_key", "content_type", "byte_size",
			"checksum_sha256", "expires_at", "cleanup_claimed_at", "referenced",
		}).AddRow(
			int64(23), "custom_s3", "library", "prefix/orphan.png", "image/png", int64(99),
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", now.Add(-time.Hour), claimedAt, false,
		))

	repo := requireImageLibraryRepository(t, db)
	intents, err := repo.ClaimExpiredLibraryUploadIntents(context.Background(), now, now.Add(-2*time.Minute), 100)
	require.NoError(t, err)
	require.Len(t, intents, 1)
	require.Equal(t, int64(23), intents[0].ID)
	require.False(t, intents[0].Referenced)
	require.NotNil(t, intents[0].CleanupClaimedAt)
	require.Equal(t, claimedAt, *intents[0].CleanupClaimedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompleteLibraryUploadIntentRequiresOwnedRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(`DELETE FROM image_library_upload_intents`).
		WithArgs(int64(31)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	repo := requireImageLibraryRepository(t, db)
	require.Error(t, repo.CompleteLibraryUploadIntent(context.Background(), 31))
	require.NoError(t, mock.ExpectationsWereMet())
}
