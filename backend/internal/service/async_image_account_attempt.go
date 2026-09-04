package service

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// AsyncImageAccountAttempt is the durable, non-secret audit record for one
// account selected while executing an asynchronous image task.
type AsyncImageAccountAttempt struct {
	AccountID         int64     `json:"account_id"`
	AccountName       string    `json:"account_name,omitempty"`
	Status            string    `json:"status"`
	StatusCode        int       `json:"status_code,omitempty"`
	UpstreamRequestID string    `json:"upstream_request_id,omitempty"`
	Error             string    `json:"error,omitempty"`
	AttemptedAt       time.Time `json:"attempted_at"`
}

const (
	AsyncImageAccountAttemptSelected  = "selected"
	AsyncImageAccountAttemptSucceeded = "succeeded"
	AsyncImageAccountAttemptFailed    = "failed"
)

type asyncImageAccountAttemptContextKey struct{}
type asyncImageExcludedAccountsContextKey struct{}
type asyncImageGeminiMaxSwitchesContextKey struct{}
type asyncImageAccountAttemptTimeoutContextKey struct{}
type asyncImageRoutingSessionHashContextKey struct{}
type asyncImageReferenceAccountSwitchContextKey struct{}

// WithAsyncImageRoutingSessionHash pins one durable reference-image task to
// its selected account while the task is retrying the same upstream request.
// The value is intentionally task-scoped and never exposed to API clients.
func WithAsyncImageRoutingSessionHash(ctx context.Context, hash string) context.Context {
	if ctx == nil || hash == "" {
		return ctx
	}
	return context.WithValue(ctx, asyncImageRoutingSessionHashContextKey{}, hash)
}

func AsyncImageRoutingSessionHash(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	hash, _ := ctx.Value(asyncImageRoutingSessionHashContextKey{}).(string)
	return hash
}

// WithAsyncImageReferenceAccountSwitch marks the invocation whose selected
// account is exhausted for reference-fetch retries. Gateway handlers use it
// to suppress their normal historical-exclusion fallback, which would
// otherwise re-select the exhausted account when no alternate is available.
func WithAsyncImageReferenceAccountSwitch(ctx context.Context, active bool) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, asyncImageReferenceAccountSwitchContextKey{}, active)
}

func AsyncImageReferenceAccountSwitchActive(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	active, _ := ctx.Value(asyncImageReferenceAccountSwitchContextKey{}).(bool)
	return active
}

func WithAsyncImageExcludedAccountIDs(ctx context.Context, ids map[int64]struct{}) context.Context {
	if len(ids) == 0 {
		return ctx
	}
	copyIDs := make(map[int64]struct{}, len(ids))
	for id := range ids {
		copyIDs[id] = struct{}{}
	}
	return context.WithValue(ctx, asyncImageExcludedAccountsContextKey{}, copyIDs)
}

func AsyncImageExcludedAccountIDs(ctx context.Context) map[int64]struct{} {
	if ctx == nil {
		return nil
	}
	ids, _ := ctx.Value(asyncImageExcludedAccountsContextKey{}).(map[int64]struct{})
	copyIDs := make(map[int64]struct{}, len(ids))
	for id := range ids {
		copyIDs[id] = struct{}{}
	}
	return copyIDs
}

func WithAsyncImageGeminiMaxAccountSwitches(ctx context.Context, max int) context.Context {
	if max < 0 {
		max = 0
	}
	return context.WithValue(ctx, asyncImageGeminiMaxSwitchesContextKey{}, max)
}

func AsyncImageGeminiMaxAccountSwitches(ctx context.Context) (int, bool) {
	if ctx == nil {
		return 0, false
	}
	max, ok := ctx.Value(asyncImageGeminiMaxSwitchesContextKey{}).(int)
	return max, ok
}

