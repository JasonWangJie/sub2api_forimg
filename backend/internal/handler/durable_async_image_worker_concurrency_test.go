//go:build unit

package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newAsyncGeminiWorkerContext(apiKey *service.APIKey) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"customer-alias","messages":[{"role":"user","content":"draw a skyline"}],"stream":false}`)
	req := httptest.NewRequest(http.MethodPost, EndpointChatCompletions, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != nil && apiKey.Group != nil {
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, apiKey.Group))
	}
	c.Request = req
	if apiKey != nil {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: apiKey.User.Concurrency})
	}
	return c, recorder
}

func TestDurableAsyncImageWorkerGeminiHoldsSharedImageSlotOnlyDuringForward(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &geminiAliasGateUpstream{}
	gateway, apiKey, cleanup := newGeminiAliasGateTestHandler(t, true, "gemini-2.5-flash-image", upstream)
	defer cleanup()
	worker := &DurableAsyncImageHandler{gateway: gateway}
	c, recorder := newAsyncGeminiWorkerContext(apiKey)

	var slotHeldDuringForward atomic.Bool
	upstream.onDo = func() {
		release, acquired := gateway.imageLimiter.TryAcquire(true, 1)
		if acquired && release != nil {
			release()
		}
		slotHeldDuringForward.Store(!acquired)
	}

	require.True(t, worker.forwardAsyncImageUpstream(c, service.PlatformGemini))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.EqualValues(t, 1, upstream.calls.Load())
	require.True(t, slotHeldDuringForward.Load(), "Gemini async forwarding must hold the shared image slot")
	release, acquired := gateway.imageLimiter.TryAcquire(true, 1)
	require.True(t, acquired, "Gemini async forwarding must release the image slot before post-processing")
	require.NotNil(t, release)
	release()
}

func TestDurableAsyncImageWorkerGeminiReleasesImageSlotWhenGatewayFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Gateway: config.GatewayConfig{ImageConcurrency: config.ImageConcurrencyConfig{
		Enabled:               true,
		MaxConcurrentRequests: 1,
		OverflowMode:          config.ImageConcurrencyOverflowModeReject,
	}}}
	gateway := &GatewayHandler{cfg: cfg, imageLimiter: &imageConcurrencyLimiter{}}
	worker := &DurableAsyncImageHandler{gateway: gateway}
	c, recorder := newAsyncGeminiWorkerContext(nil)

	require.True(t, worker.forwardAsyncImageUpstream(c, service.PlatformGemini))

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	release, acquired := gateway.imageLimiter.TryAcquire(true, 1)
	require.True(t, acquired, "an early gateway failure must release the Gemini image slot")
	require.NotNil(t, release)
	release()
}

func TestDurableAsyncImageWorkerGeminiCanceledWaitDoesNotLeakWaiterOrInvokeGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &geminiAliasGateUpstream{}
	gateway, apiKey, cleanup := newGeminiAliasGateTestHandler(t, true, "gemini-2.5-flash-image", upstream)
	defer cleanup()
	gateway.cfg.Gateway.ImageConcurrency.OverflowMode = config.ImageConcurrencyOverflowModeWait
	gateway.cfg.Gateway.ImageConcurrency.WaitTimeoutSeconds = 5
	gateway.cfg.Gateway.ImageConcurrency.MaxWaitingRequests = 1
	worker := &DurableAsyncImageHandler{gateway: gateway}
	c, recorder := newAsyncGeminiWorkerContext(apiKey)

	heldRelease, acquired := gateway.imageLimiter.TryAcquire(true, 1)
	require.True(t, acquired)
	require.NotNil(t, heldRelease)
	canceledCtx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(canceledCtx)

	require.True(t, worker.forwardAsyncImageUpstream(c, service.PlatformGemini))

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.True(t, isOpsRoutingCapacityLimited(c), "local limiter rejection must be distinguishable from an upstream 429")
	require.Zero(t, upstream.calls.Load(), "a canceled limiter wait must not invoke Gemini")
	heldRelease()

	// A leaked canceled waiter would consume max_waiting_requests=1 and make
	// this second waiter fail immediately instead of receiving the released slot.
	heldAgain, acquired := gateway.imageLimiter.TryAcquire(true, 1)
	require.True(t, acquired)
	waitResult := make(chan bool, 1)
	go func() {
		release, ok := gateway.imageLimiter.Acquire(context.Background(), true, 1, true, time.Second, 1)
		if ok && release != nil {
			release()
		}
		waitResult <- ok
	}()
	time.Sleep(20 * time.Millisecond)
	heldAgain()
	require.True(t, <-waitResult)
}

type asyncImageLocalCapacityRepoStub struct {
	service.AsyncImageTaskRepository
	transition service.AsyncImageTaskTransition
	task       *service.AsyncImageTask
}

func (s *asyncImageLocalCapacityRepoStub) TransitionAsyncImageTask(_ context.Context, transition service.AsyncImageTaskTransition) (*service.AsyncImageTask, error) {
	s.transition = transition
	return &service.AsyncImageTask{
		TaskID: transition.TaskID, Status: transition.ToStatus,
		Version: transition.ExpectedVersion + 1,
	}, nil
}

func (s *asyncImageLocalCapacityRepoStub) GetAsyncImageTaskByTaskID(context.Context, string) (*service.AsyncImageTask, error) {
	if s.task == nil {
		return nil, service.ErrAsyncImageTaskNotFound
	}
	return s.task, nil
}

func TestDurableAsyncImageWorkerDefersLocalCapacityWithoutFailingTask(t *testing.T) {
	repo := &asyncImageLocalCapacityRepoStub{}
	worker := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo)}
	task := &service.AsyncImageTask{
		TaskID: "asyncimg_capacity", Status: service.AsyncImageTaskStatusInvoking, Version: 7,
	}

	disposition := worker.deferAsyncImageForLocalCapacity(context.Background(), task, service.AsyncImageRuntimeConfig{RetryBackoffSeconds: 12})
	require.True(t, disposition.requeue)
	require.Equal(t, 12*time.Second, disposition.delay)
	require.Equal(t, service.AsyncImageTaskStatusQueued, repo.transition.ToStatus)
	require.True(t, repo.transition.IncrementRetry)
	require.True(t, repo.transition.ClearError)
}

func TestAsyncImageInvocationHeartbeatFreshUsesDatabaseLeaseWindow(t *testing.T) {
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	cfg := service.AsyncImageRuntimeConfig{WorkerLeaseSeconds: 120}

	require.True(t, asyncImageInvocationHeartbeatFresh(&service.AsyncImageTask{
		UpdatedAt: now.Add(-119 * time.Second),
	}, cfg, now))
	require.False(t, asyncImageInvocationHeartbeatFresh(&service.AsyncImageTask{
		UpdatedAt: now.Add(-120 * time.Second),
	}, cfg, now))
	require.False(t, asyncImageInvocationHeartbeatFresh(&service.AsyncImageTask{}, cfg, now))
}

func TestAsyncImageInvocationMayFinishAfterRedisLeaseLoss(t *testing.T) {
	repo := &asyncImageLocalCapacityRepoStub{task: &service.AsyncImageTask{Status: service.AsyncImageTaskStatusInvoking}}
	worker := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo)}
	require.True(t, worker.asyncImageInvocationCanOutliveQueueLease(context.Background(), "asyncimg_running"))

	repo.task.Status = service.AsyncImageTaskStatusUploading
	require.False(t, worker.asyncImageInvocationCanOutliveQueueLease(context.Background(), "asyncimg_uploading"))
}

func TestDurableAsyncImageWorkerOpenAIDelegatesSingleSharedImageLease(t *testing.T) {
	gin.SetMode(gin.TestMode)
	enteredUserConcurrency := make(chan struct{})
	continueRequest := make(chan struct{})
	cache := &concurrencyCacheMock{
		acquireUserSlotFn: func(ctx context.Context, _ int64, _ int, _ string) (bool, error) {
			close(enteredUserConcurrency)
			select {
			case <-continueRequest:
				return true, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		},
	}
	cfg := &config.Config{
		RunMode: config.RunModeSimple,
		Gateway: config.GatewayConfig{ImageConcurrency: config.ImageConcurrencyConfig{
			Enabled:               true,
			MaxConcurrentRequests: 1,
			OverflowMode:          config.ImageConcurrencyOverflowModeReject,
		}},
	}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	defer billing.Stop()
	sharedLimiter := &imageConcurrencyLimiter{}
	openAI := &OpenAIGatewayHandler{
		gatewayService:      &service.OpenAIGatewayService{},
		billingCacheService: billing,
		apiKeyService:       &service.APIKeyService{},
		concurrencyHelper:   NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, 0),
		cfg:                 cfg,
		imageLimiter:        sharedLimiter,
	}
	worker := &DurableAsyncImageHandler{openAI: openAI}
	groupID := int64(7301)
	apiKey := &service.APIKey{
		ID: 7302, UserID: 7303, GroupID: &groupID,
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true},
		User:  &service.User{ID: 7303, Concurrency: 1, Balance: 100},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a skyline","size":"1024x1024"}`)
	req := httptest.NewRequest(http.MethodPost, EndpointImagesGenerations, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, apiKey.Group))
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 1})

	done := make(chan struct{})
	go func() {
		worker.forwardAsyncImageUpstream(c, service.PlatformOpenAI)
		close(done)
	}()
	select {
	case <-enteredUserConcurrency:
	case <-time.After(time.Second):
		t.Fatal("OpenAI worker route did not pass its image gate")
	}

	secondRelease, secondAcquired := sharedLimiter.TryAcquire(true, 1)
	if secondAcquired && secondRelease != nil {
		secondRelease()
	}
	require.False(t, secondAcquired, "OpenAI Images must own exactly one shared image lease before user concurrency")
	close(continueRequest)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("OpenAI worker route did not return")
	}

	release, acquired := sharedLimiter.TryAcquire(true, 1)
	require.True(t, acquired, "OpenAI Images must release its image lease on downstream failure")
	require.NotNil(t, release)
	release()
}

func TestAsyncImageLibraryArchiveRetryClassificationStopsPermanentFailures(t *testing.T) {
	permanent := infraerrors.New(http.StatusConflict, "IMAGE_LIBRARY_ITEM_QUOTA", "image library item quota exceeded")
	require.False(t, isRetryableAsyncImageLibraryArchiveError(permanent))
	require.True(t, isRetryableAsyncImageLibraryArchiveError(errors.New("database unavailable")))
	require.True(t, isRetryableAsyncImageLibraryArchiveError(errors.Join(permanent, context.DeadlineExceeded)))
}

func TestDurableAsyncImageHandlerStopWaitsForRuntimeLoops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := &DurableAsyncImageHandler{stop: cancel}
	exited := make(chan struct{})
	h.runtimeWG.Add(1)
	go func() {
		defer h.runtimeWG.Done()
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond)
		close(exited)
	}()

	h.Stop()
	select {
	case <-exited:
	default:
		t.Fatal("Stop returned before the durable runtime exited")
	}
}
