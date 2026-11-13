//go:build postgres

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func rateLimitAtomicPostgresDB(t *testing.T) *sql.DB {
	control := openAsyncImagePostgresTestDB(t)
	schema := fmt.Sprintf("rate_limit_atomic_%d", time.Now().UnixNano())
	_, err := control.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := control.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		require.NoError(t, err)
	})
	u, err := url.Parse(os.Getenv("SUB2API_TEST_POSTGRES_DSN"))
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`CREATE TABLE accounts(id bigint PRIMARY KEY,status text,schedulable boolean,rate_limited_at timestamptz,rate_limit_reset_at timestamptz,overload_until timestamptz,temp_unschedulable_until timestamptz,temp_unschedulable_reason text,extra jsonb,credentials jsonb,expires_at timestamptz,updated_at timestamptz,deleted_at timestamptz);
 CREATE TABLE scheduler_outbox(id bigserial PRIMARY KEY,event_type text,account_id bigint,group_id bigint,payload jsonb,dedup_key text);
 CREATE UNIQUE INDEX ON scheduler_outbox(dedup_key) WHERE dedup_key IS NOT NULL;
 INSERT INTO accounts VALUES(42,'error',false,now(),now()+interval '1 hour',now()+interval '2 hours',now()+interval '3 hours','internal-only',jsonb_build_object('model_rate_limits',jsonb_build_object('gemini',jsonb_build_object('rate_limit_reset_at',now()+interval '4 hours')),'antigravity_quota_scopes',jsonb_build_object('gemini',true),'keep',17),'{"api_key":"must-preserve"}',now()+interval '1 day',now(),NULL);`)
	require.NoError(t, err)
	return db
}
func TestAccountRateLimitAtomicPostgres(t *testing.T) {
	t.Run("single_commit_preserves_configuration", func(t *testing.T) {
		db := rateLimitAtomicPostgresDB(t)
		repo := &accountRepository{sql: db}
		ctx := context.Background()
		before, err := repo.GetAccountRateLimitState(ctx, 42)
		require.NoError(t, err)
		actual, after, err := repo.ClearAccountRateLimitState(ctx, 42, before.Version())
		require.NoError(t, err)
		require.Equal(t, before.Version(), actual.Version())
		require.Empty(t, after.Preview(time.Now()).Restrictions)
		current, err := repo.GetAccountRateLimitState(ctx, 42)
		require.NoError(t, err)
		require.Equal(t, after.Version(), current.Version())
		var status, credentials string
		var enabled bool
		var keep, count int
		var expiry time.Time
		require.NoError(t, db.QueryRow("SELECT status,schedulable,credentials->>'api_key',(extra->>'keep')::int,expires_at FROM accounts WHERE id=42").Scan(&status, &enabled, &credentials, &keep, &expiry))
		require.Equal(t, "error", status)
		require.False(t, enabled)
		require.Equal(t, "must-preserve", credentials)
		require.Equal(t, 17, keep)
		require.True(t, expiry.After(time.Now()))
		require.NoError(t, db.QueryRow("SELECT count(*) FROM scheduler_outbox").Scan(&count))
		require.Equal(t, 1, count)
		_, _, err = repo.ClearAccountRateLimitState(ctx, 42, before.Version())
		require.ErrorIs(t, err, service.ErrRateLimitStateChanged)
	})
	t.Run("outbox_error_rolls_back_every_restriction", func(t *testing.T) {
		db := rateLimitAtomicPostgresDB(t)
		repo := &accountRepository{sql: db}
		ctx := context.Background()
		before, err := repo.GetAccountRateLimitState(ctx, 42)
		require.NoError(t, err)
		_, err = db.Exec("ALTER TABLE scheduler_outbox ADD CONSTRAINT injected_failure CHECK (account_id < 0)")
		require.NoError(t, err)
		_, _, err = repo.ClearAccountRateLimitState(ctx, 42, before.Version())
		require.Error(t, err)
		current, err := repo.GetAccountRateLimitState(ctx, 42)
		require.NoError(t, err)
		require.Equal(t, before.Version(), current.Version())
		_, err = db.Exec("ALTER TABLE scheduler_outbox DROP CONSTRAINT injected_failure")
		require.NoError(t, err)
		_, _, err = repo.ClearAccountRateLimitState(ctx, 42, before.Version())
		require.NoError(t, err)
	})
	t.Run("new_limit_while_waiting_on_lock_is_not_cleared", func(t *testing.T) {
		db := rateLimitAtomicPostgresDB(t)
		repo := &accountRepository{sql: db}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		before, err := repo.GetAccountRateLimitState(ctx, 42)
		require.NoError(t, err)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, "UPDATE accounts SET rate_limit_reset_at=rate_limit_reset_at+interval '5 hours' WHERE id=42")
		require.NoError(t, err)
		result := make(chan error, 1)
		go func() { _, _, err := repo.ClearAccountRateLimitState(ctx, 42, before.Version()); result <- err }()
		select {
		case err := <-result:
			t.Fatalf("clear did not wait for locked row: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		require.NoError(t, tx.Commit())
		require.ErrorIs(t, <-result, service.ErrRateLimitStateChanged)
		current, err := repo.GetAccountRateLimitState(ctx, 42)
		require.NoError(t, err)
		require.True(t, current.RateLimitResetAt.After(*before.RateLimitResetAt))
		require.NotNil(t, current.OverloadUntil)
	})
	t.Run("two_admins_same_preview_only_one_commits", func(t *testing.T) {
		db := rateLimitAtomicPostgresDB(t)
		repo := &accountRepository{sql: db}
		ctx := context.Background()
		before, err := repo.GetAccountRateLimitState(ctx, 42)
		require.NoError(t, err)
		result := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() { _, _, err := repo.ClearAccountRateLimitState(ctx, 42, before.Version()); result <- err }()
		}
		first, second := <-result, <-result
		if first == nil {
			require.ErrorIs(t, second, service.ErrRateLimitStateChanged)
		} else {
			require.ErrorIs(t, first, service.ErrRateLimitStateChanged)
			require.NoError(t, second)
		}
	})
}
