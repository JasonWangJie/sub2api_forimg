package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *imageResolutionGuardRepository) ListCooldownsBatch(ctx context.Context, ids []int64) ([]service.ImageResolutionCooldown, error) {
	out := []service.ImageResolutionCooldown{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	params := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		params[i] = fmt.Sprintf("$%d", i+1)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT s.account_id,s.requested_model,s.size_tier,s.cycle_id,s.cooldown_until,s.failure_count,e.observation
 FROM image_resolution_quality_state s LEFT JOIN image_resolution_quality_events e ON e.cycle_id=s.cycle_id AND e.cycle_id<>''
 WHERE s.account_id IN (`+strings.Join(params, ",")+`) AND s.cooldown_until>NOW()
 ORDER BY s.account_id,s.requested_model,s.size_tier`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanImageResolutionCooldown(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	return out, rows.Err()
}

var _ service.ImageResolutionGuardBatchRepository = (*imageResolutionGuardRepository)(nil)
