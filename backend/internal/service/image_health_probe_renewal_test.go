package service

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type imageProbeRenewalObservation struct {
	err      error
	deadline time.Time
	bounded  bool
}
type imageProbeLeaseRenewer struct {
	owned atomic.Bool
	calls chan imageProbeRenewalObservation
}

func (r *imageProbeLeaseRenewer) RenewProbe(ctx context.Context, _ int64, _, _ string) (bool, error) {
	deadline, bounded := ctx.Deadline()
	select {
	case r.calls <- imageProbeRenewalObservation{err: ctx.Err(), deadline: deadline, bounded: bounded}:
	default:
	}
	return r.owned.Load(), nil
}

func TestImageHealthProbeRenewalSurvivesClientCancellationAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	renewer := &imageProbeLeaseRenewer{calls: make(chan imageProbeRenewalObservation, 10)}
	renewer.owned.Store(true)
	stop := startImageCircuitProbeRenewal(ctx, renewer, 1, "sync", "owner", 5*time.Millisecond)
	defer stop()
	select {
	case observed := <-renewer.calls:
		require.NoError(t, observed.err)
		require.True(t, observed.bounded)
		require.WithinDuration(t, time.Now().Add(2*time.Second), observed.deadline, time.Second)
	case <-time.After(time.Second):
		t.Fatal("active probe was not renewed")
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); stop() }()
	}
	wg.Wait()
	for len(renewer.calls) > 0 {
		<-renewer.calls
	}
	select {
	case <-renewer.calls:
		t.Fatal("completed probe kept renewing")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestImageHealthProbeRenewalStopsAfterOwnershipLost(t *testing.T) {
	renewer := &imageProbeLeaseRenewer{calls: make(chan imageProbeRenewalObservation, 10)}
	stop := startImageCircuitProbeRenewal(context.Background(), renewer, 1, "sync", "stale-owner", 5*time.Millisecond)
	defer stop()
	select {
	case <-renewer.calls:
	case <-time.After(time.Second):
		t.Fatal("probe ownership was not checked")
	}
	select {
	case <-renewer.calls:
		t.Fatal("lost probe lease must not be renewed again")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestImageHealthOAuthRecoveryReusesProbeOwnership(t *testing.T) {
	ctx := WithOpenAIImageGenerationIntent(context.Background())
	account := &Account{ID: 9}
	probe := &imageProbeResultRecorder{imageHealthContextRecorder: imageHealthContextRecorder{t: t}}
	outerDone, err := AcquireImageCircuitAttempt(ctx, probe, account)
	require.NoError(t, err)
	recoveryCtx := withImageCircuitAttemptOwner(ctx, account.ID, outerDone)
	innerDone, err := AcquireImageCircuitAttempt(recoveryCtx, probe, account)
	require.NoError(t, err)
	require.Equal(t, 1, probe.acquireCount, "same-account recovery must not compete with its own lease")
	finishImageCircuitAttempt(innerDone, true, &http.Response{StatusCode: 200}, nil)
	finishImageCircuitAttempt(outerDone, false, &http.Response{StatusCode: 401}, nil)
	require.Equal(t, 1, probe.finishCount)
	require.True(t, probe.success, "outer pre-recovery response must not overwrite recovered success")
}
