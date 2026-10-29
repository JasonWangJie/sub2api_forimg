//go:build unit

package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageInputAdmissionLocalFallbackReusesChannel(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			h := newReferenceDownloadGatewayHarness(t, platform, 1, 3, nil, func(account *service.Account) {
				account.Extra = map[string]any{"image_input_capabilities": map[string]any{"transports": []string{"local"}}}
			})
			task := referenceLocalFallbackTask(platform, h.model, service.AsyncImageReferenceTransportPassthroughFallbackLocal)
			cfg := referenceLocalFallbackConfig()
			repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
			h.worker.tasks = service.NewAsyncImageTaskService(repo)
			admission := func(ctx context.Context) context.Context {
				ctx = service.WithAsyncImageAttemptBudget(ctx, asyncImageRemainingUpstreamAttempts(repo.task))
				return service.WithImageInputCapabilities(ctx, asyncImageInputCapabilities(h.body, "application/json"))
			}
			c, response, _ := invokeProviderRejectionGateway(t, h, task, cfg, admission)
			require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
			require.Empty(t, h.upstream.calls, "capability rejection must happen before generation")
			require.True(t, service.ImageInputLocalFallbackSuggested(c.Request.Context()))
			disposition := h.worker.handleAsyncImageUpstreamFailure(c.Request.Context(), c.Request.Context(), task, cfg, response.Code, response.Body.Bytes(), response.Header())
			require.True(t, disposition.requeue)
			require.Equal(t, service.AsyncImageReferenceTransportLocal, *repo.task.ReferenceTransport)
			require.Equal(t, 1, repo.task.ReferenceRetryCount)
			require.Zero(t, repo.task.CapacityRetryCount)
			require.Empty(t, asyncImageFixedCapabilityAccountIDs(repo.task))
			require.Empty(t, asyncImageReferenceRetryExcludedAccountIDs(repo.task, cfg), "local transport can use the originally rejected channel")
			h.body = prepareReferenceLocalFallbackBody(t, h)
			_, response, _ = invokeProviderRejectionGateway(t, h, repo.task, cfg, admission)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, []int64{1}, h.upstream.calls)
		})
	}
}

func TestAsyncImageInputAdmissionFallbackRequiresProvenNoInvocation(t *testing.T) {
	for _, scenario := range []string{"missing budget", "generation started", "ambiguous timeout", "reference budget exhausted", "already local", "text task"} {
		t.Run(scenario, func(t *testing.T) {
			task := referenceLocalFallbackTask(service.PlatformOpenAI, "gpt-image-2", service.AsyncImageReferenceTransportPassthroughFallbackLocal)
			cfg := referenceLocalFallbackConfig()
			repo := &referenceLocalFallbackRepo{&generatedWorkerResultRepo{task: task}}
			worker := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo)}
			ctx := service.WithImageInputCapabilities(context.Background(), service.ImageInputCapabilities{Transport: "passthrough", Count: 1})
			require.False(t, service.ImageAccountSupportsInput(ctx, &service.Account{ID: 1, Extra: map[string]any{
				"image_input_capabilities": map[string]any{"transports": []string{"local"}},
			}}))
			require.True(t, service.ImageInputLocalFallbackSuggested(ctx))
			if scenario != "missing budget" {
				ctx = service.WithAsyncImageAttemptBudget(ctx, 16)
			}
			status := http.StatusServiceUnavailable
			switch scenario {
			case "generation started":
				require.NoError(t, service.BeginAsyncImageUpstreamAttempt(ctx, 1))
			case "ambiguous timeout":
				status = http.StatusGatewayTimeout
			case "reference budget exhausted":
				cfg.ReferenceFetchMaxRetries = 0
			case "already local":
				mode := service.AsyncImageReferenceTransportLocal
				task.ReferenceTransport = &mode
			case "text task":
				task.RequestType = service.AsyncImageRequestTypeTextToImage
			}
			worker.handleAsyncImageUpstreamFailure(ctx, ctx, task, cfg, status, []byte(`{"error":{"message":"No available compatible accounts"}}`), http.Header{})
			require.Zero(t, repo.task.ReferenceRetryCount, "admission hint alone must never allow regeneration")
		})
	}
}
