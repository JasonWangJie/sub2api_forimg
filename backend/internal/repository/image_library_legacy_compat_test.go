package repository

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDeleteLegacyPlazaForUserWithdrawsAndSoftDeletesInOneTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT i.id.*LEFT JOIN image_plaza_publications.*i.user_id=\$1.*p.public_id=\$2.*FOR UPDATE OF i`).
		WithArgs(int64(42), "imgpub_public", "").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(17)))
	mock.ExpectQuery(`(?s)SELECT EXISTS\(.*status='admin_hidden'`).
		WithArgs(int64(17)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec(`(?s)UPDATE image_library_items.*deleted_at=NOW\(\).*WHERE id=\$1 AND user_id=\$2`).
		WithArgs(int64(17), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)UPDATE image_plaza_publications.*status='withdrawn'.*WHERE library_item_id=\$1`).
		WithArgs(int64(17)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO image_library_events").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO image_library_outbox").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := NewImageLibraryRepository(db).(*imageLibraryRepository)
	found, err := repo.DeleteLegacyPlazaForUser(context.Background(), 42, "imgpub_public", "")
	require.NoError(t, err)
	require.True(t, found)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteLegacyPlazaForUserHidesCrossUserAndMissingRecords(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT i.id.*i.user_id=\$1`).
		WithArgs(int64(42), "7", "legacy-image-plaza:7").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()

	repo := NewImageLibraryRepository(db).(*imageLibraryRepository)
	found, err := repo.DeleteLegacyPlazaForUser(context.Background(), 42, "7", "legacy-image-plaza:7")
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteLegacyPlazaIdentifierFallsBackOnlyForUnmigratedNumericID(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT i.id.*i.user_id=\$1`).
		WithArgs(int64(42), "7", "legacy-image-plaza:7").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT i.id.*i.user_id=\$1`).
		WithArgs(int64(42), "img_missing", "").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()

	library := service.NewImageLibraryService(NewImageLibraryRepository(db), nil, nil)
	handled, err := library.DeleteLegacyPlazaIdentifier(context.Background(), 42, "7")
	require.NoError(t, err)
	require.False(t, handled)
	handled, err = library.DeleteLegacyPlazaIdentifier(context.Background(), 42, "img_missing")
	require.True(t, handled)
	require.ErrorIs(t, err, service.ErrImageLibraryNotFound)
	handled, err = library.DeleteLegacyPlazaIdentifier(context.Background(), 42, "../../7")
	require.True(t, handled)
	require.ErrorIs(t, err, service.ErrImageLibraryNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
