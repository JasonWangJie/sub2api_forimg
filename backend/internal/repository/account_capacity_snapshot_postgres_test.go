//go:build postgres

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestImageCapacitySnapshotPostgres(t *testing.T) {
	db := openAsyncImagePostgresTestDB(t)
	db.SetMaxOpenConns(1)
	_, err := db.Exec(`CREATE TEMP TABLE accounts(id bigint PRIMARY KEY,status text NOT NULL DEFAULT 'active',schedulable boolean NOT NULL DEFAULT true,platform text NOT NULL DEFAULT 'gemini',deleted_at timestamptz,expires_at timestamptz,auto_pause_on_expired boolean NOT NULL DEFAULT true,rate_limit_reset_at timestamptz,overload_until timestamptz,temp_unschedulable_until timestamptz);
CREATE TEMP TABLE account_groups(account_id bigint,group_id bigint);
CREATE TEMP TABLE group_image_size_accounts(account_id bigint,group_id bigint,model text,size_tier text);
INSERT INTO accounts(id,schedulable) VALUES(12,false),(25,true),(39,true);
UPDATE accounts SET rate_limit_reset_at=now()+interval '1 hour' WHERE id=39;
INSERT INTO account_groups VALUES(12,2);
INSERT INTO group_image_size_accounts VALUES(12,2,'model','2K'),(25,2,'model','2K'),(39,2,'model','2K');`)
	require.NoError(t, err)
	repo := &accountRepository{sql: db}
	ctx := context.Background()
	defaultPool, err := repo.SnapshotImageAccountCapacity(ctx, 2, "default", "", "", []string{service.PlatformGemini})
	require.NoError(t, err)
	require.Equal(t, 1, defaultPool.CandidateCount)
	require.Equal(t, "scheduling_disabled", defaultPool.Reasons[0].Code)
	exact, err := repo.SnapshotImageAccountCapacity(ctx, 2, "model_resolution", "model", "2K", []string{service.PlatformGemini})
	require.NoError(t, err)
	require.Equal(t, 3, exact.CandidateCount)
	reasons := map[string]service.ImageCapacityReason{}
	for _, r := range exact.Reasons {
		reasons[r.Code] = r
	}
	require.Equal(t, []int64{25}, reasons["eligible_at_snapshot"].AccountIDs, "an exact image pool may reference accounts outside account_groups")
	require.Equal(t, []int64{39}, reasons["rate_limited"].AccountIDs)
	require.NotNil(t, reasons["rate_limited"].EarliestRecoveryAt)
	empty, err := repo.SnapshotImageAccountCapacity(ctx, 2, "model_resolution", "model", "4K", nil)
	require.NoError(t, err)
	require.Zero(t, empty.CandidateCount)
	_, err = db.Exec(`INSERT INTO accounts(id,schedulable) SELECT id,false FROM generate_series(100,399) id; INSERT INTO group_image_size_accounts SELECT id,2,'','4K' FROM accounts WHERE id>=100;`)
	require.NoError(t, err)
	large, err := repo.SnapshotImageAccountCapacity(ctx, 2, "resolution", "", "4K", nil)
	require.NoError(t, err)
	require.Equal(t, 300, large.CandidateCount)
	require.True(t, large.CandidatesTruncated)
	require.Equal(t, 256, large.Reasons[0].Count)
	require.Len(t, large.Reasons[0].AccountIDs, 20)
}
