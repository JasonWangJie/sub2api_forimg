package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

type asyncImageSQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type asyncImageTaskRepository struct {
	db  *sql.DB
	sql asyncImageSQLExecutor
}

func NewAsyncImageTaskRepository(db *sql.DB) service.AsyncImageTaskRepository {
	return &asyncImageTaskRepository{db: db, sql: db}
}

const asyncImageTaskColumns = `
id, task_id, user_id, api_key_id, group_id, account_id,
protocol, platform, request_type, model, status, billing_status, progress,
requested_image_size, actual_image_size, aspect_ratio, image_count, actual_cost, currency,
idempotency_key, request_hash, request_payload, prompt_preview,
upstream_request_id, billing_request_id, billing_payload,
retry_count, storage_retry_count, billing_retry_count, version,
error_code, error_message, submitted_at, started_at, upstream_succeeded_at,
finished_at, expires_at, created_at, updated_at`

const asyncImageTaskSummaryColumns = `
id, task_id, user_id, api_key_id, group_id, account_id,
protocol, platform, request_type, model, status, billing_status, progress,
requested_image_size, actual_image_size, aspect_ratio, image_count, actual_cost, currency,
idempotency_key, request_hash, NULL::bytea AS request_payload, prompt_preview,
upstream_request_id, billing_request_id, NULL::jsonb AS billing_payload,
retry_count, storage_retry_count, billing_retry_count, version,
error_code, error_message, submitted_at, started_at, upstream_succeeded_at,
finished_at, expires_at, created_at, updated_at`

func (r *asyncImageTaskRepository) CreateAsyncImageTask(ctx context.Context, params service.CreateAsyncImageTaskParams) (*service.AsyncImageTask, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, errors.New("async image task repository is not configured")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()

	task, err := insertAsyncImageTask(ctx, tx, params)
	if err != nil {
		_ = tx.Rollback()
		if isUniqueConstraintViolation(err) {
			return r.resolveAsyncImageCreateConflict(ctx, params)
		}
		return nil, false, err
	}
	if err := bindAsyncImageInputObjects(ctx, tx, params); err != nil {
		return nil, false, err
	}
	eventPayload := normalizeJSONPayload(params.OutboxPayload)
	if err := appendAsyncImageEventWithSQL(ctx, tx, service.AsyncImageEvent{
		TaskID: task.TaskID, EventType: "task_created", ToStatus: stringPointer(service.AsyncImageTaskStatusQueued), Payload: eventPayload,
	}); err != nil {
		return nil, false, err
	}
	if err := enqueueAsyncImageOutboxWithSQL(ctx, tx, service.AsyncImageOutboxEntry{
		TaskID: task.TaskID, EventType: "task_ready", DedupKey: task.TaskID + ":created", Payload: eventPayload,
	}); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return task, false, nil
}

func insertAsyncImageTask(ctx context.Context, sqlq asyncImageSQLExecutor, params service.CreateAsyncImageTaskParams) (*service.AsyncImageTask, error) {
	query := `
INSERT INTO async_image_tasks (
    task_id, user_id, api_key_id, group_id, protocol, platform, request_type,
    model, requested_image_size, aspect_ratio, image_count, idempotency_key,
    request_hash, request_payload, prompt_preview, expires_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12,
    $13, $14, $15, $16
)
RETURNING ` + asyncImageTaskColumns
	return scanAsyncImageTask(sqlq.QueryRowContext(ctx, query,
		params.TaskID, params.UserID, params.APIKeyID, params.GroupID,
		params.Protocol, params.Platform, params.RequestType, params.Model,
		params.RequestedImageSize, params.AspectRatio, params.ImageCount,
		params.IdempotencyKey, params.RequestHash, params.RequestPayload,
		params.PromptPreview, params.ExpiresAt,
	))
}

func (r *asyncImageTaskRepository) resolveAsyncImageCreateConflict(ctx context.Context, params service.CreateAsyncImageTaskParams) (*service.AsyncImageTask, bool, error) {
	if params.IdempotencyKey == nil || strings.TrimSpace(*params.IdempotencyKey) == "" {
		return nil, false, service.ErrAsyncImageTaskExists
	}
	task, err := scanAsyncImageTask(r.sql.QueryRowContext(ctx, `SELECT `+asyncImageTaskColumns+`
FROM async_image_tasks WHERE api_key_id = $1 AND idempotency_key = $2`, params.APIKeyID, *params.IdempotencyKey))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, service.ErrAsyncImageTaskExists
		}
		return nil, false, err
	}
	if task.RequestHash != params.RequestHash {
		return nil, false, service.ErrAsyncImageIdempotencyConflict
	}
	return task, true, nil
}

func (r *asyncImageTaskRepository) GetAsyncImageTaskByTaskID(ctx context.Context, taskID string) (*service.AsyncImageTask, error) {
	return getAsyncImageTask(ctx, r.sql, `task_id = $1`, taskID)
}

func (r *asyncImageTaskRepository) GetAsyncImageTaskForAPIKey(ctx context.Context, apiKeyID int64, taskID string) (*service.AsyncImageTask, error) {
	return getAsyncImageTask(ctx, r.sql, `api_key_id = $1 AND task_id = $2`, apiKeyID, taskID)
}

func (r *asyncImageTaskRepository) GetAsyncImageTaskForUser(ctx context.Context, userID int64, taskID string) (*service.AsyncImageTask, error) {
	return getAsyncImageTask(ctx, r.sql, `user_id = $1 AND task_id = $2`, userID, taskID)
}

