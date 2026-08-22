ALTER TABLE async_image_tasks
    ADD COLUMN IF NOT EXISTS reference_transport VARCHAR(40),
    ADD COLUMN IF NOT EXISTS reference_retry_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS upstream_retry_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS capacity_retry_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE async_image_tasks
    DROP CONSTRAINT IF EXISTS async_image_tasks_reference_transport_check,
    ADD CONSTRAINT async_image_tasks_reference_transport_check CHECK (
        reference_transport IS NULL OR reference_transport IN (
            'passthrough', 'local', 'passthrough_fallback_local'
        )
    ),
    DROP CONSTRAINT IF EXISTS async_image_tasks_reference_retry_count_check,
    ADD CONSTRAINT async_image_tasks_reference_retry_count_check CHECK (reference_retry_count >= 0),
    DROP CONSTRAINT IF EXISTS async_image_tasks_upstream_retry_count_check,
    ADD CONSTRAINT async_image_tasks_upstream_retry_count_check CHECK (upstream_retry_count >= 0),
    DROP CONSTRAINT IF EXISTS async_image_tasks_capacity_retry_count_check,
    ADD CONSTRAINT async_image_tasks_capacity_retry_count_check CHECK (capacity_retry_count >= 0);
