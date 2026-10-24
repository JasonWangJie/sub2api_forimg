package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// AsyncImageSizeCapabilityMismatchReason survives provider-code variations in
// persisted task attempts without changing the upstream diagnostic code.
const AsyncImageSizeCapabilityMismatchReason = GatewayFailureReason("async_image_size_capability_mismatch")

const asyncImageSizeCapabilityMismatchReason = AsyncImageSizeCapabilityMismatchReason

const asyncImageSizeCapabilityMessage = "no eligible upstream image provider supports the requested image size or resolution tier. adjust size/resolution or select a compatible route"
const asyncImageCombinedCapabilityMessage = "no eligible upstream image provider supports the requested capability combination. no eligible upstream image provider supports the requested model. select a supported model or a compatible route. no eligible upstream image provider supports the requested image size or resolution tier. adjust size/resolution or select a compatible route"

func normalizeAsyncImageCapabilityMessage(message string) string {
	return strings.TrimRight(strings.ToLower(strings.Join(strings.Fields(message), " ")), ".。")
}

// IsAsyncImageCapabilityAccountFailure recognizes already-persisted safe
// diagnostics for task-scoped account exclusion. It is not an HTTP response
// classifier: incoming combined rejections still require their structured pair
// of capability reasons in isAsyncImageSizeCapabilityMismatch.
func IsAsyncImageCapabilityAccountFailure(statusCode int, providerCode, message string) bool {
	if statusCode != http.StatusUnprocessableEntity {
		return false
	}
	message = normalizeAsyncImageCapabilityMessage(message)
	switch providerCode {
	case "image_size_not_supported":
		return message == asyncImageSizeCapabilityMessage
	case "image_request_capability_not_supported":
		return message == asyncImageCombinedCapabilityMessage
	default:
		return false
	}
}

// isAsyncImageSizeCapabilityMismatch recognizes a provider-selection failure,
// rather than a malformed size supplied by the caller. Codes such as
// image_size_not_supported are not enough on their own: only the complete known
// message confirms that another configured image account may accept the request.
// Callers must additionally require the private durable-image context.
func isAsyncImageSizeCapabilityMismatch(statusCode int, body []byte) bool {
	if statusCode != http.StatusUnprocessableEntity {
		return false
	}
	message := normalizeAsyncImageCapabilityMessage(extractUpstreamErrorMessage(body))
	if message == asyncImageSizeCapabilityMessage {
		return true
	}
	if message != asyncImageCombinedCapabilityMessage {
		return false
	}
	return hasAsyncImageCombinedCapabilityDetails(body)
}

// The combined rejection is only known to precede provider selection when its
// structured reasons are exactly the supported model/size pair. Do not broaden
// this to arbitrary 422s, partial messages or additional capability failures.
func hasAsyncImageCombinedCapabilityDetails(body []byte) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	envelope := gjson.ParseBytes(body)
	if inner := strings.TrimSpace(envelope.Get("error.message").String()); strings.HasPrefix(inner, "{") && gjson.Valid(inner) {
		envelope = gjson.Parse(inner)
	}
	codeFound := false
	for _, path := range []string{"code", "error.code"} {
		code := envelope.Get(path)
		if !code.Exists() {
			continue
		}
		if code.Type != gjson.String || code.String() != "image_request_capability_not_supported" {
			return false
		}
		codeFound = true
	}
	if !codeFound {
		return false
	}
	detailsFound := false
	for _, path := range []string{"details.capability_errors", "error.details.capability_errors"} {
		reasons := envelope.Get(path)
		if !reasons.Exists() {
			continue
		}
		if !reasons.IsArray() || len(reasons.Array()) != 2 {
			return false
		}
		modelMismatch, sizeMismatch := false, false
		for _, reason := range reasons.Array() {
			if reason.Type != gjson.String {
				return false
			}
			switch reason.String() {
			case "image_model_not_supported":
				modelMismatch = true
			case "image_size_not_supported":
				sizeMismatch = true
			default:
				return false
			}
		}
		if !modelMismatch || !sizeMismatch {
			return false
		}
		detailsFound = true
	}
	return detailsFound
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
