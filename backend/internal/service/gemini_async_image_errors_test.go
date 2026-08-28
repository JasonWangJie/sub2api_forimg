package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeminiAsyncAccountFailover400ClassifiesWrappedAndPlainResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "structured invalid request", body: `{"error":{"message":"Invalid request"}}`, want: true},
		{name: "plain invalid request", body: `Invalid request`, want: true},
		{name: "empty response", body: ``, want: false},
		{name: "unclassified empty body", body: `{"status":400}`, want: false},
		{name: "reference fetch", body: `{"error":{"message":"image_url fetch failed: timeout"}}`, want: false},
		{name: "pixel limit", body: `{"error":{"message":"IMAGE_TOO_MANY_PIXELS"}}`, want: false},
		{name: "missing prompt", body: `{"error":{"message":"prompt is required"}}`, want: false},
		{name: "content policy", body: `{"error":{"message":"violates our content policy"}}`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isGeminiAsyncAccountFailover400([]byte(tt.body)))
		})
	}
}

func TestNewGeminiAsyncAccountFailover400PreservesResponseContext(t *testing.T) {
	body := []byte(`Invalid request`)
	headers := http.Header{"X-Goog-Request-Id": []string{"req-1"}}
	err := newGeminiAsyncAccountFailover400(body, headers)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, body, err.ResponseBody)
	require.Equal(t, "req-1", err.ResponseHeaders.Get("X-Goog-Request-Id"))
	require.Equal(t, NextAccountRetry, err.NextAccountAction)
	require.Equal(t, GatewayFailureReason("async_image_invalid_request"), err.Reason)
}
