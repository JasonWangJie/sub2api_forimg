package repository

import (
	"context"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountClearRateLimitPreservesPersistentSchedulingAndNotifiesOutbox(t *testing.T) {
	var updateSQL string
	matcher := sqlmock.QueryMatcherFunc(func(expected, actual string) error {
		if strings.HasPrefix(actual, `UPDATE "accounts"`) {
			updateSQL = actual
		}
		return sqlmock.QueryMatcherRegexp.Match(expected, actual)
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := newAccountRepositoryWithSQL(client, db, nil)

	mock.ExpectExec(`UPDATE "accounts"`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO scheduler_outbox`).
		WithArgs(service.SchedulerOutboxEventAccountChanged, int64(42), nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, repo.ClearRateLimit(context.Background(), 42))
	require.NoError(t, mock.ExpectationsWereMet())

	setClause, _, found := strings.Cut(updateSQL, " WHERE ")
	require.True(t, found)
	for _, field := range []string{"rate_limited_at", "rate_limit_reset_at", "overload_until"} {
		require.Contains(t, setClause, `"`+field+`" = NULL`)
	}
	for _, field := range []string{"schedulable", "status", "error_message", "expires_at", "auto_pause_on_expired"} {
		require.NotContains(t, setClause, `"`+field+`"`, "manual cooldown reset must preserve %s", field)
	}
}
