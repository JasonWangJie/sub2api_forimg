package service

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type imageCleanupConcurrentStop struct {
	mu      sync.Mutex
	done    chan struct{}
	status  string
	message string
	delay   time.Duration
	err     error
}

func (s *imageCleanupConcurrentStop) stop(status, message string, delay time.Duration, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	priority := func(status string) int {
		switch status {
		case "paused":
			return 3
		case "cancelled":
			return 2
		default:
			return 1
		}
	}
	if s.status == "" {
		close(s.done)
	}
	if s.status == "" || priority(status) > priority(s.status) {
		s.status, s.message, s.delay = status, message, delay
	}
	if err != nil && s.err == nil {
		s.err = err
	}
}

func (s *imageCleanupConcurrentStop) stopped() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// A single coordinator owns the job lease, claims and confirmations. Only the
// bounded object I/O runs concurrently; no remote request holds a DB transaction.
func (s *ImageResultCleanupService) executeConcurrent(ctx context.Context, job *ImageResultCleanupJob, p ImageResultCleanupPolicy, storage DurableImageStorage) error {
	end := s.now().Add(30 * time.Second)
	ctx, cancel := context.WithDeadline(ctx, end)
	defer cancel()
	stop := &imageCleanupConcurrentStop{done: make(chan struct{})}
	check := func(callCtx context.Context) error {
		if stop.stopped() {
			return context.Canceled
		}
		if err := callCtx.Err(); err != nil {
			stop.stop("pending", "", time.Second, nil)
			return err
		}
		alive, err := s.repo.HeartbeatImageResultCleanupJob(callCtx, job.ID, job.LeaseVersion, s.now().Add(2*time.Minute))
		if err != nil {
			stop.stop("pending", "", time.Second, err)
			return err
		}
		if !alive {
			stop.stop("cancelled", "", 0, nil)
			return ErrImageLibraryLeaseLost
		}
		current, _, err := s.target(callCtx)
		if err != nil || current != job.Target {
			stop.stop("paused", "存储位置已变化，请核对后恢复", 0, nil)
			return ErrImageResultCleanupConflict
		}
		if allowed, reason := s.load.allow(callCtx, s.now()); !allowed {
			stop.stop("pending", reason, 60*time.Second, nil)
			return context.Canceled
		}
		return nil
	}
	// All workers share this admission gate, including HEAD and DELETE. Hold
	// the mutex through the wait so parallel waiters cannot reserve a burst.
	var gateMu sync.Mutex
	requestAt := s.now()
	gate := func(callCtx context.Context) error {
		gateMu.Lock()
		defer gateMu.Unlock()
		if stop.stopped() {
			return context.Canceled
		}
		if err := s.wait(callCtx, requestAt.Sub(s.now())); err != nil {
			stop.stop("pending", "", time.Second, nil)
			return err
		}
		if err := check(callCtx); err != nil {
			return err
		}
		if stop.stopped() {
			return context.Canceled
		}
		requestAt = s.now().Add(time.Second / time.Duration(p.RequestsPerSecond))
		return nil
	}
	type completion struct {
		object   *ImageResultCleanupObject
		outcome  string
		retry    *time.Time
		reason   string
		deferred bool
	}
	limit := min(max(1, p.Concurrency), p.BatchSize, 16)
	results := make(chan completion, limit)
	active, claimed := 0, 0
	exhausted := false
	ctxDone, stopped := ctx.Done(), (<-chan struct{})(stop.done)
	for {
		for !stop.stopped() && !exhausted && active < limit && claimed < p.BatchSize && s.now().Before(end) {
			if err := check(ctx); err != nil {
				break
			}
			queryCtx, queryCancel := context.WithTimeout(ctx, 2*time.Second)
			object, err := s.repo.ClaimImageResultCleanupObject(queryCtx, job.ID, job.LeaseVersion, s.now().UTC())
			queryCancel()
			if err != nil {
				stop.stop("pending", "", time.Second, err)
				break
			}
			if object == nil {
				exhausted = true
				break
			}
			claimed++
			if object.Action != "delete" {
				continue
			}
			active++
			go func(object *ImageResultCleanupObject) {
				deferred := false
				workerGate := func(callCtx context.Context) error {
					err := gate(callCtx)
					deferred = deferred || err != nil
					return err
				}
				outcome, retry, reason, pause := s.deleteObject(ctx, job, object, storage, workerGate)
				if pause && !deferred {
					stop.stop("paused", reason, 0, nil)
				}
				results <- completion{object, outcome, retry, reason, deferred}
			}(object)
		}
		if active == 0 {
			break
		}
		select {
		case result := <-results:
			active--
			if result.deferred {
				// No DELETE was admitted for this object. Keep its durable ticket
				// locked for the next job lease; do not count a control stop as a
				// failed storage request or reopen references while draining.
				continue
			}
			finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			err := s.repo.CompleteImageResultCleanupObject(finishCtx, job.ID, job.LeaseVersion, result.object.ID, result.outcome, result.retry, result.reason)
			finishCancel()
			if err != nil {
				stop.stop("pending", "", time.Second, fmt.Errorf("confirm result cleanup: %w", err))
			}
		case <-ctxDone:
			stop.stop("pending", "", time.Second, nil)
			ctxDone = nil
		case <-stopped:
			stopped = nil
		}
	}
	stop.mu.Lock()
	status, message, delay, err := stop.status, stop.message, stop.delay, stop.err
	stop.mu.Unlock()
	if err != nil {
		// Do not release a lease after a failed confirmation. All dispatched
		// workers have drained; its durable tickets recover after lease expiry.
		return err
	}
	if status == "" {
		status, delay = "pending", time.Second
		if exhausted {
			status, delay = "succeeded", 0
		}
	}
	var next *time.Time
	if delay > 0 {
		v := s.now().UTC().Add(delay)
		next = &v
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	return s.repo.ReleaseImageResultCleanupJob(finishCtx, job.ID, job.LeaseVersion, status, next, message)
}
