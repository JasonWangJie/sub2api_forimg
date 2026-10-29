package handler

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type unavailableImageQueue struct {
	service.AsyncImageQueue
	calls atomic.Int64
}

func (q *unavailableImageQueue) Reserve(ctx context.Context, _ time.Duration) (*service.AsyncImageQueueReservation, error) {
	q.calls.Add(1)
	return nil, errors.New("redis unavailable")
}

func TestAsyncImageQueueOutageBacksOffAndCancels(t *testing.T) {
	q := &unavailableImageQueue{}
	h := &DurableAsyncImageHandler{queue: q}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	h.asyncImageWorkerLoop(ctx, 0)
	require.Equal(t, int64(1), q.calls.Load(), "a rapid Redis refusal must not spin")
	for n := 1; n < 20; n++ {
		d := asyncImageQueueErrorDelay(n)
		require.GreaterOrEqual(t, d, 200*time.Millisecond)
		require.LessOrEqual(t, d, 30*time.Second)
	}
}

func TestAsyncImageDrainKeepsActiveExecutionAlive(t *testing.T) {
	execution, cancel := context.WithCancel(context.Background())
	defer cancel()
	intake, stopIntake := context.WithCancel(execution)
	h := &DurableAsyncImageHandler{stop: cancel, intakeCtx: intake, stopIntake: stopIntake}
	h.runtimeWG.Add(1)
	h.BeginDrain()
	require.ErrorIs(t, intake.Err(), context.Canceled)
	require.NoError(t, execution.Err(), "drain must preserve an already sent request")
	done := make(chan error, 1)
	go func() { done <- h.Shutdown(context.Background()) }()
	select {
	case <-done:
		t.Fatal("returned before active task completed")
	case <-time.After(15 * time.Millisecond):
	}
	h.runtimeWG.Done()
	require.NoError(t, <-done)
	require.ErrorIs(t, execution.Err(), context.Canceled)
}

func TestAsyncImageDrainDeadlineCancelsExecution(t *testing.T) {
	execution, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &DurableAsyncImageHandler{stop: cancel}
	h.runtimeWG.Add(1)
	ctx, stop := context.WithCancel(context.Background())
	stop()
	require.ErrorIs(t, h.Shutdown(ctx), context.Canceled)
	require.ErrorIs(t, execution.Err(), context.Canceled)
	h.runtimeWG.Done()
}

func TestAsyncImageDrainPreventsSubsequentStart(t *testing.T) {
	// Dependencies are intentionally uninitialized: Start must return at the
	// drain gate before reading settings or launching any background loops.
	h := &DurableAsyncImageHandler{tasks: &service.AsyncImageTaskService{}, queue: &unavailableImageQueue{}, storage: &service.ImageStorageSettingService{}}
	h.BeginDrain()
	h.Start()
	require.True(t, h.draining.Load())
	require.Nil(t, h.stop)
	require.NoError(t, h.Shutdown(context.Background()))
}

func TestAsyncImageDurableAttemptBudgetAndTaskDeadline(t *testing.T) {
	task := &service.AsyncImageTask{SubmittedAt: time.Now().Add(-31 * time.Minute)}
	require.True(t, asyncImageTaskDeadline(task).Before(time.Now()))
	var attempts []service.AsyncImageAccountAttempt
	for i := 0; i < 16; i++ {
		attempts = append(attempts, service.AsyncImageAccountAttempt{AccountID: 1, Status: "invocation_started"})
	}
	task.AccountAttempts, _ = json.Marshal(attempts)
	require.Zero(t, asyncImageRemainingUpstreamAttempts(task))
	task.AccountAttempts = json.RawMessage(`[{"account_id":1,"status":"selected"},{"account_id":1,"status":"failed"},{"account_id":2,"status":"selected"}]`)
	require.Equal(t, 14, asyncImageRemainingUpstreamAttempts(task))
	task.AccountAttempts = json.RawMessage(`[{"account_id":1,"status":"selected"},{"account_id":1,"status":"failed"},{"account_id":2,"status":"selected"},{"account_id":2,"status":"invocation_started"},{"account_id":2,"status":"invocation_started"}]`)
	require.Equal(t, 13, asyncImageRemainingUpstreamAttempts(task), "new instrumentation must not erase legacy attempts")
}
