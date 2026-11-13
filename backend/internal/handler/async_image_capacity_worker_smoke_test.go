//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type capacityWorkerSmokeRepo struct {
	*referenceLocalFallbackRepo
	events []service.AsyncImageEvent
}

func (r *capacityWorkerSmokeRepo) AppendAsyncImageEvent(_ context.Context, event service.AsyncImageEvent) error {
	r.events = append(r.events, event)
	return nil
}

// Selection returns no schedulable rows, while the optional failure-time
// metadata read identifies configured accounts. SQL semantics are tested in PG.
type capacityWorkerSmokePool struct {
	openAIImagesFailoverAccountRepo
	reads int
}

func (*capacityWorkerSmokePool) GetImageAccountPoolMode(context.Context, int64) (string, error) {
	return service.ImageAccountPoolModeResolution, nil
}
func (*capacityWorkerSmokePool) HasImageAccountPoolConfigured(context.Context, int64, string, string) (bool, error) {
	return true, nil
}
func (*capacityWorkerSmokePool) ListSchedulableByGroupImageAccountPool(context.Context, int64, string, string, []string) ([]service.Account, error) {
	return nil, nil
}
func (*capacityWorkerSmokePool) ListImageSizeAccountIDsByGroupID(context.Context, int64) ([]int64, error) {
	return []int64{25, 26}, nil
}
func (r *capacityWorkerSmokePool) SnapshotImageAccountCapacity(_ context.Context, _ int64, mode, model, tier string, _ []string) (service.ImageCapacityObservation, error) {
	r.reads++
	return service.ImageCapacityObservation{Source: "pool_snapshot", ObservedAt: time.Now().UTC(), PoolMode: mode, PoolModel: model, PoolSizeTier: tier, CandidateCount: 2, Reasons: []service.ImageCapacityReason{{Code: "scheduling_disabled", Count: 2, AccountIDs: []int64{25, 26}}}}, nil
}

