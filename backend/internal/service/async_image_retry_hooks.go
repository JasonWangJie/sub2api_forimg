package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/tidwall/gjson"
)

var ErrAsyncImageRetryDispatchBlocked = errors.New("async image retry dispatch blocked")
var ErrAsyncImageRetryHandled = errors.New("async image failure handled by retry policy")

type AsyncImageRetryHooks struct {
	OnFailure      func(context.Context, AsyncImageRetryFact) bool
	BeforeDispatch func(context.Context, int64) error
}

type asyncImageRetryHooksKey struct{}
type asyncImageRetryHookState struct {
	hooks   AsyncImageRetryHooks
	stopped atomic.Bool
}

func WithAsyncImageRetryHooks(ctx context.Context, hooks AsyncImageRetryHooks) context.Context {
	return context.WithValue(ctx, asyncImageRetryHooksKey{}, &asyncImageRetryHookState{hooks: hooks})
}

func asyncImageRetryHooks(ctx context.Context) AsyncImageRetryHooks {
	state, _ := ctx.Value(asyncImageRetryHooksKey{}).(*asyncImageRetryHookState)
	if state == nil {
		return AsyncImageRetryHooks{}
	}
	return state.hooks
}

func isAsyncImageRetryStopped(ctx context.Context) bool {
	state, _ := ctx.Value(asyncImageRetryHooksKey{}).(*asyncImageRetryHookState)
	return state != nil && state.stopped.Load()
}

func AsyncImageRetryFailure(ctx context.Context, fact AsyncImageRetryFact) bool {
	state, _ := ctx.Value(asyncImageRetryHooksKey{}).(*asyncImageRetryHookState)
	if state == nil || state.hooks.OnFailure == nil {
		return false
	}
	if state.hooks.OnFailure(ctx, SanitizeAsyncImageRetryFact(fact)) {
		state.stopped.Store(true)
		return true
	}
	return false
}

type asyncImageRetryAccountFilterKey struct{}
type asyncImageRetryAccountFilter struct {
	decision AsyncImageRetryDecision
	fact     AsyncImageRetryFact
}

func WithAsyncImageRetryAccountFilter(ctx context.Context, decision AsyncImageRetryDecision, fact AsyncImageRetryFact) context.Context {
	return context.WithValue(ctx, asyncImageRetryAccountFilterKey{}, asyncImageRetryAccountFilter{decision: decision, fact: fact})
}

func asyncImageRetryFilter(ctx context.Context) (asyncImageRetryAccountFilter, bool) {
	filter, ok := ctx.Value(asyncImageRetryAccountFilterKey{}).(asyncImageRetryAccountFilter)
	return filter, ok
}

func filterAsyncImageRetryAccounts(ctx context.Context, accounts []Account) []Account {
	filter, ok := asyncImageRetryFilter(ctx)
	if !ok {
		return accounts
	}
	return slices.DeleteFunc(slices.Clone(accounts), func(account Account) bool {
		return !AsyncImageRetryAccountAllowed(filter.decision, filter.fact, account.ID)
	})
}

func handleAsyncImageRetryTransportError(ctx context.Context, account *Account) bool {
	if asyncImageRetryHooks(ctx).OnFailure == nil {
		return false
	}
	return observeAsyncImageRetryFailure(ctx, account, AsyncImageRetryFact{
		Category: "execution_unknown", ErrorCode: "execution_unknown", Message: "upstream response unavailable after generation dispatch",
		Unknown: true,
	})
}

