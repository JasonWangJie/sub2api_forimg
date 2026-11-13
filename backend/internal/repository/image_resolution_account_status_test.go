package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestImageResolutionAccountStatusBatchSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewImageResolutionGuardRepository(db).(service.ImageResolutionGuardBatchRepository)
	items, err := repo.ListCooldownsBatch(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, items)
	until := time.Now().UTC().Add(time.Minute)
	query := `SELECT s.account_id.*WHERE s.account_id IN \(\$1,\$2\) AND s.cooldown_until>NOW\(\).*ORDER BY s.account_id,s.requested_model,s.size_tier`
	rows := sqlmock.NewRows([]string{"account_id", "requested_model", "size_tier", "cycle_id", "cooldown_until", "failure_count", "observation"}).
		AddRow(12, "gpt-image-2", "4K", "cycle-a", until, 1, []byte(`{"expectation":{"expected_width":3840,"expected_height":2160,"expected_pixels":8294400},"outputs":[{"width":1920,"height":1080}]}`)).
		AddRow(13, "gemini-3-pro-image-preview", "2K", "cycle-b", until, 1, nil)
	mock.ExpectQuery(query).WithArgs(int64(12), int64(13)).WillReturnRows(rows)
	items, err = repo.ListCooldownsBatch(context.Background(), []int64{12, 13})
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, 3840, items[0].RequestedWidth)
	require.Equal(t, 1080, items[0].ActualHeight)
	require.Equal(t, 25.0, items[0].AreaPercent)
	require.Equal(t, "image_resolution_downgrade", items[1].Reason)
	mock.ExpectQuery(query).WithArgs(int64(12), int64(13)).WillReturnError(errors.New("database unavailable"))
	items, err = repo.ListCooldownsBatch(context.Background(), []int64{12, 13})
	require.Error(t, err)
	require.Nil(t, items)
	require.NoError(t, mock.ExpectationsWereMet())
}
