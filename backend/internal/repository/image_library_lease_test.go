package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestClaimCleanupJobIssuesNewLeaseVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	mock.ExpectQuery(`(?s)UPDATE image_library_cleanup_jobs.*lease_version=lease_version\+1.*RETURNING.*lease_version`).
		WithArgs(now.Add(-2 * time.Minute)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "requested_by", "scope", "filters", "status", "lease_version",
			"scanned_count", "deleted_count", "deleted_bytes", "last_error", "created_at",
		}).AddRow(int64(9), nil, "expired", []byte(`{}`), "running", int64(4), int64(0), int64(0), int64(0), nil, now))

	repo := NewImageLibraryRepository(db).(*imageLibraryRepository)
	job, err := repo.ClaimCleanupJob(context.Background(), now.Add(-2*time.Minute))
	require.NoError(t, err)
	require.Equal(t, int64(4), job.LeaseVersion)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupJobHeartbeatAndFinishAreFenced(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(`(?s)UPDATE image_library_cleanup_jobs SET updated_at=NOW\(\).*lease_version=\$2`).
		WithArgs(int64(9), int64(4)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`(?s)UPDATE image_library_cleanup_jobs SET status=\$3.*lease_version=\$2`).
		WithArgs(int64(9), int64(4), "succeeded", nil).
		WillReturnResult(sqlmock.NewResult(0, 0))

	repo := NewImageLibraryRepository(db).(*imageLibraryRepository)
	alive, err := repo.HeartbeatCleanupJob(context.Background(), 9, 4)
	require.NoError(t, err)
	require.False(t, alive)
	err = repo.FinishCleanupJob(context.Background(), 9, 4, "succeeded", "")
	require.ErrorIs(t, err, service.ErrImageLibraryLeaseLost)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOutboxCompletionUsesAttemptsAsFencingToken(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(`(?s)UPDATE image_library_outbox SET completed_at=NOW\(\).*attempts=\$2`).
		WithArgs(int64(12), 3).
		WillReturnResult(sqlmock.NewResult(0, 0))

	repo := NewImageLibraryRepository(db).(*imageLibraryRepository)
	err = repo.CompleteLibraryOutbox(context.Background(), 12, 3)
	require.ErrorIs(t, err, service.ErrImageLibraryLeaseLost)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClaimStaleCleanupObjectsReactivatesReferencedObjects(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	staleBefore := time.Now().UTC().Add(-2 * time.Minute)
	mock.ExpectQuery("(?s)WITH reactivated AS.*state='active'.*image_library_items.*image_plaza_publications.*async_image_results.*UPDATE image_storage_objects o SET deletion_claimed_at=NOW").
		WithArgs(staleBefore, 25).
		WillReturnRows(sqlmock.NewRows([]string{
			"provider", "bucket", "object_key", "content_type", "byte_size", "checksum_sha256", "width", "height",
		}).AddRow("aliyun", "images", "library/orphan.png", "image/png", int64(123), "checksum", 10, 20))

	repo := NewImageLibraryRepository(db).(*imageLibraryRepository)
	objects, err := repo.ClaimStaleCleanupObjects(context.Background(), staleBefore, 25)
	require.NoError(t, err)
	require.Len(t, objects, 1)
	require.Equal(t, "library/orphan.png", objects[0].ObjectKey)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompleteCleanupObjectRollsBackForRecoverableRetryWhenJobUpdateFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ref := service.ObjectRef{Provider: "aliyun", Bucket: "images", ObjectKey: "library/orphan.png", SizeBytes: 123}
	mock.ExpectBegin()
	mock.ExpectExec("(?s)UPDATE image_storage_objects SET state='deleted'.*state='deleting'").
		WithArgs(ref.Provider, ref.Bucket, ref.ObjectKey).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)UPDATE image_library_cleanup_jobs SET deleted_bytes=deleted_bytes\+\$3`).
		WithArgs(int64(9), int64(4), ref.SizeBytes).
		WillReturnError(errors.New("database unavailable"))
	mock.ExpectRollback()

	repo := NewImageLibraryRepository(db).(*imageLibraryRepository)
	err = repo.CompleteCleanupObject(context.Background(), 9, 4, ref)
	require.EqualError(t, err, "database unavailable")
	require.NoError(t, mock.ExpectationsWereMet())
}
