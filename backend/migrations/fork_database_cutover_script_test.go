package migrations

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestForkDatabaseCutoverScriptKeepsDestructiveSafetyGates(t *testing.T) {
	repoRoot := repositoryRootForCutoverTest(t)
	scriptPath := filepath.Join(repoRoot, "deploy", "db-fast-cutover_ZJ.sh")
	script, err := os.ReadFile(scriptPath)
	require.NoError(t, err)

	text := string(script)
	for _, required := range []string{
		`ZJ_CONFIRM_TARGET_ID="TARGET:database@address:port"`,
		`expected="FREEZE:${source_id}"`,
		`expected="REPLACE:${target_id}"`,
		`expected="UNFREEZE:${source_id}"`,
		`default_transaction_read_only = on`,
		`assert_source_idle`,
		`assert_no_invoking_async_tasks`,
		`assert_target_idle`,
		`assert_target_replica_restore_privilege`,
		`database_fingerprint`,
		`constraint_fingerprint`,
		`trigger_fingerprint`,
		`extension_fingerprint`,
		`--format=directory`,
		`--data-only`,
		`session_replication_role=replica`,
		`TRUNCATE TABLE `,
		`status=incomplete`,
		`status=verified`,
		`db_fast_cutover_verify_ZJ.sql`,
	} {
		require.Contains(t, text, required)
	}
	require.NotContains(t, text, "UPDATE users SET balance")
	require.NotContains(t, text, "UPDATE api_keys SET")
}

func TestForkDatabaseCutoverVerificationCoversBillingState(t *testing.T) {
	repoRoot := repositoryRootForCutoverTest(t)
	sqlPath := filepath.Join(repoRoot, "deploy", "sql", "db_fast_cutover_verify_ZJ.sql")
	sql, err := os.ReadFile(sqlPath)
	require.NoError(t, err)

	text := string(sql)
	for _, required := range []string{
		"schema_migrations",
		"users",
		"frozen_balance",
		"api_keys",
		"user_subscriptions",
		"user_platform_quotas",
		"usage_logs",
		"usage_billing_dedup",
		"payment_orders",
		"user_affiliate_ledger",
		"async_image_tasks",
		"batch_image_jobs",
		"image_storage_objects",
		"sequence_ownership|ok",
		"SET TIME ZONE 'UTC'",
	} {
		require.Contains(t, text, required)
	}
}

func repositoryRootForCutoverTest(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
}
