//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/model"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const sizeCapabilityMessage = "No eligible upstream image provider supports the requested image size or resolution tier. Adjust size/resolution or select a compatible route."
const sizeCapabilityErrorBody = `{"error":{"type":"invalid_request_error","code":"image_size_not_supported","message":"` + sizeCapabilityMessage + `"}}`

const combinedCapabilityMessage = "No eligible upstream image provider supports the requested capability combination. No eligible upstream image provider supports the requested model. Select a supported model or a compatible route. No eligible upstream image provider supports the requested image size or resolution tier. Adjust size/resolution or select a compatible route."
const combinedCapabilityErrorBody = `{"code":"image_request_capability_not_supported","details":{"capability_errors":["image_model_not_supported","image_size_not_supported"]},"error":{"type":"invalid_request_error","code":"image_request_capability_not_supported","message":"` + combinedCapabilityMessage + `"}}`

type sizeCapabilityPassthroughRepo struct {
	service.ErrorPassthroughRepository
	rules []*model.ErrorPassthroughRule
}

func (r sizeCapabilityPassthroughRepo) List(context.Context) ([]*model.ErrorPassthroughRule, error) {
	return r.rules, nil
}

type sizeCapabilityUpstream struct {
	service.HTTPUpstream
	failures map[int64]string
	accounts []int64
}

func (u *sizeCapabilityUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.accounts = append(u.accounts, accountID)
	status, body := http.StatusOK, `{"created":1710000007,"data":[{"b64_json":"aGVsbG8="}]}`
	if failure, ok := u.failures[accountID]; ok {
		status, body = http.StatusUnprocessableEntity, failure
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"size-request"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func TestAsyncImageSizeCapabilityFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		failures   map[int64]string
		maxSwitch  int
		async      bool
		override   bool
		wantCalls  []int64
		wantStatus int
	}{
		{"next_account_succeeds", map[int64]string{1: sizeCapabilityErrorBody}, 3, true, false, []int64{1, 2}, http.StatusOK},
		{"candidate_pool_exhausted", map[int64]string{1: sizeCapabilityErrorBody, 2: sizeCapabilityErrorBody}, 3, true, false, []int64{1, 2}, http.StatusUnprocessableEntity},
		{"switch_budget_exhausted", map[int64]string{1: sizeCapabilityErrorBody}, 0, true, false, []int64{1}, http.StatusUnprocessableEntity},
		{"ordinary_422_is_terminal", map[int64]string{1: `{"error":{"type":"invalid_request_error","code":"invalid_size","message":"size must be 1024x1024"}}`}, 3, true, false, []int64{1}, http.StatusUnprocessableEntity},
		{"synchronous_422_is_terminal", map[int64]string{1: sizeCapabilityErrorBody}, 3, false, false, []int64{1}, http.StatusUnprocessableEntity},
		{"admin_passthrough_is_terminal", map[int64]string{1: sizeCapabilityErrorBody}, 3, true, true, []int64{1}, http.StatusConflict},
		{"combined_next_account_succeeds", map[int64]string{1: combinedCapabilityErrorBody}, 3, true, false, []int64{1, 2}, http.StatusOK},
		{"combined_candidate_pool_terminal", map[int64]string{1: combinedCapabilityErrorBody, 2: combinedCapabilityErrorBody}, 3, true, false, []int64{1, 2}, http.StatusUnprocessableEntity},
	} {
		t.Run(test.name, func(t *testing.T) {
			accounts := []service.Account{}
			for _, id := range []int64{1, 2} {
				accounts = append(accounts, service.Account{
					ID: id, Name: "size-test", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, Priority: int(id),
					Credentials: map[string]any{"api_key": "test", "base_url": "https://images.example/v1", "pool_mode": true, "pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []any{422}},
				})
			}
			upstream := &sizeCapabilityUpstream{failures: test.failures}
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
				status, message := http.StatusConflict, "route administratively disabled"
				h.errorPassthroughService = service.NewErrorPassthroughService(sizeCapabilityPassthroughRepo{rules: []*model.ErrorPassthroughRule{{
					ID: 1, Name: "size override", Enabled: true, ErrorCodes: []int{http.StatusUnprocessableEntity},
					MatchMode: model.MatchModeAny, ResponseCode: &status, CustomMessage: &message,
				}}}, nil)
			}
			ctx := context.Background()
			attemptCapture := &service.AsyncImageAccountAttemptCapture{}
			usageCapture := &AsyncImageUsageCapture{}
			if test.async {
				ctx = service.WithAsyncImageAccountAttemptCapture(ctx, attemptCapture)
				ctx = withAsyncImageUsageCapture(ctx, usageCapture)
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(`{"model":"gpt-image-2","prompt":"draw","size":"1024x1024"}`)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			groupID := int64(3131)
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
				ID: 90, GroupID: &groupID, Group: &service.Group{ID: groupID, AllowImageGeneration: true}, User: &service.User{ID: 91},
			})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 91})
			h.Images(c)
			require.Equal(t, test.wantCalls, upstream.accounts)
			require.Equal(t, test.wantStatus, recorder.Code)
			if test.override {
				require.Equal(t, "route administratively disabled", gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
			}
			if test.name == "next_account_succeeds" {
				require.Equal(t, int64(2), usageCapture.OpenAI().Account.ID)
				require.Equal(t, "aGVsbG8=", gjson.GetBytes(recorder.Body.Bytes(), "data.0.b64_json").String())
			}
			if strings.Contains(test.name, "exhausted") {
				require.JSONEq(t, sizeCapabilityErrorBody, recorder.Body.String())
				message := formatAsyncImageUpstreamFailure(recorder.Code, recorder.Body.Bytes())
				require.Contains(t, message, "HTTP 422")
				require.Contains(t, message, sizeCapabilityMessage)
				require.False(t, shouldRetryAsyncImageUpstreamTransient(&service.AsyncImageTask{}, testAsyncImageRetryConfig(), recorder.Code, message))
				require.False(t, isAsyncImageCapacityFailure(recorder.Code, message))
			}
			if test.async && !test.override && test.failures[1] == sizeCapabilityErrorBody {
				attempts := attemptCapture.Attempts()
				require.GreaterOrEqual(t, len(attempts), 2)
				require.Equal(t, http.StatusUnprocessableEntity, attempts[1].StatusCode)
				require.Equal(t, "image_size_not_supported", attempts[1].ProviderErrorCode)
				require.Equal(t, sizeCapabilityMessage, attempts[1].Error)
				require.Equal(t, "size-request", attempts[1].UpstreamRequestID)
			}
		})
	}
}

func TestAsyncGeminiSizeCapabilityExhaustionPreservesError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(service.WithGeminiAsyncImageGeneration(context.Background()))
	(&GatewayHandler{}).handleCCFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode: http.StatusUnprocessableEntity, ResponseBody: []byte(sizeCapabilityErrorBody),
		ProviderErrorCode: "image_size_not_supported", ProviderErrorMessage: sizeCapabilityMessage,
		Reason: service.GatewayFailureReason("async_image_size_capability_mismatch"),
	}, false)
	require.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
	require.JSONEq(t, sizeCapabilityErrorBody, recorder.Body.String())
	require.False(t, shouldRetryAsyncImageUpstreamTransient(&service.AsyncImageTask{}, testAsyncImageRetryConfig(), recorder.Code, formatAsyncImageUpstreamFailure(recorder.Code, recorder.Body.Bytes())))
}
