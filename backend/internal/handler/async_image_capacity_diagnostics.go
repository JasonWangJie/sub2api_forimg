package handler

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

// Persist before either capacity retry or exhaustion; diagnostic failure must
// never suppress the existing transition or cause an extra generation attempt.
func (h *DurableAsyncImageHandler) persistAsyncImageCapacityDiagnostics(ctx, invocationCtx context.Context, task *service.AsyncImageTask) {
	if task == nil || h.tasks == nil {
		return
	}
	service.CaptureImageCapacityFailureSnapshot(ctx, invocationCtx)
	diagnostic := service.ImageCapacityDiagnosticsFromContext(invocationCtx)
	if diagnostic == nil {
		return
	}
	payload, err := json.Marshal(diagnostic)
	if err != nil || len(payload) > 65536 {
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	err = h.tasks.Repository().AppendAsyncImageEvent(writeCtx, service.AsyncImageEvent{TaskID: task.TaskID, EventType: "capacity_diagnostic", Payload: payload})
	if err != nil {
		logger.L().Warn("async_image.capacity_diagnostic_persist_failed", zap.String("task_id", task.TaskID), zap.Error(err))
	}
}
