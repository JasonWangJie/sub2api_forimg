package repository

import (
	"context"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestListPublishedAppliesFiltersOffsetAndOldestCursorDirection(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	cursorTime := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`(?s)SELECT COUNT\(\*\).*i\.platform=\$1.*i\.model=\$2.*i\.aspect_ratio=\$3.*p\.public_title ILIKE \$4`).
		WithArgs("gemini", "gemini-image", "16:9", "%city%").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(13)))
	mock.ExpectQuery(`(?s)SELECT p\.id.*\(p\.published_at,p\.id\)>\(\$6,\$7\).*ORDER BY p\.published_at ASC,p\.id ASC LIMIT \$8`).
		WithArgs(int64(42), "gemini", "gemini-image", "16:9", "%city%", cursorTime, int64(9), 2).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	repo := NewImageLibraryRepository(db).(*imageLibraryRepository)
	result, err := repo.ListPublished(context.Background(), 42, service.ImagePublicationListParams{
		Platform: "gemini", Model: "gemini-image", AspectRatio: "16:9", Query: "city",
		Sort: "oldest", Cursor: &service.ImageLibraryCursor{CreatedAt: cursorTime, ID: 9}, Limit: 2,
	})

	require.NoError(t, err)
	require.Equal(t, int64(13), result.Total)
	require.Empty(t, result.Items)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListPublicationsAdminUsesCreatedAtForOldestCursorAndAllFilters(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	cursorTime := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`(?s)SELECT p\.id.*\(p\.created_at,p\.id\)>\(\$5,\$6\).*p\.status=\$7.*ORDER BY p\.created_at ASC,p\.id ASC LIMIT \$8`).
		WithArgs("openai", "gpt-image-2", "1:1", "%portrait%", cursorTime, int64(21), service.ImagePublicationPending, 3).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	repo := NewImageLibraryRepository(db).(*imageLibraryRepository)
	items, err := repo.ListPublicationsAdmin(context.Background(), service.ImagePublicationListParams{
		Status: service.ImagePublicationPending, Platform: "openai", Model: "gpt-image-2",
		AspectRatio: "1:1", Query: "portrait", Sort: "oldest",
		Cursor: &service.ImageLibraryCursor{CreatedAt: cursorTime, ID: 21}, Limit: 3,
	})

	require.NoError(t, err)
	require.Empty(t, items)
	require.NoError(t, mock.ExpectationsWereMet())
}
