-- Per-API-key platform billing group mappings for durable async image dual-use.
-- Resolve order: mapping row for (key, platform) → fallback to api_keys.group_id when platforms match.

CREATE TABLE IF NOT EXISTS api_key_platform_groups (
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    platform   VARCHAR(32) NOT NULL,
    group_id   BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    PRIMARY KEY (api_key_id, platform),
    CONSTRAINT api_key_platform_groups_platform_check CHECK (platform IN ('gemini', 'openai'))
);

CREATE INDEX IF NOT EXISTS idx_api_key_platform_groups_group_id
    ON api_key_platform_groups(group_id);

COMMENT ON TABLE api_key_platform_groups IS
    'Maps an API key to gemini/openai billing groups for asynchronous image generation';
