package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageRetryRuleOrderAndStableUsage(t *testing.T) {
	first := AsyncImageRetryRule{
		ID: "first", Name: "first", Enabled: true, Category: "operator_label",
		Scope: AsyncImageRetryScope{Platforms: []string{PlatformOpenAI}, SourceAccountIDs: []int64{7}, Categories: []string{"reference_fetch"}},
		Match: AsyncImageRetryMatch{HTTPStatuses: []int{400}, ContainsAny: []string{"REFERENCE"}, ContainsAll: []string{"download", "failed"}, Excludes: []string{"permanent"}, Regex: `(?i)reference.*failed`},
		Steps: []AsyncImageRetryStep{
			{ID: "original", Action: "local_same_account", MaxAttempts: 1},
			{ID: "alternate", Action: "other_account", MaxAttempts: 2, Backoff: "exponential", DelaySeconds: 4, MaxDelaySeconds: 6},
		},
	}
	second := AsyncImageRetryRule{
		ID: "second", Name: "second", Enabled: true, Match: AsyncImageRetryMatch{HTTPStatuses: []int{400}},
		Steps: []AsyncImageRetryStep{{ID: "stop", Action: "stop", MaxAttempts: 1}},
	}
	policy := AsyncImageRetryPolicy{Revision: 4, Rules: []AsyncImageRetryRule{first, second}}
	require.NoError(t, ValidateAsyncImageRetryPolicy(&policy))
	fact := AsyncImageRetryFact{
		Platform: PlatformOpenAI, AccountID: 7, Stage: "submit", HTTPStatus: 400,
		Category: "reference_fetch", Message: "Reference download failed", SafeToRetry: true, HasReferences: true,
	}

	decision := ProtectAsyncImageRetryDecision(fact, EvaluateAsyncImageRetry(policy, fact, AsyncImageRetryUsage{}))
	require.Equal(t, "local_same_account", decision.Action)
	require.Equal(t, "operator_label", decision.Category)
	require.True(t, AsyncImageRetryAccountAllowed(decision, fact, 7))
	require.False(t, AsyncImageRetryAccountAllowed(decision, fact, 8))

	usage := AsyncImageRetryUsage{Rules: map[string]int{"first": 2}, Steps: map[string]map[string]int{"first": {"original": 1, "alternate": 1}}}
	policy.Revision++
	decision = EvaluateAsyncImageRetry(policy, fact, usage)
	require.Equal(t, "other_account", decision.Action)
	require.Equal(t, 6, decision.DelaySeconds)
	require.False(t, AsyncImageRetryAccountAllowed(decision, fact, 7))
	require.True(t, AsyncImageRetryAccountAllowed(decision, fact, 8))

	usage.Steps["first"]["alternate"]++
	decision = EvaluateAsyncImageRetry(policy, fact, usage)
	require.Equal(t, "first", decision.RuleID)
	require.Equal(t, "rule_exhausted", decision.BlockReason)
	require.Equal(t, "manual_review", decision.Action)

	fact.Message = "Reference download failed permanently"
	require.Equal(t, "second", EvaluateAsyncImageRetry(policy, fact, usage).RuleID)
}

func TestAsyncImageRetryRejectsScopeOnlyAndInvalidRegex(t *testing.T) {
	policy := AsyncImageRetryPolicy{Rules: []AsyncImageRetryRule{{
		ID: "rule", Name: "Rule", Enabled: true, Scope: AsyncImageRetryScope{SourceAccountIDs: []int64{1}},
		Match: AsyncImageRetryMatch{Excludes: []string{"timeout"}},
		Steps: []AsyncImageRetryStep{{ID: "stop", Action: "stop", MaxAttempts: 1}},
	}}}
	require.ErrorContains(t, ValidateAsyncImageRetryPolicy(&policy), "positive")
	policy.Rules[0].Match.Regex = "(a)\\1"
	require.ErrorContains(t, ValidateAsyncImageRetryPolicy(&policy), "regular expression")
}

func TestAsyncImageRetryGuardsOutputAndUnknownOutcome(t *testing.T) {
	decision := AsyncImageRetryDecision{Source: "custom", RequestedAction: "other_account", Action: "other_account"}
	blocked := ProtectAsyncImageRetryDecision(AsyncImageRetryFact{Stage: "submit", SafeToRetry: true, Unknown: true, AccountID: 7}, decision)
	require.Equal(t, "manual_review", blocked.Action)
	require.Equal(t, "execution_unknown", blocked.BlockReason)

	for stage, action := range map[string]string{"download": "resume_download", "storage": "resume_storage", "billing": "resume_billing"} {
		fact := AsyncImageRetryFact{Stage: stage, HasOutput: true, SafeToRetry: true}
		guarded := ProtectAsyncImageRetryDecision(fact, decision)
		require.Equal(t, action, guarded.Action)
		require.Equal(t, "other_account", guarded.RequestedAction)
	}

	decision.AllowedAccountIDs = []int64{7, 8, 9}
	decision.ExcludedAccountIDs = []int64{8}
	fact := AsyncImageRetryFact{AccountID: 7}
	require.False(t, AsyncImageRetryAccountAllowed(decision, fact, 7))
	require.False(t, AsyncImageRetryAccountAllowed(decision, fact, 8))
	require.True(t, AsyncImageRetryAccountAllowed(decision, fact, 9))
	require.False(t, AsyncImageRetryAccountAllowed(decision, fact, 10))
}

