package service

import (
	"context"
	"errors"
	"sync/atomic"
)

var ErrAsyncImageAttemptBudgetExhausted = errors.New("async image upstream attempt budget exhausted")

const AsyncImageAccountAttemptInvoked = "invocation_started"

type asyncImageAttemptBudgetKey struct{}
type asyncImageAttemptBudget struct {
	limit int64
	used  atomic.Int64
}

// WithAsyncImageAttemptBudget bounds actual upstream calls, including same-account
// retries inside gateways. The worker supplies the remaining persisted allowance.
func WithAsyncImageAttemptBudget(ctx context.Context, remaining int) context.Context {
	if remaining < 0 {
		remaining = 0
	}
	return context.WithValue(ctx, asyncImageAttemptBudgetKey{}, &asyncImageAttemptBudget{limit: int64(remaining)})
}

func AsyncImageUpstreamAttemptCount(ctx context.Context) int {
	b, _ := ctx.Value(asyncImageAttemptBudgetKey{}).(*asyncImageAttemptBudget)
	if b == nil {
		return 0
	}
	return int(b.used.Load())
}

// HasAsyncImageAttemptBudget distinguishes measured zero calls from synchronous
// or legacy contexts that do not track generation invocations at all.
func HasAsyncImageAttemptBudget(ctx context.Context) bool {
	budget, _ := ctx.Value(asyncImageAttemptBudgetKey{}).(*asyncImageAttemptBudget)
	return budget != nil
}

// BeginAsyncImageUpstreamAttempt must run immediately before the generation HTTP
// call, after local admission checks. Missing budgets leave synchronous callers alone.
func BeginAsyncImageUpstreamAttempt(ctx context.Context, accountID int64) error {
	b, _ := ctx.Value(asyncImageAttemptBudgetKey{}).(*asyncImageAttemptBudget)
	if b == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for {
		n := b.used.Load()
		if n >= b.limit {
			return ErrAsyncImageAttemptBudgetExhausted
		}
		if b.used.CompareAndSwap(n, n+1) {
			break
		}
	}
	RecordAsyncImageAccountAttempt(ctx, AsyncImageAccountAttempt{AccountID: accountID, Status: AsyncImageAccountAttemptInvoked})
	return nil
}
