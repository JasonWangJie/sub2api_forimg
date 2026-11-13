package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const accountRateLimitStateSelect = `SELECT id,status,schedulable,rate_limited_at,rate_limit_reset_at,overload_until,temp_unschedulable_until,temp_unschedulable_reason,COALESCE(extra->'model_rate_limits','null'::jsonb),COALESCE(extra->'antigravity_quota_scopes','null'::jsonb) FROM accounts WHERE id=$1 AND deleted_at IS NULL`

func readAccountRateLimitState(ctx context.Context, exec sqlExecutor, id int64, lock bool) (*service.AccountRateLimitState, error) {
	query := accountRateLimitStateSelect
	if lock {
		query += " FOR UPDATE"
	}
	rows, err := exec.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if rows.Err() != nil {
			return nil, rows.Err()
		}
		return nil, service.ErrAccountNotFound
	}
	s := &service.AccountRateLimitState{}
	if err := rows.Scan(&s.AccountID, &s.Status, &s.Schedulable, &s.RateLimitedAt, &s.RateLimitResetAt, &s.OverloadUntil, &s.TempUnschedulableUntil, &s.TempUnschedulableReason, &s.ModelRateLimits, &s.QuotaScopes); err != nil {
		return nil, err
	}
	return s, nil
}
func (r *accountRepository) GetAccountRateLimitState(ctx context.Context, id int64) (*service.AccountRateLimitState, error) {
	return readAccountRateLimitState(ctx, r.sql, id, false)
}
func (r *accountRepository) ClearAccountRateLimitState(ctx context.Context, id int64, expected string) (*service.AccountRateLimitState, *service.AccountRateLimitState, error) {
	beginner, ok := r.sql.(interface {
		BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	})
	if !ok {
		return nil, nil, fmt.Errorf("atomic restriction clear requires a transaction-capable database")
	}
	tx, err := beginner.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	before, err := readAccountRateLimitState(ctx, tx, id, true)
	if err != nil {
		return nil, nil, err
	}
	if expected != "" && before.Version() != expected {
		return nil, nil, service.ErrRateLimitStateChanged
	}
	// All restriction fields change together. Status, manual scheduling, expiry,
	// credentials and unrelated extra keys are deliberately preserved.
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET rate_limited_at=NULL,rate_limit_reset_at=NULL,overload_until=NULL,temp_unschedulable_until=NULL,temp_unschedulable_reason=NULL,extra=COALESCE(extra,'{}'::jsonb)-'model_rate_limits'-'antigravity_quota_scopes',updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return nil, nil, err
	}
	if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &id, nil, nil); err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, err
	}
	after := &service.AccountRateLimitState{AccountID: before.AccountID, Status: before.Status, Schedulable: before.Schedulable}
	return before, after, nil
}

// Invalidate instead of read-then-set: a concurrent newer limit must never be
// overwritten with a stale post-clear snapshot. The transactional outbox repairs it.
func (r *accountRepository) SyncAccountRateLimitSnapshot(ctx context.Context, id int64) error {
	if r.schedulerCache == nil {
		return nil
	}
	return r.schedulerCache.DeleteAccount(ctx, id)
}
