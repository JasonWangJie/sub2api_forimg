//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIImagesAsyncSizeCapability422PreservesExplicitTemporaryRules(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		for _, poolMode := range []bool{false, true} {
			mode := "normal"
			if poolMode {
				mode = "pool"
			}
			for _, ruleMode := range []string{"none", "matching", "nonmatching"} {
				t.Run(accountType+"/"+mode+"/"+ruleMode, func(t *testing.T) {
					ctx := WithAsyncImageAccountAttemptCapture(context.Background(), &AsyncImageAccountAttemptCapture{})
					body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","size":"2048x1152","response_format":"b64_json"}`)
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body)).WithContext(ctx)
					c.Request.Header.Set("Content-Type", "application/json")
					repo := &errorPolicyRepoStub{}
					rateLimit := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
					svc := &OpenAIGatewayService{
						cfg: &config.Config{}, rateLimitService: rateLimit,
						httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
							StatusCode: http.StatusUnprocessableEntity,
							Header:     http.Header{"Content-Type": []string{"application/json"}},
							Body:       io.NopCloser(strings.NewReader(imageSizeCapabilityTestBody(imageSizeCapabilityTestMessage))),
						}},
					}
					rateLimit.SetAccountRuntimeBlocker(svc)
					account := &Account{
						ID: 104, Platform: PlatformOpenAI, Type: accountType, Status: StatusActive, Schedulable: true,
						Credentials: map[string]any{
							"api_key": "test-api-key", "access_token": "test-access-token",
							"base_url": "https://image-upstream.example/v1", "pool_mode": poolMode,
							"pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []int{422},
						},
					}
					if ruleMode != "none" {
						keyword := "No eligible upstream image provider"
						if ruleMode == "nonmatching" {
							keyword = "unrelated upstream failure"
						}
						account.Credentials["temp_unschedulable_enabled"] = true
						account.Credentials["temp_unschedulable_rules"] = []any{map[string]any{
							"error_code": float64(422), "keywords": []any{keyword}, "duration_minutes": float64(10),
						}}
					}
					parsed, err := svc.ParseOpenAIImagesRequest(c, body)
					require.NoError(t, err)

					result, err := svc.ForwardImages(ctx, c, account, body, parsed, "")
					require.Nil(t, result)
					var failoverErr *UpstreamFailoverError
					require.ErrorAs(t, err, &failoverErr)
					require.True(t, failoverErr.IsAsyncImageSizeCapabilityMismatch())
					require.False(t, failoverErr.RetryableOnSameAccount)
					require.Empty(t, recorder.Body.Bytes())
					if ruleMode == "matching" {
						require.Len(t, repo.modelRateLimitCalls, 1, "the configured rule must remain effective before switching accounts")
						call := repo.modelRateLimitCalls[0]
						require.Equal(t, account.ID, call.accountID)
						require.Equal(t, "gpt-image-2", call.scope)
						require.WithinDuration(t, time.Now().Add(10*time.Minute), call.resetAt, 5*time.Second)
						require.Equal(t, int64(422), gjson.Get(call.reason, "status_code").Int())
					} else {
						require.Empty(t, repo.modelRateLimitCalls, "a size capability mismatch must not create a default cooldown")
					}
					require.Zero(t, repo.tempCalls, "known-model rules must not block unrelated models")
					require.Zero(t, repo.setErrCalls)
					require.Equal(t, StatusActive, account.Status)
					require.True(t, account.Schedulable)
					require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
					require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-image-2.5-flare"))
				})
			}
		}
	}
}
