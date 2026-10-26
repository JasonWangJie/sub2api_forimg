package handler

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Keep the complete upstream 422 after the bounded account-switch loop. A 502
// mapping would incorrectly start another worker loop for the exhausted error.
func asyncImageInternalError422ErrorDetails(c *gin.Context, err *service.UpstreamFailoverError) (errType, code, message string, ok bool) {
	if c == nil || c.Request == nil || err == nil || err.StatusCode != http.StatusUnprocessableEntity || !err.IsAsyncImageInternalError422() {
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
	code = err.ProviderErrorCode
	if code == "" {
		code = strings.TrimSpace(gjson.GetBytes(err.ResponseBody, "error.code").String())
	}
	message = err.ProviderErrorMessage
	if message == "" {
		message = service.ExtractUpstreamErrorMessage(err.ResponseBody)
	}
	return errType, code, message, true
}
