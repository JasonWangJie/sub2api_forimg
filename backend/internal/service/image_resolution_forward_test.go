package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// The real Images forwarding branches must reject a locally cooled channel
// before recording an actual generation send. The ordinary request and billing
// response handling is deliberately outside this local admission decision.
type imageResolutionForwardGuard struct {
	checks [][3]string
}

func (*imageResolutionForwardGuard) Enabled(context.Context) bool { return true }
func (*imageResolutionForwardGuard) Observe(context.Context, ImageResolutionObservation) error {
	return nil
}
func (g *imageResolutionForwardGuard) IsCooling(_ context.Context, _ int64, model, tier string) (bool, error) {
	g.checks = append(g.checks, [3]string{model, tier, "cooling"})
	return true, nil
}

func TestImageResolutionForwardImagesLocalCoolingDoesNotSendOrSpendBudget(t *testing.T) {
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		for _, tracked := range []bool{false, true} {
			name := accountType + "/sync"
			if tracked {
				name = accountType + "/durable"
			}
			t.Run(name, func(t *testing.T) {
				body := []byte(`{"model":"gpt-image-2.5-flare","prompt":"test chart","resolution":"4K","aspect_ratio":"1:1"}`)
				c, rec := newOpenAIImagesTestContext(t, body)
				upstream := &httpUpstreamRecorder{resp: openAIImagesJSONResponse()}
				svc := newOpenAIImagesTestService(upstream)
				guard := &imageResolutionForwardGuard{}
				monitor := NewImageResolutionMonitor(guard)
				t.Cleanup(monitor.Close)
				svc.SetImageResolutionGuard(monitor)
				parsed, err := svc.ParseOpenAIImagesRequest(c, body)
				require.NoError(t, err)
				ctx, err := WithImageAccountPoolRoute(context.Background(), "gpt-image-2.5-flare", "4K")
				require.NoError(t, err)
				capture := &AsyncImageAccountAttemptCapture{}
				if tracked {
					ctx = WithAsyncImageAccountAttemptCapture(ctx, capture)
					ctx = WithAsyncImageAttemptBudget(ctx, 2)
				}
				c.Request = c.Request.WithContext(ctx)
				account := newOpenAIImagesAPIKeyAccount()
				account.Type = accountType
				if accountType == AccountTypeOAuth {
					account.Credentials = map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"}
				}

				result, err := svc.ForwardImages(ctx, c, account, body, parsed, "gpt-image-2")

				require.Nil(t, result)
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover, "local cooling must retain the typed account-switch signal")
				require.Equal(t, 503, failover.StatusCode)
				require.Equal(t, NextAccountRetry, failover.NextAccountAction)
				require.True(t, failover.RequestScopedTransient)
				require.Nil(t, upstream.lastReq, "a local guard rejection must not POST a generation")
				require.Zero(t, AsyncImageUpstreamAttemptCount(ctx), "only real generation sends consume the durable budget")
				require.Empty(t, capture.Attempts(), "a local rejection must not write an invoked attempt")
				require.Empty(t, rec.Body.String(), "local rejection must leave the response open for another channel")
				require.Equal(t, [][3]string{{"gpt-image-2.5-flare", "4K", "cooling"}}, guard.checks,
					"both API-key and OAuth use the client alias, not the mapped model or OAuth outer chat model")
			})
		}
	}
}
