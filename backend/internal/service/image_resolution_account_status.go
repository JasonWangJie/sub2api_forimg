package service

import (
	"context"
	"errors"
)

// ImageResolutionAccountStatus is an admin-only snapshot. A disabled guard can
// still have unexpired records; those records do not currently block routing.
type ImageResolutionAccountStatus struct {
	Enabled   bool                      `json:"enabled"`
	Cooldowns []ImageResolutionCooldown `json:"cooldowns"`
}

type ImageResolutionGuardBatchRepository interface {
	ListCooldownsBatch(context.Context, []int64) ([]ImageResolutionCooldown, error)
}

// AccountStatuses fetches only the current page in one query, including when
// enforcement is off. Never turn a failed read into a misleading empty status.
func (s *ImageResolutionGuardService) AccountStatuses(ctx context.Context, accountIDs []int64) (map[int64]*ImageResolutionAccountStatus, error) {
	out := make(map[int64]*ImageResolutionAccountStatus)
	ids := make([]int64, 0, len(accountIDs))
	for _, id := range accountIDs {
		if id <= 0 || out[id] != nil {
			continue
		}
		ids = append(ids, id)
		out[id] = &ImageResolutionAccountStatus{Cooldowns: []ImageResolutionCooldown{}}
	}
	if len(ids) == 0 {
		return out, nil
	}
	if s == nil || s.repo == nil {
		return nil, errors.New("image resolution repository unavailable")
	}
	cfg, err := s.runtimeSettings(ctx)
	if err != nil {
		return nil, err
	}
	repo, ok := s.repo.(ImageResolutionGuardBatchRepository)
	if !ok {
		return nil, errors.New("image resolution batch status unavailable")
	}
	items, err := repo.ListCooldownsBatch(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, status := range out {
		status.Enabled = cfg.Enabled
	}
	for _, item := range items {
		if status := out[item.AccountID]; status != nil {
			status.Cooldowns = append(status.Cooldowns, item)
		}
	}
	return out, nil
}
