package repository

import (
	"context"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAsyncImagePreparedRequestCASAndEventAreAtomic(t *testing.T) {
	for _, scenario := range []string{"commit", "stale", "event failure"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			repo := NewAsyncImageTaskRepository(db).(service.AsyncImagePreparedRequestRepository)
			mock.ExpectBegin()
			n := int64(1)
			if scenario == "stale" {
				n = 0
			}
			mock.ExpectExec(`UPDATE async_image_tasks SET request_payload=\$3, updated_at=NOW\(\) WHERE task_id=\$1 AND version=\$2 AND status='invoking' AND reference_transport='local' AND request_type='image_to_image'`).WithArgs("task", int64(4), []byte("encrypted-only")).WillReturnResult(sqlmock.NewResult(0, n))
			if scenario != "stale" {
				event := mock.ExpectExec(`INSERT INTO async_image_events`)
				if scenario == "event failure" {
					event.WillReturnError(errors.New("database unavailable"))
				} else {
					event.WillReturnResult(sqlmock.NewResult(1, 1))
				}
			}
			if scenario == "commit" {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			err = repo.SaveAsyncImagePreparedRequest(context.Background(), "task", 4, []byte("encrypted-only"))
			if scenario == "commit" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			if scenario == "stale" {
				require.ErrorIs(t, err, service.ErrAsyncImageInvalidTransition)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAsyncImagePreparedRequestRejectsUnboundedPersistence(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewAsyncImageTaskRepository(db).(service.AsyncImagePreparedRequestRepository)
	err = repo.SaveAsyncImagePreparedRequest(context.Background(), "task", 1, make([]byte, service.AsyncImagePreparedRequestMaxBytes+1))
	require.ErrorIs(t, err, service.ErrAsyncImageInvalidInput)
	require.NoError(t, mock.ExpectationsWereMet(), "oversized ciphertext must never begin a database transaction")
}
