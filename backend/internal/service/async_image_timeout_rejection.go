package service

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
)

const AsyncImageTimeoutRetryCode = "async_image_timeout_retry"
const AsyncImageTimeoutRejectionReason = GatewayFailureReason(AsyncImageTimeoutRetryCode)
const AsyncImageTimeoutTerminalCode = "async_image_timeout_terminal"
const AsyncImageTimeoutUnknownCode = "async_image_timeout_execution_unknown"
const AsyncImageTimeoutTerminalReason = GatewayFailureReason(AsyncImageTimeoutTerminalCode)
const AsyncImageTimeoutUnknownReason = GatewayFailureReason(AsyncImageTimeoutUnknownCode)

var asyncImageTimeoutMessagePattern = regexp.MustCompile(`(?i)timeout|\btime[\s-]+out\b|\btimed[\s-]+out\b|\bcontext\s+deadline\s+exceeded\b|超时`)

type asyncImageErrorBodyKey struct{}
type asyncImageErrorBodyTracker struct {
	io.ReadCloser
	eof, failed bool
}

func (r *asyncImageErrorBodyTracker) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err == io.EOF {
		r.eof = true
	} else if err != nil {
		r.failed = true
	}
	return n, err
}

// Track the first upstream body, before Gemini error policy can rebuild it.
// A limit-truncated or interrupted read never acquires a complete-response flag.
func trackAsyncImageErrorResponse(ctx context.Context, response *http.Response) context.Context {
	if AsyncImageAccountAttemptCaptureFromContext(ctx) == nil || response == nil || response.Body == nil || response.StatusCode < 400 {
		return ctx
	}
	tracker := &asyncImageErrorBodyTracker{ReadCloser: response.Body}
	response.Body = tracker
	return context.WithValue(ctx, asyncImageErrorBodyKey{}, tracker)
}

func asyncImageErrorBodyComplete(ctx context.Context) bool {
	tracker, _ := ctx.Value(asyncImageErrorBodyKey{}).(*asyncImageErrorBodyTracker)
	return tracker != nil && tracker.eof && !tracker.failed
}

func asyncImageErrorContainsOutput(value gjson.Result) bool {
	if value.Type == gjson.String {
		if gjson.Valid(value.Str) {
			return asyncImageErrorContainsOutput(gjson.Parse(value.Str))
		}
		return false
	}
	if value.IsArray() {
		for _, child := range value.Array() {
			if asyncImageErrorContainsOutput(child) {
				return true
			}
		}
		return false
	}
	for _, path := range []string{"data.0", "output.0", "images.0", "candidates.0"} {
		if value.Get(path).Exists() {
			return true
		}
	}
	for _, path := range []string{"response", "error.details", "error.message"} {
		if nested := value.Get(path); nested.Exists() && asyncImageErrorContainsOutput(nested) {
			return true
		}
	}
	return false
}

