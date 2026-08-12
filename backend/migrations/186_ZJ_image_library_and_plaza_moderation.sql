-- Unified image library and moderated plaza. Object identities are durable;
-- expiring signed URLs are never stored in PostgreSQL.

CREATE TABLE IF NOT EXISTS image_storage_objects (
    id BIGSERIAL PRIMARY KEY,
    provider VARCHAR(32) NOT NULL,
    bucket VARCHAR(255) NOT NULL,
    object_key TEXT NOT NULL,
    content_type VARCHAR(100) NOT NULL,
    byte_size BIGINT NOT NULL,
    checksum_sha256 VARCHAR(128) NOT NULL,
    width INTEGER,
    height INTEGER,
    state VARCHAR(20) NOT NULL DEFAULT 'active',
    deletion_claimed_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT image_storage_objects_identity_uidx UNIQUE(provider, bucket, object_key),
    CONSTRAINT image_storage_objects_byte_size_check CHECK (byte_size >= 0),
    CONSTRAINT image_storage_objects_dimensions_check CHECK ((width IS NULL OR width > 0) AND (height IS NULL OR height > 0)),
    CONSTRAINT image_storage_objects_state_check CHECK (state IN ('active', 'deleting', 'deleted'))
);

CREATE INDEX IF NOT EXISTS image_storage_objects_checksum_idx
    ON image_storage_objects(checksum_sha256, byte_size);
CREATE INDEX IF NOT EXISTS image_storage_objects_cleanup_idx
    ON image_storage_objects(state, deletion_claimed_at, id);

ALTER TABLE async_image_results
    ADD COLUMN IF NOT EXISTS storage_object_id BIGINT REFERENCES image_storage_objects(id),
    ADD COLUMN IF NOT EXISTS library_validation_status VARCHAR(20) NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS library_validation_error TEXT,
    ADD COLUMN IF NOT EXISTS library_validated_at TIMESTAMPTZ;

ALTER TABLE async_image_results
    ADD CONSTRAINT async_image_results_library_validation_check
    CHECK (library_validation_status IN ('pending', 'passed', 'quarantined'));

INSERT INTO image_storage_objects (
    provider, bucket, object_key, content_type, byte_size, checksum_sha256,
    width, height, created_at, updated_at
)
SELECT provider, bucket, object_key, content_type, byte_size, checksum,
       width, height, created_at, created_at
FROM async_image_results
ON CONFLICT (provider, bucket, object_key) DO UPDATE SET
    content_type = EXCLUDED.content_type,
    byte_size = EXCLUDED.byte_size,
    checksum_sha256 = EXCLUDED.checksum_sha256,
    width = EXCLUDED.width,
    height = EXCLUDED.height,
    updated_at = NOW();

UPDATE async_image_results r
SET storage_object_id = o.id
FROM image_storage_objects o
WHERE r.storage_object_id IS NULL
  AND o.provider = r.provider
  AND o.bucket = r.bucket
  AND o.object_key = r.object_key;

CREATE INDEX IF NOT EXISTS async_image_results_storage_object_idx
    ON async_image_results(storage_object_id);