func TestImageCapacityFullWorkerPersistsRetryAndTerminalDiagnostics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			model := "gpt-image-2"
			if platform == service.PlatformGemini {
				model = "gemini-3-pro-image-preview"
			}
			groupID, size, ratio, price := int64(3), "2K", "1:1", 0.04
			group := &service.Group{ID: groupID, Hydrated: true, Status: service.StatusActive, Platform: platform, AllowImageGeneration: true, AllowAsyncImageGeneration: true, RateMultiplier: 1, ImagePrice1K: &price, ImagePrice2K: &price, ImagePrice4K: &price}
			key := &service.APIKey{ID: 7, UserID: 9, Status: service.StatusActive, GroupID: &groupID, Group: group, User: &service.User{ID: 9, Status: service.StatusActive, Concurrency: 1, Balance: 100}}
			body, err := json.Marshal(map[string]any{"model": model, "prompt": "private-capacity-smoke-prompt", "resolution": size, "aspect_ratio": ratio, "n": 1})
			require.NoError(t, err)
			requestPayload := durableAsyncImagePayload{SourcePath: "/v1/images/generations_oa", ContentType: "application/json", Body: body}
			if platform == service.PlatformGemini {
				requestPayload.SourcePath = "/v1/chat/completions_async"
				requestPayload.Body = []byte(`{"model":"gemini-3-pro-image-preview","messages":[{"role":"user","content":"private-capacity-smoke-prompt"}],"extra_body":{"google":{"image_config":{"image_size":"2K","aspect_ratio":"1:1"}}}}`)
				requestPayload.Normalized, err = service.ParseBBGeminiImageRequest(requestPayload.Body, requestPayload.SourcePath)
				require.NoError(t, err)
			}
			payload, err := json.Marshal(requestPayload)
			require.NoError(t, err)
			cipher := resultFetchTestCipher{}
			encrypted, err := cipher.Encrypt(string(payload))
			require.NoError(t, err)
			task := &service.AsyncImageTask{TaskID: "asyncimg_capacity_worker_" + platform, Version: 1, UserID: key.UserID, APIKeyID: key.ID, GroupID: groupID, Platform: platform, Model: model, Protocol: service.AsyncImageProtocolBB, RequestType: service.AsyncImageRequestTypeTextToImage, Status: service.AsyncImageTaskStatusQueued, BillingStatus: service.AsyncImageBillingStatusPending, SubmittedAt: time.Now().UTC(), RequestedImageSize: &size, AspectRatio: &ratio, RequestPayload: []byte(encrypted)}
			base := &generatedWorkerResultRepo{task: task}
			repo := &capacityWorkerSmokeRepo{referenceLocalFallbackRepo: &referenceLocalFallbackRepo{base}}
			worker, storage, root := newGeneratedWorkerLocalHandler(t, base)
			worker.tasks, worker.encryptor = service.NewAsyncImageTaskService(repo), cipher
			worker.storage = service.NewImageStorageSettingService(nil, nil, nil,
				func(context.Context, *config.ImageStorageConfig) (service.ImageStorage, error) { return storage, nil },
				config.ImageStorageConfig{Enabled: true, Backend: config.ImageStorageBackendLocal, Provider: config.ImageStorageProviderLocal, Prefix: "images/", Local: config.ImageStorageLocalConfig{DataDir: root, LocalURL: "https://file.aiimg.lol"}},
				config.AsyncImageConfig{CapacityMaxRetries: 1, CapacityRetryBaseSeconds: 1, CapacityRetryMaxSeconds: 1, TotalMaxRetries: 16})
			groupRepo := &generatedWorkerGroupRepo{group: group}
			worker.apiKeys = service.NewAPIKeyService(&openAIReferenceURLTimeoutKeyRepo{generatedWorkerKeyRepo{key: key}}, nil, groupRepo, nil, nil, nil, nil)
			pool := &capacityWorkerSmokePool{}
			worker.accounts = service.NewAccountService(pool, groupRepo)
			upstream := &geminiAliasGateUpstream{}
			billing := &generatedWorkerBillingRepo{}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(cache.Stop)
			concurrency := service.NewConcurrencyService(&fakeConcurrencyCache{})
			if platform == service.PlatformOpenAI {
				gateway := service.NewOpenAIGatewayService(pool, &generatedWorkerUsageRepo{}, billing, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, cache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
				worker.openAI = NewOpenAIGatewayHandler(gateway, concurrency, cache, worker.apiKeys, nil, nil, nil, nil, cfg)
			} else {
				gateway := service.NewGatewayService(pool, groupRepo, &generatedWorkerUsageRepo{}, billing, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, cache, nil, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
				compat := service.NewGeminiMessagesCompatService(nil, nil, nil, nil, nil, nil, upstream, nil, cfg)
				worker.gateway = NewGatewayHandler(gateway, nil, compat, nil, nil, concurrency, cache, nil, worker.apiKeys, nil, nil, nil, nil, cfg, nil)
			}
			runtimeConfig, err := worker.storage.RuntimeConfig(context.Background())
			require.NoError(t, err)
			require.Positive(t, runtimeConfig.CapacityMaxRetries)
			// Begin at the last allowed retry, then run the next delivery to
			// exhaustion. The fixture advances delivery without waiting its delay.
			task.CapacityRetryCount = runtimeConfig.CapacityMaxRetries - 1
			for round := 0; round < 2; round++ {
				disposition := worker.processAsyncImageTask(context.Background(), task.TaskID)
				failure := ""
				if repo.task.ErrorMessage != nil {
					failure = *repo.task.ErrorMessage
				}
				require.Equal(t, round == 0, disposition.requeue, "status=%s message=%s", repo.task.Status, failure)
				require.Len(t, repo.events, round+1)
				require.Equal(t, round+1, pool.reads, "one failure-time snapshot per invocation")
				event := repo.events[round]
				require.Equal(t, "capacity_diagnostic", event.EventType)
				require.NotContains(t, string(event.Payload), "private-capacity-smoke-prompt")
				var diagnostic service.ImageCapacityDiagnostics
				require.NoError(t, json.Unmarshal(event.Payload, &diagnostic))
				require.Equal(t, groupID, diagnostic.GroupID)
				require.Equal(t, model, diagnostic.Model)
				require.Len(t, diagnostic.Observations, 1)
				require.Equal(t, "pool_snapshot", diagnostic.Observations[0].Source)
				require.Equal(t, 2, diagnostic.Observations[0].CandidateCount, "an empty schedulable result is not an empty configured pool")
				require.Equal(t, "scheduling_disabled", diagnostic.Observations[0].Reasons[0].Code)
				require.Equal(t, []int64{25, 26}, diagnostic.Observations[0].Reasons[0].AccountIDs)
				if round == 0 {
					require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
					require.Equal(t, "capacity_retry", repo.transitions[len(repo.transitions)-1].EventType)
					require.NotEmpty(t, repo.task.RequestPayload)
				} else {
					require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
					require.Equal(t, "local_capacity_exhausted", *repo.task.ErrorCode)
					require.Equal(t, "task_failed", repo.transitions[len(repo.transitions)-1].EventType)
					require.Empty(t, repo.task.RequestPayload)
				}
			}
			details := &service.AsyncImageTaskDetails{Task: repo.task, Events: repo.events}
			center := &AsyncImageTaskCenterHandler{}
			admin, err := center.detailView(context.Background(), details, true)
			require.NoError(t, err)
			require.Len(t, admin.Events, 2)
			require.NotNil(t, admin.Events[1].CapacityDiagnostics)
			user, err := center.detailView(context.Background(), details, false)
			require.NoError(t, err)
			require.Empty(t, user.Events)
			publicJSON, err := json.Marshal(user)
			require.NoError(t, err)
			require.NotContains(t, string(publicJSON), "capacity_diagnostic")
			require.NotContains(t, string(publicJSON), "account_ids")
			require.False(t, worker.processAsyncImageTask(context.Background(), task.TaskID).requeue)
			require.Len(t, repo.events, 2, "terminal delivery must not create another capacity event")
			require.Zero(t, upstream.calls.Load())
			require.Zero(t, billing.charges)
			require.Empty(t, storage.saved)
		})
	}
}
