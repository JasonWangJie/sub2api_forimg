package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	ImageCircuitScopeSync  = "sync"
	ImageCircuitScopeAsync = "async"
)

// ImageAccountCircuitBreaker tracks consecutive account-level image failures.
// Implementations must be safe for concurrent requests and may fail open when
// their backing store is unavailable.
type ImageAccountCircuitBreaker interface {
	IsOpen(context.Context, int64, string) (bool, error)
	RecordFailure(context.Context, int64, string) (bool, error)
	RecordSuccess(context.Context, int64, string) error
}

type ImageCircuitBreakerSettingsProvider interface {
	RuntimeConfig(context.Context) (AsyncImageRuntimeConfig, error)
}

// ImageCircuitScopeFromContext identifies image requests without making
// ordinary text traffic participate in the account circuit.
func ImageCircuitScopeFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	if AsyncImageAccountAttemptCaptureFromContext(ctx) != nil || IsGeminiAsyncImageGeneration(ctx) {
		return imageCircuitRouteScope(ctx, ImageCircuitScopeAsync), true
	}
	if OpenAIImageGenerationIntentFromContext(ctx) {
		return imageCircuitRouteScope(ctx, ImageCircuitScopeSync), true
	}
	if GeminiImageGenerationIntentFromContext(ctx) {
		return imageCircuitRouteScope(ctx, ImageCircuitScopeSync), true
	}
	return "", false
}

func imageCircuitRouteScope(ctx context.Context, scope string) string {
	if route, ok := ImageAccountPoolRouteFromContext(ctx); ok {
		sum := sha256.Sum256([]byte(route.Model + "\x00" + route.SizeTier))
		return fmt.Sprintf("%s:route:%x", scope, sum[:12])
	}
	return scope
}

type imageCircuitIdentity struct {
	id    int64
	scope string
}
type imageCircuitIdentityKey struct{}

func withImageCircuitIdentities(ctx context.Context) context.Context {
	if _, ok := ctx.Value(imageCircuitIdentityKey{}).(*sync.Map); ok {
		return ctx
	}
	return context.WithValue(ctx, imageCircuitIdentityKey{}, &sync.Map{})
}

// ImageCircuitIdentity supports explicit administrator grouping of accounts that
// share upstream capacity. The group name is hashed and never enters telemetry.
func ImageCircuitIdentity(ctx context.Context, account *Account) (int64, string, bool) {
	scope, ok := ImageCircuitScopeFromContext(ctx)
	if !ok || account == nil {
		return 0, "", false
	}
	identity := imageCircuitIdentity{id: account.ID, scope: scope}
	if registry, exists := ctx.Value(imageCircuitIdentityKey{}).(*sync.Map); exists {
		if group, valid := account.Extra["image_health_group"].(string); valid && group != "" {
			sum := sha256.Sum256([]byte(group))
			identity.id = 1
			identity.scope = fmt.Sprintf("%s:group:%x", scope, sum[:12])
		}
		registry.Store(account.ID, identity)
	}
	return identity.id, identity.scope, true
}

// Optional interfaces keep existing circuit implementations compatible.
type ImageCircuitProbeController interface {
	AcquireProbe(context.Context, int64, string) (string, bool, error)
	FinishProbe(context.Context, int64, string, string, bool) error
}
type ImageCircuitProbeResultController interface {
	FinishProbeResult(context.Context, int64, string, string, bool, bool) error
}
type ImageCircuitProbeRenewer interface {
	RenewProbe(context.Context, int64, string, string) (bool, error)
}
type ImageCircuitCapabilityRecorder interface {
	RecordCapabilityFailure(context.Context, int64, string) error
}

type imageCircuitAttemptOwnerKey struct{}
type imageCircuitAttemptOwner struct {
	accountID int64
	finish    func(*http.Response, error)
}

// OAuth task recovery is an internal retry of the same invocation. Reuse its
// probe instead of competing with the lease already held by the outer call.
func withImageCircuitAttemptOwner(ctx context.Context, accountID int64, finish func(*http.Response, error)) context.Context {
	return context.WithValue(ctx, imageCircuitAttemptOwnerKey{}, imageCircuitAttemptOwner{accountID: accountID, finish: finish})
}

func startImageCircuitProbeRenewal(ctx context.Context, renewer ImageCircuitProbeRenewer, id int64, scope, token string, interval time.Duration) func() {
	if renewer == nil || token == "" {
		return func() {}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				// Caller cancellation must not expire a synchronous generation
				// deliberately detached from the client connection.
				renewCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
				owned, err := renewer.RenewProbe(renewCtx, id, scope, token)
				cancel()
				if err == nil && !owned {
					return
				}
			}
		}
	}()
	return func() {
		once.Do(func() { close(stop) })
		<-done
	}
}

func IsImageAccountCircuitOpen(ctx context.Context, breaker ImageAccountCircuitBreaker, account *Account) bool {
	if !ImageAccountSupportsInput(ctx, account) {
		return true
	}
	if breaker == nil {
		return false
	}
	id, scope, ok := ImageCircuitIdentity(ctx, account)
	if !ok {
		return false
	}
	open, err := breaker.IsOpen(ctx, id, scope)
	return err == nil && open
}

