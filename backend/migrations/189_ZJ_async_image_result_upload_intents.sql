CREATE TABLE IF NOT EXISTS async_image_result_upload_intents (
    id BIGSERIAL PRIMARY KEY,
    task_id VARCHAR(64) NOT NULL REFERENCES async_image_tasks(task_id) ON DELETE CASCADE,
    image_index INTEGER NOT NULL,
    provider VARCHAR(32) NOT NULL,
    bucket VARCHAR(255) NOT NULL,
    object_key TEXT NOT NULL,
    content_type VARCHAR(128) NOT NULL,
    byte_size BIGINT NOT NULL,
    checksum_sha256 VARCHAR(64) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    cleanup_claimed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT async_image_result_upload_intents_index_check CHECK (image_index >= 0),
    CONSTRAINT async_image_result_upload_intents_bytes_check CHECK (byte_size > 0),
    UNIQUE(task_id, image_index)
);

CREATE INDEX IF NOT EXISTS async_image_result_upload_intents_cleanup_idx
    ON async_image_result_upload_intents(expires_at, cleanup_claimed_at, id);

ALTER TABLE async_image_outbox
    ADD COLUMN IF NOT EXISTS claim_token VARCHAR(64);

COMMENT ON TABLE async_image_result_upload_intents IS
    'Object identities persisted before durable async image result PUT operations';
