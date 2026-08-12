package handler

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

const asyncImageRetentionInterval = 15 * time.Minute

func (h *DurableAsyncImageHandler) asyncImageRetentionLoop(ctx context.Context) {
	retention := service.NewAsyncImageRetentionService(h.tasks, h.storage)
	if retention == nil {
		logger.L().Warn("async_image.retention_unavailable")
		return
	}
	run := func() {
		stats, err := retention.RunOnce(ctx, time.Now().UTC())
		if err != nil && ctx.Err() == nil {
			logger.L().Warn("async_image.retention_failed", zap.Error(err))
			return
		}
		if stats.StagingDeleted+stats.UploadStateDeleted+int64(stats.UploadIntentsDeleted+stats.ResultUploadIntentsDeleted+stats.InputsDeleted+stats.ResultsDeleted+stats.TasksDeleted) > 0 {
			logger.L().Info("async_image.retention_completed",
				zap.Int64("staging_deleted", stats.StagingDeleted),
				zap.Int64("upload_state_deleted", stats.UploadStateDeleted),
				zap.Int("upload_intents_deleted", stats.UploadIntentsDeleted),
				zap.Int("result_upload_intents_deleted", stats.ResultUploadIntentsDeleted),
				zap.Int("inputs_deleted", stats.InputsDeleted),
				zap.Int("results_deleted", stats.ResultsDeleted),
				zap.Int("tasks_deleted", stats.TasksDeleted),
			)
		}
	}
	run()
	ticker := time.NewTicker(asyncImageRetentionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
