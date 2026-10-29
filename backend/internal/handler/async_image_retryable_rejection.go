package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Keep a private retry marker when the gateway runs out of alternate accounts.
// Without it a complete 400 was terminal, while a mapped 502 could incorrectly
// be treated as an unknown generation outcome by the worker.
func asyncImageRetryableRejectionErrorDetails(c *gin.Context, err *service.UpstreamFailoverError) (status int, code, message string, ok bool) {
	if c == nil || c.Request == nil || err == nil {
		return 0, "", "", false
	}
	ctx := c.Request.Context()
	if service.AsyncImageAccountAttemptCaptureFromContext(ctx) == nil {
		return 0, "", "", false
	}
	if !err.IsAsyncImageRetryableRejection() {
		if !service.AsyncImageProviderRetryPolicyEnabled(ctx) || !err.IsAsyncImageBoundedRetryFailure() {
			return 0, "", "", false
		}
		// Preserve the existing atomic hybrid->local handoff before enabling
		// the shared fallback policy for subsequent channel exhaustion.
		if asyncImageReferenceLocalFallbackRequested(c, err) {
			return 0, "", "", false
		}
	}
	code = service.AsyncImageRetryableRejectionCode
	if err.IsAsyncImageFixedCapabilityFailure() {
		code = service.AsyncImageFixedCapabilityCode
	}
	if err.Reason == service.AsyncImageCapacityRejectionReason {
		code = service.AsyncImageCapacityRejectionCode
	}
	if err.Reason == service.AsyncImageReferenceTransportRejectionReason &&
		service.AsyncImageReferenceLocalFallbackFromContext(ctx) {
		code = service.AsyncImageReferenceTransportRetryCode
	}
	message = err.ProviderErrorMessage
	if message == "" {
		message = service.ExtractUpstreamErrorMessage(err.ResponseBody)
	}
	status = err.StatusCode
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		return 0, "", "", false
	}
	return status, code, message, true
}
