-- Per-group optional account pools for image generation by resolution tier (1K/2K/4K).
-- Empty tier = use default account_groups pool. Independent binding allowed (no account_groups row required).

CREATE TABLE IF NOT EXISTS group_image_size_accounts (
    id              BIGSERIAL PRIMARY KEY,
    group_id        BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    size_tier       VARCHAR(8) NOT NULL,
    account_id      BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    priority        INT NOT NULL DEFAULT 50,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_group_image_size_accounts_tier CHECK (size_tier IN ('1K', '2K', '4K')),
    CONSTRAINT uq_group_image_size_accounts_group_tier_account UNIQUE (group_id, size_tier, account_id)
);

CREATE INDEX IF NOT EXISTS idx_group_image_size_accounts_group_tier_priority
    ON group_image_size_accounts (group_id, size_tier, priority, account_id);

CREATE INDEX IF NOT EXISTS idx_group_image_size_accounts_account_id
    ON group_image_size_accounts (account_id);
