package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type imageHealthContextRecorder struct {
	t          *testing.T
	kind       string
	id         int64
	scope      string
	route      ImageAccountPoolRoute
	writeCount int
}

type imageProbeResultRecorder struct {
	imageHealthContextRecorder
	finished, success, healthFailure bool
	finishCount                      int
	acquireCount                     int
}

func (r *imageProbeResultRecorder) AcquireProbe(context.Context, int64, string) (string, bool, error) {
	r.acquireCount++
	return "owned-probe", true, nil
}
func (r *imageProbeResultRecorder) FinishProbe(context.Context, int64, string, string, bool) error {
	r.t.Fatal("result-aware completion must be used")
	return nil
}
func (r *imageProbeResultRecorder) FinishProbeResult(ctx context.Context, id int64, scope, token string, success, healthFailure bool) error {
	require.Equal(r.t, "owned-probe", token)
	require.NoError(r.t, ctx.Err())
	r.finished, r.success, r.healthFailure = true, success, healthFailure
	r.finishCount++
	return nil
}

func TestImageHealthProbeFinalResultClassification(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        int
		success       bool
		cancel        bool
		err           error
		healthFailure bool
	}{
		{name: "parsed image", status: 200, success: true},
		{name: "200 without image", status: 200},
		{name: "200 malformed body", status: 200, err: errors.New("invalid body")},
		{name: "availability", status: 503, err: errors.New("upstream failed"), healthFailure: true},
		{name: "capacity 400", status: 400, err: &UpstreamFailoverError{StatusCode: 400, Scope: GatewayFailureScopeAccount, Reason: AsyncImageCapacityRejectionReason}, healthFailure: true},
		{name: "content 400", status: 400, err: errors.New("content rejected")},
		{name: "fixed capability", status: 422, err: &UpstreamFailoverError{StatusCode: 422, Scope: GatewayFailureScopeAccount, Reason: AsyncImageSizeCapabilityMismatchReason}},
		{name: "budget after retry", status: 503, err: ErrAsyncImageAttemptBudgetExhausted},
		{name: "canceled retry", status: 503, cancel: true, err: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(WithGeminiAsyncImageGeneration(context.Background()))
			defer cancel()
			recorder := &imageProbeResultRecorder{imageHealthContextRecorder: imageHealthContextRecorder{t: t}}
			done, err := AcquireImageCircuitAttempt(ctx, recorder, &Account{ID: 1})
			require.NoError(t, err)
			require.False(t, recorder.finished, "probe stays owned until final business result")
			if tc.cancel {
				cancel()
			}
			finishImageCircuitAttempt(done, tc.success, &http.Response{StatusCode: tc.status}, tc.err)
			require.True(t, recorder.finished)
			require.Equal(t, tc.success, recorder.success)
			require.Equal(t, tc.healthFailure, recorder.healthFailure)
			finishImageCircuitAttempt(done, false, nil, errors.New("duplicate completion"))
			require.Equal(t, 1, recorder.finishCount)
		})
	}
}

func (r *imageHealthContextRecorder) record(ctx context.Context, id int64, scope, kind string) error {
	r.t.Helper()
	require.NoError(r.t, ctx.Err(), "health persistence must survive the completed invocation's cancellation")
	deadline, ok := ctx.Deadline()
	require.True(r.t, ok, "detached health write must remain time bounded")
	require.WithinDuration(r.t, time.Now().Add(2*time.Second), deadline, time.Second)
	r.kind, r.id, r.scope = kind, id, scope
	r.route, _ = ImageAccountPoolRouteFromContext(ctx)
	r.writeCount++
	return nil
}

func (r *imageHealthContextRecorder) IsOpen(context.Context, int64, string) (bool, error) {
	return false, nil
}
func (r *imageHealthContextRecorder) RecordFailure(ctx context.Context, id int64, scope string) (bool, error) {
	return false, r.record(ctx, id, scope, "failure")
}
func (r *imageHealthContextRecorder) RecordSuccess(ctx context.Context, id int64, scope string) error {
	return r.record(ctx, id, scope, "success")
}
func (r *imageHealthContextRecorder) RecordCapabilityFailure(ctx context.Context, id int64, scope string) error {
	return r.record(ctx, id, scope, "capability")
}

func TestImageHealthReportSurvivesCanceledAttemptContext(t *testing.T) {
	for _, kind := range []string{"failure", "success", "capability"} {
		t.Run(kind, func(t *testing.T) {
			ctx, err := WithImageAccountPoolRoute(WithGeminiAsyncImageGeneration(context.Background()), "gemini-3-pro-image-preview", "2K")
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(ctx)
			account := &Account{ID: 17, Extra: map[string]any{"image_health_group": "provider-a"}}
			id, scope, ok := ImageCircuitIdentity(ctx, account)
			require.True(t, ok)
			cancel()
			recorder := &imageHealthContextRecorder{t: t}
			failure := &UpstreamFailoverError{StatusCode: 503, Scope: GatewayFailureScopeAccount}
			if kind == "capability" {
				failure.StatusCode = 422
				failure.Reason = AsyncImageSizeCapabilityMismatchReason
			}
			ReportImageAccountResult(ctx, recorder, account.ID, kind == "success", failure)
			require.Equal(t, 1, recorder.writeCount)
			require.Equal(t, kind, recorder.kind)
			require.Equal(t, id, recorder.id)
			require.Equal(t, scope, recorder.scope)
			require.Equal(t, ImageAccountPoolRoute{Model: "gemini-3-pro-image-preview", SizeTier: "2K"}, recorder.route)
		})
	}
}
