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

func TestAsyncImageAttemptBudgetCorrelatesSendsWithoutChangingAllowance(t *testing.T) {
	capture := &AsyncImageAccountAttemptCapture{}
	ctx := WithAsyncImageAccountAttemptCapture(context.Background(), capture)
	ctx = WithAsyncImageAttemptBudget(ctx, 2)
	RecordAsyncImageAccountAttempt(ctx, AsyncImageAccountAttempt{AccountID: 7, Status: AsyncImageAccountAttemptSelected})
	require.NoError(t, BeginAsyncImageUpstreamAttempt(ctx, 7))
	RecordAsyncImageAccountAttempt(ctx, AsyncImageAccountAttempt{AccountID: 7, Status: AsyncImageAccountAttemptFailed})
	// A same-account gateway retry need not emit another selected event.
	require.NoError(t, BeginAsyncImageUpstreamAttempt(ctx, 7))
	RecordAsyncImageAccountAttempt(ctx, AsyncImageAccountAttempt{AccountID: 7, Status: AsyncImageAccountAttemptSucceeded})
	require.Equal(t, 2, AsyncImageUpstreamAttemptCount(ctx))
	before := capture.Attempts()
	require.ErrorIs(t, BeginAsyncImageUpstreamAttempt(ctx, 7), ErrAsyncImageAttemptBudgetExhausted)
	require.Equal(t, before, capture.Attempts(), "an exhausted budget must not create another send identity")
	require.Len(t, before, 5)
	require.Equal(t, before[0].AttemptID, before[1].AttemptID)
	require.Equal(t, before[0].AttemptID, before[2].AttemptID)
	require.Equal(t, before[3].AttemptID, before[4].AttemptID)
	require.NotEqual(t, before[0].AttemptID, before[3].AttemptID)
}
