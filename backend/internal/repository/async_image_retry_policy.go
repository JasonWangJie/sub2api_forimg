package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const asyncImageRetryVersionColumns = `revision, policy, actor_id, operation, rollback_from, created_at`

var _ service.AsyncImageRetryRepository = (*asyncImageTaskRepository)(nil)

func (r *asyncImageTaskRepository) GetAsyncImageRetryPolicy(ctx context.Context) (service.AsyncImageRetryPolicy, error) {
	return getAsyncImageRetryPolicy(ctx, r.sql, "")
}

func getAsyncImageRetryPolicy(ctx context.Context, db asyncImageSQLExecutor, lock string) (service.AsyncImageRetryPolicy, error) {
	var policy service.AsyncImageRetryPolicy
	var raw []byte
	var revision int64
	err := db.QueryRowContext(ctx, `
SELECT version.revision, version.policy
FROM async_image_retry_policy policy_row
JOIN async_image_retry_versions version ON version.revision = policy_row.revision
WHERE policy_row.singleton`+lock).Scan(&revision, &raw)
	if err != nil {
		return policy, err
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		return policy, fmt.Errorf("decode asynchronous image retry policy: %w", err)
	}
	policy.Revision = revision
	return policy, nil
}

func (r *asyncImageTaskRepository) SaveAsyncImageRetryPolicy(ctx context.Context, expected int64, policy service.AsyncImageRetryPolicy, actorID int64, rollbackFrom *int64) (service.AsyncImageRetryVersion, error) {
	var saved service.AsyncImageRetryVersion
	if expected <= 0 || actorID <= 0 {
		return saved, service.ErrAsyncImageInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return saved, err
	}
	defer func() { _ = tx.Rollback() }()

	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM async_image_retry_policy WHERE singleton FOR UPDATE`).Scan(&revision); err != nil {
		return saved, err
	}
	if revision != expected {
		return saved, service.ErrAsyncImageRetryRevisionConflict
	}
	operation := "save"
	if rollbackFrom != nil {
		version, err := getAsyncImageRetryVersion(ctx, tx, *rollbackFrom)
		if err != nil {
			return saved, err
		}
		operation, policy = "rollback", version.Policy
	}
	policy.Revision = revision + 1
	if policy.Rules == nil {
		policy.Rules = []service.AsyncImageRetryRule{}
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return saved, err
	}
	saved, err = scanAsyncImageRetryVersion(tx.QueryRowContext(ctx, `
INSERT INTO async_image_retry_versions (revision, policy, actor_id, operation, rollback_from)
VALUES ($1, $2::jsonb, $3, $4, $5) RETURNING `+asyncImageRetryVersionColumns,
		policy.Revision, raw, actorID, operation, rollbackFrom))
	if err != nil {
		return saved, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE async_image_retry_policy SET revision = $1 WHERE singleton`, policy.Revision); err != nil {
		return saved, err
	}
	return saved, tx.Commit()
}

func (r *asyncImageTaskRepository) GetAsyncImageRetryVersion(ctx context.Context, revision int64) (service.AsyncImageRetryVersion, error) {
	return getAsyncImageRetryVersion(ctx, r.sql, revision)
}

func getAsyncImageRetryVersion(ctx context.Context, db asyncImageSQLExecutor, revision int64) (service.AsyncImageRetryVersion, error) {
	version, err := scanAsyncImageRetryVersion(db.QueryRowContext(ctx,
		`SELECT `+asyncImageRetryVersionColumns+` FROM async_image_retry_versions WHERE revision = $1`, revision))
	if errors.Is(err, sql.ErrNoRows) {
		return version, service.ErrAsyncImageRetryVersionNotFound
	}
	return version, err
}

func (r *asyncImageTaskRepository) ListAsyncImageRetryVersions(ctx context.Context, limit, offset int) ([]service.AsyncImageRetryVersion, int64, error) {
	limit, offset = asyncImageRetryPage(limit, offset)
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM async_image_retry_versions`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+asyncImageRetryVersionColumns+` FROM async_image_retry_versions ORDER BY revision DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	versions := make([]service.AsyncImageRetryVersion, 0, limit)
	for rows.Next() {
		version, err := scanAsyncImageRetryVersion(rows)
		if err != nil {
			return nil, 0, err
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return versions, total, tx.Commit()
}

func scanAsyncImageRetryVersion(row interface{ Scan(...any) error }) (service.AsyncImageRetryVersion, error) {
	var version service.AsyncImageRetryVersion
	var raw []byte
	if err := row.Scan(&version.Revision, &raw, &version.ActorID, &version.Operation, &version.RollbackFrom, &version.CreatedAt); err != nil {
		return version, err
	}
	if err := json.Unmarshal(raw, &version.Policy); err != nil {
		return version, fmt.Errorf("decode asynchronous image retry version: %w", err)
	}
	version.Policy.Revision = version.Revision
	return version, nil
}

func asyncImageRetryPage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 20
	}
	return min(limit, 100), max(offset, 0)
}
