-- Persist the final object identity before a library upload starts so failed
-- writes and failed database commits can be recovered without listing storage.

CREATE TABLE IF NOT EXISTS image_library_upload_intents (
    id BIGSERIAL PRIMARY KEY,
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
    CONSTRAINT image_library_upload_intents_identity_uidx UNIQUE(provider, bucket, object_key),
    CONSTRAINT image_library_upload_intents_byte_size_check CHECK (byte_size > 0),
    CONSTRAINT image_library_upload_intents_checksum_check CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$')
);

CREATE INDEX IF NOT EXISTS image_library_upload_intents_cleanup_idx
    ON image_library_upload_intents(expires_at, cleanup_claimed_at, id);

COMMENT ON TABLE image_library_upload_intents IS
    'Pre-upload durable identities retained until a library asset commits or orphan cleanup succeeds';
