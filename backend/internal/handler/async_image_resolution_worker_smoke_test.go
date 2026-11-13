//go:build unit

package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// This fixture stores what the real quality service classified. PG's atomic
// counter/outbox semantics are separately covered by the repository PG tests.
type resolutionWorkerQualityRepo struct {
	service.ImageResolutionGuardRepository
	records  []service.ImageResolutionRecord
	cooldown *service.ImageResolutionCooldown
}

func (r *resolutionWorkerQualityRepo) Record(_ context.Context, v service.ImageResolutionRecord) (*service.ImageResolutionCooldown, error) {
	r.records = append(r.records, v)
	if v.Outcome == "mismatch" {
		r.cooldown = &service.ImageResolutionCooldown{AccountID: v.Observation.AccountID, Model: v.Observation.Expectation.RequestedModel, SizeTier: v.Observation.Expectation.SizeTier, CycleID: "smoke-cycle", CooldownUntil: v.Now.Add(10 * time.Minute)}
	}
	return r.cooldown, nil
}
func (r *resolutionWorkerQualityRepo) GetCooldown(_ context.Context, id int64, model, tier string) (*service.ImageResolutionCooldown, error) {
	if r.cooldown != nil && r.cooldown.AccountID == id && r.cooldown.Model == model && r.cooldown.SizeTier == tier {
		return r.cooldown, nil
	}
	return nil, nil
}

