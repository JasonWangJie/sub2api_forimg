package service

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
)

const (
	AsyncImageCapacityRejectionCode   = "async_image_capacity_rejection"
	AsyncImageCapacityRejectionReason = GatewayFailureReason(AsyncImageCapacityRejectionCode)
	AsyncImageFixedCapabilityCode     = "async_image_fixed_capability"
	AsyncImageReferenceLimitReason    = GatewayFailureReason("async_image_reference_limit")
)

func normalizeAsyncImageOperationalMessage(message string) string {
	return strings.TrimRight(strings.ToLower(strings.Join(strings.Fields(message), " ")), ".。")
}

// Limit this generic failure to the observed complete provider response.
func isAsyncImageRequestFailedRejection(status int, providerCode, message string) bool {
	return status == http.StatusBadRequest && strings.EqualFold(strings.TrimSpace(providerCode), "upstream_error") &&
		normalizeAsyncImageOperationalMessage(message) == "request failed"
}

func isAsyncImageRequestFailedRejectionFamily(message string) bool {
	return strings.Contains(normalizeAsyncImageOperationalMessage(message), "request failed")
}

// Inspect decoded strings as stop details may contain JSON escapes or newlines.
func hasAsyncImageTerminalRejectionDetail(value gjson.Result) bool {
	if value.Type == gjson.String {
		if isAsyncImageTerminalRejection("", value.Str) {
			return true
		}
		// Some providers wrap another error JSON document inside message.
		inner := strings.TrimSpace(value.Str)
		if (strings.HasPrefix(inner, "{") || strings.HasPrefix(inner, "[")) && gjson.Valid(inner) {
			return hasAsyncImageTerminalRejectionDetail(gjson.Parse(inner))
		}
		return false
	}
	if !value.IsObject() && !value.IsArray() {
		return false
	}
	terminal := false
	value.ForEach(func(key, child gjson.Result) bool {
		terminal = isAsyncImageTerminalRejection("", key.String()) || hasAsyncImageTerminalRejectionDetail(child)
		return !terminal
	})
	return terminal
}

// IsAsyncImageCapacityRejection accepts the complete provider response, never
// a substring of a response that may contain an execution or safety warning.
func IsAsyncImageCapacityRejection(status int, message string) bool {
	if status != http.StatusBadRequest {
		return false
	}
	switch normalizeAsyncImageOperationalMessage(message) {
	case "服务繁忙，请稍后重试", "排队等待可用账户槽位超时，当前并发已满":
		return true
	}
	return false
}

func asyncImageOperationalRejectionKind(status int, message string) string {
	if status != http.StatusBadRequest {
		return ""
	}
	if IsAsyncImageCapacityRejection(status, message) {
		return "capacity"
	}
	switch normalizeAsyncImageOperationalMessage(message) {
	case "service error, please try again later", "internal error encountered", "i wasn't able to generate the image due to an error on my side":
		return "processing"
	}
	return ""
}

// An exact rule must not be bypassed by the generic invalid_request fallback.
func isAsyncImageOperationalRejectionFamily(message string) bool {
	text := normalizeAsyncImageOperationalMessage(message)
	return containsAsyncImageRejectionPart(text, "服务繁忙", "排队等待可用账户槽位超时", "service error", "internal error encountered", "error on my side",
		"参考图无法读取", "国内下载中转失败", "图床url下载失败", "reference image source download failed")
}

var asyncImageReferenceRelayFailure = regexp.MustCompile(`^参考图 #[1-9][0-9]* 国内下载中转失败$`)
var asyncImageReferenceRequestIDSuffix = regexp.MustCompile(`(?i) \(request id: [a-z0-9_-]+\)$`)
var asyncImageReferenceSourceFailure = regexp.MustCompile(`(?i)^reference image source download failed: download failed after [1-9][0-9]* attempt\(s\): (?:get "https://[^\s"]+"?:? )?(?:context deadline exceeded|dial tcp [^\s]+: connect: network is unreachable)$`)

// This helper is called after the common permanent-input/HTTP-status guards.
// The downloader still validates HTTPS, public addresses, bytes and pixels.
func isAsyncImageReferenceAdditionalDownloadFailure(message string) bool {
	if isAsyncImageTerminalRejection("", message) {
		return false
	}
	text := normalizeAsyncImageOperationalMessage(message)
	switch text {
	case "参考图无法读取：请确认 images 引用的图片链接可直接打开且返回的是图片文件，或改用 base64 图片内容后重试", "图床url下载失败，请联系客服":
		return true
	}
	if asyncImageReferenceRelayFailure.MatchString(text) {
		return true
	}
	text = asyncImageReferenceRequestIDSuffix.ReplaceAllString(text, "")
	return asyncImageReferenceSourceFailure.MatchString(text)
}

func (e *UpstreamFailoverError) IsAsyncImageFixedCapabilityFailure() bool {
	return e != nil && (e.IsAsyncImageSizeCapabilityMismatch() || e.Reason == AsyncImageReferenceLimitReason)
}
