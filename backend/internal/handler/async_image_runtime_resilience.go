package handler

import (
	"context"
	"encoding/json"
	"math/rand"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

// BeginDrain stops intake and maintenance before dependencies are shut down.
// The execution context and heartbeats remain live for reservations in flight.
func (h *DurableAsyncImageHandler) BeginDrain() {
	if h == nil {
		return
	}
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()
	if h.draining.Swap(true) {
		return
	}
	if h.stopIntake != nil {
		h.stopIntake()
	}
	logger.L().Info("async_image.drain_started", zap.Int64("active_workers", h.activeWorkers.Load()))
}

func (h *DurableAsyncImageHandler) Shutdown(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.BeginDrain()
	h.drainWaitOnce.Do(func() {
		h.drainDone = make(chan struct{})
		go func() { h.runtimeWG.Wait(); close(h.drainDone) }()
	})
	select {
	case <-h.drainDone:
		h.stopOnce.Do(func() {
			if h.stop != nil {
				h.stop()
			}
		})
		logger.L().Info("async_image.drain_completed")
		return nil
	case <-ctx.Done():
		h.stopOnce.Do(func() {
			if h.stop != nil {
				h.stop()
			}
		})
		logger.L().Warn("async_image.drain_timeout", zap.Int64("active_workers", h.activeWorkers.Load()))
		return ctx.Err()
	}
}

func (h *DurableAsyncImageHandler) asyncImageIntakeContext(fallback context.Context) context.Context {
	if h.intakeCtx != nil {
		return h.intakeCtx
	}
	return fallback
}

func asyncImageQueueErrorDelay(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	if failures > 8 {
		failures = 8
	}
	delay := 250 * time.Millisecond * time.Duration(1<<(failures-1))
	if delay > 25*time.Second {
		delay = 25 * time.Second
	}
	return time.Duration(float64(delay) * (0.8 + rand.Float64()*0.4))
}

func waitAsyncImageQueueRetry(ctx context.Context, failures int) bool {
	timer := time.NewTimer(asyncImageQueueErrorDelay(failures))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// The durable history caps real HTTP attempts across gateway and worker loops.
// Legacy rows lack invocation markers, so count selections conservatively.
func asyncImageRemainingUpstreamAttempts(task *service.AsyncImageTask) int {
	const maxAttempts = 16
	if task == nil {
		return maxAttempts
	}
	var attempts []service.AsyncImageAccountAttempt
	_ = json.Unmarshal(task.AccountAttempts, &attempts)
	started, selectedSegment := 0, false
	for _, a := range attempts {
		if a.Status == service.AsyncImageAccountAttemptSelected {
			if selectedSegment {
				started++
			}
			selectedSegment = true
		}
		if a.Status == service.AsyncImageAccountAttemptInvoked {
			started++
			selectedSegment = false
		}
	}
	if selectedSegment {
		started++
	}
	if started >= maxAttempts {
		return 0
	}
	return maxAttempts - started
}

func asyncImageTaskDeadline(task *service.AsyncImageTask) time.Time {
	if task == nil || task.SubmittedAt.IsZero() {
		return time.Now().Add(30 * time.Minute)
	}
	return task.SubmittedAt.Add(30 * time.Minute)
}
