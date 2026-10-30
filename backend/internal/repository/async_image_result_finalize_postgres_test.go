//go:build postgres

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageResultFinalizationPreservesUpstreamTimeAndEventPostgres(t *testing.T) {
	db := openAsyncImagePostgresTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// The repository owns its transaction, so use connection-local temporary
	// tables. The helper limits the pool to one connection and closes it at exit.
	_, err := db.ExecContext(ctx, `SET search_path=pg_temp;
CREATE TEMP TABLE async_image_tasks (
 id bigserial PRIMARY KEY, task_id varchar(64) NOT NULL UNIQUE,
 user_id bigint NOT NULL DEFAULT 1, api_key_id bigint NOT NULL DEFAULT 1, group_id bigint NOT NULL DEFAULT 1, account_id bigint,
 account_attempts jsonb NOT NULL DEFAULT '[]', attempted_account_ids jsonb NOT NULL DEFAULT '[]',
 protocol text NOT NULL DEFAULT 'openai_images', platform text NOT NULL DEFAULT 'openai', request_type text NOT NULL DEFAULT 'text_to_image', model text NOT NULL DEFAULT 'gpt-image-2',
 status text NOT NULL, billing_status text NOT NULL DEFAULT 'pending', progress integer NOT NULL DEFAULT 0,
 requested_image_size text, actual_image_size text, aspect_ratio text, image_count integer NOT NULL DEFAULT 0, actual_cost numeric, currency text NOT NULL DEFAULT 'USD',
 idempotency_key text, request_hash text NOT NULL DEFAULT 'test', request_payload bytea NOT NULL DEFAULT ''::bytea, prompt_preview text, reference_image_urls jsonb NOT NULL DEFAULT '[]',
 upstream_request_id text, reconciliation_status text NOT NULL DEFAULT 'none', billing_request_id text, billing_payload jsonb,
 retry_count integer NOT NULL DEFAULT 0, reference_transport text, reference_retry_count integer NOT NULL DEFAULT 0, upstream_retry_count integer NOT NULL DEFAULT 0,
 capacity_retry_count integer NOT NULL DEFAULT 0, storage_retry_count integer NOT NULL DEFAULT 0, billing_retry_count integer NOT NULL DEFAULT 0, version bigint NOT NULL DEFAULT 2,
 error_code text, error_message text, submitted_at timestamptz NOT NULL DEFAULT now(), started_at timestamptz, upstream_succeeded_at timestamptz, finished_at timestamptz, expires_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TEMP TABLE async_image_result_fetches (task_id varchar(64) PRIMARY KEY);
CREATE TEMP TABLE async_image_staging_objects (
 task_id varchar(64) NOT NULL, image_index integer NOT NULL, content bytea NOT NULL, content_type text NOT NULL,
 byte_size bigint NOT NULL, checksum text NOT NULL, width integer, height integer, expires_at timestamptz NOT NULL
);
CREATE TEMP TABLE async_image_events (
 id bigserial PRIMARY KEY, task_id varchar(64) NOT NULL, event_type text NOT NULL, from_status text, to_status text,
 payload jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);`)
	require.NoError(t, err)
	repo := NewAsyncImageTaskRepository(db)
	firstSuccess := time.Date(2026, 10, 30, 5, 32, 23, 0, time.UTC)
	finalized := firstSuccess.Add(30 * time.Second)
	for _, status := range []string{service.AsyncImageTaskStatusInvoking, service.AsyncImageTaskStatusUpstreamSucceeded, service.AsyncImageTaskStatusUploading, service.AsyncImageTaskStatusStorageFailed} {
		t.Run(status, func(t *testing.T) {
			recovering := status != service.AsyncImageTaskStatusInvoking
			var upstreamAt *time.Time
			if recovering {
				upstreamAt = &firstSuccess
			}
			_, err := db.ExecContext(ctx, `INSERT INTO async_image_tasks(task_id,status,upstream_succeeded_at) VALUES($1,$2,$3)`, status, status, upstreamAt)
			require.NoError(t, err)
			if recovering {
				_, err = db.ExecContext(ctx, `INSERT INTO async_image_result_fetches(task_id) VALUES($1)`, status)
				require.NoError(t, err)
			}
			task, err := repo.RecordAsyncImageUpstreamSuccess(ctx, service.RecordAsyncImageUpstreamSuccessParams{
				TaskID: status, ExpectedVersion: 2, AccountID: 7, ImageCount: 1,
				BillingRequestID: "client:async-image:" + status, BillingPayload: []byte(`{"amount":0.09}`), UpstreamSucceededAt: finalized,
				StagingObjects: []service.AsyncImageStagingObject{{ImageIndex: 0, Content: []byte("png"), ContentType: "image/png", ByteSize: 3, Checksum: "sum", ExpiresAt: finalized.Add(time.Hour)}},
			})
			require.NoError(t, err)
			require.Equal(t, service.AsyncImageTaskStatusUpstreamSucceeded, task.Status)
			require.Equal(t, service.AsyncImageBillingStatusPrepared, task.BillingStatus)
			require.NotNil(t, task.UpstreamSucceededAt)
			expectedTime, expectedEvent := finalized, "upstream_succeeded"
			if recovering {
				expectedTime, expectedEvent = firstSuccess, "result_fetch_completed"
			}
			require.True(t, task.UpstreamSucceededAt.Equal(expectedTime))
			var eventCount, sourceCount, imageCount int
			var eventType string
			require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*),min(event_type) FROM async_image_events WHERE task_id=$1`, status).Scan(&eventCount, &eventType))
			require.Equal(t, 1, eventCount)
			require.Equal(t, expectedEvent, eventType)
			require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM async_image_result_fetches WHERE task_id=$1`, status).Scan(&sourceCount))
			require.Zero(t, sourceCount)
			require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM async_image_staging_objects WHERE task_id=$1`, status).Scan(&imageCount))
			require.Equal(t, 1, imageCount)
		})
	}
}
