-- Durable SC reference-image upload admission control.
--
-- The reservation row is PostgreSQL truth for in-flight bytes and optional
-- Idempotency-Key replay. Attempts are retained briefly to implement a strict
-- rolling 60-second rate limit under a per-API-key transaction lock.

CREATE TABLE IF NOT EXISTS async_image_upload_reservations (
    id BIGSERIAL PRIMARY KEY,
    reservation_id VARCHAR(64) NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
    idempotency_key VARCHAR(255),
    request_hash CHAR(64) NOT NULL,
    byte_size BIGINT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'reserved',
    input_object_id BIGINT REFERENCES async_image_input_objects(id) ON DELETE SET NULL,
    failure_reason VARCHAR(64),
    lease_expires_at TIMESTAMPTZ,
    intent_provider VARCHAR(32),
    intent_bucket VARCHAR(255),
    intent_object_key TEXT,
    intent_content_type VARCHAR(100),
    intent_byte_size BIGINT,
    intent_checksum CHAR(64),
    cleanup_claimed_at TIMESTAMPTZ,
    cleanup_delete_count SMALLINT NOT NULL DEFAULT 0,
    last_deleted_at TIMESTAMPTZ,
    idempotency_expires_at TIMESTAMPTZ,
    reserved_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    failed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT async_image_upload_reservations_status_check
        CHECK (status IN ('reserved', 'completed', 'failed')),
    CONSTRAINT async_image_upload_reservations_hash_check
        CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT async_image_upload_reservations_byte_size_check
        CHECK (byte_size > 0),
    CONSTRAINT async_image_upload_reservations_delete_count_check
        CHECK (cleanup_delete_count BETWEEN 0 AND 1),
    CONSTRAINT async_image_upload_reservations_delete_state_check CHECK (
        (cleanup_delete_count = 0 AND last_deleted_at IS NULL)
        OR (cleanup_delete_count = 1 AND last_deleted_at IS NOT NULL)
    ),
    CONSTRAINT async_image_upload_reservations_state_check CHECK (
        (status = 'reserved' AND input_object_id IS NULL AND lease_expires_at IS NOT NULL AND completed_at IS NULL AND failed_at IS NULL)
        OR (status = 'completed' AND lease_expires_at IS NULL AND completed_at IS NOT NULL AND failed_at IS NULL AND idempotency_expires_at IS NOT NULL)
        OR (status = 'failed' AND input_object_id IS NULL AND failed_at IS NOT NULL)
    ),
    CONSTRAINT async_image_upload_reservations_intent_check CHECK (
        (intent_provider IS NULL AND intent_bucket IS NULL AND intent_object_key IS NULL
         AND intent_content_type IS NULL AND intent_byte_size IS NULL AND intent_checksum IS NULL)
        OR (intent_provider IS NOT NULL AND intent_bucket IS NOT NULL AND intent_object_key IS NOT NULL
            AND intent_content_type IS NOT NULL AND intent_byte_size > 0
            AND intent_checksum ~ '^[0-9a-f]{64}$')
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS async_image_upload_reservations_owner_idempotency_uidx
    ON async_image_upload_reservations(api_key_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS async_image_upload_reservations_active_idx
    ON async_image_upload_reservations(api_key_id, lease_expires_at, id)
    WHERE status = 'reserved';

CREATE INDEX IF NOT EXISTS async_image_upload_reservations_object_idx
    ON async_image_upload_reservations(input_object_id)
    WHERE input_object_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS async_image_upload_reservations_failed_cleanup_idx
    ON async_image_upload_reservations(updated_at, id)
    WHERE status = 'failed' OR (status = 'completed' AND input_object_id IS NULL);

CREATE INDEX IF NOT EXISTS async_image_upload_reservations_intent_cleanup_idx
    ON async_image_upload_reservations(cleanup_claimed_at, last_deleted_at, lease_expires_at, id)
    WHERE intent_object_key IS NOT NULL AND status IN ('reserved', 'failed');

CREATE TABLE IF NOT EXISTS async_image_upload_attempts (
    id BIGSERIAL PRIMARY KEY,
    admission_id VARCHAR(64) NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    reservation_id VARCHAR(64),
    attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    consumed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS async_image_upload_attempts_owner_time_idx
    ON async_image_upload_attempts(api_key_id, attempted_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS async_image_upload_attempts_cleanup_idx
    ON async_image_upload_attempts(attempted_at, id);

-- A replayed upload receives a freshly signed URL. Keep every issued URL hash
-- as an ownership tombstone, including expired aliases, until the input object
-- is deleted. This prevents an old signed URL from being reclassified as an
-- unrelated remote reference after its signature expires.
CREATE TABLE IF NOT EXISTS async_image_input_url_aliases (
    url_hash CHAR(64) PRIMARY KEY,
    input_object_id BIGINT NOT NULL REFERENCES async_image_input_objects(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT async_image_input_url_aliases_hash_check
        CHECK (url_hash ~ '^[0-9a-f]{64}$')
);

CREATE INDEX IF NOT EXISTS async_image_input_url_aliases_object_idx
    ON async_image_input_url_aliases(input_object_id, expires_at DESC);

COMMENT ON TABLE async_image_upload_reservations IS
    'PostgreSQL admission reservations and idempotency truth for SC reference-image uploads';
COMMENT ON COLUMN async_image_upload_reservations.cleanup_delete_count IS
    'Successful OSS delete passes; the recovery fact is removed only after two passes';
COMMENT ON COLUMN async_image_upload_reservations.last_deleted_at IS
    'First successful OSS delete time; a second delete is delayed by at least ten minutes';
COMMENT ON TABLE async_image_upload_attempts IS
    'Rolling rate-limit attempts for SC reference-image uploads';
COMMENT ON TABLE async_image_input_url_aliases IS
    'Signed-URL ownership tombstones; expired aliases remain counted and resolvable until their input object is deleted';
