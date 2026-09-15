ALTER TABLE async_image_tasks
    ADD COLUMN IF NOT EXISTS reference_image_urls JSONB NOT NULL DEFAULT '[]'::jsonb;

COMMENT ON COLUMN async_image_tasks.reference_image_urls IS
    'Original remote HTTP(S) reference image URLs retained for administrator task-detail audit; inline data URLs are excluded';
