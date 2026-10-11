//go:build unit

package handler

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeneratedWorkerResultFailuresNeverRepeatUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name    string
		body    string
		expired bool
		status  string
	}{
		{"unsafe fileData URL", `{"candidates":[{"content":{"parts":[{"fileData":{"mimeType":"image/png","fileUri":"https://127.0.0.1/result?sig=private-secret"}}]}}]}`, false, service.AsyncImageTaskStatusFailed},
		{"plain URL response", "https://127.0.0.1/result?sig=private-secret", false, service.AsyncImageTaskStatusFailed},
		{"Markdown response", "![result](https://127.0.0.1/result?sig=private-secret)", false, service.AsyncImageTaskStatusFailed},
		{"compatibility URL envelope", `{"data":[{"url":"http://1.1.1.1/result?sig=private-secret"}]}`, false, service.AsyncImageTaskStatusFailed},
		{"invalid Base64", `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"invalid-base64!"}}]}}]}`, false, service.AsyncImageTaskStatusFailed},
		{"fake inline image", `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + base64.StdEncoding.EncodeToString([]byte("<html>not an image</html>")) + `"}}]}}]}`, false, service.AsyncImageTaskStatusFailed},
		{"no generated image", `{"candidates":[{"content":{"parts":[{"text":"reference image fetch failed: image_url fetch failed"}]}}]}`, false, service.AsyncImageTaskStatusFailed},
		{"malformed response", `{"candidates":`, false, service.AsyncImageTaskStatusFailed},
		{"result download timeout", `{"url":"https://1.1.1.1/result?sig=private-secret"}`, true, service.AsyncImageTaskStatusExecutionUnknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &geminiAliasGateUpstream{responseBody: tt.body}
			gateway, apiKey, cleanup := newGeminiAliasGateTestHandler(t, true, "gemini-2.5-flash-image", upstream)
			defer cleanup()
			repo := &generatedWorkerResultRepo{task: &service.AsyncImageTask{
				TaskID: "asyncimg_output_failure", Version: 1, Platform: service.PlatformGemini,
				Status: service.AsyncImageTaskStatusInvoking, RequestType: service.AsyncImageRequestTypeImageToImage,
				RequestPayload: []byte("encrypted original prompt"),
			}}
			h, storage, _ := newGeneratedWorkerLocalHandler(t, repo)
			h.gateway = gateway
			c, recorder := newAsyncGeminiWorkerContext(apiKey)
			capture, usage := &service.GeminiImageResponseCapture{}, &AsyncImageUsageCapture{}
			ctx := service.WithGeminiAsyncImageGeneration(c.Request.Context())
			ctx = service.WithGeminiImageResponseCapture(ctx, capture)
			ctx = withAsyncImageUsageCapture(ctx, usage)
			ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "async-image:"+repo.task.TaskID)
			c.Request = c.Request.WithContext(ctx)

			require.True(t, h.forwardAsyncImageUpstream(c, service.PlatformGemini))
			require.Equal(t, http.StatusOK, recorder.Code, "a completed upstream image response must reach the worker without failover")
			require.EqualValues(t, 1, upstream.calls.Load())
			invocationCtx := c.Request.Context()
			if tt.expired {
				var cancel context.CancelFunc
				invocationCtx, cancel = context.WithDeadline(invocationCtx, time.Now().Add(-time.Second))
				defer cancel()
			}
			disposition := h.completeAsyncImageInvocation(context.Background(), invocationCtx, repo.task, recorder.Body.Bytes(), usage, capture, testAsyncImageRetryConfig())
			require.False(t, disposition.requeue)
			require.Equal(t, tt.status, repo.task.Status)
			require.Empty(t, repo.task.RequestPayload)
			require.Zero(t, repo.task.RetryCount)
			require.Zero(t, repo.task.ReferenceRetryCount)
			require.Empty(t, storage.saved)
			require.Empty(t, repo.staging)
			require.Empty(t, repo.task.BillingPayload)
			require.NotNil(t, repo.task.ErrorMessage)
			require.NotContains(t, *repo.task.ErrorMessage, "private-secret")
			if tt.status == service.AsyncImageTaskStatusFailed {
				require.Equal(t, "upstream_invalid_output", *repo.task.ErrorCode)
			}
			// Redeliveries, including image-to-image tasks with reference retry
			// enabled, cannot return this result failure to the invocation path.
			for i := 0; i < 2; i++ {
				require.False(t, h.processAsyncImageTask(context.Background(), repo.task.TaskID).requeue)
			}
			require.EqualValues(t, 1, upstream.calls.Load())
			for _, tr := range repo.transitions {
				require.NotEqual(t, service.AsyncImageTaskStatusQueued, tr.ToStatus)
				require.NotEqual(t, service.AsyncImageTaskStatusInvoking, tr.ToStatus)
			}
		})
	}
}
