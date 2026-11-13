package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// SnapshotImageAccountCapacity samples the exact routing key on local capacity
// failure. It intentionally does not fetch credentials or extra JSON.
// This is labelled as a DB observation; cached selection may see different state.
func (r *accountRepository) SnapshotImageAccountCapacity(ctx context.Context, groupID int64, mode, model, tier string, platforms []string) (service.ImageCapacityObservation, error) {
	result := service.ImageCapacityObservation{Source: "pool_snapshot", ObservedAt: time.Now().UTC(), PoolMode: mode, PoolModel: model, PoolSizeTier: tier, Reasons: []service.ImageCapacityReason{}}
	scope := `EXISTS (SELECT 1 FROM group_image_size_accounts p WHERE p.account_id=a.id AND p.group_id=$1 AND p.model=$2 AND p.size_tier=$3)`
	args := []any{groupID, model, tier}
	if mode == "default" {
		scope = `EXISTS (SELECT 1 FROM account_groups g WHERE g.account_id=a.id AND g.group_id=$1)`
		args = []any{groupID}
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT a.id,a.status,a.schedulable,a.platform,a.deleted_at,a.expires_at,a.auto_pause_on_expired,a.rate_limit_reset_at,a.overload_until,a.temp_unschedulable_until,COUNT(*) OVER() FROM accounts a WHERE `+scope+` ORDER BY a.id LIMIT 256`, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	reasons := make(map[string]*service.ImageCapacityReason)
	observed := 0
	for rows.Next() {
		var id int64
		var status, platform string
		var schedulable, autoPause bool
		var deleted, expires, limited, overload, temporary sql.NullTime
		if err := rows.Scan(&id, &status, &schedulable, &platform, &deleted, &expires, &autoPause, &limited, &overload, &temporary, &result.CandidateCount); err != nil {
			return result, err
		}
		observed++
		code := "eligible_at_snapshot"
		var recovery *time.Time
		supported := len(platforms) == 0
		for _, p := range platforms {
			if platform == p {
				supported = true
				break
			}
		}
		switch {
		case deleted.Valid || status != service.StatusActive:
			code = "inactive"
		case !schedulable:
			code = "scheduling_disabled"
		case !supported:
			code = "platform_mismatch"
		case autoPause && expires.Valid && !expires.Time.After(result.ObservedAt):
			code = "expired"
		case temporary.Valid && temporary.Time.After(result.ObservedAt):
			code = "temporarily_unschedulable"
			recovery = &temporary.Time
		case overload.Valid && overload.Time.After(result.ObservedAt):
			code = "overloaded"
			recovery = &overload.Time
		case limited.Valid && limited.Time.After(result.ObservedAt):
			code = "rate_limited"
			recovery = &limited.Time
		}
		entry := reasons[code]
		if entry == nil {
			entry = &service.ImageCapacityReason{Code: code}
			reasons[code] = entry
		}
		entry.Count++
		if len(entry.AccountIDs) < 20 {
			entry.AccountIDs = append(entry.AccountIDs, id)
		}
		if recovery != nil && (entry.EarliestRecoveryAt == nil || recovery.Before(*entry.EarliestRecoveryAt)) {
			value := recovery.UTC()
			entry.EarliestRecoveryAt = &value
		}
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	result.CandidatesTruncated = result.CandidateCount > observed
	for _, reason := range reasons {
		result.Reasons = append(result.Reasons, *reason)
	}
	if result.CandidateCount == 0 {
		result.Reasons = append(result.Reasons, service.ImageCapacityReason{Code: "no_candidates", Count: 0})
	}
	return result, nil
}
