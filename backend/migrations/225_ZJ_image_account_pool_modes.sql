-- Extend image account pools from resolution-only routing to three independent modes:
-- resolution, exact request model, and exact request model + resolution.
-- Existing rows keep model='' and therefore retain their original resolution behavior.

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS image_account_pool_mode VARCHAR(32) NOT NULL DEFAULT 'resolution';

ALTER TABLE groups
    DROP CONSTRAINT IF EXISTS chk_groups_image_account_pool_mode;

ALTER TABLE groups
    ADD CONSTRAINT chk_groups_image_account_pool_mode
    CHECK (image_account_pool_mode IN ('resolution', 'model', 'model_resolution'));

ALTER TABLE group_image_size_accounts
    ADD COLUMN IF NOT EXISTS model VARCHAR(255) NOT NULL DEFAULT '';

ALTER TABLE group_image_size_accounts
    DROP CONSTRAINT IF EXISTS chk_group_image_size_accounts_tier;

ALTER TABLE group_image_size_accounts
    DROP CONSTRAINT IF EXISTS chk_group_image_size_accounts_dimensions;

ALTER TABLE group_image_size_accounts
    DROP CONSTRAINT IF EXISTS uq_group_image_size_accounts_group_tier_account;

ALTER TABLE group_image_size_accounts
    DROP CONSTRAINT IF EXISTS uq_group_image_size_accounts_group_model_tier_account;

ALTER TABLE group_image_size_accounts
    ADD CONSTRAINT chk_group_image_size_accounts_dimensions
    CHECK (
        (model = '' AND size_tier IN ('1K', '2K', '4K'))
        OR
        (model <> '' AND size_tier IN ('', '1K', '2K', '4K'))
    );

ALTER TABLE group_image_size_accounts
    ADD CONSTRAINT uq_group_image_size_accounts_group_model_tier_account
    UNIQUE (group_id, model, size_tier, account_id);

DROP INDEX IF EXISTS idx_group_image_size_accounts_group_tier_priority;

CREATE INDEX IF NOT EXISTS idx_group_image_size_accounts_group_model_tier_priority
    ON group_image_size_accounts (group_id, model, size_tier, priority, account_id);