func getAsyncImageTask(ctx context.Context, sqlq asyncImageSQLExecutor, where string, args ...any) (*service.AsyncImageTask, error) {
	task, err := scanAsyncImageTask(sqlq.QueryRowContext(ctx, `SELECT `+asyncImageTaskColumns+` FROM async_image_tasks WHERE `+where, args...))
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrAsyncImageTaskNotFound, nil)
	}
	return task, nil
}

func (r *asyncImageTaskRepository) ListAsyncImageTasks(ctx context.Context, filter service.AsyncImageTaskFilter) ([]*service.AsyncImageTask, int64, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	where, args := buildAsyncImageTaskFilter(filter)

	var total int64
	if err := r.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM async_image_tasks`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	selectArgs := append([]any(nil), args...)
	query := `SELECT ` + asyncImageTaskSummaryColumns + ` FROM async_image_tasks` + where +
		asyncImageTaskOrderBy(filter) + ` LIMIT $` + strconv.Itoa(len(selectArgs)+1) + ` OFFSET $` + strconv.Itoa(len(selectArgs)+2)
	selectArgs = append(selectArgs, limit, filter.Offset)
	rows, err := r.sql.QueryContext(ctx, query, selectArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	tasks, err := scanAsyncImageTasks(rows)
	return tasks, total, err
}

func buildAsyncImageTaskFilter(filter service.AsyncImageTaskFilter) (string, []any) {
	clauses := make([]string, 0, 12)
	args := make([]any, 0, 12)
	add := func(column, operator string, value any) {
		args = append(args, value)
		clauses = append(clauses, column+" "+operator+" $"+strconv.Itoa(len(args)))
	}
	if filter.UserID != nil {
		add("user_id", "=", *filter.UserID)
	}
	if filter.APIKeyID != nil {
		add("api_key_id", "=", *filter.APIKeyID)
	}
	if filter.GroupID != nil {
		add("group_id", "=", *filter.GroupID)
	}
	if filter.AccountID != nil {
		add("account_id", "=", *filter.AccountID)
	}
	if filter.TaskID != "" {
		add("task_id", "=", filter.TaskID)
	}
	if filter.Protocol != "" {
		add("protocol", "=", filter.Protocol)
	}
	if filter.Platform != "" {
		add("platform", "=", filter.Platform)
	}
	if filter.RequestType != "" {
		add("request_type", "=", filter.RequestType)
	}
	if filter.Status != "" {
		add("status", "=", filter.Status)
	}
	if filter.BillingStatus != "" {
		add("billing_status", "=", filter.BillingStatus)
	}
	if filter.Model != "" {
		add("model", "ILIKE", "%"+filter.Model+"%")
	}
	if filter.Search != "" {
		args = append(args, "%"+filter.Search+"%")
		placeholder := "$" + strconv.Itoa(len(args))
		clauses = append(clauses, "(task_id ILIKE "+placeholder+" OR model ILIKE "+placeholder+" OR COALESCE(prompt_preview, '') ILIKE "+placeholder+")")
	}
	if filter.StorageProvider != "" {
		args = append(args, filter.StorageProvider)
		clauses = append(clauses, "EXISTS (SELECT 1 FROM async_image_results air WHERE air.task_id = async_image_tasks.task_id AND air.provider = $"+strconv.Itoa(len(args))+")")
	}
	if filter.CreatedAfter != nil {
		add("created_at", ">=", *filter.CreatedAfter)
	}
	if filter.CreatedBefore != nil {
		add("created_at", "<", *filter.CreatedBefore)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func asyncImageTaskOrderBy(filter service.AsyncImageTaskFilter) string {
	column := "created_at"
	switch filter.SortBy {
	case "submitted_at", "started_at", "finished_at", "updated_at", "actual_cost", "status", "platform", "model":
		column = filter.SortBy
	}
	direction := "DESC"
	if filter.SortOrder == "asc" {
		direction = "ASC"
	}
	return " ORDER BY " + column + " " + direction + ", id " + direction
}

func (r *asyncImageTaskRepository) ListRecoverableAsyncImageTasks(
	ctx context.Context,
	statuses []string,
	updatedBefore time.Time,
	storageRetryLimit, billingRetryLimit, limit int,
) ([]*service.AsyncImageTask, error) {
	if len(statuses) == 0 {
		return []*service.AsyncImageTask{}, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if storageRetryLimit <= 0 {
		storageRetryLimit = int(^uint(0) >> 1)
	}
	if billingRetryLimit <= 0 {
		billingRetryLimit = int(^uint(0) >> 1)
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT `+asyncImageTaskSummaryColumns+`
FROM async_image_tasks
WHERE status = ANY($1) AND updated_at <= $2
	AND (status <> 'storage_failed' OR storage_retry_count < $3)
	AND (status <> 'billing_failed' OR billing_retry_count < $4)
ORDER BY updated_at, id
LIMIT $5`, pq.Array(statuses), updatedBefore, storageRetryLimit, billingRetryLimit, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanAsyncImageTasks(rows)
}