func WithAsyncImageAccountAttemptTimeout(ctx context.Context, timeout time.Duration) context.Context {
	if timeout <= 0 {
		return ctx
	}
	return context.WithValue(ctx, asyncImageAccountAttemptTimeoutContextKey{}, timeout)
}

func AsyncImageAccountAttemptTimeout(ctx context.Context) (time.Duration, bool) {
	if ctx == nil {
		return 0, false
	}
	timeout, ok := ctx.Value(asyncImageAccountAttemptTimeoutContextKey{}).(time.Duration)
	return timeout, ok && timeout > 0
}

// AsyncImageAccountAttemptCapture collects attempts from all failover loops
// belonging to one worker invocation. It is intentionally context-scoped so
// synchronous requests do not allocate or persist this state.
type AsyncImageAccountAttemptCapture struct {
	mu       sync.RWMutex
	attempts []AsyncImageAccountAttempt
	persist  func(context.Context, AsyncImageAccountAttempt)
}

func (c *AsyncImageAccountAttemptCapture) SetPersistor(fn func(context.Context, AsyncImageAccountAttempt)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.persist = fn
	c.mu.Unlock()
}

func WithAsyncImageAccountAttemptCapture(ctx context.Context, capture *AsyncImageAccountAttemptCapture) context.Context {
	if capture == nil {
		return ctx
	}
	return context.WithValue(ctx, asyncImageAccountAttemptContextKey{}, capture)
}

func AsyncImageAccountAttemptCaptureFromContext(ctx context.Context) *AsyncImageAccountAttemptCapture {
	if ctx == nil {
		return nil
	}
	capture, _ := ctx.Value(asyncImageAccountAttemptContextKey{}).(*AsyncImageAccountAttemptCapture)
	return capture
}

func RecordAsyncImageAccountAttempt(ctx context.Context, attempt AsyncImageAccountAttempt) {
	capture := AsyncImageAccountAttemptCaptureFromContext(ctx)
	if capture == nil || attempt.AccountID <= 0 {
		return
	}
	if attempt.AttemptedAt.IsZero() {
		attempt.AttemptedAt = time.Now().UTC()
	}
	capture.mu.Lock()
	capture.attempts = append(capture.attempts, attempt)
	persist := capture.persist
	capture.mu.Unlock()
	if persist != nil {
		// Account-attempt auditing is part of task durability, not the client
		// request lifecycle. Keep the write alive briefly when an upstream
		// timeout or worker cancellation has already canceled the parent.
		persistCtx := context.Background()
		if ctx != nil {
			persistCtx = context.WithoutCancel(ctx)
		}
		persistCtx, cancel := context.WithTimeout(persistCtx, 5*time.Second)
		persist(persistCtx, attempt)
		cancel()
	}
}

func (c *AsyncImageAccountAttemptCapture) Attempts() []AsyncImageAccountAttempt {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]AsyncImageAccountAttempt(nil), c.attempts...)
}

// MergeAsyncImageAccountAttempts appends new attempts and derives a stable,
// sorted account-id list for API/admin inspection.
func MergeAsyncImageAccountAttempts(existing json.RawMessage, attempts []AsyncImageAccountAttempt) (json.RawMessage, json.RawMessage) {
	var merged []AsyncImageAccountAttempt
	if len(existing) > 0 && json.Valid(existing) {
		_ = json.Unmarshal(existing, &merged)
	}
	merged = append(merged, attempts...)
	if merged == nil {
		merged = []AsyncImageAccountAttempt{}
	}
	idsSet := make(map[int64]struct{}, len(merged))
	for _, attempt := range merged {
		if attempt.AccountID > 0 {
			idsSet[attempt.AccountID] = struct{}{}
		}
	}
	ids := make([]int64, 0, len(idsSet))
	for id := range idsSet {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	attemptsJSON, _ := json.Marshal(merged)
	idsJSON, _ := json.Marshal(ids)
	return json.RawMessage(attemptsJSON), json.RawMessage(idsJSON)
}
