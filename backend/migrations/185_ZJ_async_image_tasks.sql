-- Durable asynchronous image generation tasks. Redis remains the delivery
-- mechanism; PostgreSQL is the source of truth for task and billing state.

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS allow_async_image_generation BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN groups.allow_async_image_generation IS
    'Whether the group may use the durable asynchronous image generation API';

CREATE TABLE IF NOT EXISTS async_image_tasks (
    id BIGSERIAL PRIMARY KEY,
    task_id VARCHAR(64) NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
    group_id BIGINT NOT NULL REFERENCES groups(id),
    account_id BIGINT REFERENCES accounts(id),
    protocol VARCHAR(16) NOT NULL,
    platform VARCHAR(32) NOT NULL,
    request_type VARCHAR(32) NOT NULL,
    model VARCHAR(255) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'queued',
    billing_status VARCHAR(32) NOT NULL DEFAULT 'pending',
    progress INTEGER NOT NULL DEFAULT 0,
    requested_image_size VARCHAR(32),
    actual_image_size VARCHAR(32),
    aspect_ratio VARCHAR(32),
    image_count INTEGER NOT NULL DEFAULT 0,
    actual_cost NUMERIC(20,8),
    currency VARCHAR(8) NOT NULL DEFAULT 'USD',
    idempotency_key VARCHAR(255),
    request_hash VARCHAR(64) NOT NULL,
    request_payload BYTEA NOT NULL,
    prompt_preview VARCHAR(500),
    upstream_request_id VARCHAR(255),
    billing_request_id VARCHAR(255),
    billing_payload JSONB,
    retry_count INTEGER NOT NULL DEFAULT 0,
    storage_retry_count INTEGER NOT NULL DEFAULT 0,
    billing_retry_count INTEGER NOT NULL DEFAULT 0,
    version BIGINT NOT NULL DEFAULT 1,
    error_code VARCHAR(100),
    error_message TEXT,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    upstream_succeeded_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    cleanup_claimed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT async_image_tasks_protocol_check
        CHECK (protocol IN ('bb', 'sc')),
    CONSTRAINT async_image_tasks_platform_check
        CHECK (platform IN ('gemini', 'openai')),
    CONSTRAINT async_image_tasks_request_type_check
        CHECK (request_type IN ('text_to_image', 'image_to_image')),
    CONSTRAINT async_image_tasks_status_check
        CHECK (status IN (
            'queued', 'invoking', 'upstream_succeeded', 'uploading',
            'billing_pending', 'succeeded', 'failed', 'execution_unknown',
            'storage_failed', 'billing_failed', 'expired'
        )),
    CONSTRAINT async_image_tasks_billing_status_check
        CHECK (billing_status IN ('pending', 'prepared', 'applying', 'succeeded', 'failed', 'not_billable')),
    CONSTRAINT async_image_tasks_progress_check CHECK (progress BETWEEN 0 AND 100),
    CONSTRAINT async_image_tasks_image_count_check CHECK (image_count >= 0),
    CONSTRAINT async_image_tasks_retry_count_check CHECK (retry_count >= 0),
    CONSTRAINT async_image_tasks_storage_retry_count_check CHECK (storage_retry_count >= 0),
    CONSTRAINT async_image_tasks_billing_retry_count_check CHECK (billing_retry_count >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS async_image_tasks_owner_idempotency_uidx
    ON async_image_tasks(api_key_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS async_image_tasks_user_created_idx
    ON async_image_tasks(user_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS async_image_tasks_admin_created_idx
    ON async_image_tasks(created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS async_image_tasks_status_updated_idx
    ON async_image_tasks(status, updated_at, id);

CREATE INDEX IF NOT EXISTS async_image_tasks_cleanup_idx
    ON async_image_tasks(expires_at, id)
    WHERE cleanup_claimed_at IS NULL;

CREATE INDEX IF NOT EXISTS async_image_tasks_api_key_created_idx
    ON async_image_tasks(api_key_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS async_image_results (
    id BIGSERIAL PRIMARY KEY,
    task_id VARCHAR(64) NOT NULL REFERENCES async_image_tasks(task_id) ON DELETE CASCADE,
    image_index INTEGER NOT NULL,
    provider VARCHAR(32) NOT NULL,
    bucket VARCHAR(255) NOT NULL,
    object_key TEXT NOT NULL,
    content_type VARCHAR(100) NOT NULL,
    byte_size BIGINT NOT NULL,
    checksum VARCHAR(128) NOT NULL,
    width INTEGER,
    height INTEGER,
    cleanup_claimed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT async_image_results_image_index_check CHECK (image_index >= 0),
    CONSTRAINT async_image_results_byte_size_check CHECK (byte_size >= 0),
    CONSTRAINT async_image_results_dimensions_check CHECK (
        (width IS NULL OR width > 0) AND (height IS NULL OR height > 0)
    ),
    UNIQUE(task_id, image_index)
);

CREATE INDEX IF NOT EXISTS async_image_results_task_idx
    ON async_image_results(task_id, image_index);

CREATE INDEX IF NOT EXISTS async_image_results_cleanup_idx
    ON async_image_results(created_at, id)
    WHERE cleanup_claimed_at IS NULL;

-- SC reference uploads are durable objects, so their ownership and stable
-- object identity must live in PostgreSQL instead of relying on a presigned
-- URL. Only a SHA-256 of the issued URL is retained for request matching.
CREATE TABLE IF NOT EXISTS async_image_input_objects (
    id BIGSERIAL PRIMARY KEY,
    upload_id VARCHAR(64) NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
    provider VARCHAR(32) NOT NULL,
    bucket VARCHAR(255) NOT NULL,
    object_key TEXT NOT NULL,
    content_type VARCHAR(100) NOT NULL,
    byte_size BIGINT NOT NULL,
    checksum VARCHAR(128) NOT NULL,
    width INTEGER,
    height INTEGER,
    url_hash VARCHAR(64) NOT NULL UNIQUE,
    filename VARCHAR(255),
    expires_at TIMESTAMPTZ NOT NULL,
    cleanup_claimed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT async_image_input_objects_byte_size_check CHECK (byte_size >= 0),
    CONSTRAINT async_image_input_objects_dimensions_check CHECK (
        (width IS NULL OR width > 0) AND (height IS NULL OR height > 0)
    )
);

CREATE INDEX IF NOT EXISTS async_image_input_objects_owner_idx
    ON async_image_input_objects(api_key_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS async_image_input_objects_cleanup_idx
    ON async_image_input_objects(expires_at, id)
    WHERE cleanup_claimed_at IS NULL;

CREATE TABLE IF NOT EXISTS async_image_task_inputs (
    task_id VARCHAR(64) NOT NULL REFERENCES async_image_tasks(task_id) ON DELETE CASCADE,
    input_object_id BIGINT NOT NULL REFERENCES async_image_input_objects(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(task_id, input_object_id)
);

CREATE INDEX IF NOT EXISTS async_image_task_inputs_object_idx
    ON async_image_task_inputs(input_object_id, task_id);

CREATE TABLE IF NOT EXISTS async_image_staging_objects (
    id BIGSERIAL PRIMARY KEY,
    task_id VARCHAR(64) NOT NULL REFERENCES async_image_tasks(task_id) ON DELETE CASCADE,
    image_index INTEGER NOT NULL,
    content BYTEA NOT NULL,
    content_type VARCHAR(100) NOT NULL,
    byte_size BIGINT NOT NULL,
    checksum VARCHAR(128) NOT NULL,
    width INTEGER,
    height INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT async_image_staging_image_index_check CHECK (image_index >= 0),
    CONSTRAINT async_image_staging_byte_size_check CHECK (byte_size >= 0),
    CONSTRAINT async_image_staging_dimensions_check CHECK (
        (width IS NULL OR width > 0) AND (height IS NULL OR height > 0)
    ),
    UNIQUE(task_id, image_index)
);

CREATE INDEX IF NOT EXISTS async_image_staging_expiry_idx
    ON async_image_staging_objects(expires_at, id);

CREATE TABLE IF NOT EXISTS async_image_events (
    id BIGSERIAL PRIMARY KEY,
    task_id VARCHAR(64) NOT NULL REFERENCES async_image_tasks(task_id) ON DELETE CASCADE,
    event_type VARCHAR(64) NOT NULL,
    from_status VARCHAR(32),
    to_status VARCHAR(32),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS async_image_events_task_idx
    ON async_image_events(task_id, id);

CREATE TABLE IF NOT EXISTS async_image_outbox (
    id BIGSERIAL PRIMARY KEY,
    task_id VARCHAR(64) NOT NULL REFERENCES async_image_tasks(task_id) ON DELETE CASCADE,
    event_type VARCHAR(64) NOT NULL,
    dedup_key VARCHAR(160) NOT NULL UNIQUE,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT async_image_outbox_attempts_check CHECK (attempts >= 0)
);

CREATE INDEX IF NOT EXISTS async_image_outbox_ready_idx
    ON async_image_outbox(available_at, id)
    WHERE published_at IS NULL;

COMMENT ON TABLE async_image_tasks IS 'Durable asynchronous image generation task state';
COMMENT ON TABLE async_image_input_objects IS 'Owned durable SC reference-image uploads with logical expiry';
COMMENT ON TABLE async_image_task_inputs IS 'Reference uploads retained while an asynchronous image task is active';
COMMENT ON TABLE async_image_staging_objects IS 'Short-lived generated image bytes awaiting object storage';
COMMENT ON TABLE async_image_outbox IS 'Transactional delivery outbox for asynchronous image workers';
