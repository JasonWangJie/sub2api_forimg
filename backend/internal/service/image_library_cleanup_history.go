package service

import (
	"context"
	"time"
)

const (
	imageCleanupHistoryRetention = 24 * time.Hour
	imageCleanupHistoryInterval  = time.Minute
	imageCleanupHistoryBatchSize = 500
	imageCleanupHistoryListLimit = 5
)

// History is separate from the deletion executor: pruning completed records
// must still run when automatic image cleanup is disabled or storage is down.
type imageCleanupHistoryRepository interface {
	PruneCleanupJobHistory(context.Context, time.Time, int) (int64, error)
}

func (s *ImageLibraryMaintenanceService) processCleanupHistory(ctx context.Context, now time.Time) error {
	if now.Before(s.nextCleanupHistoryAt) {
		return nil
	}
	repo, ok := s.library.repo.(imageCleanupHistoryRepository)
	if !ok {
		return nil
	}
	s.nextCleanupHistoryAt = now.Add(imageCleanupHistoryInterval)
	pruneCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := repo.PruneCleanupJobHistory(pruneCtx, now.UTC().Add(-imageCleanupHistoryRetention), imageCleanupHistoryBatchSize)
	return err
}

func normalizeImageCleanupHistoryLimit(limit int) int {
	if limit < 1 || limit > imageCleanupHistoryListLimit {
		return imageCleanupHistoryListLimit
	}
	return limit
}