CREATE TABLE IF NOT EXISTS image_library_items (
    id BIGSERIAL PRIMARY KEY,
    asset_id VARCHAR(64) NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_key_id BIGINT REFERENCES api_keys(id) ON DELETE SET NULL,
    group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL,
    storage_object_id BIGINT NOT NULL REFERENCES image_storage_objects(id),
    platform VARCHAR(32) NOT NULL DEFAULT '',
    generation_mode VARCHAR(16) NOT NULL,
    source_type VARCHAR(32) NOT NULL,
    source_task_id VARCHAR(64),
    source_result_index INTEGER,
    model VARCHAR(255) NOT NULL DEFAULT '',
    requested_size VARCHAR(32) NOT NULL DEFAULT '',
    actual_size VARCHAR(32) NOT NULL DEFAULT '',
    aspect_ratio VARCHAR(32) NOT NULL DEFAULT '',
    quality VARCHAR(32) NOT NULL DEFAULT '',
    title VARCHAR(200) NOT NULL DEFAULT '',
    private_prompt TEXT NOT NULL DEFAULT '',
    visibility VARCHAR(16) NOT NULL DEFAULT 'private',
    archive_status VARCHAR(20) NOT NULL DEFAULT 'ready',
    archive_error TEXT,
    idempotency_key VARCHAR(255),
    request_hash VARCHAR(64),
    expires_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
    purged_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT image_library_items_generation_mode_check CHECK (generation_mode IN ('realtime', 'async', 'import')),
    CONSTRAINT image_library_items_source_type_check CHECK (source_type IN ('realtime_import', 'async_task', 'legacy_plaza', 'manual_import')),
    CONSTRAINT image_library_items_visibility_check CHECK (visibility IN ('private', 'public')),
    CONSTRAINT image_library_items_archive_status_check CHECK (archive_status IN ('ready', 'archive_failed')),
    CONSTRAINT image_library_items_source_result_check CHECK (source_result_index IS NULL OR source_result_index >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS image_library_items_owner_idempotency_uidx
    ON image_library_items(user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS image_library_items_async_result_uidx
    ON image_library_items(user_id, source_task_id, source_result_index)
    WHERE source_type = 'async_task' AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS image_library_items_user_cursor_idx
    ON image_library_items(user_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS image_library_items_expiry_idx
    ON image_library_items(expires_at, id)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS image_library_items_purge_idx
    ON image_library_items(deleted_at, id)
    WHERE deleted_at IS NOT NULL AND purged_at IS NULL;
CREATE INDEX IF NOT EXISTS image_library_items_object_idx
    ON image_library_items(storage_object_id)
    WHERE deleted_at IS NULL;

-- Import attempts are persisted before remote downloads or object-store writes.
-- Rows without an idempotency key are short-lived rate-limit facts; keyed rows
-- also reserve a request while it is in progress so concurrent retries cannot
-- write the same image to object storage more than once.
CREATE TABLE IF NOT EXISTS image_library_import_attempts (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    idempotency_key VARCHAR(255),
    request_hash VARCHAR(64),
    state VARCHAR(20) NOT NULL DEFAULT 'processing',
    library_item_id BIGINT REFERENCES image_library_items(id) ON DELETE SET NULL,
    attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT image_library_import_attempts_state_check CHECK (state IN ('processing', 'completed', 'failed')),
    CONSTRAINT image_library_import_attempts_idempotency_check CHECK (
        (idempotency_key IS NULL AND request_hash IS NULL) OR
        (idempotency_key IS NOT NULL AND request_hash IS NOT NULL)
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS image_library_import_attempts_owner_key_uidx
    ON image_library_import_attempts(user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS image_library_import_attempts_rate_idx
    ON image_library_import_attempts(user_id, attempted_at DESC);

CREATE TABLE IF NOT EXISTS image_plaza_publications (
    id BIGSERIAL PRIMARY KEY,
    public_id VARCHAR(64) NOT NULL UNIQUE,
    library_item_id BIGINT NOT NULL REFERENCES image_library_items(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status VARCHAR(24) NOT NULL DEFAULT 'pending_review',
    public_title VARCHAR(200) NOT NULL DEFAULT '',
    public_prompt TEXT,
    share_prompt BOOLEAN NOT NULL DEFAULT false,
    moderation_status VARCHAR(24) NOT NULL DEFAULT 'pending',
    reviewer_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    review_reason TEXT,
    published_at TIMESTAMPTZ,
    reviewed_at TIMESTAMPTZ,
    withdrawn_at TIMESTAMPTZ,
    hidden_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT image_plaza_publications_status_check CHECK (status IN ('pending_review', 'published', 'rejected', 'withdrawn', 'admin_hidden', 'expired')),
    CONSTRAINT image_plaza_publications_moderation_check CHECK (moderation_status IN ('pending', 'approved', 'rejected'))
);

CREATE INDEX IF NOT EXISTS image_plaza_publications_public_cursor_idx
    ON image_plaza_publications(published_at DESC, id DESC)
    WHERE status = 'published';
CREATE INDEX IF NOT EXISTS image_plaza_publications_review_idx
    ON image_plaza_publications(status, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS image_plaza_publications_user_idx
    ON image_plaza_publications(user_id, created_at DESC, id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS image_plaza_publications_active_item_uidx
    ON image_plaza_publications(library_item_id)
    WHERE status IN ('pending_review', 'published', 'admin_hidden');

CREATE TABLE IF NOT EXISTS image_plaza_reports (
    id BIGSERIAL PRIMARY KEY,
    publication_id BIGINT NOT NULL REFERENCES image_plaza_publications(id) ON DELETE CASCADE,
    reporter_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason VARCHAR(64) NOT NULL,
    details TEXT NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'open',
    resolved_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    resolution TEXT,
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT image_plaza_reports_status_check CHECK (status IN ('open', 'resolved', 'dismissed'))
);

CREATE UNIQUE INDEX IF NOT EXISTS image_plaza_reports_open_reporter_uidx
    ON image_plaza_reports(publication_id, reporter_user_id)
    WHERE status = 'open';
CREATE INDEX IF NOT EXISTS image_plaza_reports_status_idx
    ON image_plaza_reports(status, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS image_library_events (
    id BIGSERIAL PRIMARY KEY,
    library_item_id BIGINT REFERENCES image_library_items(id) ON DELETE CASCADE,
    publication_id BIGINT REFERENCES image_plaza_publications(id) ON DELETE CASCADE,
    actor_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    event_type VARCHAR(64) NOT NULL,
    from_status VARCHAR(32),
    to_status VARCHAR(32),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT image_library_events_subject_check CHECK (library_item_id IS NOT NULL OR publication_id IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS image_library_events_item_idx
    ON image_library_events(library_item_id, id);
CREATE INDEX IF NOT EXISTS image_library_events_publication_idx
    ON image_library_events(publication_id, id);

CREATE TABLE IF NOT EXISTS image_library_outbox (
    id BIGSERIAL PRIMARY KEY,
    aggregate_type VARCHAR(32) NOT NULL,
    aggregate_id BIGINT NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    dedup_key VARCHAR(180) NOT NULL UNIQUE,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT image_library_outbox_attempts_check CHECK (attempts >= 0)
);

CREATE INDEX IF NOT EXISTS image_library_outbox_ready_idx
    ON image_library_outbox(available_at, id)
    WHERE completed_at IS NULL;

CREATE TABLE IF NOT EXISTS image_library_cleanup_jobs (
    id BIGSERIAL PRIMARY KEY,
    requested_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    scope VARCHAR(32) NOT NULL,
    filters JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    lease_version BIGINT NOT NULL DEFAULT 0,
    scanned_count BIGINT NOT NULL DEFAULT 0,
    deleted_count BIGINT NOT NULL DEFAULT 0,
    deleted_bytes BIGINT NOT NULL DEFAULT 0,
    last_error TEXT,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT image_library_cleanup_jobs_scope_check CHECK (scope IN ('expired', 'deleted', 'user')),
    CONSTRAINT image_library_cleanup_jobs_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed'))
);

ALTER TABLE image_library_cleanup_jobs
    ADD COLUMN IF NOT EXISTS lease_version BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS image_library_migration_state (
    migration_key VARCHAR(100) PRIMARY KEY,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    lease_version BIGINT NOT NULL DEFAULT 0,
    last_legacy_id BIGINT NOT NULL DEFAULT 0,
    migrated_count BIGINT NOT NULL DEFAULT 0,
    quarantined_count BIGINT NOT NULL DEFAULT 0,
    last_error TEXT,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT image_library_migration_state_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed'))
);

ALTER TABLE image_library_migration_state
    ADD COLUMN IF NOT EXISTS lease_version BIGINT NOT NULL DEFAULT 0;

INSERT INTO image_library_migration_state(migration_key)
VALUES ('legacy_image_plaza_v1')
ON CONFLICT (migration_key) DO NOTHING;

-- Existing plaza data was never subject to strict media validation or review.
-- Hide it immediately; the recoverable migration promotes validated records to
-- private library assets and pending-review submissions.
-- Environments that never ran 182_add_image_plaza.sql have no legacy table; skip.
DO $$
BEGIN
    IF to_regclass('public.image_plaza_items') IS NULL THEN
        RETURN;
    END IF;
    UPDATE image_plaza_items SET visibility = 'private' WHERE visibility = 'public';
END $$;

COMMENT ON TABLE image_storage_objects IS 'Durable object identities shared by async results, private library, and plaza';
COMMENT ON TABLE image_library_items IS 'Per-user server-side image library; private by default';
COMMENT ON TABLE image_plaza_publications IS 'Explicit, moderated publication state for library assets';
