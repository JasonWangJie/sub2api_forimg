//go:build postgres

package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageRecoveryRetryCapsPostgres(t *testing.T) {
	db := openAsyncImagePostgresTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	createAsyncImageRecoveryPostgresTables(t, ctx, tx)
	repo := &asyncImageTaskRepository{db: db, sql: tx}
	before := time.Now().UTC().Truncate(time.Microsecond)
	const maxCounter = 1<<31 - 1 // Production retry columns are PostgreSQL INTEGER.
	for _, row := range []struct {
		id, status       string
		storage, billing int
		fresh            bool
	}{
		{id: "invoking", status: service.AsyncImageTaskStatusInvoking, storage: maxCounter, billing: maxCounter},
		{id: "queued", status: service.AsyncImageTaskStatusQueued, storage: maxCounter, billing: maxCounter},
		{id: "storage-below", status: service.AsyncImageTaskStatusStorageFailed, storage: 4},
		{id: "storage-at", status: service.AsyncImageTaskStatusStorageFailed, storage: 5},
		{id: "storage-max", status: service.AsyncImageTaskStatusStorageFailed, storage: maxCounter},
		{id: "billing-below", status: service.AsyncImageTaskStatusBillingFailed, billing: 9},
		{id: "billing-at", status: service.AsyncImageTaskStatusBillingFailed, billing: 10},
		{id: "billing-max", status: service.AsyncImageTaskStatusBillingFailed, billing: maxCounter},
		{id: "fresh-invoking", status: service.AsyncImageTaskStatusInvoking, fresh: true},
		{id: "terminal", status: service.AsyncImageTaskStatusFailed},
	} {
		_, err = insertAsyncImageTask(ctx, tx, service.CreateAsyncImageTaskParams{
			TaskID: row.id, UserID: 1, APIKeyID: 1, GroupID: 1,
			Protocol: service.AsyncImageProtocolBB, Platform: "openai",
			RequestType: service.AsyncImageRequestTypeTextToImage, Model: "test-model",
			ImageCount: 1, RequestHash: "hash", RequestPayload: []byte("encrypted"),
		})
		require.NoError(t, err)
		updated := before.Add(-time.Hour)
		if row.fresh {
			updated = before.Add(time.Minute)
		}
		_, err = tx.ExecContext(ctx, `UPDATE async_image_tasks SET status=$2,storage_retry_count=$3,billing_retry_count=$4,updated_at=$5 WHERE task_id=$1`, row.id, row.status, row.storage, row.billing, updated)
		require.NoError(t, err)
	}
	statuses := []string{service.AsyncImageTaskStatusInvoking, service.AsyncImageTaskStatusQueued, service.AsyncImageTaskStatusStorageFailed, service.AsyncImageTaskStatusBillingFailed}
	all := []string{"invoking", "queued", "storage-below", "storage-at", "storage-max", "billing-below", "billing-at", "billing-max"}
	for _, tc := range []struct {
		name             string
		storage, billing int
		want             []string
	}{
		{name: "zero caps do not overflow", want: all},
		{name: "negative caps remain unlimited", storage: -1, billing: -20, want: all},
		{name: "positive caps exclude exhausted rows", storage: 5, billing: 10, want: []string{"invoking", "queued", "storage-below", "billing-below"}},
		{name: "storage cap remains independent", storage: 5, want: []string{"invoking", "queued", "storage-below", "billing-below", "billing-at", "billing-max"}},
		{name: "billing cap remains independent", billing: 10, want: []string{"invoking", "queued", "storage-below", "storage-at", "storage-max", "billing-below"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tasks, err := repo.ListRecoverableAsyncImageTasks(ctx, statuses, before, tc.storage, tc.billing, 100)
			require.NoError(t, err)
			ids := make([]string, 0, len(tasks))
			for _, task := range tasks {
				ids = append(ids, task.TaskID)
			}
			require.ElementsMatch(t, tc.want, ids)
		})
	}
	if int64(int(^uint(0)>>1)) > maxCounter {
		t.Run("64 bit explicit caps do not overflow", func(t *testing.T) {
			wide := int(^uint(0) >> 1)
			tasks, err := repo.ListRecoverableAsyncImageTasks(ctx, statuses, before, wide, wide, 100)
			require.NoError(t, err)
			require.Len(t, tasks, len(all))
		})
	}
	// The actual stale-invoking call uses both zero caps and a narrow status list.
	tasks, err := repo.ListRecoverableAsyncImageTasks(ctx, []string{service.AsyncImageTaskStatusInvoking}, before, 0, 0, 100)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, "invoking", tasks[0].TaskID)
}

func createAsyncImageRecoveryPostgresTables(t *testing.T, ctx context.Context, tx *sql.Tx) {
	t.Helper()
	// Only temporary relations are visible; migration fragments cannot resolve
	// an existing application's tables even when the local test DB contains any.
	_, err := tx.ExecContext(ctx, `SET LOCAL search_path=pg_temp`)
	require.NoError(t, err)
	for _, table := range []string{"users", "api_keys", "groups", "accounts"} {
		_, err = tx.ExecContext(ctx, `CREATE TEMP TABLE `+table+` (id bigint PRIMARY KEY) ON COMMIT DROP`)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `INSERT INTO `+table+`(id) VALUES(1)`)
		require.NoError(t, err)
	}
	// Reuse the migration's real types/defaults and foreign keys rather than a
	// simplified int8 fixture, which would hide the int4 parameter regression.
	migration, err := migrations.FS.ReadFile("185_ZJ_async_image_tasks.sql")
	require.NoError(t, err)
	_, definition, found := strings.Cut(string(migration), "CREATE TABLE IF NOT EXISTS async_image_tasks (")
	require.True(t, found)
	definition, _, found = strings.Cut(definition, "\n);")
	require.True(t, found)
	_, err = tx.ExecContext(ctx, "CREATE TEMP TABLE async_image_tasks ("+definition+"\n) ON COMMIT DROP")
	require.NoError(t, err)
	for _, name := range []string{"223_ZJ_async_image_reference_retry_state.sql", "224_ZJ_async_image_account_attempts.sql", "226_ZJ_async_image_reference_urls.sql"} {
		migration, err = migrations.FS.ReadFile(name)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, string(migration))
		require.NoError(t, err)
	}
}