// Only received HTTP rejections reach this classifier. A transport error with
// no response must not be converted into a replayable generation attempt.
func newAsyncImageTimeoutRejectionForContext(ctx context.Context, status int, body []byte, headers http.Header) *UpstreamFailoverError {
	if AsyncImageAccountAttemptCaptureFromContext(ctx) == nil || ctx.Value(asyncImageErrorBodyKey{}) == nil || status < 400 || status > 599 {
		return nil
	}
	message := strings.TrimSpace(extractUpstreamErrorMessage(body))
	raw := strings.TrimSpace(string(body))
	if message == "" && !gjson.ValidBytes(body) {
		message = strings.TrimSpace(string(body))
	}
	code := extractUpstreamErrorCode(body)
	response := gjson.ParseBytes(body)
	// Permanent source/input failures retain the existing reference rejection
	// path even if an upstream includes an earlier timeout in its diagnostics.
	if status == http.StatusBadRequest && isAsyncImagePermanentReferenceTimeout(message) {
		return nil
	}
	if status == http.StatusBadRequest && !IsAsyncImageInputReferenceTask(ctx) &&
		(isAsyncImageReferenceNetworkDownloadFailure(message) || isAsyncImageReferenceAdditionalDownloadFailure(message)) {
		return nil
	}
	timeoutMessage := asyncImageTimeoutMessagePattern.MatchString(asyncImageReferenceDiagnosticURLPattern.ReplaceAllString(message, ""))
	missingReference := status == http.StatusNotFound && IsAsyncImageInputReferenceTask(ctx) && strings.TrimSpace(code) == "404" && isAsyncImageMissingFileReferenceMessage(message)
	if !timeoutMessage && !missingReference {
		return nil
	}
	if !asyncImageErrorBodyComplete(ctx) || ((strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[")) && !gjson.ValidBytes(body)) {
		failure := asyncImageTimeoutFailure(status, body, headers)
		failure.Scope, failure.Reason, failure.NextAccountAction = GatewayFailureScopeRequest, AsyncImageTimeoutUnknownReason, NextAccountStop
		return failure
	}
	terminal := isAsyncImageTerminalRejection(code, message) || hasAsyncImageTerminalRejectionDetail(response) || asyncImageErrorContainsOutput(response)
	if terminal {
		failure := asyncImageTimeoutFailure(status, body, headers)
		failure.Scope, failure.Reason, failure.NextAccountAction = GatewayFailureScopeRequest, AsyncImageTimeoutTerminalReason, NextAccountStop
		if asyncImageTimeoutHasUnknownOutcome(string(body)) {
			failure.Reason = AsyncImageTimeoutUnknownReason
		}
		return failure
	}
	if !timeoutMessage {
		return nil
	}
	// Preserve reference transport repair and its existing accounting/category
	// semantics before applying the general provider-timeout retry policy.
	if isAsyncImageReferenceAccountFailoverForContext(ctx, status, body) || IsAsyncImageCapacityRejection(status, message) || isAsyncImageServiceTimeout400(status, body) {
		return nil
	}
	if status == http.StatusBadRequest &&
		response.Get("error.type").String() == "invalid_request_error" && code == "invalid_value" &&
		response.Get("error.param").String() == "url" && strings.Join(strings.Fields(message), " ") == openAIReferenceURLTimeoutMessage {
		return nil
	}
	return asyncImageTimeoutFailure(status, body, headers)
}

func isAsyncImagePermanentReferenceTimeout(message string) bool {
	lower := strings.ToLower(message)
	reference := containsAsyncImageRejectionPart(lower, "reference image", "image_url", "fileuri", "error while downloading")
	if !reference || IsAsyncImageReferenceFetchFailureMessage(message) {
		return false
	}
	return containsAsyncImageRejectionPart(lower, "http 401", "http 403", "http 404", "http status 401", "http status 403", "http status 404", "status code 401", "status code 403", "status code 404", "corrupt image", "invalid base64", "unsupported image format", "image_too_many_pixels", "pixel limit", "configured size limit", "configured aggregate limit")
}

func asyncImageTimeoutFailure(status int, body []byte, headers http.Header) *UpstreamFailoverError {
	safeMessage, providerCode, requestID := geminiAsyncUpstreamDiagnostics(body, geminiRequestIDFromHeaders(headers))
	safeMessage = asyncImageReferenceDiagnosticURLPattern.ReplaceAllString(safeMessage, "https://***")
	return &UpstreamFailoverError{
		StatusCode: status, ResponseBody: body, ResponseHeaders: headers.Clone(),
		ProviderErrorCode: providerCode, ProviderErrorMessage: safeMessage, UpstreamRequestID: requestID,
		Scope: GatewayFailureScopeAccount, Reason: AsyncImageTimeoutRejectionReason,
		NextAccountAction: NextAccountRetry, RetryableOnSameAccount: false,
	}
}

func asyncImageTimeoutHasUnknownOutcome(detail string) bool {
	if gjson.Valid(detail) {
		return asyncImageTimeoutUnknownDetail(gjson.Parse(detail))
	}
	return asyncImageTimeoutUnknownText(detail)
}

func asyncImageTimeoutUnknownDetail(value gjson.Result) bool {
	if value.Type == gjson.String {
		if gjson.Valid(value.Str) {
			return asyncImageTimeoutUnknownDetail(gjson.Parse(value.Str))
		}
		return asyncImageTimeoutUnknownText(value.Str)
	}
	if !value.IsObject() && !value.IsArray() {
		return false
	}
	unknown := false
	value.ForEach(func(key, child gjson.Result) bool {
		unknown = asyncImageTimeoutUnknownText(key.String()) || asyncImageTimeoutUnknownDetail(child)
		return !unknown
	})
	return unknown
}

func asyncImageTimeoutUnknownText(detail string) bool {
	text := strings.NewReplacer("_", " ", "-", " ", `\n`, " ").Replace(strings.ToLower(detail))
	return containsAsyncImageRejectionPart(text, "execution unknown", "outcome is unknown", "generation may have started", "generation already started", "execution may have started", "may already have been charged", "may already be charged", "already been charged", "执行结果未知", "生成结果未知", "已收费", "已扣费", "已经收费", "已经扣费")
}

func (e *UpstreamFailoverError) IsAsyncImageTimeoutTerminal() bool {
	return e != nil && e.StatusCode >= 400 && e.StatusCode <= 599 &&
		(e.Reason == AsyncImageTimeoutTerminalReason || e.Reason == AsyncImageTimeoutUnknownReason)
}

func (e *UpstreamFailoverError) IsAsyncImageTimeoutRejection() bool {
	return e != nil && e.StatusCode >= 400 && e.StatusCode <= 599 && e.Reason == AsyncImageTimeoutRejectionReason
}
