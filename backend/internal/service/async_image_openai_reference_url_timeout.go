package service

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	AsyncImageOpenAIReferenceURLTimeoutCode   = "async_image_openai_reference_url_timeout"
	AsyncImageOpenAIReferenceURLTimeoutReason = GatewayFailureReason(AsyncImageOpenAIReferenceURLTimeoutCode)
	openAIReferenceURLTimeoutMessage          = "Unable to download content from the provided URL before the timeout. Check that the URL is publicly accessible and responds promptly, or upload the file and provide a file_id instead."
)

// Match the input-fetch rejection only when the durable worker can change
// transport. The same URL body must not be sent again on this signal.
func newAsyncImageOpenAIReferenceURLTimeout(ctx context.Context, parsed *OpenAIImagesRequest, status int, body []byte, headers http.Header) *UpstreamFailoverError {
	if status != http.StatusBadRequest || !parsed.IsEdits() || parsed.Multipart ||
		AsyncImageAccountAttemptCaptureFromContext(ctx) == nil || !IsAsyncImageInputReferenceTask(ctx) ||
		!AsyncImageReferenceLocalFallbackFromContext(ctx) || !gjson.ValidBytes(body) {
		return nil
	}
	response := gjson.ParseBytes(body)
	if response.Get("error.type").String() != "invalid_request_error" ||
		response.Get("error.code").String() != "invalid_value" || response.Get("error.param").String() != "url" ||
		strings.Join(strings.Fields(response.Get("error.message").String()), " ") != openAIReferenceURLTimeoutMessage ||
		hasAsyncImageTerminalRejectionDetail(response) {
		return nil
	}
	for _, imageURL := range parsed.ReferencedImageURLs() {
		if !strings.HasPrefix(strings.ToLower(imageURL), "https://") {
			continue
		}
		_, code, requestID := geminiAsyncUpstreamDiagnostics(body, geminiRequestIDFromHeaders(headers))
		return &UpstreamFailoverError{
			StatusCode: status, ResponseBody: body, ResponseHeaders: headers.Clone(),
			ProviderErrorCode: code, ProviderErrorMessage: openAIReferenceURLTimeoutMessage, UpstreamRequestID: requestID,
			Scope: GatewayFailureScopeAccount, Reason: AsyncImageOpenAIReferenceURLTimeoutReason, NextAccountAction: NextAccountStop,
		}
	}
	return nil
}

func (e *UpstreamFailoverError) IsAsyncImageOpenAIReferenceURLTimeout() bool {
	return e != nil && e.Reason == AsyncImageOpenAIReferenceURLTimeoutReason
}

// NormalizeOpenAIImageEditReferencesForLocalFallback limits conversion to the
// references the OpenAI edits parser accepts, in their original order.
func NormalizeOpenAIImageEditReferencesForLocalFallback(body []byte) ([]byte, error) {
	parsed := &OpenAIImagesRequest{Endpoint: openAIImagesEditsEndpoint, NeedsInputRewrite: true}
	if err := parseOpenAIImagesEditReferenceURLs(body, parsed, false); err != nil {
		return nil, err
	}
	if len(parsed.InputImageURLs) == 0 {
		return nil, errors.New("image_urls is required")
	}
	prepared, _, err := rewriteOpenAIImagesEditInputs(body, "application/json", parsed)
	if err != nil {
		return nil, err
	}
	prepared, err = sjson.DeleteBytes(prepared, "image")
	if err != nil {
		return nil, err
	}
	mask := gjson.GetBytes(body, "mask.image_url")
	if maskURL := strings.TrimSpace(mask.Str); mask.Type == gjson.String && maskURL != "" {
		return sjson.SetBytes(prepared, "mask", map[string]string{"image_url": maskURL})
	}
	return sjson.DeleteBytes(prepared, "mask")
}