func TestAsyncImageRetryHTTPFactsUseCompleteResponseAndRedact(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			var fact AsyncImageRetryFact
			ctx := WithAsyncImageAccountAttemptCapture(context.Background(), &AsyncImageAccountAttemptCapture{})
			ctx = WithAsyncImageRetryHooks(ctx, AsyncImageRetryHooks{OnFailure: func(_ context.Context, f AsyncImageRetryFact) bool {
				fact = f
				return true
			}})
			account := &Account{ID: 19, Platform: platform}
			body := `{"error":{"code":"download_timeout","message":"Reference download failed https://host.invalid/path?signature=secret Bearer abcdef123 prompt: unreleased text"}}`
			resp := &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(body))}
			responseCtx := trackAsyncImageErrorResponse(ctx, resp)
			contents, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.True(t, handleAsyncImageRetryHTTPError(responseCtx, account, resp.StatusCode, contents))
			require.True(t, fact.SafeToRetry)
			require.True(t, fact.Dispatched)
			require.Equal(t, "download_timeout", fact.ErrorCode)
			require.NotContains(t, fact.Message, "host.invalid")
			require.NotContains(t, fact.Message, "abcdef123")
			require.NotContains(t, fact.Message, "unreleased text")

			require.True(t, handleAsyncImageRetryHTTPError(ctx, account, resp.StatusCode, contents))
			require.True(t, fact.Unknown)
			require.False(t, fact.SafeToRetry)
			require.True(t, handleAsyncImageRetryTransportError(ctx, account))
			require.True(t, fact.Unknown)
		})
	}
}

func TestAsyncImageRetryDispatchBlockDoesNotSpendGenerationBudget(t *testing.T) {
	ctx := WithAsyncImageAttemptBudget(context.Background(), 1)
	n := 0
	ctx = WithAsyncImageRetryHooks(ctx, AsyncImageRetryHooks{BeforeDispatch: func(_ context.Context, accountID int64) error {
		n++
		if accountID == 1 {
			return ErrAsyncImageRetryDispatchBlocked
		}
		return nil
	}})
	require.ErrorIs(t, BeginAsyncImageUpstreamAttempt(ctx, 1), ErrAsyncImageRetryDispatchBlocked)
	require.Zero(t, AsyncImageUpstreamAttemptCount(ctx))
	require.NoError(t, BeginAsyncImageUpstreamAttempt(ctx, 2))
	require.Equal(t, 1, AsyncImageUpstreamAttemptCount(ctx))
	require.ErrorIs(t, BeginAsyncImageUpstreamAttempt(ctx, 2), ErrAsyncImageAttemptBudgetExhausted)
	require.Equal(t, 2, n)
}

func TestAsyncImageRetryOpenAIOAuthStopsOnHTTPAndSSEFailures(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body, code string
		status                        int
	}{
		{"HTTP rejection", "application/json", `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"Too many requests"}}`, "rate_limit_exceeded", http.StatusTooManyRequests},
		{"terminal SSE", "text/event-stream",
			"data: {\"type\":\"response.created\",\"response\":{\"created_at\":1710000021}}\n\n" +
				"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"type\":\"server_error\",\"code\":\"server_error\",\"message\":\"temporarily unavailable\"}}}\n\n",
			"server_error", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			body := []byte(`{"model":"gpt-image-2","prompt":"test fixture","response_format":"b64_json"}`)
			req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: tc.status,
				Header:     http.Header{"Content-Type": []string{tc.contentType}},
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			account := &Account{ID: 91, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"access_token": "local-fixture-token"}}
			ctx := WithAsyncImageAttemptBudget(context.Background(), 4)
			ctx = WithAsyncImageAccountAttemptCapture(ctx, &AsyncImageAccountAttemptCapture{})
			var fact AsyncImageRetryFact
			ctx = WithAsyncImageRetryHooks(ctx, AsyncImageRetryHooks{OnFailure: func(_ context.Context, received AsyncImageRetryFact) bool {
				fact = received
				return true
			}})

			result, err := svc.ForwardImages(ctx, c, account, body, parsed, "")
			require.Nil(t, result)
			require.ErrorIs(t, err, ErrAsyncImageRetryHandled)
			require.True(t, fact.SafeToRetry)
			require.Equal(t, account.ID, fact.AccountID)
			require.Equal(t, tc.status, fact.HTTPStatus)
			require.Equal(t, tc.code, fact.ErrorCode)
			require.ErrorIs(t, BeginAsyncImageUpstreamAttempt(ctx, account.ID), ErrAsyncImageRetryHandled)
			require.Equal(t, 1, AsyncImageUpstreamAttemptCount(ctx))
		})
	}
}
