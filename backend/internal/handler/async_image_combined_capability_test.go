//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/model"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type combinedCapabilityRequestRecorder struct {
	sizeCapabilityUpstream
	bodies [][]byte
}

func (u *combinedCapabilityRequestRecorder) Do(req *http.Request, proxy string, accountID int64, concurrency int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.bodies = append(u.bodies, body)
	req.Body = io.NopCloser(bytes.NewReader(body))
	return u.sizeCapabilityUpstream.Do(req, proxy, accountID, concurrency)
}

func TestAsyncImageCombinedCapabilityHandlerBoundariesAndRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		maxSwitch  int
		async      bool
		override   bool
		wantCalls  []int64
		wantStatus int
	}{
		{"unchanged request after switch", 3, true, false, []int64{1, 2}, http.StatusOK},
		{"zero switch budget", 0, true, false, []int64{1}, http.StatusUnprocessableEntity},
		{"synchronous stays terminal", 3, false, false, []int64{1}, http.StatusUnprocessableEntity},
		{"administrator rule wins", 3, true, true, []int64{1}, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			accounts := []service.Account{}
			for _, id := range []int64{1, 2} {
				accounts = append(accounts, service.Account{
					ID: id, Name: "combined-capability-test", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, Priority: int(id),
					Credentials: map[string]any{
						"api_key": "test", "base_url": "https://images.example/v1", "pool_mode": true,
						"pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []any{422},
					},
				})
			}
			upstream := &combinedCapabilityRequestRecorder{sizeCapabilityUpstream: sizeCapabilityUpstream{failures: map[int64]string{1: combinedCapabilityErrorBody}}}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			gateway := service.NewOpenAIGatewayService(
				openAIImagesFailoverAccountRepo{accounts: accounts}, nil, nil, nil, nil, nil, nil, cfg,
				nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil,
			)
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing,
				service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
			h.maxAccountSwitches = test.maxSwitch
			if test.override {
				status, message := http.StatusConflict, "combined route administratively disabled"
				h.errorPassthroughService = service.NewErrorPassthroughService(sizeCapabilityPassthroughRepo{rules: []*model.ErrorPassthroughRule{{
					ID: 1, Name: "combined override", Enabled: true, ErrorCodes: []int{422},
					MatchMode: model.MatchModeAny, ResponseCode: &status, CustomMessage: &message,
				}}}, nil)
			}
			ctx := context.Background()
			capture := &service.AsyncImageAccountAttemptCapture{}
			if test.async {
				ctx = service.WithAsyncImageAccountAttemptCapture(ctx, capture)
				ctx = withAsyncImageUsageCapture(ctx, &AsyncImageUsageCapture{})
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(`{"model":"gpt-image-2.5-flare","prompt":"draw","size":"2048x1152"}`)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			groupID := int64(3132)
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
				ID: 90, GroupID: &groupID, Group: &service.Group{ID: groupID, AllowImageGeneration: true}, User: &service.User{ID: 91},
			})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 91})
			h.Images(c)
			require.Equal(t, test.wantCalls, upstream.accounts, "an incompatible account must not be retried even when its pool retries HTTP 422")
			require.Equal(t, test.wantStatus, recorder.Code)
			for _, body := range upstream.bodies {
				require.Equal(t, "gpt-image-2.5-flare", gjson.GetBytes(body, "model").String())
				require.Equal(t, "2048x1152", gjson.GetBytes(body, "size").String())
			}
			if test.name == "zero switch budget" {
				require.Equal(t, "image_request_capability_not_supported", gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
				require.Equal(t, combinedCapabilityMessage, gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
				attempts := capture.Attempts()
				require.Len(t, attempts, 2)
				require.Equal(t, 422, attempts[1].StatusCode)
				require.Equal(t, "image_request_capability_not_supported", attempts[1].ProviderErrorCode)
				require.Equal(t, "size-request", attempts[1].UpstreamRequestID)
				require.Equal(t, combinedCapabilityMessage, attempts[1].Error)
				message := formatAsyncImageUpstreamFailure(recorder.Code, recorder.Body.Bytes())
				require.False(t, shouldRetryAsyncImageUpstreamTransient(&service.AsyncImageTask{}, testAsyncImageRetryConfig(), recorder.Code, message))
			}
			if test.override {
				require.Equal(t, "combined route administratively disabled", gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
			}
		})
	}
}
