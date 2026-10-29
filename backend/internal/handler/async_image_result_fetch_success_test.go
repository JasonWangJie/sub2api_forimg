package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type resultFetchCommitTestRepo struct {
	*resultFetchTestRepo
	recordCalls          int
	recordErrAfterCommit bool
}

func (r *resultFetchCommitTestRepo) RecordAsyncImageUpstreamSuccess(_ context.Context, p service.RecordAsyncImageUpstreamSuccessParams) (*service.AsyncImageTask, error) {
	r.recordCalls++
	r.task.Status = service.AsyncImageTaskStatusUpstreamSucceeded
	r.task.Version++
	r.task.AccountID = &p.AccountID
	r.task.BillingRequestID = &p.BillingRequestID
	r.task.BillingPayload = p.BillingPayload
	r.task.ImageCount = p.ImageCount
	r.staging = p.StagingObjects
	r.pending = nil
	if r.recordErrAfterCommit {
		r.recordErrAfterCommit = false
		return nil, errors.New("COMMIT outcome unknown")
	}
	c := *r.task
	return &c, nil
}

func TestAsyncImageResultFetchRestartCompletesOrderedImagesAndBillsOnce(t *testing.T) {
	png, jpeg := generatedWorkerImageFixtures(t)
	groupID := int64(3)
	price := 0.04
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformGemini, RateMultiplier: 1, ImagePrice1K: &price, ImagePrice2K: &price, ImagePrice4K: &price}
	key := &service.APIKey{ID: 7, UserID: 9, GroupID: &groupID, Group: group, User: &service.User{ID: 9}}
	account := &service.Account{ID: 12, Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey}
	repo := &resultFetchCommitTestRepo{resultFetchTestRepo: &resultFetchTestRepo{generatedWorkerResultRepo: &generatedWorkerResultRepo{task: &service.AsyncImageTask{TaskID: "asyncimg_restart", Version: 2, UserID: 9, APIKeyID: 7, GroupID: 3, Platform: service.PlatformGemini, Status: service.AsyncImageTaskStatusUpstreamSucceeded}}}, recordErrAfterCommit: true}
	h, storage, _ := newGeneratedWorkerLocalHandler(t, repo.generatedWorkerResultRepo)
	h.tasks = service.NewAsyncImageTaskService(repo)
	h.encryptor = resultFetchTestCipher{}
	groupRepo := &generatedWorkerGroupRepo{group: group}
	accountRepo := &generatedWorkerAccountRepo{account: account}
	h.apiKeys = service.NewAPIKeyService(&generatedWorkerKeyRepo{key: key}, nil, groupRepo, nil, nil, nil, nil)
	h.accounts = service.NewAccountService(accountRepo, groupRepo)
	billingRepo := &generatedWorkerBillingRepo{}
	usageRepo := &generatedWorkerUsageRepo{failOnce: true}
	cfg := &config.Config{}
	h.gateway = &GatewayHandler{gatewayService: service.NewGatewayService(accountRepo, groupRepo, usageRepo, billingRepo, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, nil, nil, nil, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)}
	envelope := asyncImageResultEnvelope{Version: 1, Images: []asyncImageResultSource{{Data: png, MIME: "image/png"}, {URL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg)}}, Gemini: &service.RecordUsageInput{APIKey: key, User: key.User, Account: account, Result: &service.ForwardResult{Model: "gemini-2.5-flash-image", RequestID: "fixed-upstream", ImageCount: 2}}}
	plain, err := json.Marshal(envelope)
	require.NoError(t, err)
	cipher, _ := h.encryptor.Encrypt(string(plain))
	repo.pending = &service.AsyncImageResultFetch{EncryptedPayload: []byte(cipher), Attempts: map[int]int{}, ExpiresAt: time.Now().Add(time.Hour)}
	runtime := service.AsyncImageRuntimeConfig{DownloadMaxBytes: 1 << 20, DownloadMaxPixels: 100, StorageRetryAttempts: 3, BillingRetryAttempts: 3, RetryBackoffSeconds: 1}
	d, handled := h.resumeAsyncImageResultFetch(context.Background(), repo.task, runtime)
	require.True(t, handled)
	require.True(t, d.requeue, "usage log error must retry persisted billing")
	require.Equal(t, service.AsyncImageTaskStatusBillingFailed, repo.task.Status)
	require.Nil(t, repo.pending)
	require.Equal(t, 1, repo.recordCalls, "ambiguous commit must read back before replaying write")
	require.Equal(t, 1, billingRepo.charges)
	require.Len(t, repo.results, 2)
	require.Equal(t, "image/png", repo.results[0].ContentType)
	require.Equal(t, "image/jpeg", repo.results[1].ContentType)
	require.False(t, h.postProcessAsyncImageTask(context.Background(), repo.task, runtime).requeue)
	require.Equal(t, service.AsyncImageTaskStatusSucceeded, repo.task.Status)
	require.Equal(t, 1, billingRepo.charges)
	require.Len(t, billingRepo.commands, 2)
	require.Equal(t, billingRepo.commands[0], billingRepo.commands[1])
	require.Equal(t, "client:async-image:asyncimg_restart", billingRepo.commands[0].RequestID)
	require.Len(t, storage.saved, 2, "billing retries must not upload again")
}
