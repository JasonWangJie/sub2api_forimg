package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageResultFetchSourceAndTaskCommitTogether(t *testing.T) {
	for _, eventFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "event failure rolls back source"}[eventFailure], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			now := time.Now().UTC()
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT status, version.*FOR UPDATE`).WithArgs("fetch").WillReturnRows(sqlmock.NewRows([]string{"status", "version"}).AddRow("invoking", 1))
			mock.ExpectExec(`INSERT INTO async_image_result_fetches`).WithArgs("fetch", []byte("encrypted"), now.Add(time.Hour)).WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectQuery(`(?s)UPDATE async_image_tasks SET.*request_payload = CASE.*RETURNING`).WillReturnRows(asyncImageTaskRowsWithPayload(now, "fetch", "hash", "upstream_succeeded", []byte{}))
			event := mock.ExpectExec(`INSERT INTO async_image_events`)
			if eventFailure {
				event.WillReturnError(errors.New("database unavailable"))
				mock.ExpectRollback()
			} else {
				event.WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			}
			repo := NewAsyncImageTaskRepository(db).(service.AsyncImageResultFetchRepository)
			task, err := repo.SaveAsyncImageResultFetch(context.Background(), service.AsyncImageTaskTransition{TaskID: "fetch", ExpectedVersion: 1, ToStatus: "upstream_succeeded", ClearRequestPayload: true}, []byte("encrypted"), now.Add(time.Hour))
			if eventFailure {
				require.Error(t, err)
				require.Nil(t, task)
			} else {
				require.NoError(t, err)
				require.Empty(t, task.RequestPayload)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAsyncImageResultFetchBudgetIsReservedAtomicallyAndCannotReset(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewAsyncImageTaskRepository(db).(service.AsyncImageResultFetchRepository)
	for count := 1; count <= 3; count++ {
		mock.ExpectQuery(`(?s)UPDATE async_image_result_fetches.*expires_at>NOW\(\).*<\$3.*status IN.*RETURNING`).WithArgs("fetch", "2", 3).WillReturnRows(sqlmock.NewRows([]string{"attempt"}).AddRow(count))
		n, err := repo.BeginAsyncImageResultFetchAttempt(context.Background(), "fetch", 2, 3)
		require.NoError(t, err)
		require.Equal(t, count, n)
	}
	mock.ExpectQuery(`UPDATE async_image_result_fetches`).WithArgs("fetch", "2", 3).WillReturnError(sql.ErrNoRows)
	_, err = repo.BeginAsyncImageResultFetchAttempt(context.Background(), "fetch", 2, 3)
	require.ErrorIs(t, err, service.ErrAsyncImageInvalidTransition)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAsyncImageResultFetchFinalizationRollsBackSourceDeletionOnCommitFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT status, version.*FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"status", "version"}).AddRow("upstream_succeeded", 2))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM async_image_result_fetches`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec(`DELETE FROM async_image_staging_objects`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO async_image_staging_objects`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(`(?s)UPDATE async_image_tasks SET.*billing_payload.*RETURNING`).WillReturnRows(asyncImageTaskRowsWithPayload(now, "fetch", "hash", "upstream_succeeded", []byte{}))
	mock.ExpectExec(`INSERT INTO async_image_events`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`DELETE FROM async_image_result_fetches`).WithArgs("fetch").WillReturnError(errors.New("transient database error"))
	mock.ExpectRollback()
	_, err = NewAsyncImageTaskRepository(db).RecordAsyncImageUpstreamSuccess(context.Background(), service.RecordAsyncImageUpstreamSuccessParams{TaskID: "fetch", ExpectedVersion: 2, AccountID: 7, ImageCount: 1, BillingRequestID: "client:async-image:fetch", BillingPayload: []byte(`{"amount":1}`), UpstreamSucceededAt: now, StagingObjects: []service.AsyncImageStagingObject{{ImageIndex: 0, Content: []byte("png"), ContentType: "image/png", ByteSize: 3, Checksum: "sum", ExpiresAt: now.Add(time.Hour)}}})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAsyncImageResultFetchCleanupRetainsExpiredActiveRecoveryPhase(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	mock.ExpectExec(`(?s)DELETE FROM async_image_result_fetches.*t.status IN \('succeeded','failed','execution_unknown','expired'\)`).WithArgs(now).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)WITH doomed AS.*DELETE FROM async_image_staging_objects`).WithArgs(now, 10).WillReturnResult(sqlmock.NewResult(0, 2))
	n, err := NewAsyncImageTaskRepository(db).DeleteExpiredAsyncImageStagingObjects(context.Background(), now, 10)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveAsyncImageFetchedObject(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows int64
		err  error
	}{
		{name: "saved", rows: 1},
		{name: "same object saved again", rows: 1},
		{name: "ineligible task or conflicting object", rows: 0, err: service.ErrAsyncImageInvalidTransition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			width, height := 2, 3
			o := service.AsyncImageStagingObject{TaskID: "fetch", ImageIndex: 0, Content: []byte("image"), ContentType: "image/png", ByteSize: 5, Checksum: "sha", Width: &width, Height: &height, ExpiresAt: time.Now().UTC().Add(time.Hour)}
			mock.ExpectExec(regexp.QuoteMeta(saveAsyncImageFetchedObjectSQL)).
				WithArgs(o.TaskID, o.ImageIndex, o.Content, o.ContentType, o.ByteSize, o.Checksum, width, height, o.ExpiresAt).
				WillReturnResult(sqlmock.NewResult(0, tc.rows))
			err = NewAsyncImageTaskRepository(db).(service.AsyncImageResultFetchRepository).SaveAsyncImageFetchedObject(context.Background(), o)
			require.ErrorIs(t, err, tc.err)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestSaveAsyncImageFetchedObjectPropagatesDatabaseErrors(t *testing.T) {
	for _, kind := range []string{"execute", "rows affected"} {
		t.Run(kind, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			failure := errors.New("temporary database failure")
			expect := mock.ExpectExec(regexp.QuoteMeta(saveAsyncImageFetchedObjectSQL))
			if kind == "execute" {
				expect.WillReturnError(failure)
			} else {
				expect.WillReturnResult(sqlmock.NewErrorResult(failure))
			}
			o := service.AsyncImageStagingObject{TaskID: "fetch", Content: []byte("image"), ContentType: "image/png", ByteSize: 5, Checksum: "sha", ExpiresAt: time.Now().Add(time.Hour)}
			err = NewAsyncImageTaskRepository(db).(service.AsyncImageResultFetchRepository).SaveAsyncImageFetchedObject(context.Background(), o)
			require.ErrorIs(t, err, failure)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestSaveAsyncImageFetchedObjectRejectsInvalidObjectBeforeSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	err = NewAsyncImageTaskRepository(db).(service.AsyncImageResultFetchRepository).SaveAsyncImageFetchedObject(context.Background(), service.AsyncImageStagingObject{TaskID: "fetch"})
	require.ErrorIs(t, err, service.ErrAsyncImageInvalidInput)
	require.NoError(t, mock.ExpectationsWereMet())
}
