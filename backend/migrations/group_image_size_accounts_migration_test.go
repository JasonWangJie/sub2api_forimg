package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupImageSizeAccountsMigrationIsEmbedded(t *testing.T) {
	content, err := FS.ReadFile("222_ZJ_group_image_size_accounts.sql")
	require.NoError(t, err)
	require.Contains(t, string(content), "CREATE TABLE IF NOT EXISTS group_image_size_accounts")
	require.Contains(t, string(content), "size_tier")
	require.Contains(t, string(content), "1K")
	require.Contains(t, string(content), "4K")
}

func TestImageAccountPoolModesMigrationPreservesLegacyRows(t *testing.T) {
	content, err := FS.ReadFile("225_ZJ_image_account_pool_modes.sql")
	require.NoError(t, err)
	sql := string(content)
	require.Contains(t, sql, "image_account_pool_mode VARCHAR(32) NOT NULL DEFAULT 'resolution'")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS model VARCHAR(255) NOT NULL DEFAULT ''")
	require.Contains(t, sql, "model = '' AND size_tier IN ('1K', '2K', '4K')")
	require.Contains(t, sql, "model <> '' AND size_tier IN ('', '1K', '2K', '4K')")
	require.Contains(t, sql, "UNIQUE (group_id, model, size_tier, account_id)")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS chk_groups_image_account_pool_mode")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS chk_group_image_size_accounts_dimensions")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS uq_group_image_size_accounts_group_model_tier_account")
	require.NotContains(t, sql, "DROP TABLE")
	require.NotContains(t, sql, "DELETE FROM group_image_size_accounts")
}
