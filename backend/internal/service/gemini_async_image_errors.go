package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/tidwall/gjson"
)

const (
	geminiAsyncUpstreamMessageMaxRunes = 500
	geminiProviderErrorCodeMaxRunes    = 64
	geminiUpstreamRequestIDMaxRunes    = 255
)

// GeminiAsyncImageUpstreamError carries the safe provider diagnostics needed
// by the durable worker. It is only returned on the internal async-image path;
// synchronous Gemini callers keep the generic client-facing error mapping.
type GeminiAsyncImageUpstreamError struct {
	StatusCode           int
	ProviderErrorCode    string
	ProviderErrorMessage string
	UpstreamRequestID    string
}

func (e *GeminiAsyncImageUpstreamError) Error() string {
	if e == nil {
		return "Gemini async image upstream error"
	}
	if e.ProviderErrorMessage != "" {
		return e.ProviderErrorMessage
	}
	return fmt.Sprintf("upstream error: %d", e.StatusCode)
}

func geminiAsyncUpstreamDiagnostics(body []byte, requestID string) (message, providerCode, safeRequestID string) {
	message = strings.TrimSpace(extractUpstreamErrorMessage(body))
	if message == "" && !json.Valid(body) {
		message = strings.TrimSpace(string(body))
	}
	message = sanitizeUpstreamErrorMessage(message)
	message = logredact.RedactText(strings.Join(strings.Fields(message), " "), "api_key", "apikey", "secret", "token", "authorization")
	message = truncateGeminiAsyncDiagnostic(message, geminiAsyncUpstreamMessageMaxRunes)

	providerCode = strings.TrimSpace(gjson.GetBytes(body, "error.status").String())
	if providerCode == "" {
		inner := strings.TrimSpace(gjson.GetBytes(body, "error.message").String())
		if strings.HasPrefix(inner, "{") {
			providerCode = strings.TrimSpace(gjson.Get(inner, "error.status").String())
		}
	}
	if providerCode == "" {
		providerCode = strings.TrimSpace(extractUpstreamErrorCode(body))
	}
	providerCode = logredact.RedactText(providerCode, "api_key", "apikey", "secret", "token", "authorization")
	providerCode = truncateGeminiAsyncDiagnostic(providerCode, geminiProviderErrorCodeMaxRunes)

	safeRequestID = logredact.RedactText(strings.TrimSpace(requestID), "api_key", "apikey", "secret", "token", "authorization")
	safeRequestID = truncateGeminiAsyncDiagnostic(safeRequestID, geminiUpstreamRequestIDMaxRunes)
	return message, providerCode, safeRequestID
}

func truncateGeminiAsyncDiagnostic(value string, maxRunes int) string {
	if value == "" || maxRunes <= 0 || utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes])
}

func geminiRequestIDFromHeaders(headers http.Header) string {
	for _, key := range []string{"x-request-id", "x-goog-request-id", "request-id"} {
		if value := strings.TrimSpace(headers.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

// isGeminiAsyncAccountFailover400 classifies an async image 400 before the
// response is mapped to a client error. Some upstream gateways return a plain
// or wrapped "Invalid request" body that the normal extractor cannot decode.
// Those responses still need to reach the account failover loop.
func isGeminiAsyncAccountFailover400(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	raw := strings.ToLower(strings.TrimSpace(string(body)))
	message := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	combined := raw + " " + message
	for _, fragment := range []string{
		"image_url fetch failed", "image url fetch failed", "fileuri fetch failed",
		"failed to fetch image", "failed to download image", "download reference image",
		"fetch reference image", "image_too_many_pixels", "exceeds the configured pixel limit",
		"image container is invalid", "invalid image file", "unsupported_image_format",
		"requires multipart/form-data", "prompt is required", "content policy",
		"safety policy", "violates our content policy",
	} {
		if strings.Contains(combined, fragment) {
			return false
		}
	}
	return strings.Contains(combined, "invalid request") ||
		strings.Contains(combined, "invalid_request")
}

func newGeminiAsyncAccountFailover400(body []byte, headers http.Header) *UpstreamFailoverError {
	message, providerCode, requestID := geminiAsyncUpstreamDiagnostics(body, geminiRequestIDFromHeaders(headers))
	return &UpstreamFailoverError{
		StatusCode:           http.StatusBadRequest,
		ResponseBody:         body,
		ResponseHeaders:      headers.Clone(),
		ProviderErrorCode:    providerCode,
		ProviderErrorMessage: message,
		UpstreamRequestID:    requestID,
		NextAccountAction:    NextAccountRetry,
		Reason:               GatewayFailureReason("async_image_invalid_request"),
	}
}
