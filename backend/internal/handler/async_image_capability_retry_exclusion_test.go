//go:build unit

package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageCapabilityHardExclusionSurvivesWorkerRetryAndSticky(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		for _, variant := range []struct {
			name, body, providerCode string
			legacyRecognized         bool
		}{
			{"combined matching provider code", combinedCapabilityErrorBody, "image_request_capability_not_supported", true},
			{"combined provider status overrides code", strings.Replace(combinedCapabilityErrorBody, `"error":{`, `"error":{"status":"INVALID_ARGUMENT",`, 1), "INVALID_ARGUMENT", false},
			{"legacy size message without code", `{"error":{"message":"` + sizeCapabilityMessage + `"}}`, "", false},
		} {
			t.Run(platform+"/"+variant.name, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{
					1: {http.StatusUnprocessableEntity, "overridden by structured capability rejection"},
					2: {http.StatusServiceUnavailable, "upstream temporarily unavailable"},
				})
				h.upstream.rawErrorBodies = map[int64]string{1: variant.body}
				firstContext, firstResponse, firstCapture, _ := h.invoke(t, nil, false)
				require.Equal(t, []int64{1, 2}, h.upstream.calls)
				wantFirstStatus := http.StatusServiceUnavailable
				if platform == service.PlatformOpenAI {
					wantFirstStatus = http.StatusBadGateway
				}
				require.Equal(t, wantFirstStatus, firstResponse.Code, firstResponse.Body.String())
				var capabilityFailed, transientFailed bool
				for _, attempt := range firstCapture.Attempts() {
					if attempt.Status != service.AsyncImageAccountAttemptFailed {
						continue
					}
					switch attempt.AccountID {
					case 1:
						capabilityFailed = true
						require.Equal(t, http.StatusUnprocessableEntity, attempt.StatusCode)
						require.Equal(t, variant.providerCode, attempt.ProviderErrorCode, "retain the upstream diagnostic instead of replacing it with the internal reason")
						require.Equal(t, string(service.AsyncImageSizeCapabilityMismatchReason), string(attempt.FailureReason))
						require.Equal(t, variant.legacyRecognized, service.IsAsyncImageCapabilityAccountFailure(attempt.StatusCode, attempt.ProviderErrorCode, attempt.Error))
					case 2:
						transientFailed = true
						require.Equal(t, http.StatusServiceUnavailable, attempt.StatusCode)
					}
				}
				require.True(t, capabilityFailed)
				require.True(t, transientFailed)
				history, _ := service.MergeAsyncImageAccountAttempts(nil, firstCapture.Attempts())
				// The capability failure must survive the complete durable history,
				// including more recent transient errors that may be retried on B.
				history, _ = service.MergeAsyncImageAccountAttempts(history, []service.AsyncImageAccountAttempt{
					{AccountID: 2, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 503, Error: "upstream temporarily unavailable"},
					{AccountID: 2, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 503, Error: "upstream temporarily unavailable"},
					{AccountID: 2, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 503, Error: "upstream temporarily unavailable"},
				})
				task := &service.AsyncImageTask{
					TaskID: "asyncimg_capability_later_invocation", Version: 1, Platform: platform, Model: h.model,
					Status: service.AsyncImageTaskStatusInvoking, RequestType: service.AsyncImageRequestTypeImageToImage,
					AccountAttempts: history, RequestPayload: []byte("encrypted request"),
				}
				require.Equal(t, map[int64]struct{}{1: {}}, asyncImageNonRetryableAccountIDs(task))
				repo := &generatedWorkerResultRepo{task: task}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				disposition := h.worker.handleAsyncImageUpstreamFailure(context.Background(), firstContext.Request.Context(), task, testAsyncImageRetryConfig(), firstResponse.Code, firstResponse.Body.Bytes(), firstResponse.Header())
				require.True(t, disposition.requeue)
				require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
				require.Equal(t, map[int64]struct{}{1: {}}, asyncImageNonRetryableAccountIDs(repo.task))

				delete(h.upstream.failures, 2)
				before := len(h.upstream.calls)
				_, secondResponse, secondCapture, usage := h.invoke(t, repo.task, true)
				require.Equal(t, http.StatusOK, secondResponse.Code, secondResponse.Body.String())
				require.Equal(t, []int64{2}, h.upstream.calls[before:], "soft exclusion reset and stale sticky must never reselect capability-failed A")
				for _, attempt := range secondCapture.Attempts() {
					require.Equal(t, int64(2), attempt.AccountID)
				}
				if platform == service.PlatformGemini {
					require.Equal(t, int64(2), usage.Gemini().Account.ID)
				} else {
					require.Equal(t, int64(2), usage.OpenAI().Account.ID)
				}
				assertReferenceDownloadGatewayRequest(t, h)
			})
		}
	}
}
