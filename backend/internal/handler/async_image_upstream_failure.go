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

// handleAsyncImageUpstreamFailure applies a persisted category budget after
// alternate-account selection. Fixed capabilities cannot change on retry.
func (h *DurableAsyncImageHandler) handleAsyncImageUpstreamFailure(parent, attemptCtx context.Context, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, statusCode int, body []byte, headers http.Header) asyncImageWorkerDisposition {
	message := formatAsyncImageUpstreamFailure(statusCode, body)
	if statusCode == http.StatusBadRequest && gjson.GetBytes(body, "error.code").String() == "async_image_attempt_budget_exhausted" {
		h.failAsyncImageTask(parent, task, "generation_budget_exhausted", message, false)
		return asyncImageWorkerDisposition{}
	}
	retryAfter := service.ParseAsyncImageRetryAfter(headers.Get("Retry-After"), time.Now())
	if statusCode == http.StatusServiceUnavailable && service.HasAsyncImageAttemptBudget(attemptCtx) &&
		service.AsyncImageUpstreamAttemptCount(attemptCtx) == 0 && service.ImageInputLocalFallbackSuggested(attemptCtx) &&
		canFallbackAsyncImageReferenceLocally(task, cfg) {
		// The real invocation counter proves preselection rejected passthrough
		// before generation. Keep the same accounts eligible after inlining.
		return h.scheduleAsyncImageReferenceRetry(parent, task, cfg, "eligible image channels require locally loaded reference images", retryAfter, true, false)
	}
	logger.L().Warn("async_image.upstream_failed",
		zap.String("task_id", task.TaskID),
		zap.String("platform", task.Platform),
		zap.String("model", task.Model),
		zap.Int("status_code", statusCode),
		zap.String("message", message),
	)
	if (statusCode == http.StatusBadRequest || statusCode == http.StatusUnprocessableEntity) &&
		gjson.GetBytes(body, "error.code").String() == service.AsyncImageFixedCapabilityCode {
		h.failAsyncImageTask(parent, task, "upstream_capability_exhausted", message, false)
		return asyncImageWorkerDisposition{}
	}
	if service.IsAsyncImageCapacityRejection(statusCode, service.ExtractUpstreamErrorMessage(body)) {
		if canRetryAsyncImage(task, task.CapacityRetryCount, cfg.CapacityMaxRetries, cfg.TotalMaxRetries) {
			return h.retryAsyncImageCapacity(parent, task, cfg, message, retryAfter, "upstream_capacity")
		}
		h.failAsyncImageTask(parent, task, "upstream_capacity_exhausted", message, false)
		return asyncImageWorkerDisposition{}
	}
	if (statusCode == http.StatusBadRequest || statusCode == http.StatusUnprocessableEntity) &&
		(gjson.GetBytes(body, "error.code").String() == service.AsyncImageRetryableRejectionCode ||
			gjson.GetBytes(body, "error.code").String() == service.AsyncImageReferenceTransportRetryCode) {
		if gjson.GetBytes(body, "error.code").String() == service.AsyncImageReferenceTransportRetryCode &&
			canFallbackAsyncImageReferenceLocally(task, cfg) {
			// Transport compatibility is a soft exclusion: try another account
			// with inline references first, but allow A if no alternate exists.
			return h.scheduleAsyncImageReferenceRetry(parent, task, cfg, message, retryAfter, true, false)
		}
		if canRetryAsyncImage(task, task.UpstreamRetryCount, cfg.UpstreamTransientMaxRetries, cfg.TotalMaxRetries) {
			return h.retryAsyncImageUpstreamTransient(parent, task, cfg, message, retryAfter)
		}
		h.failAsyncImageTask(parent, task, "upstream_failed", message, false)
		return asyncImageWorkerDisposition{}
	}
	if service.IsAsyncImageServiceTimeout400AccountFailure(statusCode, service.ExtractUpstreamErrorMessage(body)) {
		h.failAsyncImageTask(parent, task, "upstream_failed", message, false)
		return asyncImageWorkerDisposition{}
	}
	if statusCode == http.StatusBadRequest &&
		gjson.GetBytes(body, "error.code").String() == service.AsyncImageReferenceLocalFallbackCode {
		if canFallbackAsyncImageReferenceLocally(task, cfg) &&
			hasAsyncImageReferenceFallbackAccount(task, attemptCtx) &&
			service.IsAsyncImageReferenceAccountFailoverMessage(statusCode, service.ExtractUpstreamErrorMessage(body)) {
			// The gateway already recorded the definite failure. Persist the local
			// transport and its account audit atomically; the next invocation must
			// retain the hard exclusion instead of creating a sticky same-account retry.
			return h.scheduleAsyncImageReferenceRetry(parent, task, cfg, message, retryAfter, true, true)
		}
		h.failAsyncImageTask(parent, task, "upstream_failed", message, false)
		return asyncImageWorkerDisposition{}
	}
	if statusCode == http.StatusBadRequest &&
		(gjson.GetBytes(body, "error.code").String() == service.AsyncImageReferenceBudgetExceededCode ||
			gjson.GetBytes(body, "error.code").String() == service.AsyncImageReferenceAccountFailoverCode ||
			service.IsAsyncImageReferenceAccountFailoverMessage(statusCode, service.ExtractUpstreamErrorMessage(body))) {
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
