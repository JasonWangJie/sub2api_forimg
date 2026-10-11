package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func generatedWorkerImageFixtures(t *testing.T) ([]byte, []byte) {
	t.Helper()
	var pngBuffer, jpegBuffer bytes.Buffer
	require.NoError(t, png.Encode(&pngBuffer, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	require.NoError(t, jpeg.Encode(&jpegBuffer, image.NewRGBA(image.Rect(0, 0, 3, 2)), nil))
	return pngBuffer.Bytes(), jpegBuffer.Bytes()
}

func generatedWorkerMixedOutputs(t *testing.T) []asyncImageCapturedOutput {
	t.Helper()
	pngData, jpegData := generatedWorkerImageFixtures(t)
	images, err := service.ExtractGeminiGeneratedImages(map[string]any{"candidates": []any{
		map[string]any{"content": map[string]any{"parts": []any{
			map[string]any{"inlineData": map[string]any{"mimeType": "image/jpeg", "data": base64.StdEncoding.EncodeToString(pngData)}},
			map[string]any{"file_data": map[string]any{"mime_type": "image/png", "file_uri": "data:image/png;base64," + base64.StdEncoding.EncodeToString(jpegData)}},
		}}},
	}})
	require.NoError(t, err)
	capture := &service.GeminiImageResponseCapture{}
	capture.Set(images, nil)
	outputs, err := extractGeminiAsyncImageOutputs(context.Background(), capture, service.AsyncImageRuntimeConfig{DownloadMaxBytes: 1 << 20, DownloadMaxPixels: 100})
	require.NoError(t, err)
	require.Len(t, outputs, 2)
	return outputs
}

func TestGeneratedWorkerMixedResultsPreserveFormatDimensionsAndBilling(t *testing.T) {
	outputs := generatedWorkerMixedOutputs(t)
	require.Equal(t, "image/png", outputs[0].ContentType)
	require.Equal(t, "image/jpeg", outputs[1].ContentType)
	require.Equal(t, []string{"1x1", "3x2"}, capturedAsyncImageOutputSizes(outputs))
	for _, output := range outputs {
		require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(output.Data)), output.Checksum)
	}
	requested := "4K"
	usage := &service.ForwardResult{}
	applyCapturedGeminiImageDimensions(usage, outputs, &requested)
	require.Equal(t, 2, usage.ImageCount)
	require.Equal(t, "4K", usage.ImageInputSize)
	require.Equal(t, "4K", usage.ImageSize)
	require.Equal(t, 1, outputs[0].Width)
	require.Equal(t, 2, outputs[1].Height)
	require.Equal(t, map[string]int{"4K": 2}, usage.ImageSizeBreakdown)
	require.Equal(t, []string{"1x1", "3x2"}, usage.ImageOutputSizes)
}

func TestGeneratedWorkerOpenAIURLAndBase64Regression(t *testing.T) {
	pngData, jpegData := generatedWorkerImageFixtures(t)
	body, err := json.Marshal(map[string]any{"data": []any{
		// Base64 still wins over a duplicate URL in an OpenAI item.
		map[string]any{"b64_json": base64.StdEncoding.EncodeToString(pngData), "url": "https://127.0.0.1/unused"},
		map[string]any{"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(jpegData)},
	}})
	require.NoError(t, err)
	outputs, err := extractOpenAIAsyncImageOutputs(context.Background(), body, service.AsyncImageRuntimeConfig{DownloadMaxBytes: 1 << 20, DownloadMaxPixels: 100})
	require.NoError(t, err)
	require.Len(t, outputs, 2)
	require.Equal(t, pngData, outputs[0].Data)
	require.Equal(t, jpegData, outputs[1].Data)
	require.Equal(t, "image/jpeg", outputs[1].ContentType)
}

func TestGeneratedWorkerRejectsInvalidAndOversizedBytes(t *testing.T) {
	pngData, jpegData := generatedWorkerImageFixtures(t)
	for _, tt := range []struct {
		name string
		data []byte
		cfg  service.AsyncImageRuntimeConfig
	}{
		{"fake image", []byte("<html>expired image</html>"), service.AsyncImageRuntimeConfig{}},
		{"truncated JPEG", jpegData[:len(jpegData)/2], service.AsyncImageRuntimeConfig{}},
		{"PNG trailing bytes", append(append([]byte(nil), pngData...), 'x'), service.AsyncImageRuntimeConfig{}},
		{"byte limit", pngData, service.AsyncImageRuntimeConfig{DownloadMaxBytes: int64(len(pngData) - 1)}},
		{"pixel limit", jpegData, service.AsyncImageRuntimeConfig{DownloadMaxPixels: 5}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateGeneratedAsyncImage(tt.data, "image/png", tt.cfg)
			require.Error(t, err)
			require.True(t, isAsyncImageInvalidOutputError(err))
		})
	}
}