func handleAsyncImageRetryHTTPError(ctx context.Context, account *Account, status int, body []byte) bool {
	if asyncImageRetryHooks(ctx).OnFailure == nil {
		return false
	}
	message := strings.TrimSpace(extractUpstreamErrorMessage(body))
	code := extractUpstreamErrorCode(body)
	if account.Platform == PlatformGemini || account.Platform == PlatformAntigravity {
		_, code, _ = geminiAsyncUpstreamDiagnostics(body, "")
	}
	raw := strings.TrimSpace(string(body))
	if message == "" && len(raw) <= 768 && !strings.HasPrefix(raw, "<") &&
		!strings.HasPrefix(raw, "{") && !strings.HasPrefix(raw, "[") {
		message = raw
	}
	if message == "" || strings.HasPrefix(message, "{") || strings.HasPrefix(message, "[") || strings.HasPrefix(message, "<") {
		message = http.StatusText(status)
	}
	parsed := gjson.ParseBytes(body)
	cyberPolicy, _, _ := detectOpenAICyberPolicy(body)
	httpBodyComplete := asyncImageErrorBodyComplete(ctx) &&
		(!(strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[")) || gjson.ValidBytes(body))
	output := asyncImageErrorContainsOutput(parsed)
	unknown := !httpBodyComplete || status == http.StatusRequestTimeout || asyncImageTimeoutHasUnknownOutcome(raw)
	category := "upstream_rejection"
	switch {
	case unknown:
		category = "execution_unknown"
	case output:
		category = "output_present"
	case cyberPolicy || isAsyncImageTerminalRejection(code, message) || hasAsyncImageTerminalRejectionDetail(parsed):
		category = "terminal_rejection"
	case isAsyncImageSizeCapabilityMismatch(status, body) || isAsyncImageInternalError422(status, body):
		category = "capability"
	case isAsyncImageReferenceAccountFailoverForContext(ctx, status, body) || IsAsyncImageReferenceFetchFailureMessage(message):
		category = "reference_fetch"
	case status == http.StatusTooManyRequests || IsAsyncImageCapacityRejection(status, message):
		category = "capacity"
	case status >= 500:
		category = "upstream_transient"
	}
	// Generic proxy timeout or gateway errors cannot establish that the
	// provider rejected generation. Require stronger provider evidence.
	if category == "upstream_transient" && (status == http.StatusBadGateway || status == http.StatusGatewayTimeout) &&
		code == "" {
		category, unknown = "execution_unknown", true
	}
	return observeAsyncImageRetryFailure(ctx, account, AsyncImageRetryFact{
		HTTPStatus: status, ErrorCode: code, Message: message, Category: category,
		HasOutput: output, Unknown: unknown,
		SafeToRetry: !unknown && !output && category != "terminal_rejection",
	})
}

func observeAsyncImageRetryFailure(ctx context.Context, account *Account, fact AsyncImageRetryFact) bool {
	fact.Platform, fact.AccountID, fact.Stage = account.Platform, account.ID, "submit"
	fact.Dispatched, fact.HasReferences, fact.OccurredAt = true, IsAsyncImageInputReferenceTask(ctx), time.Now().UTC()
	if !AsyncImageRetryFailure(ctx, fact) {
		return false
	}
	fact = SanitizeAsyncImageRetryFact(fact)
	RecordAsyncImageAccountAttempt(ctx, AsyncImageAccountAttempt{
		AccountID: account.ID, AccountName: account.Name, Status: AsyncImageAccountAttemptFailed,
		StatusCode: fact.HTTPStatus, ProviderErrorCode: fact.ErrorCode, Error: "generation failure handled by retry policy", FailureReason: fact.Category,
	})
	return true
}

func isAsyncImageRetryInterruption(err error) bool {
	return errors.Is(err, ErrAsyncImageRetryHandled) || errors.Is(err, ErrAsyncImageRetryDispatchBlocked) || errors.Is(err, ErrAsyncImageAttemptBudgetExhausted)
}

// The Gemini task gateway can select mixed-scheduling compatibility accounts.
// Route those adapters through the same dispatch and failure boundaries.
type asyncImageRetryUpstream struct {
	HTTPUpstream
	ctx     context.Context
	account *Account
}

func withAsyncImageRetryUpstream(ctx context.Context, account *Account, upstream HTTPUpstream) HTTPUpstream {
	if asyncImageRetryHooks(ctx).BeforeDispatch == nil {
		return upstream
	}
	return asyncImageRetryUpstream{HTTPUpstream: upstream, ctx: ctx, account: account}
}

func (u asyncImageRetryUpstream) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	return u.send(func() (*http.Response, error) {
		return u.HTTPUpstream.Do(req, proxyURL, accountID, concurrency)
	})
}

func (u asyncImageRetryUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.send(func() (*http.Response, error) {
		return u.HTTPUpstream.DoWithTLS(req, proxyURL, accountID, concurrency, profile)
	})
}

func (u asyncImageRetryUpstream) send(send func() (*http.Response, error)) (*http.Response, error) {
	if err := BeginAsyncImageUpstreamAttempt(u.ctx, u.account.ID); err != nil {
		return nil, err
	}
	resp, err := send()
	if err != nil || resp == nil {
		if handleAsyncImageRetryTransportError(u.ctx, u.account) {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			return nil, ErrAsyncImageRetryHandled
		}
		return resp, err
	}
	if resp.StatusCode < 400 || resp.Body == nil {
		return resp, nil
	}
	ctx := trackAsyncImageErrorResponse(u.ctx, resp)
	originalBody := resp.Body
	body, err := io.ReadAll(io.LimitReader(originalBody, 1<<20))
	if handleAsyncImageRetryHTTPError(ctx, u.account, resp.StatusCode, body) {
		_ = originalBody.Close()
		return nil, ErrAsyncImageRetryHandled
	}
	if err != nil {
		_ = originalBody.Close()
		return nil, err
	}
	resp.Body = asyncImageRetryReplayBody{Reader: io.MultiReader(bytes.NewReader(body), originalBody), Closer: originalBody}
	return resp, nil
}

type asyncImageRetryReplayBody struct {
	io.Reader
	io.Closer
}
