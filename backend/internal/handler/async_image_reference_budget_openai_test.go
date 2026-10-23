//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
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

type referenceBudgetOpenAIUpstream struct {
	service.HTTPUpstream
	failures map[int64]string
	calls    []int64
	bodies   []string
}

func (u *referenceBudgetOpenAIUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.calls = append(u.calls, accountID)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.bodies = append(u.bodies, string(body))
	status, response := http.StatusOK, `{"data":[{"b64_json":"aGVsbG8="}]}`
	if failure, ok := u.failures[accountID]; ok {
		status, response = http.StatusBadRequest, failure
	}
	return &http.Response{
		StatusCode: status, Body: io.NopCloser(strings.NewReader(response)),
		Header: http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"reference-budget-request"}, "Retry-After": []string{"27"}},
	}, nil
}

func TestOpenAIReferenceBudgetDirectAccountSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	budgetBody := `{"error":{"type":"invalid_request_error","code":"reference_download_failed","message":"` + referenceBudgetTestMessage + `"}}`
	for _, tt := range []struct {
		name              string
		failures          map[int64]string
		async, admin      bool
		maxSwitch, status int
		calls             []int64
	}{
		{"A_to_B_success", map[int64]string{1: budgetBody}, true, false, 3, 200, []int64{1, 2}},
		{"all_candidates_exhausted", map[int64]string{1: budgetBody, 2: budgetBody}, true, false, 3, 400, []int64{1, 2}},
		{"zero_switch_budget", map[int64]string{1: budgetBody}, true, false, 0, 400, []int64{1}},
		{"sync_remains_terminal", map[int64]string{1: budgetBody}, false, false, 3, 400, []int64{1}},
		{"admin_override_remains_terminal", map[int64]string{1: budgetBody}, true, true, 3, 409, []int64{1}},
		{"ordinary_400_diagnostics", map[int64]string{1: `{"error":{"type":"invalid_request_error","code":"content_policy_violation","message":"The request was rejected by the upstream safety policy."}}`}, true, false, 3, 400, []int64{1}},
		{"historical_exclusion_relaxation_keeps_budget_account_blocked", nil, true, false, 3, 200, []int64{2}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			accounts := []service.Account{}
			for _, id := range []int64{1, 2} {
				accounts = append(accounts, service.Account{
					ID: id, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: int(id),
					Credentials: map[string]any{"api_key": "test-key", "base_url": "https://images.example/v1", "pool_mode": true, "pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []any{400}},
				})
			}
			upstream := &referenceBudgetOpenAIUpstream{failures: tt.failures}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			gateway := service.NewOpenAIGatewayService(openAIImagesFailoverAccountRepo{accounts: accounts}, nil, nil, nil, nil, nil, nil, cfg,
				nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing,
				service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
			h.maxAccountSwitches = tt.maxSwitch
			if tt.admin {
				status, message := 409, "administrative override"
				h.errorPassthroughService = service.NewErrorPassthroughService(sizeCapabilityPassthroughRepo{rules: []*model.ErrorPassthroughRule{{
					ID: 1, Name: "budget override", Enabled: true, ErrorCodes: []int{400}, MatchMode: model.MatchModeAny, ResponseCode: &status, CustomMessage: &message,
				}}}, nil)
			}
			capture := &service.AsyncImageAccountAttemptCapture{}
			ctx := context.Background()
			if tt.async {
				ctx = service.WithAsyncImageAccountAttemptCapture(ctx, capture)
				ctx = withAsyncImageUsageCapture(ctx, &AsyncImageUsageCapture{})
			}
			if tt.name == "historical_exclusion_relaxation_keeps_budget_account_blocked" {
				history, err := json.Marshal([]service.AsyncImageAccountAttempt{
					{AccountID: 1, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 400, Error: referenceBudgetTestMessage},
					{AccountID: 2, Status: service.AsyncImageAccountAttemptFailed, StatusCode: 503, Error: "temporarily unavailable"},
				})
				require.NoError(t, err)
				ctx = service.WithAsyncImageExcludedAccountIDs(ctx, map[int64]struct{}{1: {}, 2: {}})
				ctx = service.WithAsyncImageNonRetryableAccountIDs(ctx, asyncImageNonRetryableAccountIDs(&service.AsyncImageTask{AccountAttempts: history}))
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(`{"model":"gpt-image-2","prompt":"draw","size":"2048x1152"}`)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			groupID := int64(3131)
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 90, GroupID: &groupID, Group: &service.Group{ID: groupID, AllowImageGeneration: true}, User: &service.User{ID: 91}})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 91})
			h.Images(c)
			require.Equal(t, tt.calls, upstream.calls, "configured same-account retries must not repeat a budget failure")
			require.Equal(t, tt.status, recorder.Code)
			for _, body := range upstream.bodies {
				require.Equal(t, "gpt-image-2", gjson.Get(body, "model").String())
				require.Equal(t, "2048x1152", gjson.Get(body, "size").String())
			}
			if tt.name == "all_candidates_exhausted" || tt.name == "zero_switch_budget" {
				require.Equal(t, service.AsyncImageReferenceBudgetExceededCode, gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
				require.Equal(t, "27", recorder.Header().Get("Retry-After"))
				require.False(t, shouldRetryAsyncImageUpstreamReferenceFetch(&service.AsyncImageTask{RequestType: service.AsyncImageRequestTypeImageToImage}, testAsyncImageRetryConfig(), recorder.Code, formatAsyncImageUpstreamFailure(recorder.Code, recorder.Body.Bytes())))
			}
			if tt.async && !tt.admin && tt.name != "historical_exclusion_relaxation_keeps_budget_account_blocked" {
				attempts := capture.Attempts()
				require.GreaterOrEqual(t, len(attempts), 2)
				require.Equal(t, 400, attempts[1].StatusCode)
				require.Equal(t, "reference-budget-request", attempts[1].UpstreamRequestID)
				wantCode := "reference_download_failed"
				if tt.name == "ordinary_400_diagnostics" {
					wantCode = "content_policy_violation"
				}
				require.Equal(t, wantCode, attempts[1].ProviderErrorCode)
			}
		})
	}
}
