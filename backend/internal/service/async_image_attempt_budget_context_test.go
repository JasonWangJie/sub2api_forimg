package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAsyncImageAttemptBudgetCancellationOnlyAppliesWhenTracked(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		name := "synchronous without budget preserves detached forwarding"
		if tracked {
			name = "asynchronous budget rejects cancellation without consuming an attempt"
		}
		t.Run(name, func(t *testing.T) {
			capture := &AsyncImageAccountAttemptCapture{}
			ctx := WithAsyncImageAccountAttemptCapture(context.Background(), capture)
			if tracked {
				ctx = WithAsyncImageAttemptBudget(ctx, 2)
			}
			ctx, cancel := context.WithCancel(ctx)
			cancel()
			err := BeginAsyncImageUpstreamAttempt(ctx, 7)
			if tracked {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.NoError(t, err)
			}
			require.Zero(t, AsyncImageUpstreamAttemptCount(ctx))
			require.Empty(t, capture.Attempts(), "rejected or untracked calls must not record an invocation")
		})
	}
}
