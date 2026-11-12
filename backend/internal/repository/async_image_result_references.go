package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func bindAsyncImageResultReferences(ctx context.Context, tx *sql.Tx, params service.CreateAsyncImageTaskParams) error {
	if !params.ResultReferencesChecked {
		return nil
	}
	if err := bindResultObjectReferences(ctx, tx, params.TaskID, params.ResultObjectReferences); err != nil {
		return err
	}
	if len(params.ReferenceImageURLs) == 0 {
		return nil
	} // The insert already marks reference-free tasks checked.
	_, err := tx.ExecContext(ctx, `UPDATE async_image_tasks SET result_references_checked=TRUE WHERE task_id=$1`, params.TaskID)
	return err
}

func bindResultObjectReferences(ctx context.Context, tx *sql.Tx, taskID string, refs []service.ObjectRef) error {
	// All callers acquire object locks in a stable order, including requests
	// referencing several results in a different user-supplied order.
	ordered := append([]service.ObjectRef(nil), refs...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		return a.Provider+"\x00"+a.Bucket+"\x00"+a.ObjectKey < b.Provider+"\x00"+b.Bucket+"\x00"+b.ObjectKey
	})
	for _, ref := range ordered {
		var objectID int64
		var state, identity string
		err := tx.QueryRowContext(ctx, `
SELECT id,state,storage_identity FROM image_storage_objects
WHERE provider=$1 AND bucket=$2 AND object_key=$3
FOR UPDATE`, ref.Provider, ref.Bucket, ref.ObjectKey).Scan(&objectID, &state, &identity)
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrAsyncImageInvalidInput
		}
		if err != nil {
			return err
		}
		if state != "active" || (identity != "" && identity != ref.StorageIdentity) {
			return service.ErrAsyncImageInvalidInput
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO async_image_task_result_references(task_id,storage_object_id)
VALUES($1,$2) ON CONFLICT DO NOTHING`, taskID, objectID); err != nil {
			return err
		}
	}
	return nil
}

func (r *asyncImageTaskRepository) BackfillAsyncImageResultReferences(ctx context.Context, cfg service.ImageResultReferenceConfig, limit int) (bool, error) {
	return backfillAsyncImageResultReferences(ctx, r.db, cfg, limit)
}

func (r *imageLibraryRepository) BackfillAsyncImageResultReferences(ctx context.Context, cfg service.ImageResultReferenceConfig, limit int) (bool, error) {
	return backfillAsyncImageResultReferences(ctx, r.db, cfg, limit)
}

// Backfill inspects only bounded, not-yet-checked live tasks. Cleanup must wait
// for done=true; failed parsing/lookup never marks a task as safely inspected.
func backfillAsyncImageResultReferences(ctx context.Context, db *sql.DB, cfg service.ImageResultReferenceConfig, limit int) (bool, error) {
	if db == nil {
		return false, errors.New("async image reference database unavailable")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `
SELECT task_id,reference_image_urls FROM async_image_tasks
WHERE result_references_checked=FALSE AND status NOT IN ('succeeded','failed','expired')
ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return false, err
	}
	type pendingTask struct {
		id   string
		urls []string
	}
	pending := make([]pendingTask, 0, limit)
	for rows.Next() {
		var task pendingTask
		var raw []byte
		if err := rows.Scan(&task.id, &raw); err != nil {
			_ = rows.Close()
			return false, err
		}
		if err := json.Unmarshal(raw, &task.urls); err != nil {
			_ = rows.Close()
			return false, err
		}
		pending = append(pending, task)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for _, task := range pending {
		refs, err := service.ResolveImageResultReferences(cfg, task.urls)
		if err != nil {
			return false, err
		}
		if err := bindResultObjectReferences(ctx, tx, task.id, refs); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE async_image_tasks SET result_references_checked=TRUE WHERE task_id=$1`, task.id); err != nil {
			return false, err
		}
	}
	var remaining bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM async_image_tasks WHERE result_references_checked=FALSE AND status NOT IN ('succeeded','failed','expired'))`).Scan(&remaining); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return !remaining, nil
}
