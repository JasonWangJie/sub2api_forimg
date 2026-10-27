package service

import (
	"context"
	"net/http"
	"strings"
)

const (
	AsyncImageRetryableRejectionCode            = "async_image_retryable_rejection"
	AsyncImageReferenceTransportRetryCode       = "async_image_reference_transport_retry"
	AsyncImageRetryableRejectionReason          = GatewayFailureReason(AsyncImageRetryableRejectionCode)
	AsyncImageReferenceTransportRejectionReason = GatewayFailureReason("async_image_reference_transport_rejected")
)

type asyncImageProviderRetryPolicyKey struct{}

// The durable worker, which owns persisted retry budgets, enables this policy.
// Synchronous callers and gateway callers without a durable budget keep their
// existing exhaustion behavior.
func WithAsyncImageProviderRetryPolicy(ctx context.Context) context.Context {
	return context.WithValue(ctx, asyncImageProviderRetryPolicyKey{}, true)
}

func AsyncImageProviderRetryPolicyEnabled(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(asyncImageProviderRetryPolicyKey{}).(bool)
	return enabled
}

func (e *UpstreamFailoverError) IsAsyncImageBoundedRetryFailure() bool {
	return e != nil && e.ShouldRetryNextAccount() &&
		(e.StatusCode == http.StatusBadRequest || e.StatusCode == http.StatusUnprocessableEntity) &&
		(e.IsAsyncImageRetryableRejection() || e.IsAsyncImageReferenceAccountFailover() ||
			e.IsAsyncImageServiceTimeout400() || e.IsAsyncImageInternalError422() || e.IsAsyncImageSizeCapabilityMismatch())
}

// AsyncImageRetryableRejectionKind classifies complete provider rejections by
// structured code and meaning, rather than matching one whole error sentence.
// A rejection may reflect one provider's limits; it is not proof that the
// request is invalid for every configured provider.
func AsyncImageRetryableRejectionKind(status int, providerCode, message string, inputReference bool) string {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		return ""
	}
	if isAsyncImageTerminalRejection(providerCode, message) {
		return ""
	}
	text := strings.ToLower(strings.Join(strings.Fields(providerCode+" "+message), " "))
	if inputReference {
		transport := containsAsyncImageRejectionPart(text, "filedata", "fileuri", "file_id", "file id")
		unsupported := containsAsyncImageRejectionPart(text, "requires", "must be", "unsupported", "not supported", "only accepts", "does not support")
		if transport && unsupported {
			return "reference_transport"
		}
		image := containsAsyncImageRejectionPart(text, "image", "reference")
		limit := containsAsyncImageRejectionPart(text, "exceed", "too large", "larger than", "maximum", "size limit")
		sizeEvidence := containsAsyncImageRejectionPart(text, "size", "byte", " mb", " mib", "mb ", "mib ", "too large", "larger than", "pixel", "dimension", "resolution")
		if image && limit && sizeEvidence {
			return "reference_limit"
		}
	}
	code := strings.ToLower(strings.TrimSpace(providerCode))
	switch code {
	case "invalid_image_request", "image_processing_error", "image_generation_error", "image_generation_failed", "generation_failed", "processing_error":
		return "processing"
	}
	if containsAsyncImageRejectionPart(text, "image", "prompt", "generation") &&
		containsAsyncImageRejectionPart(text, "could not be processed", "cannot be processed", "unable to process", "failed to process", "processing failed", "generation failed", "failed to generate") {
		return "processing"
	}
	return ""
}

func isAsyncImageTerminalRejection(providerCode, message string) bool {
	text := strings.ToLower(strings.Join(strings.Fields(providerCode+" "+message), " "))
	text = strings.NewReplacer("_", " ", "-", " ").Replace(text)
	for _, stop := range []string{
		"execution unknown", "outcome is unknown", "may already have been charged", "may already be charged", "already been charged",
		"generation may have started", "generation already started", "execution may have started",
		"do not automatically regenerate", "do not regenerate", "do not retry",
		"content policy", "content filter", "content rejected", "safety", "guardrail", "moderation", "nsfw",
		"禁止自动再生成", "不要自动再生成", "内容审核", "安全审核",
		"generated result", "generated image", "image output", "output image", "result image",
		"invalid base64", "invalid image data uri", "invalid image data", "corrupt image", "corrupted image", "image is corrupt",
		"unsupported image format", "unsupported image type",
		"image mime mismatch", "image signature mismatch", "declared image type does not match",
		"blocked network address", "url must be an absolute", "redirects must use https",
		"prompt is required", "missing required", "required parameter", "requires multipart/form data",
		"no input image", "no reference image", "at least one image", "at most 8 images",
	} {
		if strings.Contains(text, stop) {
			return true
		}
	}
	return false
}

func containsAsyncImageRejectionPart(text string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(text, part) {
			return true
		}
	}
	return false
}

func newAsyncImageRetryableRejectionForContext(ctx context.Context, status int, body []byte, headers http.Header) *UpstreamFailoverError {
	if AsyncImageAccountAttemptCaptureFromContext(ctx) == nil && !IsGeminiAsyncImageGeneration(ctx) {
		return nil
	}
	message, code, requestID := geminiAsyncUpstreamDiagnostics(body, geminiRequestIDFromHeaders(headers))
	// Classify the original message before diagnostic truncation/redaction can
	// remove the meaningful suffix of a long provider error.
	kind := AsyncImageRetryableRejectionKind(status, code, extractUpstreamErrorMessage(body), IsAsyncImageInputReferenceTask(ctx))
	if kind == "" {
		return nil
	}
	reason := AsyncImageRetryableRejectionReason
	if kind == "reference_transport" {
		reason = AsyncImageReferenceTransportRejectionReason
	}
	message = asyncImageReferenceDiagnosticURLPattern.ReplaceAllString(message, "https://***")
	return &UpstreamFailoverError{
		StatusCode: status, ResponseBody: body, ResponseHeaders: headers.Clone(),
		ProviderErrorCode: code, ProviderErrorMessage: message, UpstreamRequestID: requestID,
		Scope: GatewayFailureScopeAccount, Reason: reason, NextAccountAction: NextAccountRetry,
		// Try other accounts first. The durable worker owns the bounded fallback
		// retry after selection is exhausted, and increments persisted counters.
		RetryableOnSameAccount: false,
	}
}

func (e *UpstreamFailoverError) IsAsyncImageRetryableRejection() bool {
	return e != nil && (e.Reason == AsyncImageRetryableRejectionReason || e.Reason == AsyncImageReferenceTransportRejectionReason)
}
