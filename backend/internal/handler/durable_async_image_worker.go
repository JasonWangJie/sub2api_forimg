package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type asyncImageWorkerDisposition struct {
	requeue bool
	delay   time.Duration
}

type asyncImageCapturedOutput struct {
	Data        []byte
	ContentType string
	Checksum    string
	Width       int
	Height      int
}

var asyncImageExecutableStatuses = []string{
	service.AsyncImageTaskStatusInvoking,
	service.AsyncImageTaskStatusUpstreamSucceeded,
	service.AsyncImageTaskStatusUploading,
	service.AsyncImageTaskStatusStorageFailed,
	service.AsyncImageTaskStatusBillingPending,
	service.AsyncImageTaskStatusBillingFailed,
}

const (
	asyncImageCapacityRetryCode          = "capacity_retry"
	asyncImageReferenceFetchRetryCode    = "reference_fetch_retry"
	asyncImageUpstreamTransientRetryCode = "upstream_transient_retry"
)

func (h *DurableAsyncImageHandler) startRuntime(ctx context.Context) {
	cfg, err := h.storage.RuntimeConfig(ctx)
	if err != nil {
		logger.L().Error("async_image.runtime_config_load_failed", zap.Error(err))
		cfg = service.AsyncImageRuntimeConfig{WorkerConcurrency: 1, RecoveryIntervalSeconds: 30, WorkerLeaseSeconds: 120}
	}
	workers := cfg.WorkerConcurrency
	if workers <= 0 {
		workers = 1
	}
	logger.L().Info("async_image.runtime_started",
		zap.Int("worker_concurrency", workers),
		zap.Int("configured_worker_concurrency", cfg.WorkerConcurrency),
	)
	start := func(run func(context.Context)) {
		h.runtimeWG.Add(1)
		go func() {
			defer h.runtimeWG.Done()
			run(ctx)
		}()
	}
	start(h.asyncImageOutboxLoop)
	start(h.asyncImageRecoveryLoop)
	start(h.asyncImageRetentionLoop)
	for i := 0; i < workers; i++ {
		workerID := i
		start(func(workerCtx context.Context) {
			h.asyncImageWorkerLoop(workerCtx, workerID)
		})
	}
}

