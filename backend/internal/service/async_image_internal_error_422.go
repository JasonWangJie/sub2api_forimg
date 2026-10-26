package service

import (
	"net/http"
	"strings"
)

const AsyncImageInternalError422Code = "async_image_internal_error_422"
const AsyncImageInternalError422Reason = GatewayFailureReason(AsyncImageInternalError422Code)

// IsAsyncImageInternalError422AccountFailure recognizes the complete known
// transient provider rejection in persisted safe diagnostics. Other HTTP 422
// validation errors and appended refusal/unknown-outcome messages stay terminal.
func IsAsyncImageInternalError422AccountFailure(statusCode int, providerCode, message string) bool {
	if statusCode != http.StatusUnprocessableEntity || providerCode != "UNKNOWN" {
		return false
	}
	message = strings.TrimRight(strings.ToLower(strings.Join(strings.Fields(message), " ")), ".。")
	return message == "internal error encountered"
}

func isAsyncImageInternalError422(statusCode int, body []byte) bool {
	if statusCode != http.StatusUnprocessableEntity {
		return false
	}
	// Keep the same provider-code precedence as the persisted Gemini audit:
	// error.status, a wrapped status, then error.code. Do not infer UNKNOWN
	// from an HTTP status or from the message alone.
	_, providerCode, _ := geminiAsyncUpstreamDiagnostics(body, "")
	return IsAsyncImageInternalError422AccountFailure(statusCode, providerCode, extractUpstreamErrorMessage(body))
}

func newAsyncImageInternalError422FailoverError(body []byte, headers http.Header) *UpstreamFailoverError {
	message, providerCode, requestID := geminiAsyncUpstreamDiagnostics(body, geminiRequestIDFromHeaders(headers))
	return &UpstreamFailoverError{
		StatusCode: http.StatusUnprocessableEntity, ResponseBody: body, ResponseHeaders: headers.Clone(),
		ProviderErrorCode: providerCode, ProviderErrorMessage: message, UpstreamRequestID: requestID,
		Scope: GatewayFailureScopeAccount, Reason: AsyncImageInternalError422Reason,
		NextAccountAction: NextAccountRetry, RetryableOnSameAccount: false,
	}
}

func (e *UpstreamFailoverError) IsAsyncImageInternalError422() bool {
	return e != nil && e.Reason == AsyncImageInternalError422Reason
}
