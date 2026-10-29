package service

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestAsyncImageOperationalRejectionsStrict(t *testing.T) {
	for _, message := range []string{"服务繁忙，请稍后重试", "排队等待可用账户槽位超时，当前并发已满", "service error, please try again later.", "Internal error encountered.", "I wasn't able to generate the image due to an error on my side."} {
		t.Run(message, func(t *testing.T) {
			want := "processing"
			if IsAsyncImageCapacityRejection(400, message) {
				want = "capacity"
			}
			require.Equal(t, want, AsyncImageRetryableRejectionKind(400, "invalid_request_error", message, false))
			for _, extra := range []string{" Do not retry.", " content policy", " generation may have started", " generated result download failed", " and another unexplained condition"} {
				require.Empty(t, AsyncImageRetryableRejectionKind(400, "invalid_image_request", message+extra, true))
				require.False(t, isOpenAIAsyncAccountFailover400("Invalid request: "+message+extra))
				body, err := json.Marshal(map[string]any{"error": map[string]string{"type": "invalid_request_error", "message": message + extra}})
				require.NoError(t, err)
				require.False(t, isGeminiAsyncAccountFailover400(body))
			}
			for _, status := range []int{200, 403, 422, 502, 504} {
				require.Empty(t, AsyncImageRetryableRejectionKind(status, "invalid_request_error", message, true))
			}
			body, _ := json.Marshal(map[string]any{"error": map[string]string{"message": message, "code": "400"}})
			require.Nil(t, newAsyncImageRetryableRejectionForContext(context.Background(), 400, body, http.Header{}))
			ctx := WithAsyncImageAccountAttemptCapture(context.Background(), &AsyncImageAccountAttemptCapture{})
			failure := newAsyncImageRetryableRejectionForContext(ctx, 400, body, http.Header{"Retry-After": {"45"}, "X-Request-Id": {"req-busy"}})
			require.NotNil(t, failure)
			require.False(t, failure.RetryableOnSameAccount)
			require.Equal(t, "45", failure.ResponseHeaders.Get("Retry-After"))
			if want == "capacity" {
				require.Equal(t, AsyncImageCapacityRejectionReason, failure.Reason)
			}
		})
	}
}

func TestAsyncImageAdditionalReferenceFailuresStrict(t *testing.T) {
	for _, message := range []string{
		"参考图无法读取：请确认 images 引用的图片链接可直接打开且返回的是图片文件，或改用 Base64 图片内容后重试。",
		"参考图 #1 国内下载中转失败", "参考图 #16 国内下载中转失败", "图床url下载失败，请联系客服",
		`reference image source download failed: download failed after 3 attempt(s): context deadline exceeded (request id: req-123)`,
		`reference image source download failed: download failed after 3 attempt(s): Get "https://cdn.example/ref?token=secret": context deadline exceeded (request id: req-123)`,
		`reference image source download failed: download failed after 3 attempt(s): Get "https://***.com/*** dial tcp ***.***.***.***:443: connect: network is unreachable (request id: req123)`,
		`Error while downloading https://cdn.example/ref Get "https://cdn.example/ref": read tcp 1.2.3.4:123->5.6.7.8:443: read: connection reset by peer`,
		`Error while downloading https://***.com/*** read tcp ***.***.***.***:123->***.***.***.***:443: read: connection reset by peer`,
	} {
		t.Run(message, func(t *testing.T) {
			require.True(t, IsAsyncImageReferenceAccountFailoverMessage(400, message))
			body, _ := json.Marshal(map[string]any{"error": map[string]string{"message": message}})
			ctx := WithAsyncImageInputReferenceTask(context.Background(), true)
			require.True(t, isAsyncImageReferenceAccountFailoverForContext(ctx, 400, body))
			require.False(t, isAsyncImageReferenceAccountFailoverForContext(context.Background(), 400, body))
			failure := newAsyncImageReferenceAccountFailoverError(body, http.Header{})
			require.NotContains(t, failure.ProviderErrorMessage, "secret")
			require.True(t, IsAsyncImageReferenceAccountFailoverMessage(400, failure.ProviderErrorMessage))
			for _, suffix := range []string{" Do not retry.", " generated image", " HTTP 403 forbidden", " invalid base64", " safety policy", " generation may have started"} {
				require.False(t, IsAsyncImageReferenceAccountFailoverMessage(400, message+suffix))
			}
			for _, status := range []int{422, 502, 504} {
				require.False(t, IsAsyncImageReferenceAccountFailoverMessage(status, message))
			}
		})
	}
	for _, message := range []string{`Error while downloading https://a.example/ref Get "https://b.example/ref": read tcp a:1->b:443: read: connection reset by peer`, "参考图无法读取：永久损坏", "参考图 #1 国内下载中转失败 HTTP 404", "reference image source download failed: invalid image"} {
		require.False(t, IsAsyncImageReferenceAccountFailoverMessage(400, message))
	}
}
