package repository

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageLibraryMigrationContract(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(current), "..", "..", "migrations", "186_ZJ_image_library_and_plaza_moderation.sql")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	sqlText := string(data)
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS image_storage_objects",
		"CREATE TABLE IF NOT EXISTS image_library_items",
		"CREATE TABLE IF NOT EXISTS image_plaza_publications",
		"CREATE TABLE IF NOT EXISTS image_plaza_reports",
		"CREATE TABLE IF NOT EXISTS image_library_events",
		"CREATE TABLE IF NOT EXISTS image_library_outbox",
		"CREATE TABLE IF NOT EXISTS image_library_cleanup_jobs",
		"CREATE TABLE IF NOT EXISTS image_library_migration_state",
		"ADD COLUMN IF NOT EXISTS storage_object_id",
		"ADD COLUMN IF NOT EXISTS lease_version",
		"image_plaza_publications_active_item_uidx",
	} {
		require.Contains(t, sqlText, fragment)
	}
	require.NotContains(t, sqlText, "COALESCE(width, 1)")
	require.NotContains(t, sqlText, "COALESCE(height, 1)")
	require.NotContains(t, sqlText, "GREATEST(byte_size, 1)")
	require.True(t, strings.Contains(sqlText, "asset_id VARCHAR(64) NOT NULL UNIQUE"))
	require.True(t, strings.Contains(sqlText, "public_id VARCHAR(64) NOT NULL UNIQUE"))
}
