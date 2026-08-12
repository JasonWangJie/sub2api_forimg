-- 分组大分组标签（同名自动归类，空值表示未分类）
ALTER TABLE groups ADD COLUMN IF NOT EXISTS section VARCHAR(100) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_groups_section_sort_order ON groups (section, sort_order);
