package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestImageCapacitySnapshotScopeAndPrimaryReasons(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := newAccountRepositoryWithSQL(nil, db, nil)
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	rows := sqlmock.NewRows([]string{"id", "status", "schedulable", "platform", "deleted_at", "expires_at", "auto_pause_on_expired", "rate_limit_reset_at", "overload_until", "temp_unschedulable_until", "count"})
	rows.AddRow(12, "active", false, "gemini", nil, nil, true, future, nil, nil, 5)
	rows.AddRow(25, "active", true, "gemini", nil, nil, true, future, nil, nil, 5)
	rows.AddRow(39, "active", true, "gemini", nil, past, true, nil, nil, nil, 5)
	rows.AddRow(40, "active", true, "openai", nil, nil, true, nil, nil, nil, 5)
	rows.AddRow(41, "active", true, "gemini", nil, nil, true, nil, nil, nil, 5)
	mock.ExpectQuery(`SELECT a.id,a.status.*FROM accounts a WHERE EXISTS .*group_image_size_accounts.*p.model=\$2 AND p.size_tier=\$3.*LIMIT 256`).WithArgs(int64(2), "model", "2K").WillReturnRows(rows)
	result, err := repo.SnapshotImageAccountCapacity(context.Background(), 2, "model_resolution", "model", "2K", []string{service.PlatformGemini})
	require.NoError(t, err)
	require.Equal(t, 5, result.CandidateCount)
	require.False(t, result.CandidatesTruncated)
	reasons := map[string]service.ImageCapacityReason{}
	for _, r := range result.Reasons {
		reasons[r.Code] = r
	}
	for _, code := range []string{"scheduling_disabled", "rate_limited", "expired", "platform_mismatch", "eligible_at_snapshot"} {
		require.Equal(t, 1, reasons[code].Count)
	}
	require.NotNil(t, reasons["rate_limited"].EarliestRecoveryAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImageCapacitySnapshotDefaultAndTruncation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := newAccountRepositoryWithSQL(nil, db, nil)
	rows := sqlmock.NewRows([]string{"id", "status", "schedulable", "platform", "deleted_at", "expires_at", "auto_pause_on_expired", "rate_limit_reset_at", "overload_until", "temp_unschedulable_until", "count"}).AddRow(12, "active", false, "gemini", nil, nil, true, nil, nil, nil, 300)
	mock.ExpectQuery(`SELECT a.id,a.status.*FROM accounts a WHERE EXISTS .*account_groups.*LIMIT 256`).WithArgs(int64(2)).WillReturnRows(rows)
	result, err := repo.SnapshotImageAccountCapacity(context.Background(), 2, "default", "", "", nil)
	require.NoError(t, err)
	require.Equal(t, 300, result.CandidateCount)
	require.True(t, result.CandidatesTruncated)
	require.Equal(t, 1, result.Reasons[0].Count)
	require.NoError(t, mock.ExpectationsWereMet())
}