func (h *DurableAsyncImageHandler) asyncImageOutboxLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := h.dispatchAsyncImageOutbox(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.L().Warn("async_image.outbox_dispatch_failed", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (h *DurableAsyncImageHandler) dispatchAsyncImageOutbox(ctx context.Context) error {
	repo := h.tasks.Repository()
	if repo == nil {
		return errors.New("async image task repository is unavailable")
	}
	runtimeCfg, runtimeErr := h.storage.RuntimeConfig(ctx)
	if runtimeErr != nil {
		return runtimeErr
	}
	entries, err := repo.ClaimAsyncImageOutbox(ctx, 100, time.Now().UTC().Add(-time.Minute))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		switch entry.EventType {
		case "task_ready":
			err = h.queue.Enqueue(ctx, entry.TaskID)
			if service.IsAsyncImageQueueAlreadyQueued(err) {
				err = nil
			}
		case "library_archive":
			if !runtimeCfg.AutoArchiveToLibrary {
				message := "automatic asynchronous image library archival is disabled"
				if archiveRepo, ok := repo.(service.AsyncImageLibraryArchiveOutboxRepository); ok {
					err = archiveRepo.MarkAsyncImageOutboxTerminal(ctx, entry.ID, entry.ClaimToken, time.Now().UTC(), message)
				} else {
					err = repo.MarkAsyncImageOutboxPublished(ctx, entry.ID, entry.ClaimToken, time.Now().UTC())
				}
				if err == nil {
					logger.L().Info("async_image.library_archive_skipped", zap.String("task_id", entry.TaskID))
					continue
				}
			}
			err = h.dispatchAsyncImageLibraryArchive(ctx, entry.TaskID)
		case service.AsyncImageOutboxEventPostProcessingResume:
			err = h.queue.Enqueue(ctx, entry.TaskID)
			if service.IsAsyncImageQueueAlreadyQueued(err) {
				err = nil
			}
		default:
			err = fmt.Errorf("unsupported async image outbox event %q", entry.EventType)
		}
		if err == nil {
			if markErr := repo.MarkAsyncImageOutboxPublished(ctx, entry.ID, entry.ClaimToken, time.Now().UTC()); markErr != nil && !errors.Is(markErr, service.ErrAsyncImageOutboxClaimLost) {
				return markErr
			}
			continue
		}
		message := asyncImageSafeError(err)
		if entry.EventType == "library_archive" && !isRetryableAsyncImageLibraryArchiveError(err) {
			if archiveRepo, ok := repo.(service.AsyncImageLibraryArchiveOutboxRepository); ok {
				if markErr := archiveRepo.MarkAsyncImageOutboxTerminal(ctx, entry.ID, entry.ClaimToken, time.Now().UTC(), message); markErr != nil && !errors.Is(markErr, service.ErrAsyncImageOutboxClaimLost) {
					return markErr
				}
			} else if markErr := repo.MarkAsyncImageOutboxPublished(ctx, entry.ID, entry.ClaimToken, time.Now().UTC()); markErr != nil && !errors.Is(markErr, service.ErrAsyncImageOutboxClaimLost) {
				return markErr
			}
			logger.L().Warn("async_image.library_archive_terminal", zap.String("task_id", entry.TaskID), zap.Error(err))
			continue
		}
		if markErr := repo.MarkAsyncImageOutboxFailed(ctx, entry.ID, entry.ClaimToken, time.Now().UTC().Add(5*time.Second), message); markErr != nil && !errors.Is(markErr, service.ErrAsyncImageOutboxClaimLost) {
			return markErr
		}
	}
	return nil
}

func (h *DurableAsyncImageHandler) asyncImageRecoveryLoop(ctx context.Context) {
	for {
		cfg, err := h.storage.RuntimeConfig(ctx)
		if err != nil {
			cfg = service.AsyncImageRuntimeConfig{RecoveryIntervalSeconds: 30, WorkerLeaseSeconds: 120}
		}
		interval := time.Duration(cfg.RecoveryIntervalSeconds) * time.Second
		if interval <= 0 {
			interval = 30 * time.Second
		}
		h.recoverAsyncImageTasks(ctx, cfg)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (h *DurableAsyncImageHandler) recoverAsyncImageTasks(ctx context.Context, cfg service.AsyncImageRuntimeConfig) {
	_, _ = h.queue.MoveDueDelayedToReady(ctx, 200)
	lease := time.Duration(cfg.WorkerLeaseSeconds) * time.Second
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	_, _ = h.queue.RecoverStaleActive(ctx, lease, 200)
	repo := h.tasks.Repository()
	if repo == nil {
		return
	}
	if cfg.AutoArchiveToLibrary {
		if archiveRepo, ok := repo.(service.AsyncImageLibraryArchiveOutboxRepository); ok {
			if _, err := archiveRepo.EnqueueMissingAsyncImageLibraryArchives(ctx, 200); err != nil && ctx.Err() == nil {
				logger.L().Warn("async_image.library_archive_backfill_failed", zap.Error(err))
			}
		}
	}

	executionTimeout := asyncImageExecutionTimeout(cfg)
	startedBefore := time.Now().UTC().Add(-executionTimeout)
	if timeoutRepo, ok := repo.(service.AsyncImageInvocationTimeoutRepository); ok {
		timedOut, err := timeoutRepo.ListTimedOutInvokingAsyncImageTasks(ctx, startedBefore, 100)
		if err == nil {
			for _, task := range timedOut {
				code := "execution_timeout"
				message := asyncImageSafeError(fmt.Errorf("image generation timed out after %s", executionTimeout.Round(time.Second)))
				finished := time.Now().UTC()
				_, transitionErr := h.tasks.Transition(ctx, service.AsyncImageTaskTransition{
					TaskID: task.TaskID, ExpectedVersion: task.Version,
					FromStatuses: []string{service.AsyncImageTaskStatusInvoking},
					ToStatus:     service.AsyncImageTaskStatusFailed,
					ErrorCode:    &code, ErrorMessage: &message, FinishedAt: &finished,
					ClearRequestPayload: true, EventType: "execution_timeout",
				})
				if transitionErr != nil && !errors.Is(transitionErr, service.ErrAsyncImageInvalidTransition) {
					logger.L().Warn("async_image.mark_execution_timeout_failed", zap.String("task_id", task.TaskID), zap.Error(transitionErr))
				}
			}
		} else if ctx.Err() == nil {
			logger.L().Warn("async_image.list_execution_timeouts_failed", zap.Error(err))
		}
	}

	staleBefore := time.Now().UTC().Add(-lease)
	invoking, err := repo.ListRecoverableAsyncImageTasks(ctx, []string{service.AsyncImageTaskStatusInvoking}, staleBefore, 0, 0, 100)
	if err == nil {
		for _, task := range invoking {
			code, message, finished := "execution_unknown", "generation outcome is unknown after an interrupted upstream request", time.Now().UTC()
			_, transitionErr := h.tasks.Transition(ctx, service.AsyncImageTaskTransition{
				TaskID: task.TaskID, ExpectedVersion: task.Version,
				UpdatedBefore: &staleBefore,
				FromStatuses:  []string{service.AsyncImageTaskStatusInvoking},
				ToStatus:      service.AsyncImageTaskStatusExecutionUnknown,
				ErrorCode:     &code, ErrorMessage: &message, FinishedAt: &finished,
				ClearRequestPayload: true, EventType: "stale_invocation_detected",
			})
			if transitionErr != nil && !errors.Is(transitionErr, service.ErrAsyncImageInvalidTransition) {
				logger.L().Warn("async_image.mark_execution_unknown_failed", zap.String("task_id", task.TaskID), zap.Error(transitionErr))
			}
		}
	}

	recoverable := []string{
		service.AsyncImageTaskStatusQueued,
		service.AsyncImageTaskStatusUpstreamSucceeded,
		service.AsyncImageTaskStatusUploading,
		service.AsyncImageTaskStatusStorageFailed,
		service.AsyncImageTaskStatusBillingPending,
		service.AsyncImageTaskStatusBillingFailed,
	}
	tasks, err := repo.ListRecoverableAsyncImageTasks(
		ctx, recoverable, staleBefore,
		cfg.StorageRetryAttempts, cfg.BillingRetryAttempts, 200,
	)
	if err != nil {
		return
	}
	for _, task := range tasks {
		if enqueueErr := h.queue.Enqueue(ctx, task.TaskID); enqueueErr != nil && !service.IsAsyncImageQueueAlreadyQueued(enqueueErr) {
			logger.L().Warn("async_image.recovery_enqueue_failed", zap.String("task_id", task.TaskID), zap.Error(enqueueErr))
		}
	}
}

func (h *DurableAsyncImageHandler) asyncImageWorkerLoop(ctx context.Context, workerID int) {
	for {
		reservation, err := h.queue.Reserve(ctx, 2*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !errors.Is(err, service.ErrAsyncImageQueueEmpty) {
				logger.L().Warn("async_image.queue_reserve_failed", zap.Int("worker_id", workerID), zap.Error(err))
			}
			continue
		}
		taskID := reservation.TaskID
		processCtx, cancelProcess := context.WithCancel(ctx)
		heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
		heartbeatDone := make(chan struct{})
		go func() {
			defer close(heartbeatDone)
			h.asyncImageHeartbeatLoop(heartbeatCtx, reservation, cancelProcess)
		}()
		disposition := h.processAsyncImageTask(processCtx, taskID)
		stopHeartbeat()
		cancelProcess()
		<-heartbeatDone
		if disposition.requeue {
			if err := h.queue.RequeueAfter(ctx, reservation, disposition.delay); err != nil && !errors.Is(err, service.ErrAsyncImageQueueLeaseLost) {
				logger.L().Warn("async_image.queue_requeue_failed", zap.String("task_id", taskID), zap.Error(err))
			}
			continue
		}
		if err := h.queue.Ack(ctx, reservation); err != nil && ctx.Err() == nil && !errors.Is(err, service.ErrAsyncImageQueueLeaseLost) {
			logger.L().Warn("async_image.queue_ack_failed", zap.String("task_id", taskID), zap.Error(err))
		}
	}
}

func (h *DurableAsyncImageHandler) asyncImageHeartbeatLoop(
	ctx context.Context,
	reservation *service.AsyncImageQueueReservation,
	cancelProcess context.CancelFunc,
) {
	if reservation == nil {
		return
	}
	taskID := reservation.TaskID
	queueLeaseLost := false
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if h.cancelAsyncImageInvocationIfTimedOut(ctx, taskID, cancelProcess) {
				return
			}
			if !queueLeaseLost {
				err := h.queue.Heartbeat(ctx, reservation)
				if errors.Is(err, service.ErrAsyncImageQueueLeaseLost) {
					if h.asyncImageInvocationCanOutliveQueueLease(ctx, taskID) {
						queueLeaseLost = true
						logger.L().Warn("async_image.queue_lease_lost_invocation_continues", zap.String("task_id", taskID))
					} else {
						if cancelProcess != nil {
							cancelProcess()
						}
						return
					}
				} else if err != nil {
					logger.L().Warn("async_image.queue_heartbeat_failed", zap.String("task_id", taskID), zap.Error(err))
				}
			}
			if h != nil && h.tasks != nil && h.tasks.Repository() != nil {
				if err := h.tasks.Repository().TouchAsyncImageTask(ctx, taskID, asyncImageExecutableStatuses); err != nil {
					logger.L().Warn("async_image.task_heartbeat_failed", zap.String("task_id", taskID), zap.Error(err))
				}
			}
		}
	}
}

func (h *DurableAsyncImageHandler) cancelAsyncImageInvocationIfTimedOut(
	ctx context.Context,
	taskID string,
	cancelProcess context.CancelFunc,
) bool {
	if h == nil || h.tasks == nil || h.tasks.Repository() == nil || cancelProcess == nil {
		return false
	}
	task, err := h.tasks.Repository().GetAsyncImageTaskByTaskID(ctx, taskID)
	if err != nil || task == nil || task.Status != service.AsyncImageTaskStatusInvoking {
		return false
	}
	cfg, cfgErr := h.storage.RuntimeConfig(ctx)
	if cfgErr != nil {
		cfg = service.AsyncImageRuntimeConfig{}
	}
	if !asyncImageInvocationTimedOut(task, asyncImageExecutionTimeout(cfg), time.Now().UTC()) {
		return false
	}
	logger.L().Warn("async_image.execution_timeout_cancel",
		zap.String("task_id", taskID),
		zap.Duration("timeout", asyncImageExecutionTimeout(cfg)),
	)
	cancelProcess()
	return true
}

func asyncImageExecutionTimeout(cfg service.AsyncImageRuntimeConfig) time.Duration {
	if cfg.ExecutionTimeoutSeconds > 0 {
		return time.Duration(cfg.ExecutionTimeoutSeconds) * time.Second
	}
	return 20 * time.Minute
}

func asyncImageInvocationTimedOut(task *service.AsyncImageTask, timeout time.Duration, now time.Time) bool {
	if task == nil || timeout <= 0 {
		return false
	}
	start := task.StartedAt
	if start == nil || start.IsZero() {
		if task.CreatedAt.IsZero() {
			return false
		}
		created := task.CreatedAt
		start = &created
	}
	return !start.After(now.Add(-timeout))
}

func (h *DurableAsyncImageHandler) asyncImageInvocationCanOutliveQueueLease(ctx context.Context, taskID string) bool {
	if h == nil || h.tasks == nil || h.tasks.Repository() == nil {
		return false
	}
	task, err := h.tasks.Repository().GetAsyncImageTaskByTaskID(ctx, taskID)
	return err == nil && task != nil && task.Status == service.AsyncImageTaskStatusInvoking
}

func (h *DurableAsyncImageHandler) processAsyncImageTask(parent context.Context, taskID string) asyncImageWorkerDisposition {
	repo := h.tasks.Repository()
	if repo == nil {
		return asyncImageWorkerDisposition{requeue: true, delay: 10 * time.Second}
	}
	task, err := repo.GetAsyncImageTaskByTaskID(parent, taskID)
	if err != nil {
		return asyncImageWorkerDisposition{}
	}
	cfg, err := h.storage.RuntimeConfig(parent)
	if err != nil {
		return asyncImageWorkerDisposition{requeue: true, delay: 10 * time.Second}
	}
	switch task.Status {
	case service.AsyncImageTaskStatusQueued:
		startedAt, progress := time.Now().UTC(), 10
		var referenceTransport *string
		if task.ReferenceTransport == nil {
			mode := asyncImageReferenceTransport(task, cfg)
			referenceTransport = &mode
		}
		task, err = h.tasks.Transition(parent, service.AsyncImageTaskTransition{
			TaskID: task.TaskID, ExpectedVersion: task.Version,
			FromStatuses: []string{service.AsyncImageTaskStatusQueued},
			ToStatus:     service.AsyncImageTaskStatusInvoking,
			Progress:     &progress, StartedAt: &startedAt, ReferenceTransport: referenceTransport,
			EventType: "invocation_started",
		})
		if err != nil {
			return asyncImageWorkerDisposition{requeue: true, delay: 3 * time.Second}
		}
		return h.invokeAsyncImageTask(parent, task, cfg)
	case service.AsyncImageTaskStatusInvoking:
		if asyncImageInvocationHeartbeatFresh(task, cfg, time.Now().UTC()) {
			// Redis delivery can be reclaimed independently from PostgreSQL. A
			// fresh database heartbeat means the original worker may still be
			// running, so leave the task untouched and let stale recovery decide
			// only after the full lease window has elapsed.
			return asyncImageWorkerDisposition{}
		}
		// Only the goroutine that successfully performed queued -> invoking may
		// invoke upstream. Observing invoking from a later delivery means the
		// prior process may have sent the request and must never be replayed.
		code, message, finished := "execution_unknown", "generation outcome is unknown after an interrupted upstream request", time.Now().UTC()
		reconciliation := "pending"
		_, _ = h.tasks.Transition(parent, service.AsyncImageTaskTransition{
			TaskID: task.TaskID, ExpectedVersion: task.Version,
			FromStatuses: []string{service.AsyncImageTaskStatusInvoking},
			ToStatus:     service.AsyncImageTaskStatusExecutionUnknown,
			ErrorCode:    &code, ErrorMessage: &message, FinishedAt: &finished,
			ClearRequestPayload: true, EventType: "duplicate_invocation_blocked", ReconciliationStatus: &reconciliation,
		})
		return asyncImageWorkerDisposition{}
	case service.AsyncImageTaskStatusUpstreamSucceeded, service.AsyncImageTaskStatusUploading,
		service.AsyncImageTaskStatusStorageFailed, service.AsyncImageTaskStatusBillingPending,
		service.AsyncImageTaskStatusBillingFailed:
		return h.postProcessAsyncImageTask(parent, task, cfg)
	case service.AsyncImageTaskStatusSucceeded:
		return asyncImageWorkerDisposition{}
	default:
		return asyncImageWorkerDisposition{}
	}
}

func asyncImageInvocationHeartbeatFresh(task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, now time.Time) bool {
	if task == nil || task.UpdatedAt.IsZero() {
		return false
	}
	lease := time.Duration(cfg.WorkerLeaseSeconds) * time.Second
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	return task.UpdatedAt.After(now.Add(-lease))
}

func (h *DurableAsyncImageHandler) invokeAsyncImageTask(parent context.Context, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig) asyncImageWorkerDisposition {
	attemptCapture := &service.AsyncImageAccountAttemptCapture{}
	parent = service.WithAsyncImageAccountAttemptCapture(parent, attemptCapture)
	parent = service.WithAsyncImageExcludedAccountIDs(parent, asyncImageRecentFailedAccountIDs(task))
	if task != nil && task.Platform == service.PlatformGemini {
		parent = service.WithAsyncImageGeminiMaxAccountSwitches(parent, cfg.GeminiAsyncMaxAccountSwitches)
	}
	storage, enabled, storageErr := h.storage.DurableStorage(parent)
	if storageErr != nil || !enabled || storage == nil {
		h.failAsyncImageTask(parent, task, "storage_unavailable", "image result storage is not configured", false)
		return asyncImageWorkerDisposition{}
	}
	apiKey, subscription, err := h.reloadAsyncImageIdentity(parent, task, true)
	if err != nil {
		h.failAsyncImageTask(parent, task, "eligibility_failed", asyncImageSafeError(err), false)
		return asyncImageWorkerDisposition{}
	}
	payload, err := h.decryptAsyncImagePayload(task.RequestPayload)
	if err != nil {
		h.failAsyncImageTask(parent, task, "request_decryption_failed", "stored image request could not be decrypted", false)
		return asyncImageWorkerDisposition{}
	}

	executionTimeout := asyncImageExecutionTimeout(cfg)
	executionCtx, cancel := context.WithTimeout(parent, executionTimeout)
	defer cancel()
	body, path, contentType, err := h.buildAsyncImageUpstreamRequest(executionCtx, task, payload, cfg, storage)
	if err != nil {
		if shouldRetryAsyncImageReferenceFetch(task, cfg, err) {
			return h.retryAsyncImageReferenceFetch(parent, task, cfg, err)
		}
		h.failAsyncImageTask(parent, task, "invalid_reference_image", asyncImageSafeError(err), false)
		return asyncImageWorkerDisposition{}
	}
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("User-Agent", "sub2api-async-image-worker/1")
	ctx := request.WithContext(executionCtx).Context()
	ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "async-image:"+task.TaskID)
	ctx = context.WithValue(ctx, ctxkey.RequestID, "async-image:"+task.TaskID)
	ctx = context.WithValue(ctx, ctxkey.UserID, apiKey.UserID)
	ctx = context.WithValue(ctx, ctxkey.Group, apiKey.Group)
	usageCapture := &AsyncImageUsageCapture{}
	ctx = withAsyncImageUsageCapture(ctx, usageCapture)
	geminiCapture := &service.GeminiImageResponseCapture{}
	if task.RequestedImageSize != nil && strings.TrimSpace(*task.RequestedImageSize) != "" {
		ctx = service.WithImageSizeAccountPoolTier(ctx, *task.RequestedImageSize)
	}
	if task.Platform == service.PlatformGemini {
		ctx = service.WithGeminiAsyncImageGeneration(ctx)
		ctx = service.WithAsyncImageGeminiMaxAccountSwitches(ctx, cfg.GeminiAsyncMaxAccountSwitches)
		if task.RequestedImageSize != nil && strings.EqualFold(strings.TrimSpace(*task.RequestedImageSize), "0.5K") {
			if !service.AsyncImageGeminiModelSupportsHalfK(cfg, task.Model) {
				h.failAsyncImageTask(parent, task, "unsupported_image_dimensions", "0.5K is no longer enabled for this Gemini model", false)
				return asyncImageWorkerDisposition{}
			}
			ctx = service.WithGeminiHalfKCapability(ctx)
		}
		ctx = service.WithGeminiImageResponseCapture(ctx, geminiCapture)
	}
	request = request.WithContext(ctx)
	ginContext.Request = request
	ginContext.Set(string(middleware.ContextKeyAPIKey), apiKey)
	ginContext.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID, Concurrency: apiKey.User.Concurrency})
	ginContext.Set(string(middleware.ContextKeyUserRole), apiKey.User.Role)
	if subscription != nil {
		ginContext.Set(string(middleware.ContextKeySubscription), subscription)
	}
	ginContext.Set(ctxKeyInboundEndpoint, NormalizeInboundEndpoint(payload.SourcePath))

	if !h.forwardAsyncImageUpstream(ginContext, task.Platform) {
		message := "OpenAI image gateway is unavailable"
		if task.Platform == service.PlatformGemini {
			message = "Gemini image gateway is unavailable"
		}
		h.failAsyncImageTask(parent, task, "gateway_unavailable", message, false)
		return asyncImageWorkerDisposition{}
	}
	if isOpsRoutingCapacityLimited(ginContext) {
		return h.deferAsyncImageForLocalCapacity(parent, task, cfg)
	}
	if executionCtx.Err() != nil {
		h.markAsyncImageExecutionUnknown(parent, task, fmt.Sprintf("image generation timed out after %s; upstream reconciliation pending", executionTimeout.Round(time.Second)))
		return asyncImageWorkerDisposition{}
	}
	if recorder.Code < http.StatusOK || recorder.Code >= http.StatusMultipleChoices {
		message := formatAsyncImageUpstreamFailure(recorder.Code, recorder.Body.Bytes())
		retryAfter := service.ParseAsyncImageRetryAfter(recorder.Header().Get("Retry-After"), time.Now())
		logger.L().Warn("async_image.upstream_failed",
			zap.String("task_id", task.TaskID),
			zap.String("platform", task.Platform),
			zap.String("model", task.Model),
			zap.Int("status_code", recorder.Code),
			zap.String("message", message),
		)
		if isAsyncImageAmbiguousUpstreamFailure(recorder.Code, message) {
			h.markAsyncImageExecutionUnknown(parent, task, message)
			return asyncImageWorkerDisposition{}
		}
		if shouldRetryAsyncImageUpstreamReferenceFetch(task, cfg, recorder.Code, message) {
			return h.retryAsyncImageUpstreamReferenceFetch(parent, task, cfg, message, retryAfter)
		}
		if isAsyncImageCapacityFailure(recorder.Code, message) {
			if shouldRetryAsyncImageCapacity(task, cfg, recorder.Code, message) {
				return h.retryAsyncImageCapacity(parent, task, cfg, message, retryAfter, "upstream_capacity")
			}
			h.failAsyncImageTask(parent, task, "upstream_capacity_exhausted", message, false)
			return asyncImageWorkerDisposition{}
		}
		if shouldRetryAsyncImageUpstreamTransient(task, cfg, recorder.Code, message) {
			return h.retryAsyncImageUpstreamTransient(parent, task, cfg, message, retryAfter)
		}
		h.failAsyncImageTask(parent, task, "upstream_failed", message, false)
		return asyncImageWorkerDisposition{}
	}

	// Must use the gin request context: it carries ClientRequestID=async-image:<task_id>
	// so PrepareRecordUsage builds client:async-image:<task_id>. executionCtx alone
	// only has the timeout and would fall back to the upstream UUID, failing
	// ValidatePreparedUsageBilling with "prepared usage request id mismatch".
	outputs, prepared, accountID, upstreamRequestID, actualSize, err := h.captureAsyncImageInvocation(ginContext.Request.Context(), task, recorder.Body.Bytes(), usageCapture, geminiCapture, cfg)
	if err != nil {
		h.markAsyncImageExecutionUnknown(parent, task, asyncImageSafeError(err))
		return asyncImageWorkerDisposition{}
	}
	billingPayload, err := json.Marshal(prepared)
	if err != nil {
		h.markAsyncImageExecutionUnknown(parent, task, "prepared billing command could not be persisted")
		return asyncImageWorkerDisposition{}
	}
	stagingExpiry := time.Now().UTC().Add(24 * time.Hour)
	staging := make([]service.AsyncImageStagingObject, 0, len(outputs))
	for index, output := range outputs {
		width, height := output.Width, output.Height
		staging = append(staging, service.AsyncImageStagingObject{
			TaskID: task.TaskID, ImageIndex: index, Content: output.Data,
			ContentType: output.ContentType, ByteSize: int64(len(output.Data)), Checksum: output.Checksum,
			Width: &width, Height: &height, ExpiresAt: stagingExpiry,
		})
	}
	billingRequestID := "client:async-image:" + task.TaskID
	accountAttempts, attemptedAccountIDs := asyncImageAttemptState(task, parent)
	storedTask, err := h.tasks.RecordUpstreamSuccess(parent, service.RecordAsyncImageUpstreamSuccessParams{
		TaskID: task.TaskID, ExpectedVersion: task.Version, AccountID: accountID,
		UpstreamRequestID: upstreamRequestID, ActualImageSize: actualSize,
		AccountAttempts: accountAttempts, AttemptedAccountIDs: attemptedAccountIDs,
		ImageCount: len(staging), BillingRequestID: billingRequestID,
		BillingPayload: billingPayload, StagingObjects: staging,
		UpstreamSucceededAt: time.Now().UTC(),
		EventPayload:        json.RawMessage(fmt.Sprintf(`{"image_count":%d}`, len(staging))),
	})
	if err != nil {
		logger.L().Error("async_image.upstream_result_persist_failed", zap.String("task_id", task.TaskID), zap.Error(err))
		return asyncImageWorkerDisposition{}
	}
	return h.postProcessAsyncImageTask(parent, storedTask, cfg)
}

