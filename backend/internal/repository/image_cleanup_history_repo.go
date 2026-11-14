package repository

import (
	"context"
	"time"

	"github.com/lib/pq"
)

// Active work and unresolved object tickets remain visible even after 24 hours:
// their records are recovery facts, rather than finished task history.
const imageCleanupHistoryVisibleSQL = `j.status IN ('pending','running','paused')
 OR COALESCE(j.finished_at,j.updated_at,j.created_at)>=NOW()-INTERVAL '24 hours'
 OR EXISTS(SELECT 1 FROM image_result_cleanup_objects o WHERE o.job_id=j.id AND o.state IN ('deleting','retry'))`

func normalizeImageCleanupHistoryLimit(limit int) int {
	if limit < 1 || limit > 5 {
		return 5
	}
	return limit
}

// PruneCleanupJobHistory removes only finished bookkeeping. It never removes
// results, storage objects, or the tickets needed to reconcile remote deletes.
// The existing queue lock serializes result-job resume and legacy child binding.
func (r *imageLibraryRepository) PruneCleanupJobHistory(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit < 1 || limit > 500 {
		limit = 500
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var locked bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(8017722801)`).Scan(&locked); err != nil || !locked {
		return 0, err
	}
	// Expired previews cannot authorize a new job. Bound their cleanup as well,
	// and skip previews held by an in-flight confirmation.
	if _, err = tx.ExecContext(ctx, `DELETE FROM image_result_cleanup_previews WHERE token_hash IN (
 SELECT token_hash FROM image_result_cleanup_previews WHERE expires_at<=NOW()
 ORDER BY expires_at,token_hash LIMIT $1 FOR UPDATE SKIP LOCKED)`, limit); err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT j.id FROM image_library_cleanup_jobs j
 WHERE j.status IN ('succeeded','failed','cancelled')
 AND COALESCE(j.finished_at,j.updated_at,j.created_at)<$1
 AND (j.result_lease_until IS NULL OR j.result_lease_until<=NOW())
 AND NOT EXISTS(SELECT 1 FROM image_result_cleanup_objects o WHERE o.job_id=j.id AND o.state IN ('deleting','retry'))
 AND NOT EXISTS(SELECT 1 FROM image_result_cleanup_previews p WHERE p.consumed_job_id=j.id)
 AND NOT EXISTS(SELECT 1 FROM image_library_cleanup_jobs parent WHERE parent.result_child_job_id=j.id)
 AND NOT EXISTS(SELECT 1 FROM image_library_cleanup_jobs child WHERE child.id=j.result_child_job_id AND (
  child.status IN ('pending','running','paused') OR child.result_lease_until>NOW()
  OR EXISTS(SELECT 1 FROM image_result_cleanup_objects o WHERE o.job_id=child.id AND o.state IN ('deleting','retry'))))
 ORDER BY COALESCE(j.finished_at,j.updated_at,j.created_at),j.id LIMIT $2 FOR UPDATE OF j SKIP LOCKED`, before.UTC(), limit)
	if err != nil {
		return 0, err
	}
	ids := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, tx.Commit()
	}
	// A single long-running cleanup can own many more tickets than jobs in this
	// batch. Reap at most 5,000 completed tickets and keep its job until drained.
	if _, err = tx.ExecContext(ctx, `DELETE FROM image_result_cleanup_objects WHERE id IN (
 SELECT o.id FROM image_result_cleanup_objects o
 WHERE o.job_id=ANY($1) AND o.state NOT IN ('deleting','retry')
 ORDER BY o.id LIMIT $2 FOR UPDATE OF o SKIP LOCKED)`, pq.Array(ids), 5000); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM image_library_cleanup_jobs j WHERE j.id=ANY($1)
 AND NOT EXISTS(SELECT 1 FROM image_result_cleanup_objects o WHERE o.job_id=j.id)`, pq.Array(ids))
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}
