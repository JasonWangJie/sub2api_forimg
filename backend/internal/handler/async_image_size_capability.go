package handler

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// A provider capability mismatch can switch accounts, but remains a definite
// client error after that bounded loop. Returning 502 here would make the durable
// worker enqueue the same incompatible request again as a transient failure.
func asyncImageSizeCapabilityErrorDetails(c *gin.Context, err *service.UpstreamFailoverError) (errType, code, message string, ok bool) {
	if c == nil || c.Request == nil || err == nil || err.StatusCode != http.StatusUnprocessableEntity || !err.IsAsyncImageSizeCapabilityMismatch() {
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
	code = strings.TrimSpace(gjson.GetBytes(err.ResponseBody, "error.code").String())
	if code == "" {
		code = err.ProviderErrorCode
	}
	message = err.ProviderErrorMessage
	if message == "" {
		message = service.ExtractUpstreamErrorMessage(err.ResponseBody)
	}
	return errType, code, message, true
}