func (h *DurableAsyncImageHandler) deferAsyncImageForLocalCapacity(
	ctx context.Context,
	task *service.AsyncImageTask,
	cfg service.AsyncImageRuntimeConfig,
) asyncImageWorkerDisposition {
	if task == nil {
		return asyncImageWorkerDisposition{}
	}
	if !canRetryAsyncImage(task, task.CapacityRetryCount, cfg.CapacityMaxRetries, cfg.TotalMaxRetries) {
		h.failAsyncImageTask(
			ctx,
			task,
			"local_capacity_exhausted",
			fmt.Sprintf("image generation could not be scheduled after %d retries because no account capacity was available", task.CapacityRetryCount),
			false,
		)
		return asyncImageWorkerDisposition{}
	}
	return h.retryAsyncImageCapacity(ctx, task, cfg, "no local account capacity was available", 0, "local_capacity")
}

func canRetryAsyncImage(task *service.AsyncImageTask, categoryCount, categoryMax, totalMax int) bool {
	return task != nil && categoryCount < categoryMax && task.RetryCount < totalMax
}

func isRetryableAsyncImageReferenceFetchError(err error) bool {
	if err == nil {
		return false
	}
	var downloadErr *service.AsyncImageReferenceDownloadError
	if errors.As(err, &downloadErr) {
		switch downloadErr.StatusCode {
		case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
			http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable,
			http.StatusGatewayTimeout, 522, 524:
			return true
		}
		if downloadErr.StatusCode > 0 {
			return false
		}
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && (dnsErr.IsTemporary || dnsErr.IsTimeout) {
		return true
	}
	lower := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"unexpected eof", "connection reset", "connection refused", "broken pipe",
		"tls handshake timeout", "context deadline exceeded", "i/o timeout",
		"temporary failure in name resolution", "server misbehaving",
	} {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func shouldRetryAsyncImageReferenceFetch(task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, err error) bool {
	return isRetryableAsyncImageReferenceFetchError(err) &&
		canRetryAsyncImage(task, task.ReferenceRetryCount, cfg.ReferenceFetchMaxRetries, cfg.TotalMaxRetries)
}

func isAsyncImageExplicitReferenceFetchFailure(message string) bool {
	lower := strings.ToLower(message)
	for _, permanent := range []string{
		"http 401", "http 403", "http 404", "401 unauthorized", "403 forbidden", "404 not found",
		"image_too_many_pixels", "exceeds the configured", "invalid image", "corrupt image",
	} {
		if strings.Contains(lower, permanent) {
			return false
		}
	}
	for _, fragment := range []string{
		"image_url fetch failed", "image url fetch failed", "failed to fetch image",
		"failed to download image", "download reference image", "fetch reference image",
		"could not fetch image", "unable to fetch image", "fileuri fetch failed",
	} {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func isAsyncImageMultipartRequiredFailure(message string) bool {
	return strings.Contains(strings.ToLower(message), "requires multipart/form-data")
}

func shouldRetryAsyncImageUpstreamReferenceFetch(task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, _ int, message string) bool {
	if task == nil || task.RequestType != service.AsyncImageRequestTypeImageToImage ||
		!canRetryAsyncImage(task, task.ReferenceRetryCount, cfg.ReferenceFetchMaxRetries, cfg.TotalMaxRetries) {
		return false
	}
	mode := asyncImageReferenceTransport(task, cfg)
	if isAsyncImageMultipartRequiredFailure(message) {
		return task.Platform == service.PlatformOpenAI && mode == service.AsyncImageReferenceTransportPassthroughFallbackLocal
	}
	return mode != service.AsyncImageReferenceTransportLocal && isAsyncImageExplicitReferenceFetchFailure(message)
}

func isAsyncImageCapacityFailure(statusCode int, message string) bool {
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	lower := strings.ToLower(message)
	for _, fragment := range []string{
		"all available accounts exhausted", "errnoavailableaccounts", "errnoavailablecompactaccounts",
		"model capacity exhausted", "no account capacity", "capacity temporarily unavailable",
	} {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func shouldRetryAsyncImageCapacity(task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, statusCode int, message string) bool {
	return isAsyncImageCapacityFailure(statusCode, message) &&
		canRetryAsyncImage(task, task.CapacityRetryCount, cfg.CapacityMaxRetries, cfg.TotalMaxRetries)
}

func isAsyncImageAmbiguousUpstreamFailure(statusCode int, message string) bool {
	if statusCode != http.StatusBadGateway && statusCode != http.StatusGatewayTimeout {
		return false
	}
	lower := strings.ToLower(message)
	for _, fragment := range []string{
		"unexpected eof", " eof", "connection reset", "connection refused", "broken pipe",
		"tls handshake timeout", "context deadline exceeded", "i/o timeout",
	} {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func isAsyncImageUpstreamTransientFailure(statusCode int, message string) bool {
	switch statusCode {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
	default:
		return false
	}
	lower := strings.ToLower(message)
	for _, permanent := range []string{
		"authentication failed", "permission denied", "invalid api key", "requires multipart/form-data",
		"access forbidden", "not authorized", "invalid credential", "credentials are invalid",
		"prompt is required", "invalid request", "image_too_many_pixels", "quota exceeded",
	} {
		if strings.Contains(lower, permanent) {
			return false
		}
	}
	return true
}

func shouldRetryAsyncImageUpstreamTransient(task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, statusCode int, message string) bool {
	return isAsyncImageUpstreamTransientFailure(statusCode, message) &&
		canRetryAsyncImage(task, task.UpstreamRetryCount, cfg.UpstreamTransientMaxRetries, cfg.TotalMaxRetries)
}

var asyncImageRetryRandom = rand.Float64

func asyncImageRetryDelay(attempt, baseSeconds, maxSeconds, jitterPercent, retryAfterMaxSeconds int, retryAfter time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if baseSeconds < 1 {
		baseSeconds = 1
	}
	if maxSeconds < baseSeconds {
		maxSeconds = baseSeconds
	}
	if retryAfterMaxSeconds < 1 {
		retryAfterMaxSeconds = 1
	}
	maxRetryAfter := time.Duration(retryAfterMaxSeconds) * time.Second
	if retryAfter > 0 {
		delay := min(retryAfter, maxRetryAfter)
		if jitterPercent > 0 {
			delay += time.Duration(float64(delay) * asyncImageRetryRandom() * float64(jitterPercent) / 100)
		}
		return min(delay, maxRetryAfter)
	}
	delay := time.Duration(baseSeconds) * time.Second
	maximum := time.Duration(maxSeconds) * time.Second
	for n := 1; n < attempt && delay < maximum; n++ {
		if delay > maximum/2 {
			delay = maximum
		} else {
			delay *= 2
		}
	}
	if jitterPercent > 0 {
		factor := 1 + (asyncImageRetryRandom()*2-1)*float64(jitterPercent)/100
		delay = time.Duration(float64(delay) * factor)
	}
	if delay < time.Second {
		delay = time.Second
	}
	return min(delay, maximum)
}

func asyncImageReferenceRetryAfter(err error) time.Duration {
	var downloadErr *service.AsyncImageReferenceDownloadError
	if errors.As(err, &downloadErr) {
		return downloadErr.RetryAfter
	}
	return 0
}

func (h *DurableAsyncImageHandler) retryAsyncImageReferenceFetch(ctx context.Context, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, cause error) asyncImageWorkerDisposition {
	return h.scheduleAsyncImageReferenceRetry(ctx, task, cfg, asyncImageSafeError(cause), asyncImageReferenceRetryAfter(cause), false)
}

func (h *DurableAsyncImageHandler) retryAsyncImageUpstreamReferenceFetch(ctx context.Context, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, message string, retryAfter time.Duration) asyncImageWorkerDisposition {
	fallbackLocal := asyncImageReferenceTransport(task, cfg) == service.AsyncImageReferenceTransportPassthroughFallbackLocal
	return h.scheduleAsyncImageReferenceRetry(ctx, task, cfg, message, retryAfter, fallbackLocal)
}

func (h *DurableAsyncImageHandler) scheduleAsyncImageReferenceRetry(ctx context.Context, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, detail string, retryAfter time.Duration, fallbackLocal bool) asyncImageWorkerDisposition {
	if task == nil {
		return asyncImageWorkerDisposition{}
	}
	attempt := task.ReferenceRetryCount + 1
	code := asyncImageReferenceFetchRetryCode
	message := fmt.Sprintf("reference image fetch failed; scheduled retry %d/%d: %s", attempt, cfg.ReferenceFetchMaxRetries, asyncImageSafeError(errors.New(detail)))
	progress := 0
	transition := service.AsyncImageTaskTransition{
		TaskID: task.TaskID, ExpectedVersion: task.Version,
		FromStatuses: []string{service.AsyncImageTaskStatusInvoking}, ToStatus: service.AsyncImageTaskStatusQueued,
		Progress: &progress, ErrorCode: &code, ErrorMessage: &message,
		IncrementReferenceRetry: true, EventType: "reference_image_fetch_retry",
	}
	transition = enrichAsyncImageAttemptTransition(ctx, task, transition)
	if fallbackLocal {
		mode := service.AsyncImageReferenceTransportLocal
		transition.ReferenceTransport = &mode
	}
	transition.EventPayload, _ = json.Marshal(map[string]any{"retry": attempt, "max": cfg.ReferenceFetchMaxRetries, "fallback_local": fallbackLocal})
	if _, err := h.tasks.Transition(ctx, transition); err != nil {
		logger.L().Warn("async_image.reference_retry_transition_failed", zap.String("task_id", task.TaskID), zap.Error(err))
		return asyncImageWorkerDisposition{}
	}
	delay := asyncImageRetryDelay(
		attempt, cfg.ReferenceFetchRetryBaseSeconds, cfg.ReferenceFetchRetryMaxSeconds,
		cfg.RetryJitterPercent, cfg.RetryAfterMaxSeconds, retryAfter,
	)
	return asyncImageWorkerDisposition{requeue: true, delay: delay}
}

func (h *DurableAsyncImageHandler) retryAsyncImageCapacity(ctx context.Context, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, detail string, retryAfter time.Duration, reason string) asyncImageWorkerDisposition {
	attempt := task.CapacityRetryCount + 1
	code := asyncImageCapacityRetryCode
	message := fmt.Sprintf("image generation capacity unavailable; scheduled retry %d/%d: %s", attempt, cfg.CapacityMaxRetries, asyncImageSafeError(errors.New(detail)))
	progress := 0
	eventPayload, _ := json.Marshal(map[string]any{"retry": attempt, "max": cfg.CapacityMaxRetries, "reason": reason})
	transition := service.AsyncImageTaskTransition{
		TaskID: task.TaskID, ExpectedVersion: task.Version,
		FromStatuses: []string{service.AsyncImageTaskStatusInvoking}, ToStatus: service.AsyncImageTaskStatusQueued,
		Progress: &progress, ErrorCode: &code, ErrorMessage: &message,
		IncrementCapacityRetry: true, EventType: "capacity_retry", EventPayload: eventPayload,
	}
	if _, err := h.tasks.Transition(ctx, enrichAsyncImageAttemptTransition(ctx, task, transition)); err != nil {
		logger.L().Warn("async_image.capacity_retry_transition_failed", zap.String("task_id", task.TaskID), zap.Error(err))
		return asyncImageWorkerDisposition{}
	}
	delay := asyncImageRetryDelay(
		attempt, cfg.CapacityRetryBaseSeconds, cfg.CapacityRetryMaxSeconds,
		cfg.RetryJitterPercent, cfg.RetryAfterMaxSeconds, retryAfter,
	)
	return asyncImageWorkerDisposition{requeue: true, delay: delay}
}

func (h *DurableAsyncImageHandler) retryAsyncImageUpstreamTransient(ctx context.Context, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, detail string, retryAfter time.Duration) asyncImageWorkerDisposition {
	attempt := task.UpstreamRetryCount + 1
	code := asyncImageUpstreamTransientRetryCode
	message := fmt.Sprintf("upstream image service temporarily unavailable; scheduled retry %d/%d: %s", attempt, cfg.UpstreamTransientMaxRetries, asyncImageSafeError(errors.New(detail)))
	progress := 0
	eventPayload, _ := json.Marshal(map[string]any{"retry": attempt, "max": cfg.UpstreamTransientMaxRetries})
	transition := service.AsyncImageTaskTransition{
		TaskID: task.TaskID, ExpectedVersion: task.Version,
		FromStatuses: []string{service.AsyncImageTaskStatusInvoking}, ToStatus: service.AsyncImageTaskStatusQueued,
		Progress: &progress, ErrorCode: &code, ErrorMessage: &message,
		IncrementUpstreamRetry: true, EventType: "upstream_transient_retry", EventPayload: eventPayload,
	}
	if _, err := h.tasks.Transition(ctx, enrichAsyncImageAttemptTransition(ctx, task, transition)); err != nil {
		logger.L().Warn("async_image.upstream_retry_transition_failed", zap.String("task_id", task.TaskID), zap.Error(err))
		return asyncImageWorkerDisposition{}
	}
	delay := asyncImageRetryDelay(
		attempt, cfg.UpstreamTransientRetryBaseSeconds, cfg.UpstreamTransientRetryMaxSeconds,
		cfg.RetryJitterPercent, cfg.RetryAfterMaxSeconds, retryAfter,
	)
	return asyncImageWorkerDisposition{requeue: true, delay: delay}
}

// forwardAsyncImageUpstream keeps the image concurrency lease scoped to the
// single upstream invocation. Gemini's compatibility Chat Completions route
// has no public image gate, so the worker acquires the shared Gateway lease
// here. OpenAI delegates to Images, which already owns the same shared lease;
// acquiring again here would double-count one request and reject at limit 1.
func (h *DurableAsyncImageHandler) forwardAsyncImageUpstream(c *gin.Context, platform string) bool {
	if h == nil || c == nil {
		return false
	}
	switch platform {
	case service.PlatformGemini:
		if h.gateway == nil {
			return false
		}
		release, acquired := h.gateway.acquireGeminiImageGenerationSlot(c)
		if !acquired {
			return true
		}
		if release != nil {
			defer release()
		}
		h.gateway.ChatCompletions(c)
		return true
	case service.PlatformOpenAI:
		if h.openAI == nil {
			return false
		}
		h.openAI.Images(c)
		return true
	default:
		return false
	}
}

func (h *DurableAsyncImageHandler) buildAsyncImageUpstreamRequest(
	ctx context.Context,
	task *service.AsyncImageTask,
	payload *durableAsyncImagePayload,
	cfg service.AsyncImageRuntimeConfig,
	storage service.DurableImageStorage,
) ([]byte, string, string, error) {
	if task.Platform == service.PlatformOpenAI {
		path := EndpointImagesGenerations
		if task.RequestType == service.AsyncImageRequestTypeImageToImage {
			path = EndpointImagesEdits
		}
		contentType := payload.ContentType
		if strings.TrimSpace(contentType) == "" {
			contentType = "application/json"
		}
		if isMultipartContentType(contentType) {
			return append([]byte(nil), payload.Body...), path, contentType, nil
		}
		mode := asyncImageReferenceTransport(task, cfg)
		downloadRemote := mode == service.AsyncImageReferenceTransportLocal || asyncOpenAIRequestHasDataURI(payload.Body)
		body, preparedLocal, err := h.prepareAsyncOpenAIReferenceImagesForTransport(ctx, payload.Body, cfg, downloadRemote)
		if err == nil && preparedLocal && task.RequestType == service.AsyncImageRequestTypeImageToImage {
			body, contentType, err = buildAsyncOpenAIEditMultipart(body, contentType)
		}
		return body, path, contentType, err
	}
	if payload.Normalized == nil {
		return nil, "", "", errors.New("normalized Gemini image request is missing")
	}
	// Gemini HTTPS references are passed through as fileData.fileUri; upstream
	// fetches them. Local storage is only needed for data-URI validation.
	_ = storage
	downloader := service.AsyncImageReferenceDownloader{
		MaxBytes:     cfg.DownloadMaxBytes,
		MaxPixels:    cfg.DownloadMaxPixels,
		Timeout:      time.Duration(cfg.DownloadTimeoutSeconds) * time.Second,
		MaxRedirects: cfg.DownloadMaxRedirects,
		Budget: &service.AsyncImageReferenceBudget{
			MaxImages: cfg.MaxReferenceImages, MaxTotalBytes: cfg.MaxReferenceTotalBytes,
			MaxTotalPixels: cfg.MaxReferenceTotalPixels,
		},
	}
	body, err := service.BuildGeminiAsyncChatBodyWithTransport(ctx, payload.Normalized, downloader, asyncImageReferenceTransport(task, cfg))
	return body, EndpointChatCompletions, "application/json", err
}

func asyncImageReferenceTransport(task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig) string {
	if task != nil && task.ReferenceTransport != nil {
		mode := strings.ToLower(strings.TrimSpace(*task.ReferenceTransport))
		switch mode {
		case service.AsyncImageReferenceTransportPassthrough, service.AsyncImageReferenceTransportLocal, service.AsyncImageReferenceTransportPassthroughFallbackLocal:
			return mode
		}
	}
	if task != nil && task.Platform == service.PlatformGemini {
		return cfg.GeminiReferenceTransportMode
	}
	return cfg.OpenAIReferenceTransportMode
}

func asyncOpenAIRequestHasDataURI(body []byte) bool {
	return bytes.Contains(bytes.ToLower(body), []byte(`data:image/`))
}

func isMultipartContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	return err == nil && strings.EqualFold(mediaType, "multipart/form-data")
}

// OpenAI's native /images/edits contract is multipart. Async JSON compatibility
// requests are converted here after remote references have been inlined.
func buildAsyncOpenAIEditMultipart(body []byte, contentType string) ([]byte, string, error) {
	if isMultipartContentType(contentType) {
		return body, contentType, nil
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body, contentType, nil
	}
	var out bytes.Buffer
	w := multipart.NewWriter(&out)
	writeField := func(name, value string) error { return w.WriteField(name, value) }
	imageIndex := 0
	unsupportedReference := false
	writeDataImage := func(field, raw string) error {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil
		}
		if !strings.HasPrefix(strings.ToLower(raw), "data:") {
			unsupportedReference = true
			return nil
		}
		comma := strings.IndexByte(raw, ',')
		if comma < 0 {
			return fmt.Errorf("invalid data URI for %s", field)
		}
		meta := raw[:comma]
		mediaType := "image/png"
		if semi := strings.IndexByte(meta, ';'); semi > len("data:") {
			mediaType = strings.TrimPrefix(meta[:semi], "data:")
		}
		data, err := base64.StdEncoding.DecodeString(raw[comma+1:])
		if err != nil {
			return fmt.Errorf("decode %s: %w", field, err)
		}
		ext := strings.TrimPrefix(strings.ToLower(mediaType), "image/")
		if ext == "" {
			ext = "png"
		}
		part, err := w.CreateFormFile(field, fmt.Sprintf("image-%d.%s", imageIndex, ext))
		if err != nil {
			return err
		}
		imageIndex++
		_, err = part.Write(data)
		return err
	}
	var referenceValues func(any) []string
	referenceValues = func(value any) []string {
		switch item := value.(type) {
		case string:
			return []string{item}
		case []any:
			out := make([]string, 0, len(item))
			for _, child := range item {
				out = append(out, referenceValues(child)...)
			}
			return out
		case map[string]any:
			if nested, ok := item["image_url"]; ok {
				return referenceValues(nested)
			}
			if nested, ok := item["url"]; ok {
				return referenceValues(nested)
			}
		}
		return nil
	}
	addReference := func(raw string) error { return writeDataImage("image", raw) }
	for _, key := range []string{"image_urls", "images", "image"} {
		for _, raw := range referenceValues(root[key]) {
			if err := addReference(raw); err != nil {
				return nil, "", err
			}
		}
	}
	for _, raw := range referenceValues(root["mask"]) {
		if err := writeDataImage("mask", raw); err != nil {
			return nil, "", err
		}
		break
	}
	if imageIndex == 0 {
		return body, contentType, nil
	}
	if unsupportedReference {
		return body, contentType, nil
	}
	for key, value := range root {
		if key == "image" || key == "images" || key == "image_urls" || key == "mask" {
			continue
		}
		var scalar string
		switch typed := value.(type) {
		case string:
			scalar = typed
		case bool:
			scalar = strconv.FormatBool(typed)
		case float64:
			scalar = strconv.FormatFloat(typed, 'f', -1, 64)
		default:
			continue
		}
		if err := writeField(key, scalar); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return out.Bytes(), w.FormDataContentType(), nil
}

// prepareAsyncOpenAIReferenceImages is retained for tests and callers that
// explicitly request local preparation of every reference.
func (h *DurableAsyncImageHandler) prepareAsyncOpenAIReferenceImages(ctx context.Context, body []byte, cfg service.AsyncImageRuntimeConfig) ([]byte, error) {
	prepared, _, err := h.prepareAsyncOpenAIReferenceImagesForTransport(ctx, body, cfg, true)
	return prepared, err
}

func (h *DurableAsyncImageHandler) prepareAsyncOpenAIReferenceImagesForTransport(ctx context.Context, body []byte, cfg service.AsyncImageRuntimeConfig, downloadRemote bool) ([]byte, bool, error) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		// Multipart requests are already uploaded bytes and do not contain
		// provider-fetchable image_url fields in the JSON body.
		return append([]byte(nil), body...), false, nil
	}
	downloader := service.AsyncImageReferenceDownloader{
		MaxBytes: cfg.DownloadMaxBytes, MaxPixels: cfg.DownloadMaxPixels,
		Timeout:      time.Duration(cfg.DownloadTimeoutSeconds) * time.Second,
		MaxRedirects: cfg.DownloadMaxRedirects,
		Budget: &service.AsyncImageReferenceBudget{
			MaxImages: cfg.MaxReferenceImages, MaxTotalBytes: cfg.MaxReferenceTotalBytes,
			MaxTotalPixels: cfg.MaxReferenceTotalPixels,
		},
	}
	changed, err := inlineAsyncOpenAIReferencesWithTransport(ctx, &root, downloader, downloadRemote)
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return append([]byte(nil), body...), false, nil
	}
	rewritten, err := json.Marshal(root)
	if err != nil {
		return nil, false, fmt.Errorf("marshal prepared OpenAI image request: %w", err)
	}
	return rewritten, true, nil
}

func inlineAsyncOpenAIReferences(ctx context.Context, value *any, downloader service.AsyncImageReferenceDownloader) (bool, error) {
	return inlineAsyncOpenAIReferencesWithTransport(ctx, value, downloader, true)
}

func inlineAsyncOpenAIReferencesWithTransport(ctx context.Context, value *any, downloader service.AsyncImageReferenceDownloader, downloadRemote bool) (bool, error) {
	if value == nil || *value == nil {
		return false, nil
	}
	changed := false
	switch node := (*value).(type) {
	case map[string]any:
		for key, child := range node {
			var rewriteChanged bool
			var err error
			switch key {
			case "image_url":
				child, rewriteChanged, err = inlineAsyncOpenAIImageURLValue(ctx, child, downloader, downloadRemote)
			case "image_urls":
				child, rewriteChanged, err = inlineAsyncOpenAIImageURLList(ctx, child, downloader, downloadRemote)
			}
			if err != nil {
				return false, err
			}
			if rewriteChanged {
				node[key] = child
				changed = true
			}
			childChanged, err := inlineAsyncOpenAIReferencesWithTransport(ctx, &child, downloader, downloadRemote)
			if err != nil {
				return false, err
			}
			if childChanged {
				node[key] = child
				changed = true
			}
		}
	case []any:
		for i := range node {
			child := node[i]
			childChanged, err := inlineAsyncOpenAIReferencesWithTransport(ctx, &child, downloader, downloadRemote)
			if err != nil {
				return false, err
			}
			if childChanged {
				node[i] = child
				changed = true
			}
		}
	}
	return changed, nil
}

func inlineAsyncOpenAIImageURLValue(ctx context.Context, value any, downloader service.AsyncImageReferenceDownloader, downloadRemote bool) (any, bool, error) {
	switch node := value.(type) {
	case string:
		prepared, changed, err := inlineAsyncOpenAIImageURL(ctx, node, downloader, downloadRemote)
		return prepared, changed, err
	case map[string]any:
		rawURL, ok := node["url"].(string)
		if !ok {
			return value, false, nil
		}
		prepared, changed, err := inlineAsyncOpenAIImageURL(ctx, rawURL, downloader, downloadRemote)
		if err != nil || !changed {
			return value, false, err
		}
		node["url"] = prepared
		return node, true, nil
	default:
		return value, false, nil
	}
}

func inlineAsyncOpenAIImageURLList(ctx context.Context, value any, downloader service.AsyncImageReferenceDownloader, downloadRemote bool) (any, bool, error) {
	values, ok := value.([]any)
	if !ok {
		return value, false, nil
	}
	changed := false
	for i, raw := range values {
		prepared, itemChanged, err := inlineAsyncOpenAIImageURLValue(ctx, raw, downloader, downloadRemote)
		if err != nil {
			return value, false, err
		}
		if itemChanged {
			values[i] = prepared
			changed = true
		}
	}
	return values, changed, nil
}

func inlineAsyncOpenAIImageURL(ctx context.Context, raw string, downloader service.AsyncImageReferenceDownloader, downloadRemote bool) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "https://") && !downloadRemote {
		if err := downloader.ValidatePassthroughURL(raw); err != nil {
			return "", false, fmt.Errorf("validate OpenAI reference image: %w", err)
		}
		return raw, false, nil
	}
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "data:") {
		return "", false, errors.New("OpenAI reference image must be an absolute HTTPS URL or an image data URI")
	}
	ref, err := downloader.Download(ctx, raw)
	if err != nil {
		return "", false, fmt.Errorf("download OpenAI reference image: %w", err)
	}
	return "data:" + ref.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(ref.Data), true, nil
}

func (h *DurableAsyncImageHandler) captureAsyncImageInvocation(
	ctx context.Context,
	task *service.AsyncImageTask,
	responseBody []byte,
	usageCapture *AsyncImageUsageCapture,
	geminiCapture *service.GeminiImageResponseCapture,
	cfg service.AsyncImageRuntimeConfig,
) ([]asyncImageCapturedOutput, *service.PreparedUsageBilling, int64, *string, *string, error) {
	var (
		outputs           []asyncImageCapturedOutput
		prepared          *service.PreparedUsageBilling
		accountID         int64
		upstreamRequestID string
		actualSize        string
		err               error
	)
	if task.Platform == service.PlatformGemini {
		images := geminiCapture.Images()
		for _, generated := range images {
			output, validateErr := validateGeneratedAsyncImage(generated.Data, generated.MIMEType, cfg)
			if validateErr != nil {
				return nil, nil, 0, nil, nil, validateErr
			}
			outputs = append(outputs, output)
		}
		usage := usageCapture.Gemini()
		if usage == nil || usage.Account == nil || usage.Result == nil {
			return nil, nil, 0, nil, nil, errors.New("Gemini usage capture is missing after upstream success")
		}
		applyCapturedGeminiImageDimensions(usage.Result, outputs, task.RequestedImageSize)
		usage.RequestPayloadHash = task.RequestHash
		prepared, err = h.gateway.gatewayService.PrepareRecordUsage(ctx, usage)
		accountID = usage.Account.ID
		upstreamRequestID = strings.TrimSpace(usage.Result.RequestID)
		actualSize = strings.TrimSpace(usage.Result.ImageOutputSize)
		if actualSize == "" {
			actualSize = strings.TrimSpace(usage.Result.ImageSize)
		}
	} else {
		outputs, err = extractOpenAIAsyncImageOutputs(ctx, responseBody, cfg)
		if err != nil {
			return nil, nil, 0, nil, nil, err
		}
		usage := usageCapture.OpenAI()
		if usage == nil || usage.Account == nil || usage.Result == nil {
			return nil, nil, 0, nil, nil, errors.New("OpenAI usage capture is missing after upstream success")
		}
		applyCapturedOpenAIImageDimensions(usage.Result, outputs, task.RequestedImageSize)
		usage.RequestPayloadHash = task.RequestHash
		prepared, err = h.openAI.gatewayService.PrepareRecordUsage(ctx, usage)
		accountID = usage.Account.ID
		upstreamRequestID = strings.TrimSpace(usage.Result.RequestID)
		if upstreamRequestID == "" {
			upstreamRequestID = strings.TrimSpace(usage.Result.ResponseID)
		}
		actualSize = strings.TrimSpace(usage.Result.ImageOutputSize)
		if actualSize == "" {
			actualSize = strings.TrimSpace(usage.Result.ImageSize)
		}
	}
	if err != nil {
		return nil, nil, 0, nil, nil, fmt.Errorf("prepare existing image billing: %w", err)
	}
	if len(outputs) == 0 {
		return nil, nil, 0, nil, nil, errors.New("upstream response did not contain an image")
	}
	if err := service.ValidatePreparedUsageBilling(prepared, task.TaskID, task.APIKeyID); err != nil {
		return nil, nil, 0, nil, nil, err
	}
	var upstreamRequestIDPtr, actualSizePtr *string
	if upstreamRequestID != "" {
		upstreamRequestIDPtr = &upstreamRequestID
	}
	if actualSize != "" {
		actualSizePtr = &actualSize
	}
	return outputs, prepared, accountID, upstreamRequestIDPtr, actualSizePtr, nil
}

func capturedAsyncImageOutputSizes(outputs []asyncImageCapturedOutput) []string {
	sizes := make([]string, 0, len(outputs))
	for _, output := range outputs {
		if output.Width > 0 && output.Height > 0 {
			sizes = append(sizes, fmt.Sprintf("%dx%d", output.Width, output.Height))
		}
	}
	return sizes
}

func applyCapturedGeminiImageDimensions(result *service.ForwardResult, outputs []asyncImageCapturedOutput, requestedSize *string) {
	if result == nil {
		return
	}
	if len(outputs) > 0 {
		result.ImageCount = len(outputs)
	}
	if requestedSize != nil && strings.TrimSpace(result.ImageInputSize) == "" {
		result.ImageInputSize = strings.TrimSpace(*requestedSize)
	}
	result.ImageOutputSizes = capturedAsyncImageOutputSizes(outputs)
	if len(result.ImageOutputSizes) > 0 {
		result.ImageOutputSize = result.ImageOutputSizes[0]
	}
	service.ApplyForwardImageBillingResolution(result)
}

func applyCapturedOpenAIImageDimensions(result *service.OpenAIForwardResult, outputs []asyncImageCapturedOutput, requestedSize *string) {
	if result == nil {
		return
	}
	if len(outputs) > 0 {
		result.ImageCount = len(outputs)
	}
	if requestedSize != nil && strings.TrimSpace(result.ImageInputSize) == "" {
		result.ImageInputSize = strings.TrimSpace(*requestedSize)
	}
	result.ImageOutputSizes = capturedAsyncImageOutputSizes(outputs)
	if len(result.ImageOutputSizes) > 0 {
		result.ImageOutputSize = result.ImageOutputSizes[0]
	}
	service.ApplyOpenAIImageBillingResolution(result)
}

func validateGeneratedAsyncImage(data []byte, contentType string, cfg service.AsyncImageRuntimeConfig) (asyncImageCapturedOutput, error) {
	validated, err := (service.AsyncImageReferenceDownloader{MaxBytes: cfg.DownloadMaxBytes}).ValidateBytes(data, contentType)
	if err != nil {
		return asyncImageCapturedOutput{}, fmt.Errorf("invalid generated image: %w", err)
	}
	return asyncImageCapturedOutput{
		Data: validated.Data, ContentType: validated.MIMEType, Checksum: validated.SHA256,
		Width: validated.Width, Height: validated.Height,
	}, nil
}

func extractOpenAIAsyncImageOutputs(ctx context.Context, body []byte, cfg service.AsyncImageRuntimeConfig) ([]asyncImageCapturedOutput, error) {
	var envelope struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(body), &envelope); err != nil {
		return nil, errors.New("OpenAI returned an invalid image response")
	}
	downloader := service.AsyncImageReferenceDownloader{
		MaxBytes:     cfg.DownloadMaxBytes,
		Timeout:      time.Duration(cfg.DownloadTimeoutSeconds) * time.Second,
		MaxRedirects: cfg.DownloadMaxRedirects,
	}
	outputs := make([]asyncImageCapturedOutput, 0, len(envelope.Data))
	for _, item := range envelope.Data {
		if encoded := strings.TrimSpace(item.B64JSON); encoded != "" {
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, errors.New("OpenAI returned invalid base64 image data")
			}
			output, err := validateGeneratedAsyncImage(data, http.DetectContentType(data), cfg)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, output)
			continue
		}
		if rawURL := strings.TrimSpace(item.URL); rawURL != "" {
			reference, err := downloader.Download(ctx, rawURL)
			if err != nil {
				return nil, fmt.Errorf("download OpenAI generated image: %w", err)
			}
			outputs = append(outputs, asyncImageCapturedOutput{
				Data: reference.Data, ContentType: reference.MIMEType, Checksum: reference.SHA256,
				Width: reference.Width, Height: reference.Height,
			})
		}
	}
	if len(outputs) == 0 {
		return nil, errors.New("OpenAI response did not contain a generated image")
	}
	return outputs, nil
}