type generatedWorkerResultRepo struct {
	service.AsyncImageTaskRepository
	task          *service.AsyncImageTask
	transitions   []service.AsyncImageTaskTransition
	staging       []service.AsyncImageStagingObject
	results       []service.AsyncImageResult
	intents       []service.AsyncImageResultUploadIntent
	replaceFails  bool
	stagingPurged bool
}

func (r *generatedWorkerResultRepo) GetAsyncImageTaskByTaskID(context.Context, string) (*service.AsyncImageTask, error) {
	task := *r.task
	return &task, nil
}

func (r *generatedWorkerResultRepo) TransitionAsyncImageTask(_ context.Context, tr service.AsyncImageTaskTransition) (*service.AsyncImageTask, error) {
	if r.task.Version != tr.ExpectedVersion {
		return nil, service.ErrAsyncImageInvalidTransition
	}
	r.transitions = append(r.transitions, tr)
	task := *r.task
	task.Status, task.Version = tr.ToStatus, task.Version+1
	if tr.BillingStatus != nil {
		task.BillingStatus = *tr.BillingStatus
	}
	if tr.ActualCost != nil {
		task.ActualCost = tr.ActualCost
	}
	if tr.ClearError {
		task.ErrorCode, task.ErrorMessage = nil, nil
	} else {
		task.ErrorCode, task.ErrorMessage = tr.ErrorCode, tr.ErrorMessage
	}
	if tr.ClearRequestPayload {
		task.RequestPayload = nil
	}
	if tr.IncrementStorageRetry {
		task.StorageRetryCount++
	}
	if tr.IncrementBillingRetry {
		task.BillingRetryCount++
	}
	if tr.IncrementRetry || tr.IncrementReferenceRetry || tr.IncrementUpstreamRetry || tr.IncrementCapacityRetry {
		task.RetryCount++
	}
	r.task = &task
	copy := task
	return &copy, nil
}

func (r *generatedWorkerResultRepo) ListAsyncImageStagingObjects(context.Context, string) ([]service.AsyncImageStagingObject, error) {
	return r.staging, nil
}

func (r *generatedWorkerResultRepo) PrepareAsyncImageResultUploadIntents(_ context.Context, _ string, intents []service.AsyncImageResultUploadIntent) error {
	r.intents = intents
	return nil
}

func (r *generatedWorkerResultRepo) ReplaceAsyncImageResults(_ context.Context, _ string, results []service.AsyncImageResult) error {
	if r.replaceFails {
		r.replaceFails = false
		return errors.New("database temporarily unavailable after PUT")
	}
	r.results = results
	return nil
}

func (r *generatedWorkerResultRepo) DeleteAsyncImageStagingObjects(context.Context, string) error {
	r.stagingPurged = true
	return nil
}

type generatedWorkerLocalStorage struct {
	*service.LocalImageStorage
	t     *testing.T
	repo  *generatedWorkerResultRepo
	saved []string
}

func (s *generatedWorkerLocalStorage) SaveObject(ctx context.Context, key, contentType string, data []byte) (service.ObjectRef, error) {
	require.NotEmpty(s.t, s.repo.intents, "upload intents must be persisted before writing objects")
	s.saved = append(s.saved, key)
	return s.LocalImageStorage.SaveObject(ctx, key, contentType, data)
}

