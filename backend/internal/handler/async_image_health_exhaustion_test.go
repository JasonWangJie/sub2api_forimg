//go:build unit

package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Open immediately after the real forwarding path reports one failure. The
// next scheduler pass sees no candidate while its same-account exclusion set
// is still empty, reproducing the interaction between pool retries and health.
type imageHealthExhaustionBreaker struct {
	open     bool
	failures int
	filtered int
}

func (b *imageHealthExhaustionBreaker) IsOpen(context.Context, int64, string) (bool, error) {
	if b.open {
		b.filtered++
	}
	return b.open, nil
}
func (b *imageHealthExhaustionBreaker) RecordFailure(context.Context, int64, string) (bool, error) {
	b.failures++
	b.open = true
	return true, nil
}
func (b *imageHealthExhaustionBreaker) RecordSuccess(context.Context, int64, string) error {
	b.open = false
	return nil
}

func newImageHealthExhaustionHarness(t *testing.T, platform string, breaker *imageHealthExhaustionBreaker) *referenceDownloadGatewayHarness {
	t.Helper()
	groupID := int64(8501)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: platform, Status: service.StatusActive, AllowImageGeneration: true}
	account := service.Account{ID: 1, Name: "pool-health", Platform: platform, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials:   map[string]any{"api_key": "test-key", "base_url": "https://upstream.example/v1", "pool_mode": true, "pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []any{503}},
		AccountGroups: []service.AccountGroup{{AccountID: 1, GroupID: groupID}}}
	upstream := &referenceDownloadGatewayUpstream{t: t, platform: platform, failures: map[int64]referenceDownloadGatewayFailure{1: {503, "upstream temporarily unavailable"}}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	h := &referenceDownloadGatewayHarness{worker: &DurableAsyncImageHandler{}, platform: platform, upstream: upstream, apiKey: &service.APIKey{ID: 8502, UserID: 8503, GroupID: &groupID, Group: group, Status: service.StatusActive, User: &service.User{ID: 8503, Concurrency: 2, Balance: 100}}}
	if platform == service.PlatformGemini {
		gateway, cleanup := newTestGatewayHandler(t, group, []*service.Account{&account})
		t.Cleanup(cleanup)
		gateway.cfg = cfg
		gateway.maxAccountSwitchesGemini = 3
		gateway.gatewayService.SetImageAccountCircuitBreaker(breaker)
		gateway.geminiCompatService = service.NewGeminiMessagesCompatService(nil, nil, nil, nil, nil, nil, upstream, nil, cfg)
		gateway.geminiCompatService.SetImageAccountCircuitBreaker(breaker)
		h.worker.gateway = gateway
		h.model = "gemini-3-pro-image-preview"
		h.path = EndpointChatCompletions
		h.body = []byte(`{"model":"gemini-3-pro-image-preview","stream":false,"messages":[{"role":"user","content":"draw a skyline"}]}`)
	} else {
		gateway := service.NewOpenAIGatewayService(openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
		gateway.SetImageAccountCircuitBreaker(breaker)
		billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(billing.Stop)
		handler := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(&fakeConcurrencyCache{}), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
		handler.maxAccountSwitches = 3
		h.worker.openAI = handler
		h.model = "gpt-image-2"
		h.path = EndpointImagesGenerations
		h.body = []byte(`{"model":"gpt-image-2","prompt":"draw a skyline","size":"2048x1152"}`)
	}
	return h
}

func TestImageHealthCircuitExhaustionPreservesLastUpstreamFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGemini} {
		for _, async := range []bool{false, true} {
			name := platform + "/sync"
			if async {
				name = platform + "/async"
			}
			t.Run(name, func(t *testing.T) {
				breaker := &imageHealthExhaustionBreaker{}
				h := newImageHealthExhaustionHarness(t, platform, breaker)
				_, response, _, _ := invokeReferenceLocalFallbackGateway(t, h, nil, nil, async, false, false)
				want := http.StatusServiceUnavailable
				if platform == service.PlatformOpenAI {
					want = http.StatusBadGateway
				}
				require.Equal(t, want, response.Code, response.Body.String())
				wantCalls := []int64{1}
				if platform == service.PlatformGemini && !async {
					// The synchronous Gemini compatibility service finishes its
					// existing five HTTP attempts before reporting account health.
					wantCalls = []int64{1, 1, 1, 1, 1}
				}
				require.Equal(t, wantCalls, h.upstream.calls, "opening the circuit must prevent another handler-level invocation")
				require.Equal(t, 1, breaker.failures)
				require.Positive(t, breaker.filtered, "the second scheduler pass must be filtered by health")
				require.Equal(t, "17", response.Header().Get("Retry-After"), "exhaustion must retain upstream retry advice")
				require.NotContains(t, response.Body.String(), "No available")
				require.NotContains(t, response.Body.String(), "No schedulable")
			})
		}
	}
}

func TestImageHealthCircuitInitialEmptyPoolKeepsCapacityResponse(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			breaker := &imageHealthExhaustionBreaker{open: true}
			h := newImageHealthExhaustionHarness(t, platform, breaker)
			_, response, capture, _ := invokeReferenceLocalFallbackGateway(t, h, nil, nil, true, false, false)
			require.Empty(t, h.upstream.calls)
			require.Zero(t, breaker.failures)
			require.Empty(t, capture.Attempts(), "an initially open circuit must not select an account")
			require.Contains(t, response.Body.String(), "No available")
			require.Empty(t, response.Header().Get("Retry-After"))
		})
	}
}
