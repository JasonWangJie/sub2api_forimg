-- Existing high-volume tables: build without blocking image generation writes.
CREATE INDEX CONCURRENTLY IF NOT EXISTS async_image_tasks_result_references_pending_idx
 ON async_image_tasks(id)
 WHERE result_references_checked=FALSE AND status NOT IN ('succeeded','failed','expired');
CREATE INDEX CONCURRENTLY IF NOT EXISTS async_image_results_object_identity_idx
 ON async_image_results(provider,bucket,object_key);
