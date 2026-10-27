package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAsyncImageRetryableRejectionSemanticFamilies(t *testing.T) {
	for _, test := range []struct {
		name, code, message, kind string
		input                     bool
	}{
		{"production reference limit", "400", "reference image exceeds limit: 20 MB limit exceeded", "reference_limit", true},
		{"different limit and wording", "", "Provider rejected input image larger than its maximum 32 MiB", "reference_limit", true},
		{"different prefix and count", "", "request abc: reference image size limit exceeded (maximum 16MB)", "reference_limit", true},
		{"production fileData", "400", "Provider API error: fileData requires a user YouTube reference or a gateway file_id (request id: changing-id)", "reference_transport", true},
		{"different transport wording", "", "remote fileUri is not supported by this upstream", "reference_transport", true},
		{"production processing", "invalid_image_request", "The prompt or input images could not be processed. Adjust the request and try again.", "processing", false},
		{"processing paraphrase", "", "Image generation failed: unable to process this prompt, please try a different provider", "processing", false},
		{"structured code", "image_processing_error", "Provider request 123 failed", "processing", false},
		{"invalid base64 overrides code", "invalid_image_request", "invalid base64 input image", "", true},
		{"safety overrides code", "invalid_image_request", "Image processing failed: content_policy_violation", "", true},
		{"missing input overrides code", "invalid_image_request", "Missing required reference image", "", true},
		{"missing prompt", "invalid_image_request", "prompt is required", "", false},
		{"global image count", "invalid_image_request", "at most 8 images are allowed", "", true},
		{"local address block", "invalid_image_request", "blocked network address", "", true},
		{"output download", "invalid_image_request", "Generated image download failed", "", true},
		{"unknown execution", "invalid_image_request", "generation may have started", "", false},
		{"alternate charged wording", "invalid_image_request", "Image processing failed; generation may already be charged", "", true},
		{"output fetch word order", "invalid_image_request", "Failed to fetch generated image", "", true},
		{"corrupt image word order", "invalid_image_request", "Input image is corrupt", "", true},
		{"explicit stop", "invalid_image_request", "Image processing failed; do not retry", "", false},
		{"long explicit stop", "invalid_image_request", strings.Repeat("detail ", 120) + " do not automatically regenerate", "", false},
		{"unrelated 400", "invalid_request_error", "Unrecognized query parameter", "", true},
		{"reference context required", "400", "fileData requires a gateway file_id", "", false},
		{"download time budget is not an image byte limit", "", "Reference image download exceeded its total time budget for https://example.test/ref (budgetMs=35000; reason=timeout)", "", true},
		{"direct and proxy timeout is not a size limit", "", "Reference image download failed; direct=timeout of 15000ms exceeded; proxy=HTTP 502", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, status := range []int{400, 422} {
				require.Equal(t, test.kind, AsyncImageRetryableRejectionKind(status, test.code, test.message, test.input))
			}
			for _, status := range []int{200, 401, 403, 429, 500, 502, 504} {
				require.Empty(t, AsyncImageRetryableRejectionKind(status, test.code, test.message, test.input))
			}
		})
	}
}

func TestAsyncImageRetryableRejectionPrivateContextAndDiagnostics(t *testing.T) {
	for _, code := range []string{"invalid_image_request", "image_processing_error"} {
		body, err := json.Marshal(map[string]any{"error": map[string]any{"code": code, "message": "Image processing failed for https://example.test/ref?token=private"}})
		require.NoError(t, err)
		headers := http.Header{"Retry-After": {"13"}, "X-Request-Id": {"req-rejection"}}
		require.Nil(t, newAsyncImageRetryableRejectionForContext(context.Background(), 400, body, headers))
		ctx := WithAsyncImageAccountAttemptCapture(context.Background(), &AsyncImageAccountAttemptCapture{})
		failure := newAsyncImageRetryableRejectionForContext(ctx, 400, body, headers)
		require.NotNil(t, failure)
		require.True(t, failure.IsAsyncImageRetryableRejection())
		require.True(t, failure.ShouldRetryNextAccount())
		require.False(t, failure.RetryableOnSameAccount, "alternate accounts must be tried before durable fallback")
		require.Equal(t, code, failure.ProviderErrorCode)
		require.Equal(t, "req-rejection", failure.UpstreamRequestID)
		require.Equal(t, "13", failure.ResponseHeaders.Get("Retry-After"))
		require.NotContains(t, failure.ProviderErrorMessage, "example.test")
		require.NotContains(t, failure.ProviderErrorMessage, "private")
		require.Equal(t, body, failure.ResponseBody)
	}
}