func (h *DurableAsyncImageHandler) postProcessAsyncImageTask(ctx context.Context, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig) asyncImageWorkerDisposition {
	repo := h.tasks.Repository()
	if repo == nil {
		return asyncImageWorkerDisposition{requeue: true, delay: 10 * time.Second}
	}
	if task.Status == service.AsyncImageTaskStatusUpstreamSucceeded || task.Status == service.AsyncImageTaskStatusStorageFailed {
		progress := 70
		updated, err := h.tasks.Transition(ctx, service.AsyncImageTaskTransition{
			TaskID: task.TaskID, ExpectedVersion: task.Version,
			FromStatuses: []string{task.Status}, ToStatus: service.AsyncImageTaskStatusUploading,
			Progress: &progress, ClearError: true, EventType: "storage_started",
		})
		if err != nil {
			return asyncImageWorkerDisposition{requeue: true, delay: 3 * time.Second}
		}
		task = updated
	}
	if task.Status == service.AsyncImageTaskStatusUploading {
		if err := h.uploadAsyncImageStaging(ctx, task); err != nil {
			code, message := "storage_failed", asyncImageSafeError(err)
			failed, transitionErr := h.tasks.Transition(ctx, service.AsyncImageTaskTransition{
				TaskID: task.TaskID, ExpectedVersion: task.Version,
				FromStatuses: []string{service.AsyncImageTaskStatusUploading},
				ToStatus:     service.AsyncImageTaskStatusStorageFailed,
				ErrorCode:    &code, ErrorMessage: &message,
				IncrementStorageRetry: true, EventType: "storage_failed",
			})
			if transitionErr != nil {
				return asyncImageWorkerDisposition{requeue: true, delay: 5 * time.Second}
			}
			if failed.StorageRetryCount < cfg.StorageRetryAttempts {
				return asyncImageWorkerDisposition{requeue: true, delay: time.Duration(cfg.RetryBackoffSeconds) * time.Second}
			}
			return asyncImageWorkerDisposition{}
		}
		billingStatus, progress := service.AsyncImageBillingStatusApplying, 90
		updated, err := h.tasks.Transition(ctx, service.AsyncImageTaskTransition{
			TaskID: task.TaskID, ExpectedVersion: task.Version,
			FromStatuses: []string{service.AsyncImageTaskStatusUploading},
			ToStatus:     service.AsyncImageTaskStatusBillingPending,
			Progress:     &progress, BillingStatus: &billingStatus, ClearError: true, EventType: "billing_started",
		})
		if err != nil {
			return asyncImageWorkerDisposition{requeue: true, delay: 3 * time.Second}
		}
		task = updated
	}
	if task.Status == service.AsyncImageTaskStatusBillingFailed {
		billingStatus := service.AsyncImageBillingStatusApplying
		updated, err := h.tasks.Transition(ctx, service.AsyncImageTaskTransition{
			TaskID: task.TaskID, ExpectedVersion: task.Version,
			FromStatuses:  []string{service.AsyncImageTaskStatusBillingFailed},
			ToStatus:      service.AsyncImageTaskStatusBillingPending,
			BillingStatus: &billingStatus, ClearError: true, EventType: "billing_retry_started",
		})
		if err != nil {
			return asyncImageWorkerDisposition{requeue: true, delay: 3 * time.Second}
		}
		task = updated
	}
	if task.Status != service.AsyncImageTaskStatusBillingPending {
		return asyncImageWorkerDisposition{}
	}
	prepared := &service.PreparedUsageBilling{}
	if len(task.BillingPayload) == 0 || json.Unmarshal(task.BillingPayload, prepared) != nil {
		return h.recordAsyncImageBillingFailure(ctx, task, cfg, errors.New("prepared billing command is invalid"))
	}
	if err := service.ValidatePreparedUsageBilling(prepared, task.TaskID, task.APIKeyID); err != nil {
		return h.recordAsyncImageBillingFailure(ctx, task, cfg, err)
	}
	if err := h.applyAsyncImageBilling(ctx, task, prepared); err != nil {
		return h.recordAsyncImageBillingFailure(ctx, task, cfg, err)
	}
	finished, progress, billingStatus, cost := time.Now().UTC(), 100, service.AsyncImageBillingStatusSucceeded, prepared.ActualCost()
	if prepared.NotBillable {
		billingStatus = service.AsyncImageBillingStatusNotBillable
	}
	succeededTask, err := h.tasks.Transition(ctx, service.AsyncImageTaskTransition{
		TaskID: task.TaskID, ExpectedVersion: task.Version,
		FromStatuses: []string{service.AsyncImageTaskStatusBillingPending},
		ToStatus:     service.AsyncImageTaskStatusSucceeded,
		Progress:     &progress, BillingStatus: &billingStatus, ActualCost: &cost,
		FinishedAt: &finished, ClearError: true, ClearRequestPayload: true, EventType: "task_succeeded",
		AutoArchiveToLibrary: cfg.AutoArchiveToLibrary,
	})
	if err != nil {
		return asyncImageWorkerDisposition{requeue: true, delay: 3 * time.Second}
	}
	if err := repo.DeleteAsyncImageStagingObjects(ctx, task.TaskID); err != nil {
		logger.L().Warn("async_image.staging_cleanup_failed", zap.String("task_id", task.TaskID), zap.Error(err))
	}
	_ = succeededTask // Results remain in async_image_results regardless of library policy.
	return asyncImageWorkerDisposition{}
}

