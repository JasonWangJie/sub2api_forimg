package service

import "testing"

func TestIsAsyncImageReferenceFetchFailureMessage(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    bool
	}{
		{name: "image_url", message: "image_url fetch failed: curl: (28) timeout", want: true},
		{name: "spaced image URL", message: "image URL fetch failed: context deadline exceeded", want: true},
		{name: "fileUri fetch", message: "fileUri fetch failed: connection reset", want: true},
		{name: "fileUri download", message: "fileUri download failed: timeout", want: true},
		{name: "fetch image", message: "failed to fetch image from CDN", want: true},
		{name: "download image", message: "failed to download image from CDN", want: true},
		{name: "download reference", message: "download reference image: unexpected HTTP status 503", want: true},
		{name: "OpenAI reference", message: "download OpenAI reference image: net/http: TLS handshake timeout", want: true},
		{name: "fetch reference", message: "fetch reference image: temporary failure in name resolution", want: true},
		{name: "reference download", message: "Reference image download failed for https://images.example/ref.png direct=timeout; proxy=Request failed with status code 502", want: true},
		{name: "reference download total budget", message: "Reference image download exceeded its total time budget for https://images.example/ref.png (budgetMs=35000; reason=timeout)", want: true},
		{name: "reference fetch", message: "reference image fetch failed: failed after 3 attempts: handshake or fetch error", want: true},
		{name: "whitespace and case", message: " \n REFERENCE   IMAGE\tDOWNLOAD FAILED: timeout \n", want: true},
		{name: "could not fetch", message: "could not fetch image: connection reset", want: true},
		{name: "unable to fetch", message: "unable to fetch image: timeout", want: true},
		{name: "upstream fileData remains identifiable", message: "invalid fileData image: image URL fetch failed: context deadline exceeded", want: true},
		{name: "fetch invalid image", message: "invalid image: fetch failed with handshake error", want: true},
		{name: "fetch container compatibility", message: "download OpenAI reference image: error: reason=INVALID_IMAGE message=image container is invalid or contains trailing data", want: true},
		{name: "URL digits are not HTTP status", message: "Reference image download failed for https://images.example/403.png?version=404: timeout", want: true},
		{name: "status prefix digits are not 404", message: "Reference image download failed: HTTP 4040", want: true},
		{name: "empty", message: "", want: false},
		{name: "generic timeout", message: "context deadline exceeded", want: false},
		{name: "generic upstream", message: "Internal error encountered", want: false},
		{name: "generic reference mention", message: "reference image request failed", want: false},
		{name: "bare invalid image", message: "INVALID_IMAGE: invalid image", want: false},
		{name: "bare container", message: "image container is invalid or contains trailing data", want: false},
		{name: "generated download", message: "download generated image: download reference image: HTTP status 503", want: false},
		{name: "generated image fetch", message: "failed to fetch image: generated image download timeout", want: false},
		{name: "result image fetch", message: "failed to download image: result image is unavailable", want: false},
		{name: "output image fetch", message: "failed to fetch image: output image timeout", want: false},
		{name: "execution unknown", message: "reference image fetch failed; generation outcome is unknown", want: false},
		{name: "content policy", message: "reference image fetch failed: rejected by the upstream content policy", want: false},
		{name: "safety policy", message: "Reference image download failed: rejected by the upstream safety policy", want: false},
		{name: "guardrails", message: "image_url fetch failed: guardrails refusal", want: false},
		{name: "Chinese safety", message: "reference image fetch failed: 图片或提示词未通过安全审核", want: false},
		{name: "stop regenerating", message: "Reference image download failed: Do not automatically regenerate", want: false},
		{name: "stop retrying", message: "reference image fetch failed: Do not retry", want: false},
		{name: "HTTP unauthorized", message: "download reference image: HTTP 401", want: false},
		{name: "HTTP forbidden", message: "Reference image download failed: HTTP 403", want: false},
		{name: "HTTP not found", message: "Reference image download failed: HTTP 404", want: false},
		{name: "downloader status", message: "download OpenAI reference image: download reference image: unexpected HTTP status 403", want: false},
		{name: "provider status code", message: "Reference image download failed: Request failed with status code 404", want: false},
		{name: "JSON status code", message: `reference image fetch failed: {"status_code":401}`, want: false},
		{name: "status equals", message: "reference image fetch failed: status=403", want: false},
		{name: "reason unauthorized", message: "image_url fetch failed: 401 Unauthorized", want: false},
		{name: "reason forbidden", message: "image_url fetch failed: 403 Forbidden", want: false},
		{name: "reason not found", message: "image_url fetch failed: 404 Not Found", want: false},
		{name: "configured size", message: "download OpenAI reference image: reference image exceeds the configured size limit", want: false},
		{name: "aggregate bytes", message: "download reference image: reference image bytes exceed the configured aggregate limit", want: false},
		{name: "provider megabytes", message: "reference image fetch failed: 20 MB limit exceeded", want: false},
		{name: "provider mebibytes", message: "reference image fetch failed: 32 MiB limit exceeded", want: false},
		{name: "too many pixels", message: "reference image fetch failed: IMAGE_TOO_MANY_PIXELS", want: false},
		{name: "pixel limit", message: "Reference image download failed: pixel limit exceeded", want: false},
		{name: "corrupt", message: "reference image fetch failed: corrupt image", want: false},
		{name: "corrupted", message: "reference image fetch failed: corrupted image", want: false},
		{name: "MIME mismatch", message: "download reference image: IMAGE_MIME_MISMATCH", want: false},
		{name: "signature mismatch", message: "download reference image: IMAGE_SIGNATURE_MISMATCH", want: false},
		{name: "invalid base64", message: "download reference image: invalid base64 image data URI", want: false},
		{name: "unsupported format", message: "failed to download image: unsupported image format", want: false},
		{name: "invalid URL", message: "download reference image: reference image URL must be an absolute HTTPS URL or an image data URI", want: false},
		{name: "blocked URL", message: "download reference image: reference image URL points to a blocked network address", want: false},
		{name: "redirect limit", message: "download reference image: reference image redirect limit exceeded", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAsyncImageReferenceFetchFailureMessage(tt.message); got != tt.want {
				t.Errorf("IsAsyncImageReferenceFetchFailureMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}
