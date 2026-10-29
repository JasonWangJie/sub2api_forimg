package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageInputCapabilityHintRequiresOtherwiseCompatibleLocal(t *testing.T) {
	for _, tc := range []struct {
		limits map[string]any
		hint   bool
	}{
		{map[string]any{"transports": []string{"local"}}, true},
		{map[string]any{"transports": []string{"local"}, "max_images": 1}, false},
		{map[string]any{"transports": []string{"local"}, "max_total_bytes": 1}, false},
		{map[string]any{"transports": []string{"passthrough"}}, false},
	} {
		ctx := WithImageInputCapabilities(context.Background(), ImageInputCapabilities{Transport: "passthrough", Count: 2, TotalBytes: 10})
		ImageAccountSupportsInput(ctx, &Account{ID: 1, Extra: map[string]any{"image_input_capabilities": tc.limits}})
		require.Equal(t, tc.hint, ImageInputLocalFallbackSuggested(ctx))
		require.False(t, HasAsyncImageAttemptBudget(ctx), "zero calls without tracking is not evidence of no invocation")
		ctx = WithAsyncImageAttemptBudget(ctx, 1)
		require.True(t, HasAsyncImageAttemptBudget(ctx))
		require.Zero(t, AsyncImageUpstreamAttemptCount(ctx))
	}
}
