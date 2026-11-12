package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestAllCleanupLeavesUploadRecoveryToRetention(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	// No database operation is allowed here: the administrator's broad age
	// filter is not authorization to remove active inputs or PUT recovery facts.
	repo := &imageLibraryRepository{db: db}
	batch, err := repo.prepareStorageResidueCleanupBatch(context.Background(), 7, 3, json.RawMessage(`{"before":"2099-01-01T00:00:00Z"}`), 100)
	require.NoError(t, err)
	require.True(t, batch.Done)
	require.Empty(t, batch.Objects)
	require.Zero(t, batch.MatchedItems)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAllCleanupPreviewDoesNotIncludeUploadInputs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("(?s)SELECT COUNT.*FROM image_library_items").WillReturnRows(sqlmock.NewRows([]string{"items", "bytes"}).AddRow(1, 10))
	mock.ExpectQuery("(?s)SELECT COUNT.*FROM async_image_results").WillReturnRows(sqlmock.NewRows([]string{"items", "bytes"}).AddRow(2, 20))
	mock.ExpectQuery("(?s)SELECT COUNT.*FROM image_storage_objects").WillReturnRows(sqlmock.NewRows([]string{"items", "bytes"}).AddRow(3, 30))
	preview, err := (&imageLibraryRepository{db: db}).PreviewCleanup(context.Background(), "all", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.EqualValues(t, 6, preview.MatchedItems)
	require.EqualValues(t, 60, preview.MatchedBytes)
	require.NoError(t, mock.ExpectationsWereMet())
}
