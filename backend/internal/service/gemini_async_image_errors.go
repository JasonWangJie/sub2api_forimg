package service

import (
	"net/http"
	"strings"
)

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
	return &UpstreamFailoverError{
		StatusCode:        http.StatusBadRequest,
		ResponseBody:      body,
		ResponseHeaders:   headers.Clone(),
		NextAccountAction: NextAccountRetry,
		Reason:            GatewayFailureReason("async_image_invalid_request"),
	}
}
