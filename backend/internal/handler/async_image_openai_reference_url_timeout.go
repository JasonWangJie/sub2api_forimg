package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
)

type asyncImageOpenAIReferenceRetryKey struct{}

type asyncImageOpenAIReferenceRetry struct {
	originalAccountID int64
	originalSent      bool
	originalRejected  bool
	alternateSent     bool
	originalExcluded  bool
}

// The timeout rejection separates the initial URL request from the two possible
// follow-up sends. Read the durable audit so a later worker cannot repeat them.
func asyncImageOpenAIReferenceRetryState(attemptsJSON json.RawMessage) asyncImageOpenAIReferenceRetry {
	var attempts []service.AsyncImageAccountAttempt
	if json.Unmarshal(attemptsJSON, &attempts) != nil {
		return asyncImageOpenAIReferenceRetry{}
	}
	for i := len(attempts) - 1; i >= 0; i-- {
		attempt := attempts[i]
		if attempt.Status == service.AsyncImageAccountAttemptFailed && attempt.AccountID > 0 &&
			attempt.StatusCode == http.StatusBadRequest && attempt.FailureReason == string(service.AsyncImageOpenAIReferenceURLTimeoutReason) {
			state := asyncImageOpenAIReferenceRetry{originalAccountID: attempt.AccountID}
			for _, later := range attempts[i+1:] {
				switch {
				case later.AccountID != state.originalAccountID && later.Status == service.AsyncImageAccountAttemptInvoked:
					state.alternateSent = true
				case later.AccountID == state.originalAccountID && later.Status == service.AsyncImageAccountAttemptInvoked:
					state.originalSent = true
					state.originalRejected = false
				case later.AccountID == state.originalAccountID && later.Status == service.AsyncImageAccountAttemptFailed:
					state.originalRejected = later.FailureReason == string(service.AsyncImageOpenAIReferenceMultipartRejectionReason)
				}
			}
			return state
		}
	}
	return asyncImageOpenAIReferenceRetry{}
}

func asyncImageOpenAIReferenceRetryFromContext(ctx context.Context) asyncImageOpenAIReferenceRetry {
	state, _ := ctx.Value(asyncImageOpenAIReferenceRetryKey{}).(asyncImageOpenAIReferenceRetry)
	return state
}

func canRetryAsyncImageOpenAIReference(ctx context.Context, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig) bool {
	return canRetryAsyncImage(task, task.ReferenceRetryCount, cfg.ReferenceFetchMaxRetries, cfg.TotalMaxRetries) &&
		time.Now().Before(asyncImageTaskDeadline(task)) &&
		service.AsyncImageUpstreamAttemptCount(ctx) < asyncImageRemainingUpstreamAttempts(task)
}

func isAsyncImageOpenAIReferenceTransientDownload(err error) bool {
	var download *service.AsyncImageReferenceDownloadError
	if !errors.As(err, &download) {
		return false
	}
	switch download.Phase {
	case "validate":
		var dnsErr *net.DNSError
		return errors.As(download.Err, &dnsErr) && (dnsErr.IsTemporary || dnsErr.IsTimeout)
	case "response":
		return isRetryableAsyncImageReferenceFetchError(download)
	case "download", "read":
		// A URL in net/http's error wrapper can contain words such as
		// "connection reset". Classify the cause, including redirect errors.
		cause := download.Err
		var requestErr *url.Error
		if errors.As(cause, &requestErr) {
			cause = requestErr.Err
		}
		return isRetryableAsyncImageReferenceFetchError(cause)
	}
	return false
}

func (h *DurableAsyncImageHandler) finishAsyncImageOpenAIReferenceAttempt(parent, attemptCtx context.Context, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, status int, body []byte, message string, retryAfter time.Duration) asyncImageWorkerDisposition {
	state := asyncImageOpenAIReferenceRetryFromContext(attemptCtx)
	if capture := service.AsyncImageAccountAttemptCaptureFromContext(attemptCtx); capture != nil &&
		service.AsyncImageUpstreamAttemptCount(attemptCtx) > 0 {
		attempts := capture.Attempts()
		for i := len(attempts) - 1; i >= 0; i-- {
			attempt := attempts[i]
			if attempt.Status != service.AsyncImageAccountAttemptFailed {
				continue
			}
			if attempt.FailureReason == service.AsyncImageTaskStatusExecutionUnknown ||
				service.IsAsyncImageOpenAIReferenceOutcomeUnknown(attempt.StatusCode, attempt.ProviderErrorCode+" "+attempt.Error) ||
				isAsyncImageAmbiguousUpstreamFailure(status, message) {
				h.markAsyncImageExecutionUnknown(parent, task, message)
				return asyncImageWorkerDisposition{}
			}
			if attempt.AccountID == state.originalAccountID && !state.originalSent &&
				attempt.FailureReason == string(service.AsyncImageOpenAIReferenceMultipartRejectionReason) &&
				gjson.GetBytes(body, "error.code").String() == service.AsyncImageOpenAIReferenceMultipartRejectionCode &&
				canRetryAsyncImageOpenAIReference(attemptCtx, task, cfg) {
				return h.scheduleAsyncImageReferenceRetry(parent, task, cfg, message, retryAfter, "", true)
			}
			break
		}
	}
	h.failAsyncImageTask(parent, task, "upstream_failed", message, false)
	return asyncImageWorkerDisposition{}
}
