-- Bounded, recoverable image-result cleanup. No object listing or payload reads.
ALTER TABLE image_storage_objects ADD COLUMN IF NOT EXISTS storage_identity TEXT NOT NULL DEFAULT '';
ALTER TABLE async_image_tasks ADD COLUMN IF NOT EXISTS result_references_checked BOOLEAN NOT NULL DEFAULT FALSE;
CREATE TABLE IF NOT EXISTS async_image_task_result_references (
 task_id VARCHAR(64) NOT NULL REFERENCES async_image_tasks(task_id) ON DELETE CASCADE,
 storage_object_id BIGINT NOT NULL REFERENCES image_storage_objects(id) ON DELETE RESTRICT,
 PRIMARY KEY(task_id,storage_object_id)
);
CREATE INDEX IF NOT EXISTS async_image_task_result_references_object_idx
 ON async_image_task_result_references(storage_object_id,task_id);


CREATE TABLE IF NOT EXISTS image_result_cleanup_policy (
 id SMALLINT PRIMARY KEY CHECK(id=1),
 config JSONB NOT NULL,
 revision BIGINT NOT NULL DEFAULT 1,
 next_run_at TIMESTAMPTZ,
 updated_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS image_result_cleanup_previews (
 token_hash VARCHAR(64) PRIMARY KEY,
 requested_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 request JSONB NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 consumed_job_id BIGINT REFERENCES image_library_cleanup_jobs(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS image_result_cleanup_previews_expiry_idx ON image_result_cleanup_previews(expires_at);

ALTER TABLE image_library_cleanup_jobs DROP CONSTRAINT IF EXISTS image_library_cleanup_jobs_scope_check;
ALTER TABLE image_library_cleanup_jobs ADD CONSTRAINT image_library_cleanup_jobs_scope_check
 CHECK(scope IN ('expired','deleted','user','async_results','all'));
ALTER TABLE image_library_cleanup_jobs DROP CONSTRAINT IF EXISTS image_library_cleanup_jobs_status_check;
ALTER TABLE image_library_cleanup_jobs ADD CONSTRAINT image_library_cleanup_jobs_status_check
 CHECK(status IN ('pending','running','paused','cancelled','succeeded','failed'));
ALTER TABLE image_library_cleanup_jobs
 ADD COLUMN IF NOT EXISTS result_request JSONB,
 ADD COLUMN IF NOT EXISTS result_cursor BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN IF NOT EXISTS result_cursor_created_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
 ADD COLUMN IF NOT EXISTS result_lease_until TIMESTAMPTZ,
 ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ,
 ADD COLUMN IF NOT EXISTS cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
 ADD COLUMN IF NOT EXISTS detached_count BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN IF NOT EXISTS protected_count BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN IF NOT EXISTS skipped_count BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN IF NOT EXISTS failed_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE image_library_cleanup_jobs ADD COLUMN IF NOT EXISTS last_skip_reason TEXT;
ALTER TABLE image_library_cleanup_jobs ADD COLUMN IF NOT EXISTS result_child_job_id BIGINT REFERENCES image_library_cleanup_jobs(id);
CREATE INDEX IF NOT EXISTS image_result_cleanup_jobs_ready_idx
 ON image_library_cleanup_jobs(next_attempt_at,id) WHERE result_request IS NOT NULL AND status IN ('pending','running');
CREATE TABLE IF NOT EXISTS image_result_cleanup_objects (
 id BIGSERIAL PRIMARY KEY,
 job_id BIGINT NOT NULL REFERENCES image_library_cleanup_jobs(id),
 result_id BIGINT NOT NULL,
 storage_object_id BIGINT NOT NULL REFERENCES image_storage_objects(id),
 object_ref JSONB NOT NULL,
 verified_identity TEXT NOT NULL DEFAULT '',
 state VARCHAR(16) NOT NULL DEFAULT 'deleting' CHECK(state IN ('deleting','retry','deleted','missing','protected','skipped')),
 attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt_at TIMESTAMPTZ,
 last_error TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(job_id,result_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS image_result_cleanup_objects_active_idx
 ON image_result_cleanup_objects(storage_object_id) WHERE state IN ('deleting','retry');
CREATE INDEX IF NOT EXISTS image_result_cleanup_objects_job_idx
 ON image_result_cleanup_objects(job_id,state,next_attempt_at,id);
