//go:build unit

package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAsyncImageOperationalFailureRecovery(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGemini} {
		for _, message := range []string{"服务繁忙，请稍后重试", "service error, please try again later.", "Internal error encountered.", "I wasn't able to generate the image due to an error on my side."} {
			t.Run(platform+message, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{1: {400, message}})
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
				cfg := referenceLocalFallbackConfig()
				_, response, _ := invokeProviderRejectionGateway(t, h, task, cfg)
				require.Equal(t, 200, response.Code, response.Body.String())
				require.Equal(t, []int64{1, 2}, h.upstream.calls)
			})
		}
		t.Run(platform+"capacity exhaustion uses capacity budget", func(t *testing.T) {
			h := newReferenceDownloadGatewayHarness(t, platform, 1, 3, map[int64]referenceDownloadGatewayFailure{1: {400, "服务繁忙，请稍后重试"}})
			task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
			cfg := referenceLocalFallbackConfig()
			cfg.CapacityMaxRetries = 2
			cfg.CapacityRetryBaseSeconds = 30
			cfg.CapacityRetryMaxSeconds = 120
			cfg.RetryJitterPercent = 0
			repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
			h.worker.tasks = service.NewAsyncImageTaskService(repo)
			for i := 0; i < 3; i++ {
				c, response, _ := invokeProviderRejectionGateway(t, h, repo.task, cfg)
				require.Equal(t, service.AsyncImageCapacityRejectionCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				if i > 0 {
					response.Header().Del("Retry-After")
				}
				d := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), repo.task, cfg, response.Code, response.Body.Bytes(), response.Header())
				require.Equal(t, i < 2, d.requeue)
				require.Zero(t, repo.task.UpstreamRetryCount)
				if i < 2 {
					require.Equal(t, i+1, repo.task.CapacityRetryCount)
					if i == 0 {
						require.Equal(t, 17*time.Second, d.delay, "the provider hint takes precedence over default backoff")
					} else {
						require.Equal(t, 60*time.Second, d.delay, "without a provider hint use exponential capacity backoff")
					}
					repo.task.Status = service.AsyncImageTaskStatusInvoking
				}
			}
			require.Equal(t, 2, repo.task.CapacityRetryCount)
			require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
		})
	}
}

func TestAsyncImageAdditionalReferenceHybridRecovery(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGemini} {
		for _, message := range []string{"参考图无法读取：请确认 images 引用的图片链接可直接打开且返回的是图片文件，或改用 Base64 图片内容后重试。", "参考图 #1 国内下载中转失败", "图床url下载失败，请联系客服", `reference image source download failed: download failed after 3 attempt(s): context deadline exceeded (request id: req-123)`} {
			t.Run(platform+message, func(t *testing.T) {
				h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{1: {400, message}})
				cfg := referenceLocalFallbackConfig()
				task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
				repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
				h.worker.tasks = service.NewAsyncImageTaskService(repo)
				c, response, _ := invokeProviderRejectionGateway(t, h, task, cfg)
				require.Equal(t, []int64{1}, h.upstream.calls)
				d := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), repo.task, cfg, response.Code, response.Body.Bytes(), response.Header())
				require.True(t, d.requeue)
				require.Equal(t, service.AsyncImageReferenceTransportLocal, *repo.task.ReferenceTransport)
				require.Equal(t, 1, repo.task.ReferenceRetryCount)
				h.body = prepareReferenceLocalFallbackBody(t, h)
				_, response, _ = invokeProviderRejectionGateway(t, h, repo.task, cfg)
				require.Equal(t, 200, response.Code, response.Body.String())
				require.Equal(t, []int64{1, 2}, h.upstream.calls)
				require.Contains(t, string(h.upstream.bodies[1]), durableAsyncImageOnePixelPNG)
			})
		}
	}
}

func TestAsyncImageFixedCapabilityStopsOnlyFixedFailures(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			h := newReferenceDownloadGatewayHarness(t, platform, 1, 3, map[int64]referenceDownloadGatewayFailure{1: {400, providerRejectionMessages[0]}})
			cfg := referenceLocalFallbackConfig()
			task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
			repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
			h.worker.tasks = service.NewAsyncImageTaskService(repo)
			c, response, capture := invokeProviderRejectionGateway(t, h, task, cfg)
			require.Equal(t, service.AsyncImageFixedCapabilityCode, gjson.GetBytes(response.Body.Bytes(), "error.code").String())
			d := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), repo.task, cfg, response.Code, response.Body.Bytes(), response.Header())
			require.False(t, d.requeue)
			require.Zero(t, repo.task.RetryCount)
			require.Equal(t, []int64{1}, h.upstream.calls)
			history, _ := service.MergeAsyncImageAccountAttempts(nil, capture.Attempts())
			task.AccountAttempts = history
			require.Equal(t, map[int64]struct{}{1: {}}, asyncImageFixedCapabilityAccountIDs(task))
		})
	}
	rows := []service.AsyncImageAccountAttempt{
		{AccountID: 1, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 422, FailureReason: string(service.AsyncImageSizeCapabilityMismatchReason)},
		{AccountID: 2, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 400, FailureReason: string(service.AsyncImageReferenceTransportRejectionReason)},
		{AccountID: 3, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 400, FailureReason: string(service.AsyncImageServiceTimeout400Reason)},
		{AccountID: 4, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 422, FailureReason: string(service.AsyncImageInternalError422Reason)},
	}
	history, _ := json.Marshal(rows)
	mode := service.AsyncImageReferenceTransportLocal
	require.Equal(t, map[int64]struct{}{1: {}}, asyncImageFixedCapabilityAccountIDs(&service.AsyncImageTask{AccountAttempts: history, ReferenceTransport: &mode}))
}

func TestAsyncImageFixedCapabilitySurvivesTransientWorkerRetry(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			h := newReferenceDownloadGatewayHarness(t, platform, 2, 3, map[int64]referenceDownloadGatewayFailure{
				1: {422, sizeCapabilityMessage}, 2: {503, "upstream temporarily unavailable"},
			})
			cfg := referenceLocalFallbackConfig()
			task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthrough)
			repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
			h.worker.tasks = service.NewAsyncImageTaskService(repo)
			c, response, _ := invokeProviderRejectionGateway(t, h, task, cfg)
			require.Equal(t, []int64{1, 2}, h.upstream.calls)
			d := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, cfg, response.Code, response.Body.Bytes(), response.Header())
			require.True(t, d.requeue)
			require.Equal(t, map[int64]struct{}{1: {}}, asyncImageFixedCapabilityAccountIDs(repo.task))
			delete(h.upstream.failures, 2)
			_, response, _ = invokeProviderRejectionGateway(t, h, repo.task, cfg)
			require.Equal(t, 200, response.Code, response.Body.String())
			require.Equal(t, []int64{1, 2, 2}, h.upstream.calls, "fixed incompatibility survives soft reset and sticky A")
		})
	}
}
