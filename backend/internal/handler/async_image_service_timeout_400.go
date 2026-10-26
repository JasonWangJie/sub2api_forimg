package handler

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Preserve the known timeout rejection after account-switch exhaustion instead
// of mapping it to a 502 that would restart the worker's retry loop.
func asyncImageServiceTimeout400ErrorDetails(c *gin.Context, err *service.UpstreamFailoverError) (errType, code, message string, ok bool) {
	if c == nil || c.Request == nil || err == nil || err.StatusCode != http.StatusBadRequest || !err.IsAsyncImageServiceTimeout400() {
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
	if code == "" {
		code = service.AsyncImageServiceTimeout400Code
	}
	message = err.ProviderErrorMessage
	if message == "" {
		message = service.ExtractUpstreamErrorMessage(err.ResponseBody)
	}
	return errType, code, message, true
}
