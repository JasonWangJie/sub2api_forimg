-- Encrypted response sources survive worker restarts without repeating generation.
CREATE TABLE IF NOT EXISTS async_image_result_fetches (
    task_id VARCHAR(64) PRIMARY KEY REFERENCES async_image_tasks(task_id) ON DELETE CASCADE,
    encrypted_payload BYTEA NOT NULL,
    attempts JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(attempts) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS async_image_result_fetches_expiry_idx ON async_image_result_fetches(expires_at);