// ListTimedOutInvokingAsyncImageTasks returns invoking tasks whose wall-clock
// start (started_at, else created_at) is at or before startedBefore. Heartbeats
// refresh updated_at, so lease-based recovery alone cannot end hung upstream calls.
func (r *asyncImageTaskRepository) ListTimedOutInvokingAsyncImageTasks(
	ctx context.Context,
	startedBefore time.Time,
	limit int,
) ([]*service.AsyncImageTask, error) {
	if r == nil || r.sql == nil {
		return nil, service.ErrAsyncImageInvalidInput
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT `+asyncImageTaskSummaryColumns+`
FROM async_image_tasks
WHERE status = $1 AND COALESCE(started_at, created_at) <= $2
ORDER BY COALESCE(started_at, created_at), id
LIMIT $3`, service.AsyncImageTaskStatusInvoking, startedBefore, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanAsyncImageTasks(rows)
}

func (r *asyncImageTaskRepository) TouchAsyncImageTask(ctx context.Context, taskID string, statuses []string) error {
	if r == nil || r.sql == nil || strings.TrimSpace(taskID) == "" || len(statuses) == 0 {
		return service.ErrAsyncImageInvalidInput
	}
	_, err := r.sql.ExecContext(ctx, `
UPDATE async_image_tasks
SET updated_at = NOW()
WHERE task_id = $1 AND status = ANY($2)`, taskID, pq.Array(statuses))
	return err
}

func (r *asyncImageTaskRepository) TransitionAsyncImageTask(ctx context.Context, transition service.AsyncImageTaskTransition) (*service.AsyncImageTask, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("async image task repository is not configured")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var currentStatus string
	var currentVersion int64
	if err := tx.QueryRowContext(ctx, `SELECT status, version FROM async_image_tasks WHERE task_id = $1 FOR UPDATE`, transition.TaskID).Scan(&currentStatus, &currentVersion); err != nil {
		return nil, translatePersistenceError(err, service.ErrAsyncImageTaskNotFound, nil)
	}
	if transition.ExpectedVersion > 0 && transition.ExpectedVersion != currentVersion {
		return nil, service.ErrAsyncImageInvalidTransition
	}
	if len(transition.FromStatuses) > 0 && !containsString(transition.FromStatuses, currentStatus) {
		return nil, service.ErrAsyncImageInvalidTransition
	}
	if !service.CanTransitionAsyncImageTask(currentStatus, transition.ToStatus) {
		return nil, service.ErrAsyncImageInvalidTransition
	}
	if transition.Progress != nil && (*transition.Progress < 0 || *transition.Progress > 100) {
		return nil, service.ErrAsyncImageInvalidInput
	}
	if transition.ImageCount != nil && *transition.ImageCount < 0 {
		return nil, service.ErrAsyncImageInvalidInput
	}

	task, err := updateAsyncImageTaskTransition(ctx, tx, currentVersion, transition)
	if err != nil {
		return nil, err
	}
	eventType := strings.TrimSpace(transition.EventType)
	if eventType == "" {
		eventType = "status_changed"
	}
	if err := appendAsyncImageEventWithSQL(ctx, tx, service.AsyncImageEvent{
		TaskID: transition.TaskID, EventType: eventType,
		FromStatus: stringPointer(currentStatus), ToStatus: stringPointer(transition.ToStatus),
		Payload: normalizeJSONPayload(transition.EventPayload),
	}); err != nil {
		return nil, err
	}
	if transition.ToStatus == service.AsyncImageTaskStatusSucceeded {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO async_image_outbox(task_id,event_type,dedup_key,payload)
VALUES($1,'library_archive',$2,'{}'::jsonb)
ON CONFLICT(dedup_key) DO NOTHING`, transition.TaskID, transition.TaskID+":library_archive"); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return task, nil
}

