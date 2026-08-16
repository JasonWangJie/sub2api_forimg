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
