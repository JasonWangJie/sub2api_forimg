ALTER TABLE async_image_tasks
    ADD COLUMN IF NOT EXISTS account_attempts JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS attempted_account_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS reconciliation_status VARCHAR(32) NOT NULL DEFAULT 'none';

ALTER TABLE async_image_tasks
    DROP CONSTRAINT IF EXISTS async_image_tasks_account_attempts_check,
    ADD CONSTRAINT async_image_tasks_account_attempts_check CHECK (jsonb_typeof(account_attempts) = 'array'),
    DROP CONSTRAINT IF EXISTS async_image_tasks_attempted_account_ids_check,
    ADD CONSTRAINT async_image_tasks_attempted_account_ids_check CHECK (jsonb_typeof(attempted_account_ids) = 'array'),
    DROP CONSTRAINT IF EXISTS async_image_tasks_reconciliation_status_check,
    ADD CONSTRAINT async_image_tasks_reconciliation_status_check CHECK (reconciliation_status IN ('none', 'pending', 'confirmed_success', 'confirmed_failure', 'unavailable'));

COMMENT ON COLUMN async_image_tasks.account_attempts IS 'Non-secret account selection/failure history for one async image task';
COMMENT ON COLUMN async_image_tasks.attempted_account_ids IS 'Distinct account IDs attempted by this async image task';
COMMENT ON COLUMN async_image_tasks.reconciliation_status IS 'Whether an interrupted upstream request has been reconciled';
