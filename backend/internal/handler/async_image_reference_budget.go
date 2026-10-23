package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func isAsyncImageReferenceBudgetAccountFailure(attempt service.AsyncImageAccountAttempt) bool {
	return attempt.Status == service.AsyncImageAccountAttemptFailed && attempt.AccountID > 0 &&
		attempt.StatusCode == http.StatusBadRequest &&
		(attempt.ProviderErrorCode == service.AsyncImageReferenceBudgetExceededCode ||
			service.IsAsyncImageReferenceBudgetExceededMessage(attempt.StatusCode, attempt.Error))
}

// Unlike the normal recent-three exclusion, this set spans the complete task
// audit. A later transient failure on B must never make budget-failed A eligible.
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
		if isAsyncImageReferenceBudgetAccountFailure(attempt) {
			ids[attempt.AccountID] = struct{}{}
		}
	}
	return ids
}

// Only the private durable worker receives this exhaustion marker. It keeps the
// upstream's definite 400 and safe diagnostics while preventing worker replay.
func asyncImageReferenceBudgetErrorDetails(c *gin.Context, err *service.UpstreamFailoverError) (errType, code, message string, ok bool) {
	if c == nil || c.Request == nil || err == nil || err.StatusCode != http.StatusBadRequest || !err.IsAsyncImageReferenceBudgetExceeded() {
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
	return errType, service.AsyncImageReferenceBudgetExceededCode, message, true
}
