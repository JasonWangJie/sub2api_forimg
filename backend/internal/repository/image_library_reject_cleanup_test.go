package repository

import (
	"context"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestTransitionPublicationRejectDeletesRealtimeImportAndEnqueuesCleanup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	retention := time.Now().UTC().AddDate(0, 0, 90)
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id,library_item_id,status FROM image_plaza_publications WHERE public_id=\$1 FOR UPDATE`).
		WithArgs("imgpub_1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "library_item_id", "status"}).
			AddRow(int64(9), int64(17), service.ImagePublicationPending))
	mock.ExpectExec(`(?s)UPDATE image_plaza_publications SET status=\$2::text,moderation_status=\$3`).
		WithArgs(int64(9), service.ImagePublicationRejected, "rejected", int64(1), "nsfw", retention).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE image_library_items SET visibility=\$2::text`).
		WithArgs(int64(17), service.ImageLibraryVisibilityPrivate, retention).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO image_library_events`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(`SELECT source_type FROM image_library_items WHERE id=\$1`).
		WithArgs(int64(17)).
		WillReturnRows(sqlmock.NewRows([]string{"source_type"}).AddRow("realtime_import"))
	mock.ExpectQuery(`SELECT user_id FROM image_library_items WHERE id=\$1`).
		WithArgs(int64(17)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(int64(42)))
	mock.ExpectExec(`UPDATE image_library_items SET deleted_at=NOW\(\)`).
		WithArgs(int64(17)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE image_plaza_publications SET status='withdrawn'`).
		WithArgs(int64(17)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO image_library_events`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO image_library_outbox`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(`(?s)SELECT p.id,p.public_id,p.library_item_id,i.asset_id`).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "public_id", "library_item_id", "asset_id", "user_id", "status", "public_title", "public_prompt",
			"share_prompt", "moderation_status", "review_reason", "published_at", "reviewed_at", "expires_at", "created_at", "updated_at",
		}).AddRow(
			int64(9), "imgpub_1", int64(17), "img_1", int64(42), service.ImagePublicationRejected, "title", nil,
			false, "rejected", "nsfw", nil, now, retention, now, now,
		))
	mock.ExpectCommit()

	repo := NewImageLibraryRepository(db).(*imageLibraryRepository)
	publication, err := repo.TransitionPublication(context.Background(), 1, "imgpub_1", "reject", "nsfw", retention)
	require.NoError(t, err)
	require.Equal(t, service.ImagePublicationRejected, publication.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTransitionPublicationRejectKeepsAsyncTaskLibraryItem(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	retention := time.Now().UTC().AddDate(0, 0, 90)
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id,library_item_id,status FROM image_plaza_publications WHERE public_id=\$1 FOR UPDATE`).
		WithArgs("imgpub_2").
		WillReturnRows(sqlmock.NewRows([]string{"id", "library_item_id", "status"}).
			AddRow(int64(10), int64(18), service.ImagePublicationPending))
	mock.ExpectExec(`(?s)UPDATE image_plaza_publications SET status=\$2::text,moderation_status=\$3`).
		WithArgs(int64(10), service.ImagePublicationRejected, "rejected", int64(1), "bad", retention).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE image_library_items SET visibility=\$2::text`).
		WithArgs(int64(18), service.ImageLibraryVisibilityPrivate, retention).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO image_library_events`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(`SELECT source_type FROM image_library_items WHERE id=\$1`).
		WithArgs(int64(18)).
		WillReturnRows(sqlmock.NewRows([]string{"source_type"}).AddRow("async_task"))
	mock.ExpectQuery(`(?s)SELECT p.id,p.public_id,p.library_item_id,i.asset_id`).
		WithArgs(int64(10)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "public_id", "library_item_id", "asset_id", "user_id", "status", "public_title", "public_prompt",
			"share_prompt", "moderation_status", "review_reason", "published_at", "reviewed_at", "expires_at", "created_at", "updated_at",
		}).AddRow(
			int64(10), "imgpub_2", int64(18), "img_2", int64(42), service.ImagePublicationRejected, "title", nil,
			false, "rejected", "bad", nil, now, retention, now, now,
		))
	mock.ExpectCommit()

	repo := NewImageLibraryRepository(db).(*imageLibraryRepository)
	publication, err := repo.TransitionPublication(context.Background(), 1, "imgpub_2", "reject", "bad", retention)
	require.NoError(t, err)
	require.Equal(t, service.ImagePublicationRejected, publication.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}
