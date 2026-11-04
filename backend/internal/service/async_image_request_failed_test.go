package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAsyncImageRequestFailedRequiresComplete400AndProviderCode(t *testing.T) {
	for _, test := range []struct {
		name, code, message string
		status              int
		want                string
	}{
		{"production", "upstream_error", "request failed", 400, "processing"},
		{"normalized", " UPSTREAM_ERROR ", " Request\tFAILED. ", 400, "processing"},
		{"different status", "upstream_error", "request failed", 422, ""},
		{"missing code", "", "request failed", 400, ""},
		{"different code", "invalid_request_error", "request failed", 400, ""},
		{"other failure", "upstream_error", "request failed: invalid request", 400, ""},
		{"content refusal", "upstream_error", "request failed: content policy", 400, ""},
		{"unknown execution", "upstream_error", "request failed; execution unknown", 400, ""},
		{"explicit stop", "upstream_error", "request failed; do not retry", 400, ""},
		{"output failure", "upstream_error", "generated image request failed", 400, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, input := range []bool{false, true} {
				require.Equal(t, test.want, AsyncImageRetryableRejectionKind(test.status, test.code, test.message, input))
			}
		})
	}
	for _, body := range [][]byte{
		[]byte(`{"error":{"type":"invalid_request_error","code":"upstream_error","message":"request failed: invalid request"}}`),
		[]byte(`{"error":{"type":"invalid_request_error","code":"upstream_error","message":"request failed"}}`),
	} {
		require.False(t, isGeminiAsyncAccountFailover400(body), "the generic invalid_request fallback must not bypass this exact rule")
		require.False(t, isOpenAIAsyncAccountFailover400(extractUpstreamErrorMessage(body)))
	}
	// Existing explicit processing and reference-limit errors retain their policy.
	require.Equal(t, "processing", AsyncImageRetryableRejectionKind(400, "image_processing_error", "Upstream request failed", false))
	require.Equal(t, "reference_limit", AsyncImageRetryableRejectionKind(400, "", "request failed: reference image exceeds the maximum 20 MB size", true))
}

func TestAsyncImageRequestFailedPreservesDiagnosticsAndStopDetails(t *testing.T) {
	body := []byte(`{"error":{"code":"upstream_error","message":"request failed"}}`)
	headers := http.Header{"X-Request-Id": {"req-request-failed"}, "Retry-After": {"17"}}
	require.Nil(t, newAsyncImageRetryableRejectionForContext(context.Background(), 400, body, headers))
	ctx := WithAsyncImageAccountAttemptCapture(context.Background(), &AsyncImageAccountAttemptCapture{})
	failure := newAsyncImageRetryableRejectionForContext(ctx, 400, body, headers)
	require.NotNil(t, failure)
	require.True(t, failure.IsAsyncImageRetryableRejection())
	require.True(t, failure.ShouldRetryNextAccount())
	require.False(t, failure.RetryableOnSameAccount)
	require.Equal(t, "upstream_error", failure.ProviderErrorCode)
	require.Equal(t, "request failed", failure.ProviderErrorMessage)
	require.Equal(t, "req-request-failed", failure.UpstreamRequestID)
	require.Equal(t, "17", failure.ResponseHeaders.Get("Retry-After"))
	require.Equal(t, body, failure.ResponseBody)
	for _, body := range []string{
		`{"error":{"code":"upstream_error","message":"request failed","details":"Do not retry"}}`,
		`{"error":{"code":"upstream_error","message":"request failed","cause":{"code":"execution_unknown"}}}`,
		`{"error":{"code":"upstream_error","message":"request failed","type":"content_policy_violation"}}`,
		`{"error":{"code":"upstream_error","message":"request failed","details":"generation may have started"}}`,
		`{"error":{"code":"upstream_error","message":"request failed","details":"generated image download failed"}}`,
		`{"error":{"code":"upstream_error","message":"request failed","details":"Do\nnot\nretry"}}`,
		`{"error":{"code":"upstream_error","message":"request failed","details":"\u5185\u5bb9\u5ba1\u6838"}}`,
		`{"error":{"code":"upstream_error","message":"request failed","details":[{"cause":"execution_unknown"}]}}`,
	} {
		require.Nil(t, newAsyncImageRetryableRejectionForContext(ctx, 400, []byte(body), headers), body)
	}
	wrapped, err := json.Marshal(map[string]any{"error": map[string]string{
		"message": `{"error":{"code":"upstream_error","message":"request failed","details":"Do\nnot\nretry"}}`,
	}})
	require.NoError(t, err)
	require.Equal(t, "request failed", extractUpstreamErrorMessage(wrapped))
	require.Nil(t, newAsyncImageRetryableRejectionForContext(ctx, 400, wrapped, headers))
}

func TestAsyncGeminiUpstreamRequestFailedRequiresExactRejection(t *testing.T) {
	body := []byte(`{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"Upstream request failed. Please retry later."}}`)
	headers := http.Header{"X-Request-Id": {"req-sc"}, "Retry-After": {"17"}}
	openAI := WithAsyncImageAccountAttemptCapture(context.Background(), &AsyncImageAccountAttemptCapture{})
	gemini := WithGeminiAsyncImageGeneration(openAI)
	require.Nil(t, newAsyncImageRetryableRejectionForContext(context.Background(), 400, body, headers))
	require.Nil(t, newAsyncImageRetryableRejectionForContext(openAI, 400, body, headers))
	require.Nil(t, newAsyncImageRetryableRejectionForContext(gemini, 422, body, headers))
	failure := newAsyncImageRetryableRejectionForContext(gemini, 400, body, headers)
	require.NotNil(t, failure)
	require.True(t, failure.IsAsyncImageRetryableRejection())
	require.True(t, failure.ShouldRetryNextAccount())
	require.False(t, failure.RetryableOnSameAccount)
	require.Equal(t, "INVALID_ARGUMENT", failure.ProviderErrorCode)
	require.Equal(t, "Upstream request failed. Please retry later.", failure.ProviderErrorMessage)
	require.Equal(t, "req-sc", failure.UpstreamRequestID)
	require.Equal(t, "17", failure.ResponseHeaders.Get("Retry-After"))
	for _, body := range []string{
		`{"error":{"status":"INVALID_ARGUMENT","message":"Invalid size: expected 1024x1024"}}`,
		`{"error":{"message":"Upstream request failed. Please retry later."}}`,
		`{"error":{"status":"FAILED_PRECONDITION","message":"Upstream request failed. Please retry later."}}`,
		`{"error":{"status":"INVALID_ARGUMENT","message":"Upstream request failed. Please retry later. Invalid request."}}`,
		`{"error":{"type":"invalid_request_error","status":"INVALID_ARGUMENT","message":"Upstream request failed. Please retry later.","details":[{"cause":"execution_unknown"}]}}`,
		`{"error":{"status":"INVALID_ARGUMENT","message":"Upstream request failed. Please retry later.","details":"Do\nnot\nretry"}}`,
	} {
		require.Nil(t, newAsyncImageRetryableRejectionForContext(gemini, 400, []byte(body), headers), body)
		require.False(t, isGeminiAsyncAccountFailover400([]byte(body)), body)
	}
}
