package repository

import (
	"context"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestCreateAssetFromTaskLocksUnclaimedTaskAndResultAgainstRetention(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs(int64(186_000_000_042)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)SELECT id FROM image_library_items.*source_task_id=\$2.*source_result_index=\$3`).
		WithArgs(int64(42), "asyncimg_task", 0).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`(?s)FROM async_image_tasks t.*JOIN async_image_results r.*t.cleanup_claimed_at IS NULL.*r.cleanup_claimed_at IS NULL.*FOR UPDATE OF t,r`).
		WithArgs("asyncimg_task", int64(42), 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"user_id", "api_key_id", "group_id", "platform", "model", "requested_image_size",
			"actual_image_size", "aspect_ratio", "prompt_preview", "provider", "bucket",
			"object_key", "content_type", "byte_size", "checksum", "width", "height",
		}))
	mock.ExpectRollback()

	repo := NewImageLibraryRepository(db).(*imageLibraryRepository)
	_, reused, err := repo.CreateAssetFromTask(context.Background(), 42, "asyncimg_task", 0, nil, "", time.Now().UTC().Add(24*time.Hour), 1000, 5<<30)
	require.Error(t, err)
	require.False(t, reused)
	require.NoError(t, mock.ExpectationsWereMet())
}
