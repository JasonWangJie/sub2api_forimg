package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestImageLibraryCleanupWhereUsesBoundParameters(t *testing.T) {
	before := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	filters, err := json.Marshal(map[string]any{"before": before, "user_id": 42})
	require.NoError(t, err)

	where, args, err := imageLibraryCleanupWhere("expired", filters)
	require.NoError(t, err)
	require.Equal(t, "i.deleted_at IS NULL AND i.expires_at<=$1", where)
	require.Equal(t, []any{before}, args)

	where, args, err = imageLibraryCleanupWhere("user", filters)
	require.NoError(t, err)
	require.Equal(t, "i.user_id=$2", where)
	require.Equal(t, []any{before, int64(42)}, args)
}

func TestImageLibraryCleanupWhereRejectsInvalidUserScope(t *testing.T) {
	_, _, err := imageLibraryCleanupWhere("user", json.RawMessage(`{"user_id":0}`))
	require.Error(t, err)
	_, _, err = imageLibraryCleanupWhere("unknown", json.RawMessage(`{}`))
	require.Error(t, err)
}

func TestPrepareExpiredCleanupBatchMarksPublicationExpiredAndAppendsEvent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT TRUE FROM image_library_cleanup_jobs.*FOR UPDATE`).
		WithArgs(int64(9), int64(4)).
		WillReturnRows(sqlmock.NewRows([]string{"alive"}).AddRow(true))
	mock.ExpectQuery(`(?s)SELECT i.id,i.storage_object_id FROM image_library_items.*FOR UPDATE SKIP LOCKED`).
		WithArgs(100).
		WillReturnRows(sqlmock.NewRows([]string{"id", "storage_object_id"}).AddRow(int64(17), int64(23)))
	mock.ExpectQuery(`(?s)SELECT id,library_item_id,status FROM image_plaza_publications.*FOR UPDATE`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "library_item_id", "status"}).AddRow(int64(29), int64(17), service.ImagePublicationPublished))
	mock.ExpectExec(`(?s)UPDATE image_plaza_publications SET status='expired'.*status IN \('pending_review','published','admin_hidden'\)`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO image_library_events").
		WithArgs(int64(17), int64(29), nil, "publication.expired", service.ImagePublicationPublished, service.ImagePublicationExpired, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`(?s)UPDATE image_library_items SET deleted_at=.*purged_at=NOW`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)UPDATE image_storage_objects o SET state='deleting'.*RETURNING`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"provider", "bucket", "object_key", "content_type", "byte_size", "checksum_sha256", "width", "height"}))
	mock.ExpectExec(`(?s)UPDATE image_library_cleanup_jobs.*scanned_count=scanned_count\+\$3`).
		WithArgs(int64(9), int64(4), 1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := NewImageLibraryRepository(db).(*imageLibraryRepository)
	batch, err := repo.PrepareCleanupBatch(context.Background(), 9, 4, "expired", json.RawMessage(`{}`), 100)
	require.NoError(t, err)
	require.Equal(t, int64(1), batch.MatchedItems)
	require.True(t, batch.Done)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPrepareOutboxCleanupProtectsActivePublicationObject(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT storage_object_id FROM image_library_items WHERE id=\$1 FOR UPDATE`).
		WithArgs(int64(17)).
		WillReturnRows(sqlmock.NewRows([]string{"storage_object_id"}).AddRow(int64(23)))
	mock.ExpectExec(`UPDATE image_library_items SET deleted_at=COALESCE`).
		WithArgs(int64(17)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)UPDATE image_storage_objects o SET state='deleting'.*image_plaza_publications p.*status IN \('pending_review','published','admin_hidden'\).*RETURNING`).
		WithArgs(int64(23)).
		WillReturnRows(sqlmock.NewRows([]string{"provider", "bucket", "object_key", "content_type", "byte_size", "checksum_sha256", "width", "height"}))
	mock.ExpectCommit()

	repo := NewImageLibraryRepository(db).(*imageLibraryRepository)
	objects, err := repo.PrepareOutboxCleanup(context.Background(), 17)
	require.NoError(t, err)
	require.Empty(t, objects)
	require.NoError(t, mock.ExpectationsWereMet())
}
