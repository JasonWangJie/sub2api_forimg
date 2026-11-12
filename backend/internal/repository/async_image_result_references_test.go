package repository

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBindResultObjectReferencesRejectsUnavailableObject(t *testing.T) {
	for _, tc := range []struct {
		name, state, identity string
		available             bool
	}{
		{"active", "active", "identity", true}, {"legacy", "active", "", true}, {"deleting", "deleting", "identity", false}, {"deleted", "deleted", "identity", false}, {"other_storage", "active", "other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			mock.ExpectBegin()
			mock.ExpectQuery("(?s)SELECT id,state,storage_identity.*FOR UPDATE").WithArgs("custom_s3", "images", "images/results/a.png").WillReturnRows(sqlmock.NewRows([]string{"id", "state", "identity"}).AddRow(42, tc.state, tc.identity))
			if tc.available {
				mock.ExpectExec("INSERT INTO async_image_task_result_references").WithArgs("task", int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			mock.ExpectRollback()
			tx, err := db.Begin()
			require.NoError(t, err)
			err = bindResultObjectReferences(context.Background(), tx, "task", []service.ObjectRef{{Provider: "custom_s3", Bucket: "images", ObjectKey: "images/results/a.png", StorageIdentity: "identity"}})
			if tc.available {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, service.ErrAsyncImageInvalidInput)
			}
			_ = tx.Rollback()
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestBackfillResultReferencesWaitsForLockedUninspectedTasks(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery("(?s)SELECT task_id,reference_image_urls.*result_references_checked=FALSE.*LIMIT.*FOR UPDATE SKIP LOCKED").WithArgs(50).WillReturnRows(sqlmock.NewRows([]string{"task_id", "reference_image_urls"}))
	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectCommit()
	done, err := backfillAsyncImageResultReferences(context.Background(), db, service.ImageResultReferenceConfig{}, 50)
	require.NoError(t, err)
	require.False(t, done)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBackfillResultReferencesFailsClosedForMissingOwnResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery("(?s)SELECT task_id,reference_image_urls.*FOR UPDATE SKIP LOCKED").WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"task_id", "reference_image_urls"}).AddRow("task", `["https://cdn.example/images/results/missing.png"]`))
	mock.ExpectQuery("(?s)SELECT id,state,storage_identity.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "state", "identity"}))
	mock.ExpectRollback()
	done, err := backfillAsyncImageResultReferences(context.Background(), db, service.ImageResultReferenceConfig{Provider: "custom_s3", Bucket: "images", URLBases: []string{"https://cdn.example"}, ResultPrefix: "images/results/"}, 1)
	require.Error(t, err)
	require.False(t, done)
	require.NoError(t, mock.ExpectationsWereMet())
}
