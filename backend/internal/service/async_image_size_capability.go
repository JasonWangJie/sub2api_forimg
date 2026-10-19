package service

import (
	"net/http"
	"strings"
)

const asyncImageSizeCapabilityMismatchReason = GatewayFailureReason("async_image_size_capability_mismatch")

// isAsyncImageSizeCapabilityMismatch recognizes a provider-selection failure,
// rather than a malformed size supplied by the caller. Codes such as
// image_size_not_supported are not enough on their own: only the complete known
// message confirms that another configured image account may accept the request.
// Callers must additionally require the private durable-image context.
func isAsyncImageSizeCapabilityMismatch(statusCode int, body []byte) bool {
	if statusCode != http.StatusUnprocessableEntity {
		return false
	}
	message := strings.ToLower(strings.Join(strings.Fields(extractUpstreamErrorMessage(body)), " "))
	message = strings.TrimRight(message, ".。")
	return message == "no eligible upstream image provider supports the requested image size or resolution tier. adjust size/resolution or select a compatible route"
}

func newAsyncImageSizeCapabilityFailoverError(body []byte, headers http.Header) *UpstreamFailoverError {
	message, providerCode, requestID := geminiAsyncUpstreamDiagnostics(body, geminiRequestIDFromHeaders(headers))
	return &UpstreamFailoverError{
		StatusCode:           http.StatusUnprocessableEntity,
		ResponseBody:         body,
		ResponseHeaders:      headers.Clone(),
		ProviderErrorCode:    providerCode,
		ProviderErrorMessage: message,
		UpstreamRequestID:    requestID,
		Scope:                GatewayFailureScopeAccount,
		Reason:               asyncImageSizeCapabilityMismatchReason,
		NextAccountAction:    NextAccountRetry,
	}
}

// IsAsyncImageSizeCapabilityMismatch lets the account-switch exhaustion path
// preserve this definite 422 failure instead of creating a retryable 502.
func (e *UpstreamFailoverError) IsAsyncImageSizeCapabilityMismatch() bool {
	return e != nil && e.Reason == asyncImageSizeCapabilityMismatchReason
}
