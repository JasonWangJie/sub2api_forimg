-- 图片广场：全局共享生成图元数据（图片文件存本地 data/image_plaza）
CREATE TABLE IF NOT EXISTS image_plaza_items (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    prompt TEXT NOT NULL,
    title VARCHAR(200) NOT NULL DEFAULT '',
    model VARCHAR(128) NOT NULL,
    size VARCHAR(32) NOT NULL DEFAULT '',
    quality VARCHAR(32) NOT NULL DEFAULT '',
    format VARCHAR(16) NOT NULL DEFAULT 'png',
    background VARCHAR(32) NOT NULL DEFAULT 'auto',
    style VARCHAR(32) NOT NULL DEFAULT 'auto',
    storage_path VARCHAR(512) NOT NULL,
    content_type VARCHAR(64) NOT NULL DEFAULT 'image/png',
    file_size BIGINT NOT NULL DEFAULT 0,
    visibility VARCHAR(20) NOT NULL DEFAULT 'public',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_image_plaza_items_visibility_created
    ON image_plaza_items (visibility, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_image_plaza_items_user_created
    ON image_plaza_items (user_id, created_at DESC);

COMMENT ON TABLE image_plaza_items IS '图片广场（全局共享）';
COMMENT ON COLUMN image_plaza_items.visibility IS '可见性: public / private';
COMMENT ON COLUMN image_plaza_items.storage_path IS '相对 data 目录的存储路径';