func TestAsyncImageResolutionFullWorkerDeliversAndCoolsWithoutRegeneration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	png, _ := generatedWorkerImageFixtures(t)
	var generations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		generations.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "quality-worker-upstream")
		_, _ = fmt.Fprintf(w, `{"data":[{"size":"4096x4096","b64_json":%q}]}`, base64.StdEncoding.EncodeToString(png))
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	upstream := &referenceTimeout454HTTPUpstream{client: &http.Client{Transport: transport, Timeout: 5 * time.Second}, target: target}
	const model = "gpt-image-2"
	size, ratio := "4K", "1:1"
	groupID, price := int64(3), 0.04
	group := &service.Group{ID: groupID, Hydrated: true, Status: service.StatusActive, Platform: service.PlatformOpenAI, AllowImageGeneration: true, AllowAsyncImageGeneration: true, RateMultiplier: 1, ImagePrice1K: &price, ImagePrice2K: &price, ImagePrice4K: &price}
	key := &service.APIKey{ID: 7, UserID: 9, Status: service.StatusActive, GroupID: &groupID, Group: group, User: &service.User{ID: 9, Status: service.StatusActive, Concurrency: 1, Balance: 100}}
	body := []byte(`{"model":"gpt-image-2","prompt":"Render a lighthouse.","resolution":"4K","aspect_ratio":"1:1","n":1}`)
	payload, err := json.Marshal(durableAsyncImagePayload{SourcePath: "/v1/images/generations_oa", ContentType: "application/json", Body: body})
	require.NoError(t, err)
	cipher := resultFetchTestCipher{}
	encrypted, err := cipher.Encrypt(string(payload))
	require.NoError(t, err)
	task := &service.AsyncImageTask{TaskID: "asyncimg_resolution_smoke", Version: 1, UserID: key.UserID, APIKeyID: key.ID, GroupID: groupID, Platform: service.PlatformOpenAI, Model: model, Protocol: service.AsyncImageProtocolBB, RequestType: service.AsyncImageRequestTypeTextToImage, Status: service.AsyncImageTaskStatusQueued, BillingStatus: service.AsyncImageBillingStatusPending, SubmittedAt: time.Now().UTC(), RequestedImageSize: &size, AspectRatio: &ratio, RequestPayload: []byte(encrypted)}
	base := &generatedWorkerResultRepo{task: task}
	repo := &openAIReferenceURLTimeoutRepo{&referenceLocalFallbackRepo{base}}
	root := t.TempDir()
	local, err := service.NewLocalImageStorageWithURLOptions(root, "https://generated.example", "", "", nil)
	require.NoError(t, err)
	storageSettings := service.NewImageStorageSettingService(nil, nil, nil, func(context.Context, *config.ImageStorageConfig) (service.ImageStorage, error) { return local, nil }, config.ImageStorageConfig{Enabled: true, Backend: config.ImageStorageBackendLocal, Provider: config.ImageStorageProviderLocal, Prefix: "images/", Local: config.ImageStorageLocalConfig{DataDir: root, LocalURL: "https://generated.example"}}, config.AsyncImageConfig{StorageRetryAttempts: 3, BillingRetryAttempts: 3, TotalMaxRetries: 16})
	qualityRepo := &resolutionWorkerQualityRepo{}
	settings := &toggleSettingRepo{values: map[string]string{}}
	guard := service.NewImageResolutionGuardService(qualityRepo, settings, nil, nil, nil)
	qualitySettings := service.DefaultImageResolutionGuardSettings()
	qualitySettings.Enabled = true
	qualitySettings.EmailEnabled = false
	_, err = guard.UpdateSettings(context.Background(), &qualitySettings)
	require.NoError(t, err)
	monitor := service.NewImageResolutionMonitor(guard)
	defer monitor.Close()
	monitor.SetRuntimeConfigProvider(storageSettings)
	groupRepo := &generatedWorkerGroupRepo{group: group}
	account := mockOpenAIReferenceURLTimeoutAccount(45, groupID, 1)
	accountRepo := openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	defer cache.Stop()
	billing := &generatedWorkerBillingRepo{}
	gateway := service.NewOpenAIGatewayService(accountRepo, &generatedWorkerUsageRepo{}, billing, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, cache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	gateway.SetImageResolutionGuard(monitor)
	worker := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo), storage: storageSettings, encryptor: cipher}
	worker.apiKeys = service.NewAPIKeyService(&openAIReferenceURLTimeoutKeyRepo{generatedWorkerKeyRepo{key: key}}, nil, groupRepo, nil, nil, nil, nil)
	worker.accounts = service.NewAccountService(accountRepo, groupRepo)
	worker.openAI = NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(&fakeConcurrencyCache{}), cache, worker.apiKeys, nil, nil, nil, nil, cfg)
	worker.SetImageResolutionGuard(guard)
	disposition := worker.processAsyncImageTask(context.Background(), task.TaskID)
	for round := 0; disposition.requeue && round < 3; round++ {
		disposition = worker.processAsyncImageTask(context.Background(), task.TaskID)
	}
	require.False(t, disposition.requeue)
	require.Equal(t, service.AsyncImageTaskStatusSucceeded, repo.task.Status)
	require.Equal(t, service.AsyncImageBillingStatusNotBillable, repo.task.BillingStatus, "same existing Simple-mode billing path")
	require.Zero(t, billing.charges)
	require.EqualValues(t, 1, generations.Load(), "quality anomaly must never POST another generation")
	require.Len(t, repo.results, 1)
	stored, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(repo.results[0].ObjectKey)))
	require.NoError(t, err)
	require.Equal(t, png, stored)
	require.Len(t, qualityRepo.records, 1)
	observation := qualityRepo.records[0].Observation
	require.Equal(t, "mismatch", qualityRepo.records[0].Outcome)
	require.Equal(t, int64(2880*2880), observation.Expectation.ExpectedPixels)
	require.Equal(t, "4K", observation.Expectation.SizeTier)
	require.NotZero(t, observation.Expectation.AttemptStartedAt)
	require.Equal(t, observation.Expectation.AttemptStartedAt, observation.StartedAt)
	for _, scope := range []struct {
		model, tier string
		cooling     bool
	}{{model, "4K", true}, {model, "2K", false}, {"gpt-image-2.5-flare", "4K", false}} {
		cooling, err := guard.IsCooling(context.Background(), 45, scope.model, scope.tier)
		require.NoError(t, err)
		require.Equal(t, scope.cooling, cooling)
	}
	for round := 0; round < 2; round++ {
		require.False(t, worker.processAsyncImageTask(context.Background(), task.TaskID).requeue)
	}
	require.EqualValues(t, 1, generations.Load())
	require.Len(t, qualityRepo.records, 1)
}
