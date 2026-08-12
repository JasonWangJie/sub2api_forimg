package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestForkImageWorkflowMigrationsUseZJOwnershipMarker(t *testing.T) {
	forkMigrations := []struct {
		marked string
		legacy string
	}{
		{"182_ZJ_add_image_plaza.sql", "182_add_image_plaza.sql"},
		{"185_ZJ_async_image_tasks.sql", "185_async_image_tasks.sql"},
		{"186_ZJ_image_library_and_plaza_moderation.sql", "186_image_library_and_plaza_moderation.sql"},
		{"187_ZJ_async_image_upload_reservations.sql", "187_async_image_upload_reservations.sql"},
		{"188_ZJ_plaza_submission_deferred_upload.sql", "188_plaza_submission_deferred_upload.sql"},
		{"189_ZJ_async_image_result_upload_intents.sql", "189_async_image_result_upload_intents.sql"},
		{"192_ZJ_image_library_upload_intents.sql", "192_image_library_upload_intents.sql"},
	}

	for _, migration := range forkMigrations {
		t.Run(migration.marked, func(t *testing.T) {
			_, err := FS.ReadFile(migration.marked)
			require.NoError(t, err)

			_, err = FS.ReadFile(migration.legacy)
			require.Error(t, err)
		})
	}
}
