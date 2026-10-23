package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// handleAsyncImageUpstreamFailure handles a complete error response only. An
// exhausted direct-account failover must remain terminal rather than entering
// the worker's independent reference retry loop and selecting the same account.
func (h *DurableAsyncImageHandler) handleAsyncImageUpstreamFailure(parent, attemptCtx context.Context, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, statusCode int, body []byte, headers http.Header) asyncImageWorkerDisposition {
	message := formatAsyncImageUpstreamFailure(statusCode, body)
	retryAfter := service.ParseAsyncImageRetryAfter(headers.Get("Retry-After"), time.Now())
	logger.L().Warn("async_image.upstream_failed",
		zap.String("task_id", task.TaskID),
		zap.String("platform", task.Platform),
		zap.String("model", task.Model),
		zap.Int("status_code", statusCode),
		zap.String("message", message),
	)
	if statusCode == http.StatusBadRequest &&
		(gjson.GetBytes(body, "error.code").String() == service.AsyncImageReferenceBudgetExceededCode ||
			service.IsAsyncImageReferenceBudgetExceededMessage(statusCode, service.ExtractUpstreamErrorMessage(body))) {
		h.failAsyncImageTask(parent, task, "upstream_failed", message, false)
		return asyncImageWorkerDisposition{}
	}
	// A reference-fetch-looking timeout does not independently prove generation
	// never started. Preserve unknown until that upstream contract is established.
	if isAsyncImageAmbiguousUpstreamFailure(statusCode, message) {
		h.markAsyncImageExecutionUnknown(parent, task, message)
		return asyncImageWorkerDisposition{}
	}
	// Include this invocation's attempt before applying the reference budget.
	recordAsyncImageReferenceFetchFailure(attemptCtx, message)
	if shouldRetryAsyncImageUpstreamReferenceFetchWithContext(attemptCtx, task, cfg, statusCode, message) {
		return h.retryAsyncImageUpstreamReferenceFetch(parent, task, cfg, message, retryAfter)
	}
	if isAsyncImageCapacityFailure(statusCode, message) {
		if shouldRetryAsyncImageCapacity(task, cfg, statusCode, message) {
			return h.retryAsyncImageCapacity(parent, task, cfg, message, retryAfter, "upstream_capacity")
		}
		h.failAsyncImageTask(parent, task, "upstream_capacity_exhausted", message, false)
		return asyncImageWorkerDisposition{}
	}
	if shouldRetryAsyncImageUpstreamTransient(task, cfg, statusCode, message) {
		return h.retryAsyncImageUpstreamTransient(parent, task, cfg, message, retryAfter)
	}
	h.failAsyncImageTask(parent, task, "upstream_failed", message, false)
	return asyncImageWorkerDisposition{}
}
