package service

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// AsyncImageReferenceBudgetExceededCode identifies a definite upstream reference
// download budget failure after the bounded account-switch loop is exhausted.
// The durable worker must not turn that exhaustion into another same-account
// reference retry.
const AsyncImageReferenceBudgetExceededCode = "async_image_reference_budget_exceeded"

var asyncImageReferenceBudgetExceededPattern = regexp.MustCompile(`(?i)^reference image download exceeded its total time budget for (https://[^\s]+) \(budgetms=([1-9][0-9]{0,8}); reason=timeout\)$`)

// IsAsyncImageReferenceBudgetExceededMessage accepts only the complete known
// HTTP 400 response. Generic download timeouts, appended refusal/replay warnings
// and ambiguous generation failures intentionally remain outside this rule.
// Callers must additionally require the private durable-image context.
func IsAsyncImageReferenceBudgetExceededMessage(statusCode int, message string) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	message = strings.Join(strings.Fields(message), " ")
	matches := asyncImageReferenceBudgetExceededPattern.FindStringSubmatch(message)
	if len(matches) != 3 {
		return false
	}
	source, err := url.Parse(matches[1])
	return err == nil && strings.EqualFold(source.Scheme, "https") && source.Hostname() != "" && source.User == nil && source.Fragment == ""
}

func isAsyncImageReferenceBudgetExceeded(statusCode int, body []byte) bool {
	return IsAsyncImageReferenceBudgetExceededMessage(statusCode, extractUpstreamErrorMessage(body))
}

func newAsyncImageReferenceBudgetFailoverError(body []byte, headers http.Header) *UpstreamFailoverError {
	_, providerCode, requestID := geminiAsyncUpstreamDiagnostics(body, geminiRequestIDFromHeaders(headers))
	// The source URL may contain signed credentials and is unnecessary for
	// account selection. Preserve the known failure and budget without storing
	// that URL in account-attempt diagnostics or the exhausted client response.
	message := "Reference image download exceeded its total time budget"
	matches := asyncImageReferenceBudgetExceededPattern.FindStringSubmatch(strings.Join(strings.Fields(extractUpstreamErrorMessage(body)), " "))
	if len(matches) == 3 {
		message = "Reference image download exceeded its total time budget for https://*** (budgetMs=" + matches[2] + "; reason=timeout)"
	}
	return &UpstreamFailoverError{
		StatusCode:             http.StatusBadRequest,
		ResponseBody:           body,
		ResponseHeaders:        headers.Clone(),
		ProviderErrorCode:      providerCode,
		ProviderErrorMessage:   message,
		UpstreamRequestID:      requestID,
		Scope:                  GatewayFailureScopeAccount,
		Reason:                 GatewayFailureReason(AsyncImageReferenceBudgetExceededCode),
		NextAccountAction:      NextAccountRetry,
		RetryableOnSameAccount: false,
	}
}

func (e *UpstreamFailoverError) IsAsyncImageReferenceBudgetExceeded() bool {
	return e != nil && e.Reason == GatewayFailureReason(AsyncImageReferenceBudgetExceededCode)
}