func updateAsyncImageTaskTransition(ctx context.Context, sqlq asyncImageSQLExecutor, currentVersion int64, transition service.AsyncImageTaskTransition) (*service.AsyncImageTask, error) {
	billingPayloadSet := len(transition.BillingPayload) > 0
	billingPayload := normalizeJSONPayload(transition.BillingPayload)
	query := `
UPDATE async_image_tasks SET
    status = $2,
    progress = CASE WHEN $3::boolean THEN $4 ELSE progress END,
    account_id = CASE WHEN $5::boolean THEN $6 ELSE account_id END,
    billing_status = CASE WHEN $7::boolean THEN $8 ELSE billing_status END,
    actual_cost = CASE WHEN $9::boolean THEN $10 ELSE actual_cost END,
    actual_image_size = CASE WHEN $11::boolean THEN $12 ELSE actual_image_size END,
    image_count = CASE WHEN $13::boolean THEN $14 ELSE image_count END,
    upstream_request_id = CASE WHEN $15::boolean THEN $16 ELSE upstream_request_id END,
    billing_request_id = CASE WHEN $17::boolean THEN $18 ELSE billing_request_id END,
    billing_payload = CASE WHEN $19::boolean THEN $20::jsonb ELSE billing_payload END,
    error_code = CASE WHEN $21::boolean THEN NULL WHEN $22::boolean THEN $23 ELSE error_code END,
    error_message = CASE WHEN $21::boolean THEN NULL WHEN $24::boolean THEN $25 ELSE error_message END,
    retry_count = retry_count + CASE WHEN $26::boolean OR $27::boolean OR $28::boolean THEN 1 ELSE 0 END,
    storage_retry_count = storage_retry_count + CASE WHEN $27::boolean THEN 1 ELSE 0 END,
    billing_retry_count = billing_retry_count + CASE WHEN $28::boolean THEN 1 ELSE 0 END,
    started_at = CASE WHEN $29::boolean THEN $30 ELSE started_at END,
    upstream_succeeded_at = CASE WHEN $31::boolean THEN $32 ELSE upstream_succeeded_at END,
    finished_at = CASE WHEN $33::boolean THEN $34 ELSE finished_at END,
    expires_at = CASE WHEN $35::boolean THEN $36 ELSE expires_at END,
    request_payload = CASE WHEN $37::boolean THEN ''::bytea ELSE request_payload END,
    version = version + 1,
    updated_at = NOW()
WHERE task_id = $1
  AND version = $38
  AND ($39::boolean = false OR updated_at <= $40)
RETURNING ` + asyncImageTaskColumns
	task, err := scanAsyncImageTask(sqlq.QueryRowContext(ctx, query,
		transition.TaskID, transition.ToStatus,
		transition.Progress != nil, transition.Progress,
		transition.AccountID != nil, transition.AccountID,
		transition.BillingStatus != nil, transition.BillingStatus,
		transition.ActualCost != nil, transition.ActualCost,
		transition.ActualImageSize != nil, transition.ActualImageSize,
		transition.ImageCount != nil, transition.ImageCount,
		transition.UpstreamRequestID != nil, transition.UpstreamRequestID,
		transition.BillingRequestID != nil, transition.BillingRequestID,
		billingPayloadSet, billingPayload,
		transition.ClearError, transition.ErrorCode != nil, transition.ErrorCode,
		transition.ErrorMessage != nil, transition.ErrorMessage,
		transition.IncrementRetry,
		transition.IncrementStorageRetry,
		transition.IncrementBillingRetry,
		transition.StartedAt != nil, transition.StartedAt,
		transition.UpstreamSucceededAt != nil, transition.UpstreamSucceededAt,
		transition.FinishedAt != nil, transition.FinishedAt,
		transition.ExpiresAt != nil, transition.ExpiresAt,
		transition.ClearRequestPayload, currentVersion,
		transition.UpdatedBefore != nil, transition.UpdatedBefore,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAsyncImageInvalidTransition
	}
	return task, err
}

func (r *asyncImageTaskRepository) RecordAsyncImageUpstreamSuccess(ctx context.Context, params service.RecordAsyncImageUpstreamSuccessParams) (*service.AsyncImageTask, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("async image task repository is not configured")
	}
	if !json.Valid(params.BillingPayload) || len(params.StagingObjects) == 0 {
		return nil, service.ErrAsyncImageInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var currentStatus string
	var currentVersion int64
	if err := tx.QueryRowContext(ctx, `SELECT status, version FROM async_image_tasks WHERE task_id = $1 FOR UPDATE`, params.TaskID).Scan(&currentStatus, &currentVersion); err != nil {
		return nil, translatePersistenceError(err, service.ErrAsyncImageTaskNotFound, nil)
	}
	if currentStatus != service.AsyncImageTaskStatusInvoking || (params.ExpectedVersion > 0 && params.ExpectedVersion != currentVersion) {
		return nil, service.ErrAsyncImageInvalidTransition
	}
	if err := validateAsyncImageStagingObjects(params.TaskID, params.StagingObjects); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM async_image_staging_objects WHERE task_id = $1`, params.TaskID); err != nil {
		return nil, err
	}
	for i := range params.StagingObjects {
		object := params.StagingObjects[i]
		if _, err := tx.ExecContext(ctx, `
INSERT INTO async_image_staging_objects (
    task_id, image_index, content, content_type, byte_size, checksum, width, height, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			params.TaskID, object.ImageIndex, object.Content, object.ContentType,
			object.ByteSize, object.Checksum, object.Width, object.Height, object.ExpiresAt,
		); err != nil {
			return nil, err
		}
	}
	actualImageSizeSet := params.ActualImageSize != nil
	task, err := scanAsyncImageTask(tx.QueryRowContext(ctx, `
UPDATE async_image_tasks SET
    status = 'upstream_succeeded',
    billing_status = 'prepared',
    progress = GREATEST(progress, 60),
    account_id = $2,
    upstream_request_id = $3,
    actual_image_size = CASE WHEN $4::boolean THEN $5 ELSE actual_image_size END,
    image_count = $6,
    billing_request_id = $7,
    billing_payload = $8::jsonb,
    upstream_succeeded_at = $9,
    error_code = NULL,
    error_message = NULL,
    request_payload = ''::bytea,
    version = version + 1,
    updated_at = NOW()
WHERE task_id = $1 AND version = $10
RETURNING `+asyncImageTaskColumns,
		params.TaskID, params.AccountID, params.UpstreamRequestID,
		actualImageSizeSet, params.ActualImageSize, params.ImageCount,
		params.BillingRequestID, params.BillingPayload, params.UpstreamSucceededAt, currentVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAsyncImageInvalidTransition
	}
	if err != nil {
		return nil, err
	}
	if err := appendAsyncImageEventWithSQL(ctx, tx, service.AsyncImageEvent{
		TaskID: params.TaskID, EventType: "upstream_succeeded",
		FromStatus: stringPointer(currentStatus), ToStatus: stringPointer(service.AsyncImageTaskStatusUpstreamSucceeded),
		Payload: normalizeJSONPayload(params.EventPayload),
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return task, nil
}

func validateAsyncImageStagingObjects(taskID string, objects []service.AsyncImageStagingObject) error {
	seen := make(map[int]struct{}, len(objects))
	for i := range objects {
		object := &objects[i]
		if object.TaskID != "" && object.TaskID != taskID {
			return service.ErrAsyncImageInvalidInput
		}
		if object.ImageIndex < 0 || len(object.Content) == 0 || strings.TrimSpace(object.ContentType) == "" || object.ByteSize != int64(len(object.Content)) || strings.TrimSpace(object.Checksum) == "" || object.ExpiresAt.IsZero() {
			return service.ErrAsyncImageInvalidInput
		}
		if _, exists := seen[object.ImageIndex]; exists {
			return service.ErrAsyncImageInvalidInput
		}
		seen[object.ImageIndex] = struct{}{}
	}
	return nil
}

func (r *asyncImageTaskRepository) ReplaceAsyncImageResults(ctx context.Context, taskID string, results []service.AsyncImageResult) error {
	if r == nil || r.db == nil {
		return errors.New("async image task repository is not configured")
	}
	if err := validateAsyncImageResults(taskID, results); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM async_image_tasks WHERE task_id = $1)`, taskID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return service.ErrAsyncImageTaskNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM async_image_results WHERE task_id = $1`, taskID); err != nil {
		return err
	}
	for i := range results {
		result := results[i]
		var storageObjectID int64
		if err := tx.QueryRowContext(ctx, `
INSERT INTO image_storage_objects (
    provider, bucket, object_key, content_type, byte_size, checksum_sha256,
    width, height
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (provider, bucket, object_key) DO UPDATE SET
    content_type=EXCLUDED.content_type, byte_size=EXCLUDED.byte_size,
    checksum_sha256=EXCLUDED.checksum_sha256, width=EXCLUDED.width,
    height=EXCLUDED.height, state='active', deletion_claimed_at=NULL,
    deleted_at=NULL, updated_at=NOW()
RETURNING id`,
			result.Provider, result.Bucket, result.ObjectKey, result.ContentType,
			result.ByteSize, result.Checksum, result.Width, result.Height,
		).Scan(&storageObjectID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO async_image_results (
    task_id, image_index, provider, bucket, object_key, content_type,
    byte_size, checksum, width, height, storage_object_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			taskID, result.ImageIndex, result.Provider, result.Bucket, result.ObjectKey,
			result.ContentType, result.ByteSize, result.Checksum, result.Width, result.Height, storageObjectID,
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM async_image_result_upload_intents WHERE task_id=$1`, taskID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *asyncImageTaskRepository) PrepareAsyncImageResultUploadIntents(
	ctx context.Context,
	taskID string,
	intents []service.AsyncImageResultUploadIntent,
) error {
	if r == nil || r.db == nil || strings.TrimSpace(taskID) == "" || len(intents) == 0 {
		return service.ErrAsyncImageInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM async_image_tasks WHERE task_id=$1 FOR UPDATE`, taskID).Scan(&status); err != nil {
		return translatePersistenceError(err, service.ErrAsyncImageTaskNotFound, nil)
	}
	if status != service.AsyncImageTaskStatusUploading {
		return service.ErrAsyncImageInvalidTransition
	}
	seen := make(map[int]struct{}, len(intents))
	for _, intent := range intents {
		ref := intent.ObjectRef
		if (intent.TaskID != "" && intent.TaskID != taskID) || intent.ImageIndex < 0 ||
			strings.TrimSpace(ref.Provider) == "" || strings.TrimSpace(ref.Bucket) == "" || strings.TrimSpace(ref.ObjectKey) == "" ||
			strings.TrimSpace(ref.ContentType) == "" || ref.SizeBytes <= 0 || strings.TrimSpace(ref.ChecksumSHA256) == "" || intent.ExpiresAt.IsZero() {
			return service.ErrAsyncImageInvalidInput
		}
		if _, exists := seen[intent.ImageIndex]; exists {
			return service.ErrAsyncImageInvalidInput
		}
		seen[intent.ImageIndex] = struct{}{}
		var persistedTaskID string
		err := tx.QueryRowContext(ctx, `
INSERT INTO async_image_result_upload_intents AS existing(
    task_id,image_index,provider,bucket,object_key,content_type,byte_size,checksum_sha256,expires_at
) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT(task_id,image_index) DO UPDATE SET
	expires_at=GREATEST(existing.expires_at,EXCLUDED.expires_at),
	cleanup_claimed_at=NULL,updated_at=NOW()
WHERE existing.provider=EXCLUDED.provider
  AND existing.bucket=EXCLUDED.bucket
  AND existing.object_key=EXCLUDED.object_key
  AND existing.content_type=EXCLUDED.content_type
  AND existing.byte_size=EXCLUDED.byte_size
  AND existing.checksum_sha256=EXCLUDED.checksum_sha256
RETURNING task_id`,
			taskID, intent.ImageIndex, ref.Provider, ref.Bucket, ref.ObjectKey,
			ref.ContentType, ref.SizeBytes, ref.ChecksumSHA256, intent.ExpiresAt,
		).Scan(&persistedTaskID)
		if err == sql.ErrNoRows {
			return service.ErrAsyncImageInvalidTransition
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validateAsyncImageResults(taskID string, results []service.AsyncImageResult) error {
	seen := make(map[int]struct{}, len(results))
	for i := range results {
		result := &results[i]
		if result.TaskID != "" && result.TaskID != taskID {
			return service.ErrAsyncImageInvalidInput
		}
		if result.ImageIndex < 0 || strings.TrimSpace(result.Provider) == "" || strings.TrimSpace(result.Bucket) == "" || strings.TrimSpace(result.ObjectKey) == "" || strings.TrimSpace(result.ContentType) == "" || result.ByteSize < 0 || strings.TrimSpace(result.Checksum) == "" {
			return service.ErrAsyncImageInvalidInput
		}
		if _, exists := seen[result.ImageIndex]; exists {
			return service.ErrAsyncImageInvalidInput
		}
		seen[result.ImageIndex] = struct{}{}
	}
	return nil
}

func (r *asyncImageTaskRepository) ListAsyncImageResults(ctx context.Context, taskID string) ([]service.AsyncImageResult, error) {
	rows, err := r.sql.QueryContext(ctx, `
SELECT id, task_id, image_index, provider, bucket, object_key, content_type,
       byte_size, checksum, width, height, created_at
FROM async_image_results WHERE task_id = $1 ORDER BY image_index, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	results := make([]service.AsyncImageResult, 0)
	for rows.Next() {
		var result service.AsyncImageResult
		var width, height sql.NullInt64
		if err := rows.Scan(
			&result.ID, &result.TaskID, &result.ImageIndex, &result.Provider,
			&result.Bucket, &result.ObjectKey, &result.ContentType, &result.ByteSize,
			&result.Checksum, &width, &height, &result.CreatedAt,
		); err != nil {
			return nil, err
		}
		result.Width = nullableInt(width)
		result.Height = nullableInt(height)
		results = append(results, result)
	}
	return results, rows.Err()
}

func (r *asyncImageTaskRepository) ListAsyncImageStagingObjects(ctx context.Context, taskID string) ([]service.AsyncImageStagingObject, error) {
	rows, err := r.sql.QueryContext(ctx, `
SELECT id, task_id, image_index, content, content_type, byte_size, checksum,
       width, height, created_at, expires_at
FROM async_image_staging_objects WHERE task_id = $1 ORDER BY image_index, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	objects := make([]service.AsyncImageStagingObject, 0)
	for rows.Next() {
		var object service.AsyncImageStagingObject
		var width, height sql.NullInt64
		if err := rows.Scan(
			&object.ID, &object.TaskID, &object.ImageIndex, &object.Content,
			&object.ContentType, &object.ByteSize, &object.Checksum, &width,
			&height, &object.CreatedAt, &object.ExpiresAt,
		); err != nil {
			return nil, err
		}
		object.Width = nullableInt(width)
		object.Height = nullableInt(height)
		objects = append(objects, object)
	}
	return objects, rows.Err()
}

func (r *asyncImageTaskRepository) DeleteAsyncImageStagingObjects(ctx context.Context, taskID string) error {
	_, err := r.sql.ExecContext(ctx, `DELETE FROM async_image_staging_objects WHERE task_id = $1`, taskID)
	return err
}

func (r *asyncImageTaskRepository) DeleteExpiredAsyncImageStagingObjects(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 || limit > 10000 {
		limit = 500
	}
	result, err := r.sql.ExecContext(ctx, `
WITH doomed AS (
    SELECT id FROM async_image_staging_objects
    WHERE expires_at <= $1 ORDER BY expires_at, id LIMIT $2
)
DELETE FROM async_image_staging_objects s USING doomed d WHERE s.id = d.id`, before, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r *asyncImageTaskRepository) AppendAsyncImageEvent(ctx context.Context, event service.AsyncImageEvent) error {
	return appendAsyncImageEventWithSQL(ctx, r.sql, event)
}

func appendAsyncImageEventWithSQL(ctx context.Context, sqlq asyncImageSQLExecutor, event service.AsyncImageEvent) error {
	if strings.TrimSpace(event.TaskID) == "" || strings.TrimSpace(event.EventType) == "" {
		return service.ErrAsyncImageInvalidInput
	}
	_, err := sqlq.ExecContext(ctx, `
INSERT INTO async_image_events (task_id, event_type, from_status, to_status, payload)
VALUES ($1, $2, $3, $4, $5::jsonb)`,
		event.TaskID, event.EventType, event.FromStatus, event.ToStatus, normalizeJSONPayload(event.Payload),
	)
	return translatePersistenceError(err, service.ErrAsyncImageTaskNotFound, nil)
}

func (r *asyncImageTaskRepository) ListAsyncImageEvents(ctx context.Context, taskID string) ([]service.AsyncImageEvent, error) {
	rows, err := r.sql.QueryContext(ctx, `
SELECT id, task_id, event_type, from_status, to_status, payload, created_at
FROM async_image_events WHERE task_id = $1 ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	events := make([]service.AsyncImageEvent, 0)
	for rows.Next() {
		var event service.AsyncImageEvent
		var fromStatus, toStatus sql.NullString
		var payload []byte
		if err := rows.Scan(
			&event.ID, &event.TaskID, &event.EventType, &fromStatus,
			&toStatus, &payload, &event.CreatedAt,
		); err != nil {
			return nil, err
		}
		event.FromStatus = nullableString(fromStatus)
		event.ToStatus = nullableString(toStatus)
		event.Payload = append(json.RawMessage(nil), payload...)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (r *asyncImageTaskRepository) EnqueueAsyncImageOutbox(ctx context.Context, entry service.AsyncImageOutboxEntry) error {
	return enqueueAsyncImageOutboxWithSQL(ctx, r.sql, entry)
}

func enqueueAsyncImageOutboxWithSQL(ctx context.Context, sqlq asyncImageSQLExecutor, entry service.AsyncImageOutboxEntry) error {
	if strings.TrimSpace(entry.TaskID) == "" || strings.TrimSpace(entry.EventType) == "" || strings.TrimSpace(entry.DedupKey) == "" {
		return service.ErrAsyncImageInvalidInput
	}
	availableAt := entry.AvailableAt
	if availableAt.IsZero() {
		availableAt = time.Now().UTC()
	}
	_, err := sqlq.ExecContext(ctx, `
INSERT INTO async_image_outbox (task_id, event_type, dedup_key, payload, available_at)
VALUES ($1, $2, $3, $4::jsonb, $5)
ON CONFLICT (dedup_key) DO NOTHING`,
		entry.TaskID, entry.EventType, entry.DedupKey, normalizeJSONPayload(entry.Payload), availableAt,
	)
	return translatePersistenceError(err, service.ErrAsyncImageTaskNotFound, nil)
}

func (r *asyncImageTaskRepository) ClaimAsyncImageOutbox(ctx context.Context, limit int, staleBefore time.Time) ([]service.AsyncImageOutboxEntry, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("async image task repository is not configured")
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if staleBefore.IsZero() {
		staleBefore = time.Now().UTC().Add(-time.Minute)
	}
	claimToken := uuid.NewString()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `
WITH claimable AS (
    SELECT id
    FROM async_image_outbox
    WHERE published_at IS NULL
      AND available_at <= NOW()
      AND (claimed_at IS NULL OR claimed_at < $1)
    ORDER BY available_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT $2
)
UPDATE async_image_outbox o
SET claimed_at = NOW(), claim_token = $3, attempts = o.attempts + 1, updated_at = NOW()
FROM claimable c
WHERE o.id = c.id
RETURNING o.id, o.task_id, o.event_type, o.dedup_key, o.payload,
	          o.attempts, o.available_at, o.claimed_at, o.claim_token, o.published_at,
	          o.last_error, o.created_at, o.updated_at`, staleBefore, limit, claimToken)
	if err != nil {
		return nil, err
	}
	entries, err := scanAsyncImageOutboxEntries(rows)
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *asyncImageTaskRepository) MarkAsyncImageOutboxPublished(ctx context.Context, id int64, claimToken string, publishedAt time.Time) error {
	if strings.TrimSpace(claimToken) == "" {
		return service.ErrAsyncImageOutboxClaimLost
	}
	if publishedAt.IsZero() {
		publishedAt = time.Now().UTC()
	}
	result, err := r.sql.ExecContext(ctx, `
UPDATE async_image_outbox
SET published_at = $2, claimed_at = NULL, claim_token = NULL, last_error = NULL, updated_at = $2
WHERE id = $1 AND published_at IS NULL AND claim_token = $3`, id, publishedAt, claimToken)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrAsyncImageOutboxClaimLost
	}
	return nil
}

func (r *asyncImageTaskRepository) MarkAsyncImageOutboxFailed(ctx context.Context, id int64, claimToken string, availableAt time.Time, message string) error {
	if strings.TrimSpace(claimToken) == "" {
		return service.ErrAsyncImageOutboxClaimLost
	}
	if availableAt.IsZero() {
		availableAt = time.Now().UTC()
	}
	result, err := r.sql.ExecContext(ctx, `
UPDATE async_image_outbox
SET available_at = $2, claimed_at = NULL, claim_token = NULL, last_error = $3, updated_at = NOW()
WHERE id = $1 AND published_at IS NULL AND claim_token = $4`, id, availableAt, message, claimToken)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrAsyncImageOutboxClaimLost
	}
	return nil
}

func (r *asyncImageTaskRepository) MarkAsyncImageOutboxTerminal(ctx context.Context, id int64, claimToken string, publishedAt time.Time, message string) error {
	if strings.TrimSpace(claimToken) == "" {
		return service.ErrAsyncImageOutboxClaimLost
	}
	if publishedAt.IsZero() {
		publishedAt = time.Now().UTC()
	}
	result, err := r.sql.ExecContext(ctx, `
UPDATE async_image_outbox
SET published_at = $2, claimed_at = NULL, claim_token = NULL, last_error = $3, updated_at = $2
WHERE id = $1 AND published_at IS NULL AND claim_token = $4`, id, publishedAt, truncateString(message, 2000), claimToken)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrAsyncImageOutboxClaimLost
	}
	return nil
}

// EnqueueMissingAsyncImageLibraryArchives backfills tasks that predate the
// transactional library_archive event and repairs the former publish-before-
// archive delivery window. Terminal failures keep last_error and are never
// resurrected; deleted library rows also remain authoritative tombstones.
func (r *asyncImageTaskRepository) EnqueueMissingAsyncImageLibraryArchives(ctx context.Context, limit int) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("async image task repository is not configured")
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	result, err := r.sql.ExecContext(ctx, `
WITH reactivated AS (
  UPDATE async_image_outbox o
  SET published_at=NULL,claimed_at=NULL,claim_token=NULL,available_at=NOW(),last_error=NULL,updated_at=NOW()
  WHERE o.id IN (
    SELECT existing.id
    FROM async_image_outbox existing
    JOIN async_image_tasks t ON t.task_id=existing.task_id
    WHERE existing.event_type='library_archive'
      AND existing.published_at IS NOT NULL AND existing.last_error IS NULL
      AND t.status='succeeded' AND t.billing_status IN ('succeeded','not_billable')
      AND EXISTS (
        SELECT 1 FROM async_image_results r
        WHERE r.task_id=t.task_id
          AND COALESCE(r.library_validation_status,'pending')<>'quarantined'
          AND NOT EXISTS (
            SELECT 1 FROM image_library_items i
            WHERE i.user_id=t.user_id AND i.source_task_id=t.task_id
              AND i.source_result_index=r.image_index
          )
      )
    ORDER BY existing.id
    LIMIT $1
  )
  RETURNING o.id
)
INSERT INTO async_image_outbox(task_id,event_type,dedup_key,payload)
SELECT t.task_id,'library_archive',t.task_id || ':library_archive','{}'::jsonb
FROM async_image_tasks t
WHERE t.status='succeeded'
  AND t.billing_status IN ('succeeded','not_billable')
  AND EXISTS (
    SELECT 1 FROM async_image_results r
    WHERE r.task_id=t.task_id
      AND COALESCE(r.library_validation_status,'pending')<>'quarantined'
      AND NOT EXISTS (
        SELECT 1 FROM image_library_items i
        WHERE i.user_id=t.user_id AND i.source_task_id=t.task_id
          AND i.source_result_index=r.image_index
      )
  )
  AND NOT EXISTS (
    SELECT 1 FROM async_image_outbox o
    WHERE o.dedup_key=t.task_id || ':library_archive'
  )
ORDER BY t.id
LIMIT $1
ON CONFLICT(dedup_key) DO NOTHING`, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func scanAsyncImageOutboxEntries(rows *sql.Rows) ([]service.AsyncImageOutboxEntry, error) {
	entries := make([]service.AsyncImageOutboxEntry, 0)
	for rows.Next() {
		var entry service.AsyncImageOutboxEntry
		var payload []byte
		var claimedAt, publishedAt sql.NullTime
		var claimToken sql.NullString
		var lastError sql.NullString
		if err := rows.Scan(
			&entry.ID, &entry.TaskID, &entry.EventType, &entry.DedupKey,
			&payload, &entry.Attempts, &entry.AvailableAt, &claimedAt, &claimToken,
			&publishedAt, &lastError, &entry.CreatedAt, &entry.UpdatedAt,
		); err != nil {
			return nil, err
		}
		entry.Payload = append(json.RawMessage(nil), payload...)
		entry.ClaimedAt = nullableTime(claimedAt)
		if claimToken.Valid {
			entry.ClaimToken = claimToken.String
		}
		entry.PublishedAt = nullableTime(publishedAt)
		entry.LastError = nullableString(lastError)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

type asyncImageRowScanner interface {
	Scan(dest ...any) error
}

func scanAsyncImageTask(scanner asyncImageRowScanner) (*service.AsyncImageTask, error) {
	var task service.AsyncImageTask
	var accountID sql.NullInt64
	var requestedImageSize, actualImageSize, aspectRatio sql.NullString
	var actualCost sql.NullFloat64
	var idempotencyKey, promptPreview sql.NullString
	var upstreamRequestID, billingRequestID sql.NullString
	var errorCode, errorMessage sql.NullString
	var requestPayload, billingPayload []byte
	var startedAt, upstreamSucceededAt, finishedAt, expiresAt sql.NullTime
	if err := scanner.Scan(
		&task.ID, &task.TaskID, &task.UserID, &task.APIKeyID, &task.GroupID, &accountID,
		&task.Protocol, &task.Platform, &task.RequestType, &task.Model, &task.Status,
		&task.BillingStatus, &task.Progress, &requestedImageSize, &actualImageSize,
		&aspectRatio, &task.ImageCount, &actualCost, &task.Currency, &idempotencyKey,
		&task.RequestHash, &requestPayload, &promptPreview, &upstreamRequestID,
		&billingRequestID, &billingPayload,
		&task.RetryCount, &task.StorageRetryCount, &task.BillingRetryCount, &task.Version,
		&errorCode, &errorMessage, &task.SubmittedAt, &startedAt,
		&upstreamSucceededAt, &finishedAt, &expiresAt, &task.CreatedAt, &task.UpdatedAt,
	); err != nil {
		return nil, err
	}
	task.AccountID = nullableInt64(accountID)
	task.RequestedImageSize = nullableString(requestedImageSize)
	task.ActualImageSize = nullableString(actualImageSize)
	task.AspectRatio = nullableString(aspectRatio)
	task.ActualCost = nullableFloat64(actualCost)
	task.IdempotencyKey = nullableString(idempotencyKey)
	task.RequestPayload = append([]byte(nil), requestPayload...)
	task.PromptPreview = nullableString(promptPreview)
	task.UpstreamRequestID = nullableString(upstreamRequestID)
	task.BillingRequestID = nullableString(billingRequestID)
	task.BillingPayload = append(json.RawMessage(nil), billingPayload...)
	task.ErrorCode = nullableString(errorCode)
	task.ErrorMessage = nullableString(errorMessage)
	task.StartedAt = nullableTime(startedAt)
	task.UpstreamSucceededAt = nullableTime(upstreamSucceededAt)
	task.FinishedAt = nullableTime(finishedAt)
	task.ExpiresAt = nullableTime(expiresAt)
	return &task, nil
}

func scanAsyncImageTasks(rows *sql.Rows) ([]*service.AsyncImageTask, error) {
	tasks := make([]*service.AsyncImageTask, 0)
	for rows.Next() {
		task, err := scanAsyncImageTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func normalizeJSONPayload(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 || !json.Valid(payload) {
		return json.RawMessage(`{}`)
	}
	return payload
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullableInt(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	converted := int(value.Int64)
	return &converted
}

func nullableFloat64(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func nullableTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func stringPointer(value string) *string {
	return &value
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// Compile-time guard keeps additions to the service port visible here.
var _ service.AsyncImageTaskRepository = (*asyncImageTaskRepository)(nil)
