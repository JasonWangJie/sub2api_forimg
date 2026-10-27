package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func isAsyncImageReferenceAccountFailure(attempt service.AsyncImageAccountAttempt) bool {
	if attempt.Status != service.AsyncImageAccountAttemptFailed || attempt.AccountID <= 0 {
		return false
	}
	statusCode, message := attempt.StatusCode, attempt.Error
	const worker400Prefix = "上游生图失败（HTTP 400）："
	if strings.HasPrefix(message, worker400Prefix) {
		message = strings.TrimPrefix(message, worker400Prefix)
		if statusCode == 0 {
			statusCode = http.StatusBadRequest
		}
	}
	return statusCode == http.StatusBadRequest &&
		(attempt.ProviderErrorCode == service.AsyncImageReferenceBudgetExceededCode ||
			attempt.ProviderErrorCode == service.AsyncImageReferenceAccountFailoverCode ||
			attempt.FailureReason == service.AsyncImageReferenceBudgetExceededCode ||
			attempt.FailureReason == service.AsyncImageReferenceAccountFailoverCode ||
			service.IsAsyncImageReferenceAccountFailoverMessage(statusCode, message))
}

// Unlike the normal recent-three exclusion, this set spans the complete task
// audit. A different failure on B must not revive an account whose recognized
// provider rejection already required switching.
func asyncImageNonRetryableAccountIDs(task *service.AsyncImageTask) map[int64]struct{} {
	if task == nil {
		return nil
	}
	var attempts []service.AsyncImageAccountAttempt
	if json.Unmarshal(task.AccountAttempts, &attempts) != nil {
		return nil
	}
	ids := make(map[int64]struct{})
	for _, attempt := range attempts {
		if isAsyncImageReferenceAccountFailure(attempt) ||
			(attempt.Status == service.AsyncImageAccountAttemptFailed && attempt.AccountID > 0 &&
				attempt.StatusCode == http.StatusBadRequest &&
				(attempt.FailureReason == string(service.AsyncImageServiceTimeout400Reason) ||
					service.IsAsyncImageServiceTimeout400AccountFailure(attempt.StatusCode, attempt.Error))) ||
			(attempt.Status == service.AsyncImageAccountAttemptFailed && attempt.AccountID > 0 &&
				attempt.StatusCode == http.StatusUnprocessableEntity &&
				(attempt.FailureReason == string(service.AsyncImageSizeCapabilityMismatchReason) ||
					attempt.FailureReason == string(service.AsyncImageInternalError422Reason) ||
					service.IsAsyncImageCapabilityAccountFailure(attempt.StatusCode, attempt.ProviderErrorCode, attempt.Error) ||
					service.IsAsyncImageInternalError422AccountFailure(attempt.StatusCode, attempt.ProviderErrorCode, attempt.Error))) {
			ids[attempt.AccountID] = struct{}{}
		}
	}
	return ids
}

// Only the private durable worker receives this exhaustion marker. It keeps the
// upstream's definite 400 and safe diagnostics while preventing worker replay.
func asyncImageReferenceAccountErrorDetails(c *gin.Context, err *service.UpstreamFailoverError) (errType, code, message string, ok bool) {
	if c == nil || c.Request == nil || err == nil || err.StatusCode != http.StatusBadRequest || !err.IsAsyncImageReferenceAccountFailover() {
		return "", "", "", false
	}
	ctx := c.Request.Context()
	if service.AsyncImageAccountAttemptCaptureFromContext(ctx) == nil && !service.IsGeminiAsyncImageGeneration(ctx) {
		return "", "", "", false
	}
	errType = strings.TrimSpace(gjson.GetBytes(err.ResponseBody, "error.type").String())
	if errType == "" {
		errType = "upstream_error"
	}
	message = err.ProviderErrorMessage
	if message == "" {
		message = service.ExtractUpstreamErrorMessage(err.ResponseBody)
	}
	code = service.AsyncImageReferenceAccountFailoverCode
	if err.IsAsyncImageReferenceBudgetExceeded() {
		code = service.AsyncImageReferenceBudgetExceededCode
	}
	if asyncImageReferenceLocalFallbackRequested(c, err) {
		code = service.AsyncImageReferenceLocalFallbackCode
	}
	return errType, code, message, true
}

// The worker owns transport changes and persists them together with the failed
// account. Stop before consuming the remaining accounts with the same URL body.
func asyncImageReferenceLocalFallbackRequested(c *gin.Context, err *service.UpstreamFailoverError) bool {
	if c == nil || c.Request == nil || err == nil || err.StatusCode != http.StatusBadRequest ||
		(!err.IsAsyncImageReferenceAccountFailover() && err.Reason != service.AsyncImageReferenceTransportRejectionReason) {
		return false
	}
	ctx := c.Request.Context()
	return service.AsyncImageAccountAttemptCaptureFromContext(ctx) != nil &&
		service.IsAsyncImageInputReferenceTask(ctx) && service.AsyncImageReferenceLocalFallbackFromContext(ctx)
}

func canFallbackAsyncImageReferenceLocally(task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig) bool {
	return task != nil && task.RequestType == service.AsyncImageRequestTypeImageToImage &&
		asyncImageReferenceTransport(task, cfg) == service.AsyncImageReferenceTransportPassthroughFallbackLocal &&
		canRetryAsyncImage(task, task.ReferenceRetryCount, cfg.ReferenceFetchMaxRetries, cfg.TotalMaxRetries)
}

func hasAsyncImageReferenceFallbackAccount(task *service.AsyncImageTask, ctx context.Context) bool {
	attemptsJSON, _ := asyncImageAttemptState(task, ctx)
	var attempts []service.AsyncImageAccountAttempt
	if json.Unmarshal(attemptsJSON, &attempts) != nil {
		return false
	}
	for _, attempt := range attempts {
		if isAsyncImageReferenceAccountFailure(attempt) {
			return true
		}
	}
	return false
}
