package service

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

// AsyncImageReferenceBudgetExceededCode identifies a definite upstream reference
// download budget failure after the bounded account-switch loop is exhausted.
// The durable worker must not turn that exhaustion into another same-account
// reference retry.
const AsyncImageReferenceBudgetExceededCode = "async_image_reference_budget_exceeded"

// AsyncImageReferenceAccountFailoverCode marks a definite upstream input-image
// fetch failure whose bounded account-switch loop must not restart on exhaustion.
const AsyncImageReferenceAccountFailoverCode = "async_image_reference_account_failover"

// AsyncImageReferenceLocalFallbackCode is an internal gateway handoff to the
// worker, distinct from an exhausted account-switch loop.
const AsyncImageReferenceLocalFallbackCode = "async_image_reference_local_fallback"

var asyncImageReferenceBudgetExceededPattern = regexp.MustCompile(`(?i)^reference image download exceeded its total time budget for (https://[^\s]+) \(budgetms=([1-9][0-9]{0,8}); reason=timeout\)$`)
var asyncImageReferenceNetworkDownloadPattern = regexp.MustCompile(`(?i)^error while downloading (https://[^\s]+?):? get "(https://[^"\s]+)": dial tcp [^"\s]+: i/o timeout$`)
var asyncImageReferenceRedactedNetworkDownloadPattern = regexp.MustCompile(`(?i)^error while downloading (https://\*{3}(?:\.\*{3})*\.[a-z]+(?:/\*{3})+) get "(https://\*{3}(?:\.\*{3})*\.[a-z]+(?:/\*{3})+) dial tcp \*{3}\.\*{3}\.\*{3}\.\*{3}:443: i/o timeout$`)
var asyncImageReferenceTLSDownloadPattern = regexp.MustCompile(`(?i)^error while downloading (https://[^\s]+?):? get "(https://[^"\s]+)": net/http: tls handshake timeout$`)
var asyncImageReferenceRedactedTLSDownloadPattern = regexp.MustCompile(`(?i)^error while downloading (https://\*{3}(?:\.\*{3})*\.[a-z]+(?:/\*{3})+) get "(https://\*{3}(?:\.\*{3})*\.[a-z]+(?:/\*{3})+) net/http: tls handshake timeout$`)
var asyncImageReferenceReadResetPattern = regexp.MustCompile(`(?i)^error while downloading (https://[^\s]+?)(?::? get "(https://[^"\s]+)"?:?)? read tcp [^\s]+: read: connection reset by peer$`)
var asyncImageReferenceDiagnosticURLPattern = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)

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

// IsAsyncImageReferenceAccountFailoverMessage only handles complete HTTP 400
// input-reference failures. HTTP 502/504 execution ambiguity and result fetching
// remain outside this account-switch rule.
func IsAsyncImageReferenceAccountFailoverMessage(statusCode int, message string) bool {
	if statusCode != http.StatusBadRequest || !IsAsyncImageReferenceFetchFailureMessage(message) {
		return false
	}
	if strings.Contains(strings.ToLower(message), "reference image download exceeded its total time budget") {
		return IsAsyncImageReferenceBudgetExceededMessage(statusCode, message)
	}
	return true
}

func isAsyncImageReferenceAccountFailover(statusCode int, body []byte) bool {
	return IsAsyncImageReferenceAccountFailoverMessage(statusCode, extractUpstreamErrorMessage(body))
}

func isAsyncImageReferenceAccountFailoverForContext(ctx context.Context, statusCode int, body []byte) bool {
	message := extractUpstreamErrorMessage(body)
	// This provider format does not identify the image as an input reference.
	// Require the durable task's explicit image-to-image context so an output
	// download failure during text-to-image cannot restart generation.
	if (isAsyncImageReferenceNetworkDownloadFailure(message) || isAsyncImageReferenceAdditionalDownloadFailure(message)) && !IsAsyncImageInputReferenceTask(ctx) {
		return false
	}
	return IsAsyncImageReferenceAccountFailoverMessage(statusCode, message)
}

func isAsyncImageReferenceNetworkDownloadFailure(message string) bool {
	message = strings.Join(strings.Fields(message), " ")
	if matches := asyncImageReferenceReadResetPattern.FindStringSubmatch(message); len(matches) == 3 {
		if matches[2] != "" && matches[1] != matches[2] {
			return false
		}
		source, err := url.Parse(matches[1])
		return err == nil && strings.EqualFold(source.Scheme, "https") && source.Hostname() != "" && source.User == nil && source.Fragment == ""
	}
	var matches []string
	for _, pattern := range []*regexp.Regexp{
		asyncImageReferenceNetworkDownloadPattern, asyncImageReferenceRedactedNetworkDownloadPattern,
		asyncImageReferenceTLSDownloadPattern, asyncImageReferenceRedactedTLSDownloadPattern,
	} {
		matches = pattern.FindStringSubmatch(message)
		if len(matches) != 0 {
			break
		}
	}
	if len(matches) != 3 || matches[1] != matches[2] {
		return false
	}
	source, err := url.Parse(matches[1])
	return err == nil && strings.EqualFold(source.Scheme, "https") && source.Hostname() != "" && source.User == nil && source.Fragment == ""
}

func newAsyncImageReferenceAccountFailoverError(body []byte, headers http.Header) *UpstreamFailoverError {
	if isAsyncImageReferenceBudgetExceeded(http.StatusBadRequest, body) {
		return newAsyncImageReferenceBudgetFailoverError(body, headers)
	}
	_, providerCode, requestID := geminiAsyncUpstreamDiagnostics(body, geminiRequestIDFromHeaders(headers))
	message := strings.Join(strings.Fields(extractUpstreamErrorMessage(body)), " ")
	if isAsyncImageReferenceNetworkDownloadFailure(message) {
		if strings.HasSuffix(strings.ToLower(message), "read: connection reset by peer") {
			message = `Error while downloading https://*** read tcp ***:443: read: connection reset by peer`
		} else if strings.HasSuffix(strings.ToLower(message), "net/http: tls handshake timeout") {
			message = `Error while downloading https://*** Get "https://***": net/http: TLS handshake timeout`
		} else {
			message = `Error while downloading https://*** Get "https://***": dial tcp ***:443: i/o timeout`
		}
	} else {
		// Remove entire URLs before diagnostic truncation so long signed paths
		// cannot leak or push the failure prototype beyond the retained prefix.
		message = asyncImageReferenceDiagnosticURLPattern.ReplaceAllString(message, "https://***")
		message = logredact.RedactText(sanitizeUpstreamErrorMessage(message), "api_key", "apikey", "secret", "token", "authorization")
		message = truncateGeminiAsyncDiagnostic(message, geminiAsyncUpstreamMessageMaxRunes)
		if !IsAsyncImageReferenceAccountFailoverMessage(http.StatusBadRequest, message) {
			message = "Reference image fetch failed"
		}
	}
	return &UpstreamFailoverError{
		StatusCode: http.StatusBadRequest, ResponseBody: body, ResponseHeaders: headers.Clone(),
		ProviderErrorCode: providerCode, ProviderErrorMessage: message, UpstreamRequestID: requestID,
		Scope: GatewayFailureScopeAccount, Reason: GatewayFailureReason(AsyncImageReferenceAccountFailoverCode),
		NextAccountAction: NextAccountRetry, RetryableOnSameAccount: false,
	}
}

func (e *UpstreamFailoverError) IsAsyncImageReferenceAccountFailover() bool {
	return e != nil && (e.Reason == GatewayFailureReason(AsyncImageReferenceAccountFailoverCode) || e.IsAsyncImageReferenceBudgetExceeded())
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
