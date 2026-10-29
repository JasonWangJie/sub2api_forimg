package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *asyncImageTaskRepository) SaveAsyncImagePreparedRequest(ctx context.Context, taskID string, expectedVersion int64, encrypted []byte) error {
	if r == nil || r.db == nil {
		return errors.New("async image task repository is not configured")
	}
	if strings.TrimSpace(taskID) == "" || expectedVersion <= 0 || len(encrypted) == 0 || len(encrypted) > service.AsyncImagePreparedRequestMaxBytes {
		return service.ErrAsyncImageInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE async_image_tasks SET request_payload=$3, updated_at=NOW()
WHERE task_id=$1 AND version=$2 AND status='invoking' AND reference_transport='local' AND request_type='image_to_image'`, taskID, expectedVersion, encrypted)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrAsyncImageInvalidTransition
	}
	if err := appendAsyncImageEventWithSQL(ctx, tx, service.AsyncImageEvent{
		TaskID: taskID, EventType: "reference_snapshot_saved", Payload: json.RawMessage(`{"stage":"input","transport":"local"}`),
	}); err != nil {
		return err
	}
	return tx.Commit()
}
