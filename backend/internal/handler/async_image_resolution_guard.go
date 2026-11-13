package handler

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

func (h *DurableAsyncImageHandler) SetImageResolutionGuard(guard service.ImageResolutionGuardObserver) {
	h.imageResolutionGuard = guard
}

// Reporting uses the same task identifier during download/finalization recovery.
// A quality warning never changes the successful result or its billing command.
func (h *DurableAsyncImageHandler) observeAsyncImageResolution(ctx context.Context, task *service.AsyncImageTask, usage *AsyncImageUsageCapture, outputs []asyncImageCapturedOutput, accountID int64, requestID *string) {
	if h.imageResolutionGuard == nil || task == nil {
		return
	}
	var expectation *service.ImageResolutionExpectation
	accountName := ""
	if input := usage.OpenAI(); input != nil {
		if input.Result != nil {
			expectation = input.Result.ImageResolutionExpectation
		}
		if input.Account != nil {
			accountName = input.Account.Name
		}
	}
	if input := usage.Gemini(); input != nil {
		if input.Result != nil {
			expectation = input.Result.ImageResolutionExpectation
		}
		if input.Account != nil {
			accountName = input.Account.Name
		}
	}
	if expectation == nil {
		logger.L().Debug("async_image.resolution_skipped", zap.String("task_id", task.TaskID), zap.String("reason", "no_verifiable_target"))
		return
	} // old recovery snapshots have no trusted target
	observation := service.ImageResolutionObservation{ID: "async-image:" + task.TaskID, AccountID: accountID, AccountName: accountName, Platform: task.Platform,
		TaskID: task.TaskID, Expectation: *expectation, StartedAt: task.SubmittedAt}
	if requestID != nil {
		observation.RequestID = *requestID
	}
	if task.StartedAt != nil {
		observation.StartedAt = *task.StartedAt
	}
	if !expectation.AttemptStartedAt.IsZero() {
		observation.StartedAt = expectation.AttemptStartedAt
	}
	for _, output := range outputs {
		observation.Outputs = append(observation.Outputs, service.ImageResolutionOutput{Width: output.Width, Height: output.Height})
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := h.imageResolutionGuard.Observe(persistCtx, observation); err != nil {
		logger.L().Warn("async_image.resolution_observe_failed", zap.String("task_id", task.TaskID), zap.Error(err))
	}
}
