package service

import (
	"net/http"
	"strings"
)

const AsyncImageServiceTimeout400Code = "async_image_service_timeout_400"
const AsyncImageServiceTimeout400Reason = GatewayFailureReason(AsyncImageServiceTimeout400Code)

// IsAsyncImageServiceTimeout400AccountFailure also recognizes persisted safe
// diagnostics. The complete known message is required; unrelated timeouts and
// appended refusal or unknown-outcome instructions must not restart generation.
func IsAsyncImageServiceTimeout400AccountFailure(statusCode int, message string) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	message = strings.TrimRight(strings.ToLower(strings.Join(strings.Fields(message), " ")), ".。")
	return message == "service timeout, please try again later"
}

func isAsyncImageServiceTimeout400(statusCode int, body []byte) bool {
	return IsAsyncImageServiceTimeout400AccountFailure(statusCode, extractUpstreamErrorMessage(body))
}

func newAsyncImageServiceTimeout400FailoverError(body []byte, headers http.Header) *UpstreamFailoverError {
	message, providerCode, requestID := geminiAsyncUpstreamDiagnostics(body, geminiRequestIDFromHeaders(headers))
	return &UpstreamFailoverError{
		StatusCode: http.StatusBadRequest, ResponseBody: body, ResponseHeaders: headers.Clone(),
		ProviderErrorCode: providerCode, ProviderErrorMessage: message, UpstreamRequestID: requestID,
		Scope: GatewayFailureScopeAccount, Reason: AsyncImageServiceTimeout400Reason,
		NextAccountAction: NextAccountRetry, RetryableOnSameAccount: false,
	}
}

func (e *UpstreamFailoverError) IsAsyncImageServiceTimeout400() bool {
	return e != nil && e.Reason == AsyncImageServiceTimeout400Reason
}
