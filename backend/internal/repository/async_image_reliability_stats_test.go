package repository

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageReliabilityStatsKeepsFiltersAndNullableTimings(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	uid := int64(42)
	columns := []string{"total", "unknown", "expired", "admin", "tracked", "first", "calls", "selected", "switched", "switch_ok", "local", "local_ok", "fetch", "fetch_ok", "unknown_events", "p95", "p99", "queue", "generation", "postprocess", "oldest", "cost"}
	mock.ExpectQuery(`(?s)WITH tasks AS .*FROM async_image_tasks WHERE user_id = \$1 AND platform = \$2\).*FROM async_image_events e JOIN tasks t.*GROUP BY e.task_id.*LEFT JOIN events e.*FROM facts`).WithArgs(uid, "openai").WillReturnRows(sqlmock.NewRows(columns).AddRow(10, 1, 3, 2, 4, 3, 8, 9, 2, 1, 2, 1, 1, 1, 2, 1250.0, 1800.0, nil, 1000.0, 250.0, nil, 0.12))
	stats, err := NewAsyncImageTaskRepository(db).(service.AsyncImageReliabilityStatsRepository).AsyncImageReliabilityStats(context.Background(), service.AsyncImageTaskFilter{UserID: &uid, Platform: "openai"})
	require.NoError(t, err)
	require.Equal(t, int64(8), stats.HTTPInvocations)
	require.Equal(t, int64(9), stats.AccountSelections)
	require.Equal(t, int64(3), stats.Expired)
	require.Equal(t, 1250.0, *stats.P95DurationMS)
	require.Nil(t, stats.P95QueueMS)
	require.Nil(t, stats.OldestQueuedAgeMS)
	require.InDelta(t, 0.12, stats.ActualCost, 0.00001)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAsyncImageDurationAllowsUnfilteredAdminStats(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery(`(?s)FROM async_image_tasks WHERE TRUE AND status = 'succeeded' AND finished_at IS NOT NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"avg"}).AddRow(nil))
	average, err := NewAsyncImageTaskRepository(db).(service.AsyncImageTaskDurationStatsRepository).
		AverageCompletedAsyncImageTaskDuration(context.Background(), service.AsyncImageTaskFilter{})
	require.NoError(t, err)
	require.Nil(t, average)
	require.NoError(t, mock.ExpectationsWereMet())
}
