-- Deferred plaza submissions: review before any OSS upload.
-- Realtime 投稿先只落元数据申请；审核通过后用户再同步上传并发布到广场。

CREATE TABLE IF NOT EXISTS image_plaza_submission_requests (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(64) NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status VARCHAR(32) NOT NULL DEFAULT 'pending_review',
    title VARCHAR(200) NOT NULL DEFAULT '',
    private_prompt TEXT NOT NULL DEFAULT '',
    public_title VARCHAR(200) NOT NULL DEFAULT '',
    public_prompt TEXT,
    share_prompt BOOLEAN NOT NULL DEFAULT FALSE,
    platform VARCHAR(32) NOT NULL DEFAULT '',
    generation_mode VARCHAR(16) NOT NULL DEFAULT 'realtime',
    source_type VARCHAR(32) NOT NULL DEFAULT 'realtime_import',
    model VARCHAR(255) NOT NULL DEFAULT '',
    requested_size VARCHAR(32) NOT NULL DEFAULT '',
    aspect_ratio VARCHAR(32) NOT NULL DEFAULT '',
    quality VARCHAR(32) NOT NULL DEFAULT '',
    content_type VARCHAR(128) NOT NULL DEFAULT '',
    byte_size BIGINT NOT NULL DEFAULT 0,
    checksum_sha256 VARCHAR(64) NOT NULL DEFAULT '',
    client_blob_key VARCHAR(255) NOT NULL,
    api_key_id BIGINT REFERENCES api_keys(id) ON DELETE SET NULL,
    group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL,
    reviewer_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    review_reason TEXT,
    reviewed_at TIMESTAMPTZ,
    library_item_id BIGINT REFERENCES image_library_items(id) ON DELETE SET NULL,
    publication_public_id VARCHAR(64),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT image_plaza_submission_requests_status_check CHECK (
        status IN ('pending_review', 'approved_pending_sync', 'rejected', 'withdrawn', 'synced')
    ),
    CONSTRAINT image_plaza_submission_requests_generation_mode_check CHECK (
        generation_mode IN ('realtime', 'async', 'import')
    ),
    CONSTRAINT image_plaza_submission_requests_source_type_check CHECK (
        source_type IN ('realtime_import', 'async_task', 'legacy_plaza', 'manual_import')
    ),
    CONSTRAINT image_plaza_submission_requests_byte_size_check CHECK (byte_size >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS image_plaza_submission_requests_active_blob_uidx
    ON image_plaza_submission_requests(user_id, client_blob_key)
    WHERE status IN ('pending_review', 'approved_pending_sync');

CREATE INDEX IF NOT EXISTS image_plaza_submission_requests_user_cursor_idx
    ON image_plaza_submission_requests(user_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS image_plaza_submission_requests_review_idx
    ON image_plaza_submission_requests(status, created_at DESC, id DESC);

COMMENT ON TABLE image_plaza_submission_requests IS
    'Metadata-only plaza submission queue; OSS upload happens only after approve when the user syncs';
