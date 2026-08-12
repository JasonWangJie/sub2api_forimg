//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type geminiAliasGateUpstream struct {
	service.HTTPUpstream
	calls    atomic.Int64
	lastPath string
	onDo     func()
}

func (u *geminiAliasGateUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls.Add(1)
	u.lastPath = req.URL.Path
	if u.onDo != nil {
		u.onDo()
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"candidates":[{"content":{"parts":[{"text":"ok"}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`,
		)),
		Request: req,
	}, nil
}

func newGeminiAliasGateTestHandler(
	t *testing.T,
	allowImageGeneration bool,
	mappedModel string,
	upstream *geminiAliasGateUpstream,
) (*GatewayHandler, *service.APIKey, func()) {
	t.Helper()

	groupID := int64(7201)
	accountID := int64(7202)
	group := &service.Group{
		ID:                   groupID,
		Hydrated:             true,
		Platform:             service.PlatformGemini,
		Status:               service.StatusActive,
		AllowImageGeneration: allowImageGeneration,
	}
	account := &service.Account{
		ID:       accountID,
		Name:     "gemini-alias-account",
		Platform: service.PlatformGemini,
		Type:     service.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "test-upstream-key",
			"model_mapping": map[string]any{
				"customer-alias": mappedModel,
			},
		},
		Concurrency:   1,
		Priority:      1,
		Status:        service.StatusActive,
		Schedulable:   true,
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}

	h, cleanup := newTestGatewayHandler(t, group, []*service.Account{account})
	cfg := &config.Config{
		RunMode: config.RunModeSimple,
		Gateway: config.GatewayConfig{
			ImageConcurrency: config.ImageConcurrencyConfig{
				Enabled:               true,
				MaxConcurrentRequests: 1,
				OverflowMode:          config.ImageConcurrencyOverflowModeReject,
			},
		},
	}
	h.cfg = cfg
	h.imageLimiter = &imageConcurrencyLimiter{}
	h.geminiCompatService = service.NewGeminiMessagesCompatService(nil, nil, nil, nil, nil, nil, upstream, nil, cfg)

	apiKey := &service.APIKey{
		ID:      7203,
		UserID:  7204,
		GroupID: &groupID,
		Group:   group,
		Status:  service.StatusActive,
		User: &service.User{
			ID:          7204,
			Concurrency: 2,
			Balance:     100,
		},
	}
	return h, apiKey, cleanup
}

func newGeminiAliasGateTestContext(apiKey *service.APIKey) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"draw a skyline"}]}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/customer-alias:generateContent", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, apiKey.Group))
	c.Request = req
	c.Params = gin.Params{{Key: "modelAction", Value: "customer-alias:generateContent"}}
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 2})
	return c, recorder
}

func TestGeminiV1BetaModels_AccountAliasToImageModelRejectedWhenGroupDisabled(t *testing.T) {
	upstream := &geminiAliasGateUpstream{}
	h, apiKey, cleanup := newGeminiAliasGateTestHandler(t, false, "gemini-2.5-flash-image", upstream)
	defer cleanup()
	c, recorder := newGeminiAliasGateTestContext(apiKey)

	h.GeminiV1BetaModels(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), service.ImageGenerationPermissionMessage())
	require.Zero(t, upstream.calls.Load(), "a mapped image model must be rejected before upstream")
}

func TestGeminiV1BetaModels_AccountAliasToImageModelUsesAndReleasesImageSlot(t *testing.T) {
	upstream := &geminiAliasGateUpstream{}
	h, apiKey, cleanup := newGeminiAliasGateTestHandler(t, true, "gemini-2.5-flash-image", upstream)
	defer cleanup()
	c, recorder := newGeminiAliasGateTestContext(apiKey)

	var slotHeldDuringForward atomic.Bool
	upstream.onDo = func() {
		release, acquired := h.imageLimiter.TryAcquire(true, 1)
		if acquired && release != nil {
			release()
		}
		slotHeldDuringForward.Store(!acquired)
	}

	h.GeminiV1BetaModels(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.EqualValues(t, 1, upstream.calls.Load())
	require.Contains(t, upstream.lastPath, "/gemini-2.5-flash-image:generateContent")
	require.True(t, slotHeldDuringForward.Load(), "mapped image forwarding must hold the image slot")

	release, acquired := h.imageLimiter.TryAcquire(true, 1)
	require.True(t, acquired, "the image slot must be released when the handler returns")
	require.NotNil(t, release)
	release()
}

func TestGeminiV1BetaModels_AccountAliasToTextModelDoesNotUseImageSlot(t *testing.T) {
	upstream := &geminiAliasGateUpstream{}
	h, apiKey, cleanup := newGeminiAliasGateTestHandler(t, false, "gemini-2.5-flash", upstream)
	defer cleanup()
	c, recorder := newGeminiAliasGateTestContext(apiKey)

	heldRelease, acquired := h.imageLimiter.TryAcquire(true, 1)
	require.True(t, acquired)
	require.NotNil(t, heldRelease)
	defer heldRelease()

	h.GeminiV1BetaModels(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.EqualValues(t, 1, upstream.calls.Load())
	require.NotContains(t, recorder.Body.String(), "Image generation concurrency limit exceeded")
}
