package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestPruneCleanupJobHistoryRollsBackBookkeepingTogether(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	before := time.Now().UTC().Add(-24 * time.Hour)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT pg_try_advisory_xact_lock`).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectExec(`DELETE FROM image_result_cleanup_previews`).WithArgs(500).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)SELECT j.id FROM image_library_cleanup_jobs j.*FOR UPDATE OF j SKIP LOCKED`).
		WithArgs(before, 500).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(9)))
	mock.ExpectExec(`(?s)DELETE FROM image_result_cleanup_objects.*state NOT IN.*SKIP LOCKED`).WithArgs(sqlmock.AnyArg(), 5000).WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectExec(`DELETE FROM image_library_cleanup_jobs`).WithArgs(sqlmock.AnyArg()).WillReturnError(errors.New("simulated final delete failure"))
	mock.ExpectRollback()
	n, err := requireImageLibraryRepository(t, db).PruneCleanupJobHistory(context.Background(), before, 500)
	require.ErrorContains(t, err, "simulated final delete failure")
	require.Zero(t, n)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPruneCleanupJobHistoryDoesNotWaitForQueueOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT pg_try_advisory_xact_lock`).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(false))
	mock.ExpectRollback()
	n, err := requireImageLibraryRepository(t, db).PruneCleanupJobHistory(context.Background(), time.Now(), 500)
	require.NoError(t, err)
	require.Zero(t, n)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupHistoryListLimit(t *testing.T) {
	for _, resultOnly := range []bool{false, true} {
		for _, input := range []int{-1, 0, 1, 5, 30, 100} {
			t.Run(fmt.Sprintf("resultOnly=%t/limit=%d", resultOnly, input), func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				defer db.Close()
				want := input
				if input < 1 || input > 5 {
					want = 5
				}
				mock.ExpectQuery(`(?s)FROM image_library_cleanup_jobs j.*INTERVAL '24 hours'.*state IN \('deleting','retry'\).*ORDER BY j.id DESC LIMIT \$1`).
					WithArgs(want).WillReturnRows(sqlmock.NewRows([]string{"id"}))
				repo := requireImageLibraryRepository(t, db)
				if resultOnly {
					jobs, listErr := repo.ListImageResultCleanupJobs(context.Background(), input)
					require.NoError(t, listErr)
					require.Empty(t, jobs)
				} else {
					jobs, listErr := repo.ListCleanupJobs(context.Background(), input)
					require.NoError(t, listErr)
					require.Empty(t, jobs)
				}
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}
