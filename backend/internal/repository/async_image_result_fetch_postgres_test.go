//go:build postgres

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// This opt-in suite works without the Docker integration harness. It accepts
// only an explicit loopback PostgreSQL URL, and shadows the production table
// names with transaction-local temporary tables. No persistent rows are used.
func openAsyncImagePostgresTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("SUB2API_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("set SUB2API_TEST_POSTGRES_DSN to an isolated local PostgreSQL URL")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		t.Fatal("SUB2API_TEST_POSTGRES_DSN must be a loopback PostgreSQL URL")
	}
	// lib/pq also accepts host overrides in the query string. Keep the URL's
	// checked host authoritative so a malformed test DSN cannot reach production.
	if u.Query().Has("host") || u.Query().Has("hostaddr") || u.Query().Has("service") || u.Query().Has("servicefile") {
		t.Fatal("PostgreSQL test URL must not override its host or service")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	return db
}

func TestSaveAsyncImageFetchedObjectPostgres(t *testing.T) {
	db := openAsyncImagePostgresTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `CREATE TEMP TABLE async_image_tasks (
 task_id varchar(64) PRIMARY KEY, status varchar(32) NOT NULL
) ON COMMIT DROP`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `CREATE TEMP TABLE async_image_staging_objects (
 id bigserial PRIMARY KEY,
 task_id varchar(64) NOT NULL REFERENCES async_image_tasks(task_id) ON DELETE CASCADE,
 image_index integer NOT NULL CHECK(image_index >= 0),
 content bytea NOT NULL, content_type varchar(100) NOT NULL,
 byte_size bigint NOT NULL CHECK(byte_size >= 0), checksum varchar(128) NOT NULL,
 width integer CHECK(width IS NULL OR width > 0), height integer CHECK(height IS NULL OR height > 0),
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL,
 UNIQUE(task_id,image_index)
) ON COMMIT DROP`)
	require.NoError(t, err)
	repo := &asyncImageTaskRepository{db: db, sql: tx}
	width, height := 3, 2
	content := []byte{0, 255, 128, 1, 2, 3}
	sum := sha256.Sum256(content)
	o := service.AsyncImageStagingObject{TaskID: "fetch", ImageIndex: 0, Content: content, ContentType: "image/png", ByteSize: int64(len(content)), Checksum: hex.EncodeToString(sum[:]), Width: &width, Height: &height, ExpiresAt: time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)}
	_, err = tx.ExecContext(ctx, `INSERT INTO async_image_tasks(task_id,status) VALUES($1,$2)`, o.TaskID, service.AsyncImageTaskStatusUpstreamSucceeded)
	require.NoError(t, err)

	// This executes the real lib/pq parameter binding and PostgreSQL parser.
	require.NoError(t, repo.SaveAsyncImageFetchedObject(ctx, o))
	objects, err := repo.ListAsyncImageStagingObjects(ctx, o.TaskID)
	require.NoError(t, err)
	require.Len(t, objects, 1)
	require.Equal(t, content, objects[0].Content)
	require.Equal(t, o.Checksum, objects[0].Checksum)
	require.Equal(t, width, *objects[0].Width)
	require.Equal(t, height, *objects[0].Height)
	require.True(t, o.ExpiresAt.Equal(objects[0].ExpiresAt))

	// Retrying the same object succeeds and keeps the first bytes and expiry.
	duplicate := o
	duplicate.ExpiresAt = o.ExpiresAt.Add(time.Hour)
	for _, status := range []string{service.AsyncImageTaskStatusUpstreamSucceeded, service.AsyncImageTaskStatusUploading, service.AsyncImageTaskStatusStorageFailed} {
		_, err = tx.ExecContext(ctx, `UPDATE async_image_tasks SET status=$2 WHERE task_id=$1`, o.TaskID, status)
		require.NoError(t, err)
		require.NoError(t, repo.SaveAsyncImageFetchedObject(ctx, duplicate))
	}
	objects, err = repo.ListAsyncImageStagingObjects(ctx, o.TaskID)
	require.NoError(t, err)
	require.Len(t, objects, 1)
	require.Equal(t, content, objects[0].Content)
	require.True(t, o.ExpiresAt.Equal(objects[0].ExpiresAt))

	// Nullable dimensions must retain their SQL types and remain idempotent.
	withoutDimensions := o
	withoutDimensions.ImageIndex = 1
	withoutDimensions.Width, withoutDimensions.Height = nil, nil
	require.NoError(t, repo.SaveAsyncImageFetchedObject(ctx, withoutDimensions))
	require.NoError(t, repo.SaveAsyncImageFetchedObject(ctx, withoutDimensions))
	objects, err = repo.ListAsyncImageStagingObjects(ctx, o.TaskID)
	require.NoError(t, err)
	require.Len(t, objects, 2)
	require.Nil(t, objects[1].Width)
	require.Nil(t, objects[1].Height)

	// A changed image must not silently replace the completed image.
	conflicting := o
	conflicting.Content = []byte("different image")
	conflicting.ByteSize = int64(len(conflicting.Content))
	conflicting.Checksum = "different-checksum"
	require.ErrorIs(t, repo.SaveAsyncImageFetchedObject(ctx, conflicting), service.ErrAsyncImageInvalidTransition)
	conflicting = o
	conflicting.Content = append([]byte(nil), o.Content...)
	conflicting.Content[0] = 1
	require.ErrorIs(t, repo.SaveAsyncImageFetchedObject(ctx, conflicting), service.ErrAsyncImageInvalidTransition, "even a supplied matching checksum cannot replace different bytes")
	objects, err = repo.ListAsyncImageStagingObjects(ctx, o.TaskID)
	require.NoError(t, err)
	require.Equal(t, content, objects[0].Content)

	// Even an exact duplicate cannot bypass a terminal/non-result task state.
	for _, status := range []string{service.AsyncImageTaskStatusQueued, service.AsyncImageTaskStatusInvoking, service.AsyncImageTaskStatusSucceeded, service.AsyncImageTaskStatusFailed, service.AsyncImageTaskStatusExecutionUnknown, service.AsyncImageTaskStatusExpired, service.AsyncImageTaskStatusBillingPending, service.AsyncImageTaskStatusBillingFailed} {
		_, err = tx.ExecContext(ctx, `UPDATE async_image_tasks SET status=$2 WHERE task_id=$1`, o.TaskID, status)
		require.NoError(t, err)
		require.ErrorIs(t, repo.SaveAsyncImageFetchedObject(ctx, o), service.ErrAsyncImageInvalidTransition, status)
	}
	missing := o
	missing.TaskID = "missing"
	require.ErrorIs(t, repo.SaveAsyncImageFetchedObject(ctx, missing), service.ErrAsyncImageInvalidTransition)
	objects, err = repo.ListAsyncImageStagingObjects(ctx, o.TaskID)
	require.NoError(t, err)
	require.Len(t, objects, 2)
}