func newGeneratedWorkerLocalHandler(t *testing.T, repo *generatedWorkerResultRepo) (*DurableAsyncImageHandler, *generatedWorkerLocalStorage, string) {
	t.Helper()
	root := t.TempDir()
	local, err := service.NewLocalImageStorageWithURLOptions(root, "https://file.aiimg.lol", "", "", nil)
	require.NoError(t, err)
	storage := &generatedWorkerLocalStorage{LocalImageStorage: local, t: t, repo: repo}
	settings := service.NewImageStorageSettingService(nil, nil, nil,
		func(context.Context, *config.ImageStorageConfig) (service.ImageStorage, error) { return storage, nil },
		config.ImageStorageConfig{
			Enabled: true, Backend: config.ImageStorageBackendLocal, Provider: config.ImageStorageProviderLocal,
			Prefix: "images/", Local: config.ImageStorageLocalConfig{DataDir: root, LocalURL: "https://file.aiimg.lol"},
		},
		config.AsyncImageConfig{StorageRetryAttempts: 3, BillingRetryAttempts: 3, RetryBackoffSeconds: 1},
	)
	return &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo), storage: settings}, storage, root
}

type generatedWorkerBillingRepo struct {
	service.UsageBillingRepository
	commands []service.UsageBillingCommand
	charges  int
}

func (r *generatedWorkerBillingRepo) Apply(_ context.Context, command *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	r.commands = append(r.commands, *command)
	applied := len(r.commands) == 1
	if applied {
		r.charges++
	}
	return &service.UsageBillingApplyResult{Applied: applied}, nil
}

type generatedWorkerUsageRepo struct {
	service.UsageLogRepository
	failOnce bool
}

func (r *generatedWorkerUsageRepo) Create(_ context.Context, _ *service.UsageLog) (bool, error) {
	if r.failOnce {
		r.failOnce = false
		return false, errors.New("usage log temporarily unavailable after billing")
	}
	return true, nil
}

type generatedWorkerKeyRepo struct {
	service.APIKeyRepository
	key *service.APIKey
}

func (r *generatedWorkerKeyRepo) GetByIDIncludeDeleted(context.Context, int64) (*service.APIKey, error) {
	return r.key, nil
}

type generatedWorkerGroupRepo struct {
	service.GroupRepository
	group *service.Group
}

func (r *generatedWorkerGroupRepo) GetByID(context.Context, int64) (*service.Group, error) {
	return r.group, nil
}

type generatedWorkerAccountRepo struct {
	service.AccountRepository
	account *service.Account
}

func (r *generatedWorkerAccountRepo) GetByID(context.Context, int64) (*service.Account, error) {
	return r.account, nil
}