func (h *DurableAsyncImageHandler) dispatchAsyncImageLibraryArchive(ctx context.Context, taskID string) error {
	if h == nil || h.tasks == nil || h.tasks.Repository() == nil {
		return errors.New("async image task repository is unavailable")
	}
	task, err := h.tasks.Repository().GetAsyncImageTaskByTaskID(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Status != service.AsyncImageTaskStatusSucceeded {
		return infraerrors.New(http.StatusConflict, "ASYNC_IMAGE_NOT_ARCHIVABLE", "asynchronous image task is not ready for library archival")
	}
	return h.archiveAsyncImageTaskResults(ctx, task)
}

func isRetryableAsyncImageLibraryArchiveError(err error) bool {
	if err == nil {
		return false
	}
	type multiUnwrapper interface{ Unwrap() []error }
	var joined multiUnwrapper
	if errors.As(err, &joined) {
		for _, nested := range joined.Unwrap() {
			if isRetryableAsyncImageLibraryArchiveError(nested) {
				return true
			}
		}
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	code := infraerrors.Code(err)
	return code < http.StatusBadRequest || code >= http.StatusInternalServerError
}

func (h *DurableAsyncImageHandler) archiveAsyncImageTaskResults(ctx context.Context, task *service.AsyncImageTask) error {
	if h == nil || task == nil || task.Status != service.AsyncImageTaskStatusSucceeded {
		return nil
	}
	if h.library == nil {
		return errors.New("image library service is unavailable")
	}
	var firstErr error
	for imageIndex := 0; imageIndex < task.ImageCount; imageIndex++ {
		if _, _, err := h.library.FromTask(ctx, task.UserID, task.TaskID, imageIndex, ""); err != nil {
			firstErr = errors.Join(firstErr, fmt.Errorf("archive image %d: %w", imageIndex, err))
		}
	}
	return firstErr
}

func (h *DurableAsyncImageHandler) uploadAsyncImageStaging(ctx context.Context, task *service.AsyncImageTask) error {
	storage, enabled, err := h.storage.DurableStorage(ctx)
	if err != nil || !enabled || storage == nil {
		return errors.New("image result storage is unavailable")
	}
	settings, err := h.storage.Get(ctx)
	if err != nil {
		return err
	}
	objects, err := h.tasks.Repository().ListAsyncImageStagingObjects(ctx, task.TaskID)
	if err != nil {
		return err
	}
	if len(objects) == 0 {
		return errors.New("generated image staging data is missing")
	}
	results := make([]service.AsyncImageResult, 0, len(objects))
	prefix := strings.TrimSuffix(settings.Prefix, "/")
	intentResolver, ok := storage.(service.DurableImageStorageIntentResolver)
	if !ok {
		return errors.New("image result storage cannot persist upload intent")
	}
	partitionTime := task.SubmittedAt
	if partitionTime.IsZero() {
		partitionTime = task.CreatedAt
	}
	intentExpiry := time.Now().UTC().Add(24 * time.Hour)
	intents := make([]service.AsyncImageResultUploadIntent, 0, len(objects))
	keys := make(map[int]string, len(objects))
	for _, object := range objects {
		key := strings.TrimPrefix(fmt.Sprintf("%s/results/%s/%s/%03d%s", prefix, service.ImageObjectDatePartition(partitionTime), task.TaskID, object.ImageIndex, asyncImageExtension(object.ContentType)), "/")
		intent, intentErr := intentResolver.ObjectIntent(key, object.ContentType, object.ByteSize, object.Checksum)
		if intentErr != nil {
			return intentErr
		}
		keys[object.ImageIndex] = key
		intents = append(intents, service.AsyncImageResultUploadIntent{
			TaskID: task.TaskID, ImageIndex: object.ImageIndex,
			ObjectRef: intent, ExpiresAt: intentExpiry,
		})
	}
	if err := h.tasks.Repository().PrepareAsyncImageResultUploadIntents(ctx, task.TaskID, intents); err != nil {
		return err
	}
	for index, object := range objects {
		key := keys[object.ImageIndex]
		ref, saveErr := storage.SaveObject(ctx, key, object.ContentType, object.Content)
		if saveErr != nil {
			return saveErr
		}
		if !compatibleAsyncImageObjectRef(intents[index].ObjectRef, ref) {
			return errors.New("stored asynchronous image identity did not match its upload intent")
		}
		results = append(results, service.AsyncImageResult{
			TaskID: task.TaskID, ImageIndex: object.ImageIndex,
			Provider: ref.Provider, Bucket: ref.Bucket, ObjectKey: ref.ObjectKey,
			ContentType: object.ContentType, ByteSize: object.ByteSize, Checksum: object.Checksum,
			Width: object.Width, Height: object.Height,
		})
	}
	return h.tasks.Repository().ReplaceAsyncImageResults(ctx, task.TaskID, results)
}

func sameAsyncImageObjectRef(expected, actual service.ObjectRef) bool {
	return expected.Provider == actual.Provider && expected.Bucket == actual.Bucket &&
		expected.ObjectKey == actual.ObjectKey && expected.ContentType == actual.ContentType &&
		expected.SizeBytes == actual.SizeBytes && strings.EqualFold(expected.ChecksumSHA256, actual.ChecksumSHA256)
}

// compatibleAsyncImageObjectRef allows Superbed to finalize ObjectKey to the
// permanent public URL returned by the upload API while keeping other identity fields.
func compatibleAsyncImageObjectRef(expected, actual service.ObjectRef) bool {
	if sameAsyncImageObjectRef(expected, actual) {
		return true
	}
	if !strings.EqualFold(expected.Provider, config.ImageStorageProviderSuperbed) ||
		!strings.EqualFold(actual.Provider, config.ImageStorageProviderSuperbed) {
		return false
	}
	if expected.Bucket != actual.Bucket || expected.ContentType != actual.ContentType ||
		expected.SizeBytes != actual.SizeBytes ||
		!strings.EqualFold(expected.ChecksumSHA256, actual.ChecksumSHA256) {
		return false
	}
	return strings.HasPrefix(actual.ObjectKey, "https://") || strings.HasPrefix(actual.ObjectKey, "http://")
}

func (h *DurableAsyncImageHandler) applyAsyncImageBilling(ctx context.Context, task *service.AsyncImageTask, prepared *service.PreparedUsageBilling) error {
	if h == nil || h.apiKeys == nil || task == nil || prepared == nil {
		return errors.New("prepared asynchronous image billing context is incomplete")
	}
	apiKey, err := h.apiKeys.GetByIDForPreparedBilling(ctx, task.APIKeyID)
	if err != nil || apiKey == nil || apiKey.ID != task.APIKeyID || apiKey.UserID != task.UserID {
		return errors.New("prepared asynchronous image billing identity is unavailable")
	}
	account, err := h.accounts.GetByID(ctx, prepared.Command.AccountID)
	if err != nil {
		return err
	}
	group, err := h.apiKeys.GetGroupByID(ctx, task.GroupID)
	if err != nil || group == nil {
		return errors.New("prepared asynchronous image billing group is unavailable")
	}
	keyCopy := *apiKey
	groupID := task.GroupID
	keyCopy.GroupID = &groupID
	keyCopy.Group = group
	if keyCopy.User == nil {
		keyCopy.User = &service.User{ID: task.UserID}
	}
	billingCtx := context.WithValue(ctx, ctxkey.ClientRequestID, "async-image:"+task.TaskID)
	if task.Platform == service.PlatformGemini {
		return h.gateway.gatewayService.ApplyPreparedRecordUsage(billingCtx, prepared, &service.RecordUsageInput{
			APIKey: &keyCopy, User: keyCopy.User, Account: account, APIKeyService: h.apiKeys,
		})
	}
	return h.openAI.gatewayService.ApplyPreparedRecordUsage(billingCtx, prepared, &service.OpenAIRecordUsageInput{
		APIKey: &keyCopy, User: keyCopy.User, Account: account, APIKeyService: h.apiKeys,
	})
}

func (h *DurableAsyncImageHandler) recordAsyncImageBillingFailure(ctx context.Context, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, billingErr error) asyncImageWorkerDisposition {
	code, message, billingStatus := "billing_failed", asyncImageSafeError(billingErr), service.AsyncImageBillingStatusFailed
	failed, err := h.tasks.Transition(ctx, service.AsyncImageTaskTransition{
		TaskID: task.TaskID, ExpectedVersion: task.Version,
		FromStatuses:  []string{service.AsyncImageTaskStatusBillingPending},
		ToStatus:      service.AsyncImageTaskStatusBillingFailed,
		BillingStatus: &billingStatus, ErrorCode: &code, ErrorMessage: &message,
		IncrementBillingRetry: true, EventType: "billing_failed",
	})
	if err != nil {
		return asyncImageWorkerDisposition{requeue: true, delay: 5 * time.Second}
	}
	if failed.BillingRetryCount < cfg.BillingRetryAttempts {
		return asyncImageWorkerDisposition{requeue: true, delay: time.Duration(cfg.RetryBackoffSeconds) * time.Second}
	}
	return asyncImageWorkerDisposition{}
}

func (h *DurableAsyncImageHandler) reloadAsyncImageIdentity(ctx context.Context, task *service.AsyncImageTask, enforceCurrentGroup bool) (*service.APIKey, *service.UserSubscription, error) {
	if h == nil || h.apiKeys == nil || task == nil {
		return nil, nil, errors.New("API key service is unavailable")
	}
	apiKey, err := h.apiKeys.GetByID(ctx, task.APIKeyID)
	if err != nil || apiKey == nil || apiKey.User == nil {
		return nil, nil, errors.New("task API key is unavailable")
	}
	if apiKey.UserID != task.UserID || !apiKey.User.IsActive() {
		return nil, nil, errors.New("task user is inactive")
	}
	if enforceCurrentGroup {
		if !apiKey.IsActive() || apiKey.IsExpired() || apiKey.IsQuotaExhausted() {
			return nil, nil, errors.New("task API key is disabled, expired, or exhausted")
		}
		billingGroup, resolveErr := h.apiKeys.ResolveAsyncImageBillingGroup(ctx, apiKey, task.Platform)
		if resolveErr != nil || billingGroup == nil || billingGroup.ID != task.GroupID {
			return nil, nil, errors.New("task API key group or platform changed before execution")
		}
		groupID := billingGroup.ID
		apiKey.GroupID = &groupID
		apiKey.Group = billingGroup
	} else if apiKey.Group == nil || apiKey.GroupID == nil || *apiKey.GroupID != task.GroupID {
		// Soft path: still prefer the task snapshotted billing group for scheduling/pricing context.
		if group, groupErr := h.apiKeys.GetGroupByID(ctx, task.GroupID); groupErr == nil && group != nil {
			groupID := group.ID
			apiKey.GroupID = &groupID
			apiKey.Group = group
		}
	}
	var subscription *service.UserSubscription
	if apiKey.Group != nil && apiKey.Group.IsSubscriptionType() && h.subscriptions != nil {
		subscription, err = h.subscriptions.GetActiveSubscription(ctx, apiKey.UserID, task.GroupID)
		if err != nil && enforceCurrentGroup {
			return nil, nil, errors.New("task subscription is no longer active")
		}
	}
	return apiKey, subscription, nil
}

func (h *DurableAsyncImageHandler) decryptAsyncImagePayload(ciphertext []byte) (*durableAsyncImagePayload, error) {
	if len(ciphertext) == 0 || h == nil || h.encryptor == nil {
		return nil, errors.New("encrypted task request is missing")
	}
	plain, err := h.encryptor.Decrypt(string(ciphertext))
	if err != nil {
		return nil, err
	}
	var payload durableAsyncImagePayload
	if err := json.Unmarshal([]byte(plain), &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (h *DurableAsyncImageHandler) failAsyncImageTask(ctx context.Context, task *service.AsyncImageTask, code, message string, incrementRetry bool) {
	if task == nil {
		return
	}
	message = asyncImageSafeError(errors.New(message))
	finished := time.Now().UTC()
	transition := service.AsyncImageTaskTransition{
		TaskID: task.TaskID, ExpectedVersion: task.Version,
		FromStatuses: []string{task.Status}, ToStatus: service.AsyncImageTaskStatusFailed,
		ErrorCode: &code, ErrorMessage: &message, IncrementRetry: incrementRetry,
		FinishedAt: &finished, ClearRequestPayload: true, EventType: "task_failed",
	}
	_, err := h.tasks.Transition(ctx, enrichAsyncImageAttemptTransition(ctx, task, transition))
	if err != nil {
		logger.L().Warn("async_image.fail_transition_failed", zap.String("task_id", task.TaskID), zap.Error(err))
	}
}

func (h *DurableAsyncImageHandler) markAsyncImageExecutionUnknown(ctx context.Context, task *service.AsyncImageTask, message string) {
	if task == nil {
		return
	}
	code, finished := "execution_unknown", time.Now().UTC()
	message = asyncImageSafeError(errors.New(message))
	reconciliation := "pending"
	transition := service.AsyncImageTaskTransition{
		TaskID: task.TaskID, ExpectedVersion: task.Version,
		FromStatuses: []string{service.AsyncImageTaskStatusInvoking},
		ToStatus:     service.AsyncImageTaskStatusExecutionUnknown,
		ErrorCode:    &code, ErrorMessage: &message, FinishedAt: &finished,
		ClearRequestPayload: true, EventType: "execution_unknown", ReconciliationStatus: &reconciliation,
	}
	_, err := h.tasks.Transition(ctx, enrichAsyncImageAttemptTransition(ctx, task, transition))
	if err != nil {
		logger.L().Warn("async_image.execution_unknown_transition_failed", zap.String("task_id", task.TaskID), zap.Error(err))
	}
}

func asyncImageAttemptState(task *service.AsyncImageTask, ctx context.Context) (json.RawMessage, json.RawMessage) {
	if task == nil {
		return nil, nil
	}
	capture := service.AsyncImageAccountAttemptCaptureFromContext(ctx)
	if capture == nil {
		return task.AccountAttempts, task.AttemptedAccountIDs
	}
	return service.MergeAsyncImageAccountAttempts(task.AccountAttempts, capture.Attempts())
}

func enrichAsyncImageAttemptTransition(ctx context.Context, task *service.AsyncImageTask, transition service.AsyncImageTaskTransition) service.AsyncImageTaskTransition {
	if task == nil {
		return transition
	}
	accountAttempts, attemptedIDs := asyncImageAttemptState(task, ctx)
	if len(accountAttempts) > 0 {
		transition.AccountAttempts = accountAttempts
	}
	if len(attemptedIDs) > 0 {
		transition.AttemptedAccountIDs = attemptedIDs
	}
	if capture := service.AsyncImageAccountAttemptCaptureFromContext(ctx); capture != nil {
		attempts := capture.Attempts()
		if len(attempts) > 0 {
			last := attempts[len(attempts)-1]
			if transition.AccountID == nil {
				id := last.AccountID
				transition.AccountID = &id
			}
			if transition.UpstreamRequestID == nil && last.UpstreamRequestID != "" {
				requestID := last.UpstreamRequestID
				transition.UpstreamRequestID = &requestID
			}
			if transition.ErrorMessage == nil && last.Error != "" {
				message := asyncImageSafeError(errors.New(last.Error))
				transition.ErrorMessage = &message
			}
		}
	}
	return transition
}

func asyncImageRecentFailedAccountIDs(task *service.AsyncImageTask) map[int64]struct{} {
	if task == nil || len(task.AccountAttempts) == 0 {
		return nil
	}
	var attempts []service.AsyncImageAccountAttempt
	if err := json.Unmarshal(task.AccountAttempts, &attempts); err != nil {
		return nil
	}
	ids := make(map[int64]struct{})
	seen := 0
	for i := len(attempts) - 1; i >= 0 && seen < 3; i-- {
		if attempts[i].Status != service.AsyncImageAccountAttemptFailed || attempts[i].AccountID <= 0 {
			continue
		}
		ids[attempts[i].AccountID] = struct{}{}
		seen++
	}
	return ids
}

func asyncImageSafeError(err error) string {
	if err == nil {
		return "asynchronous image operation failed"
	}
	message := logredact.RedactText(strings.Join(strings.Fields(strings.TrimSpace(err.Error())), " "), "api_key", "secret", "token", "authorization")
	if message == "" {
		message = "asynchronous image operation failed"
	}
	runes := []rune(message)
	if len(runes) > 500 {
		runes = runes[:500]
	}
	return string(runes)
}

func formatAsyncImageUpstreamFailure(statusCode int, body []byte) string {
	detail := strings.TrimSpace(service.ExtractUpstreamErrorMessage(body))
	if detail == "" {
		detail = strings.TrimSpace(string(body))
	}
	detail = logredact.RedactText(strings.Join(strings.Fields(detail), " "), "api_key", "secret", "token", "authorization")
	if detail == "" {
		detail = "no upstream error body"
	}
	runes := []rune(detail)
	if len(runes) > 320 {
		detail = string(runes[:320]) + "..."
	}
	prefix := asyncImageUpstreamFailurePrefix(detail)
	if statusCode <= 0 {
		return prefix + "：网关无有效响应（" + detail + "）"
	}
	return fmt.Sprintf("%s（HTTP %d）：%s", prefix, statusCode, detail)
}

func asyncImageUpstreamFailurePrefix(detail string) string {
	lower := strings.ToLower(detail)
	switch {
	case strings.Contains(lower, "prompt is required"),
		strings.Contains(lower, "non-empty text prompt"),
		strings.Contains(lower, "invalid prompt"),
		strings.Contains(lower, "prompt required"),
		strings.Contains(lower, "empty prompt"):
		return "提示词无效"
	default:
		return "上游生图失败"
	}
}
