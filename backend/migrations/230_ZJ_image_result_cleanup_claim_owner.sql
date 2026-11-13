-- Bind each object request to the job lease that owns it. Existing tickets use
-- zero so the first upgraded worker can safely recover them on its next lease.
ALTER TABLE image_result_cleanup_objects
 ADD COLUMN IF NOT EXISTS claim_lease_version BIGINT NOT NULL DEFAULT 0;