func TestGeneratedWorkerStorageAndBillingRetriesReuseStoredResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	outputs := generatedWorkerMixedOutputs(t)
	requestID := "client:async-image:asyncimg_result_retry"
	prepared := service.PreparedUsageBilling{
		Command: service.UsageBillingCommand{RequestID: requestID, APIKeyID: 7, UserID: 9, AccountID: 12, ImageCount: 2, BalanceCost: 0.08},
		Cost:    service.CostBreakdown{TotalCost: 0.04, ActualCost: 0.08}, Platform: service.PlatformGemini,
	}
	payload, err := json.Marshal(prepared)
	require.NoError(t, err)
	repo := &generatedWorkerResultRepo{replaceFails: true, task: &service.AsyncImageTask{
		TaskID: "asyncimg_result_retry", Version: 1, Platform: service.PlatformGemini,
		UserID: 9, APIKeyID: 7, GroupID: 3, Status: service.AsyncImageTaskStatusUpstreamSucceeded,
		BillingStatus: service.AsyncImageBillingStatusPending, BillingPayload: payload, ImageCount: 2,
		SubmittedAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}}
	for index, output := range outputs {
		width, height := output.Width, output.Height
		repo.staging = append(repo.staging, service.AsyncImageStagingObject{
			TaskID: repo.task.TaskID, ImageIndex: index, Content: output.Data,
			ContentType: output.ContentType, ByteSize: int64(len(output.Data)), Checksum: output.Checksum,
			Width: &width, Height: &height,
		})
	}
	h, storage, root := newGeneratedWorkerLocalHandler(t, repo)
	groupRepo := &generatedWorkerGroupRepo{group: &service.Group{ID: 3}}
	accountRepo := &generatedWorkerAccountRepo{account: &service.Account{ID: 12}}
	h.apiKeys = service.NewAPIKeyService(&generatedWorkerKeyRepo{key: &service.APIKey{ID: 7, UserID: 9}}, nil, groupRepo, nil, nil, nil, nil)
	h.accounts = service.NewAccountService(accountRepo, groupRepo)
	billingRepo := &generatedWorkerBillingRepo{}
	usageRepo := &generatedWorkerUsageRepo{failOnce: true}
	h.gateway = &GatewayHandler{gatewayService: service.NewGatewayService(
		accountRepo, groupRepo, usageRepo, billingRepo,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)}
	ctx := context.Background()

	// A manifest failure after PUT must retry storage without invoking generation
	// or applying any charge. The object keys are deterministic across attempts.
	disposition := h.processAsyncImageTask(ctx, repo.task.TaskID)
	require.True(t, disposition.requeue)
	require.Equal(t, service.AsyncImageTaskStatusStorageFailed, repo.task.Status)
	require.Equal(t, 1, repo.task.StorageRetryCount)
	require.Zero(t, billingRepo.charges)
	firstKeys := append([]string(nil), storage.saved...)
	require.Len(t, firstKeys, 2)

	disposition = h.processAsyncImageTask(ctx, repo.task.TaskID)
	require.True(t, disposition.requeue, "the simulated usage-log failure should retry billing")
	require.Equal(t, service.AsyncImageTaskStatusBillingFailed, repo.task.Status)
	require.Equal(t, firstKeys, storage.saved[2:])
	require.Equal(t, 1, billingRepo.charges)

	disposition = h.processAsyncImageTask(ctx, repo.task.TaskID)
	require.False(t, disposition.requeue)
	require.Equal(t, service.AsyncImageTaskStatusSucceeded, repo.task.Status)
	require.Equal(t, service.AsyncImageBillingStatusSucceeded, repo.task.BillingStatus)
	require.Equal(t, 0.08, *repo.task.ActualCost)
	require.Equal(t, payload, []byte(repo.task.BillingPayload))
	require.Len(t, billingRepo.commands, 2)
	require.Equal(t, billingRepo.commands[0], billingRepo.commands[1])
	require.Equal(t, requestID, billingRepo.commands[0].RequestID)
	require.Equal(t, 2, billingRepo.commands[0].ImageCount)
	require.Equal(t, 1, billingRepo.charges)
	require.Len(t, storage.saved, 4, "billing retry must not upload again")
	require.True(t, repo.stagingPurged)
	require.False(t, h.processAsyncImageTask(ctx, repo.task.TaskID).requeue)
	require.Len(t, billingRepo.commands, 2, "completed task delivery must not bill again")
	for _, tr := range repo.transitions {
		require.NotEqual(t, service.AsyncImageTaskStatusQueued, tr.ToStatus)
		require.NotEqual(t, service.AsyncImageTaskStatusInvoking, tr.ToStatus)
	}

	require.Len(t, repo.results, 2)
	for index, result := range repo.results {
		wantExt := []string{".png", ".jpg"}[index]
		require.Equal(t, wantExt, filepath.Ext(result.ObjectKey))
		require.Equal(t, outputs[index].ContentType, result.ContentType)
		require.Equal(t, outputs[index].Checksum, result.Checksum)
		require.Equal(t, int64(len(outputs[index].Data)), result.ByteSize)
		require.Equal(t, outputs[index].Width, *result.Width)
		require.Equal(t, outputs[index].Height, *result.Height)
		stored, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(result.ObjectKey)))
		require.NoError(t, err)
		require.Equal(t, outputs[index].Data, stored)
	}
	for _, protocol := range []string{"BB", "SC"} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/query", nil)
		details := &service.AsyncImageTaskDetails{Task: repo.task, Results: repo.results}
		if protocol == "BB" {
			h.writeBBQuery(c, details, service.AsyncImageRuntimeConfig{})
		} else {
			h.writeSCQuery(c, details, service.AsyncImageRuntimeConfig{})
		}
		require.Equal(t, http.StatusOK, recorder.Code)
		var response struct {
			Status string `json:"status"`
			Data   []struct {
				URL string `json:"url"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.Equal(t, "succeeded", response.Status)
		require.Len(t, response.Data, 2)
		for index, item := range response.Data {
			require.Equal(t, "https://file.aiimg.lol/"+repo.results[index].ObjectKey, item.URL)
		}
		require.NotContains(t, recorder.Body.String(), "base64")
	}
}

// Keep this compile-time assertion alongside the storage/billing retry test:
// local storage must continue to support both manifests and durable reads.
var _ service.DurableImageStorage = (*generatedWorkerLocalStorage)(nil)