// AcquireImageCircuitAttempt reserves at most one half-open probe at the actual
// invocation boundary. Merely scanning candidate accounts never consumes a probe.
func AcquireImageCircuitAttempt(ctx context.Context, breaker ImageAccountCircuitBreaker, account *Account) (func(*http.Response, error), error) {
	noop := func(*http.Response, error) {}
	if !ImageAccountSupportsInput(ctx, account) {
		return noop, &UpstreamFailoverError{StatusCode: 503, Scope: GatewayFailureScopeRequest, RequestScopedTransient: true, NextAccountAction: NextAccountRetry, ResponseBody: []byte(`{"error":{"code":"image_input_capability_mismatch","message":"Channel input capability does not match this request"}}`)}
	}
	if owner, ok := ctx.Value(imageCircuitAttemptOwnerKey{}).(imageCircuitAttemptOwner); ok && owner.accountID == account.ID && owner.finish != nil {
		return owner.finish, nil
	}
	probe, ok := breaker.(ImageCircuitProbeController)
	if !ok {
		return noop, nil
	}
	id, scope, image := ImageCircuitIdentity(ctx, account)
	if !image {
		return noop, nil
	}
	token, allowed, err := probe.AcquireProbe(ctx, id, scope)
	if err != nil {
		return noop, nil
	} // Existing fail-open behavior for Redis outages.
	if !allowed {
		return noop, &UpstreamFailoverError{StatusCode: 503, Scope: GatewayFailureScopeRequest, RequestScopedTransient: true, NextAccountAction: NextAccountRetry, ResponseBody: []byte(`{"error":{"code":"image_channel_cooling","message":"Image channel is cooling down"}}`)}
	}
	stopRenewal := func() {}
	if renewer, ok := probe.(ImageCircuitProbeRenewer); ok && token != "" {
		stopRenewal = startImageCircuitProbeRenewal(ctx, renewer, id, scope, token, 10*time.Second)
	}
	var once sync.Once
	return func(resp *http.Response, err error) {
		once.Do(func() {
			stopRenewal()
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			success := err == nil && resp != nil && resp.StatusCode < 400
			healthFailure := false
			if !success && ctx.Err() == nil && !errors.Is(err, ErrAsyncImageAttemptBudgetExhausted) {
				healthFailure = IsImageAccountCircuitFailure(err)
				if resp != nil {
					healthFailure = healthFailure || IsImageAccountCircuitFailure(&OpenAIImagesUpstreamError{StatusCode: resp.StatusCode})
				}
			}
			if controller, ok := probe.(ImageCircuitProbeResultController); ok {
				_ = controller.FinishProbeResult(finishCtx, id, scope, token, success, healthFailure)
			} else {
				_ = probe.FinishProbe(finishCtx, id, scope, token, success)
			}
		})
	}, nil
}

// finishImageCircuitAttempt runs after response parsing and any same-account
// retries finish. A 200 header alone is not a successful generation result.
func finishImageCircuitAttempt(done func(*http.Response, error), success bool, response *http.Response, err error) {
	if done == nil {
		return
	}
	if !success && err == nil {
		err = errors.New("image invocation did not produce a successful result")
	}
	done(response, err)
}

func ReportImageAccountResult(ctx context.Context, breaker ImageAccountCircuitBreaker, accountID int64, success bool, err error) {
	if breaker == nil || accountID <= 0 {
		return
	}
	scope, ok := ImageCircuitScopeFromContext(ctx)
	if !ok {
		return
	}
	if registry, exists := ctx.Value(imageCircuitIdentityKey{}).(*sync.Map); exists {
		if value, found := registry.Load(accountID); found {
			identity := value.(imageCircuitIdentity)
			accountID, scope = identity.id, identity.scope
		}
	}
	// Gateway handlers release the per-account timeout before reporting. Keep
	// its route/group values, but give the health write its own bounded lifetime.
	reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if success {
		_ = breaker.RecordSuccess(reportCtx, accountID, scope)
		return
	}
	if err == nil {
		return
	}
	var rejection *UpstreamFailoverError
	if errors.As(err, &rejection) && rejection.IsAsyncImageSizeCapabilityMismatch() {
		route, hasRoute := ImageAccountPoolRouteFromContext(ctx)
		if recorder, ok := breaker.(ImageCircuitCapabilityRecorder); ok && hasRoute && route.Model != "" && route.SizeTier != "" {
			_ = recorder.RecordCapabilityFailure(reportCtx, accountID, scope)
		}
		return
	}
	if IsImageAccountCircuitFailure(err) {
		_, _ = breaker.RecordFailure(reportCtx, accountID, scope)
	}
}

// IsImageAccountCircuitFailure excludes request/content/storage errors from
// account health while retaining authentication, throttling and upstream
// availability failures.
func IsImageAccountCircuitFailure(err error) bool {
	if err == nil {
		return false
	}
	var upstream *UpstreamFailoverError
	if errors.As(err, &upstream) {
		if upstream.IsAsyncImageServiceTimeout400() || upstream.IsAsyncImageInternalError422() || upstream.Reason == AsyncImageCapacityRejectionReason {
			return upstream.Scope != GatewayFailureScopeRequest
		}
		if upstream.StatusCode == http.StatusRequestTimeout || upstream.StatusCode == http.StatusTooManyRequests ||
			upstream.StatusCode == http.StatusUnauthorized || upstream.StatusCode == http.StatusForbidden ||
			upstream.StatusCode >= 500 {
			return !upstream.RequestScopedTransient && upstream.Scope != GatewayFailureScopeRequest
		}
		return false
	}
	var images *OpenAIImagesUpstreamError
	if errors.As(err, &images) {
		return images.StatusCode == http.StatusRequestTimeout || images.StatusCode == http.StatusTooManyRequests ||
			images.StatusCode == http.StatusUnauthorized || images.StatusCode == http.StatusForbidden || images.StatusCode >= 500
	}
	return false
}
