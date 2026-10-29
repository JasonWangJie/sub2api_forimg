package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type preparedRequestTestRepo struct {
	*generatedWorkerResultRepo
	writes            int
	writeErr          error
	commitBeforeError bool
}

func (r *preparedRequestTestRepo) SaveAsyncImagePreparedRequest(_ context.Context, taskID string, version int64, encrypted []byte) error {
	r.writes++
	if taskID != r.task.TaskID || version != r.task.Version || r.task.Status != service.AsyncImageTaskStatusInvoking {
		return service.ErrAsyncImageInvalidTransition
	}
	if r.writeErr == nil || r.commitBeforeError {
		r.task.RequestPayload = append([]byte(nil), encrypted...)
	}
	return r.writeErr
}

func preparedRequestFixture(t *testing.T, platform string) (*service.AsyncImageTask, *durableAsyncImagePayload, []byte, string, string) {
	t.Helper()
	mode := service.AsyncImageReferenceTransportLocal
	task := &service.AsyncImageTask{TaskID: "local_snapshot", RequestHash: "unchanged-request-hash", Model: "gemini-2.5-flash-image",
		Platform: platform, RequestType: service.AsyncImageRequestTypeImageToImage, ReferenceTransport: &mode,
		Status: service.AsyncImageTaskStatusInvoking, Version: 2, SubmittedAt: time.Now().UTC()}
	png, _ := generatedWorkerImageFixtures(t)
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	payload := &durableAsyncImagePayload{SourcePath: "/v1/images/tasks_async", ContentType: "application/json"}
	if platform == service.PlatformGemini {
		payload.Normalized = &service.AsyncImageNormalizedRequest{Model: task.Model, Kind: service.AsyncImageKindEdit, Platform: platform,
			Prompt: "fixture prompt", Parts: []service.AsyncImageInputPart{{Type: "image_url", URL: uri}, {Type: "text", Text: "fixture prompt"}}}
	} else {
		task.Model = "gpt-image-2"
		payload.Body = []byte(fmt.Sprintf(`{"model":%q,"prompt":"fixture prompt","image_urls":[%q],"size":"1024x1024"}`, task.Model, uri))
	}
	h := &DurableAsyncImageHandler{}
	body, path, contentType, err := h.buildAsyncImageUpstreamRequest(context.Background(), task, payload, testAsyncImageRetryConfig(), nil)
	require.NoError(t, err)
	require.True(t, asyncImagePreparedRequestEligible(task, body, path, contentType))
	return task, payload, body, path, contentType
}

func TestAsyncImagePreparedRequestPersistsAndReusesExactBytes(t *testing.T) {
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			task, payload, body, path, contentType := preparedRequestFixture(t, platform)
			repo := &preparedRequestTestRepo{generatedWorkerResultRepo: &generatedWorkerResultRepo{task: task}}
			h := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo), encryptor: resultFetchTestCipher{}}
			_, stop := h.persistAsyncImagePreparedRequest(context.Background(), context.Background(), task, payload, body, path, contentType, testAsyncImageRetryConfig())
			require.False(t, stop)
			require.Equal(t, 1, repo.writes)
			require.EqualValues(t, 2, task.Version, "snapshot write must preserve the invocation CAS version")
			require.NotContains(t, string(task.RequestPayload), "fixture prompt")
			// A new handler has neither the old memory cache nor original URL.
			restarted := &DurableAsyncImageHandler{encryptor: resultFetchTestCipher{}}
			stored, err := restarted.decryptAsyncImagePayload(task.RequestPayload)
			require.NoError(t, err)
			require.Nil(t, stored.Normalized)
			require.Empty(t, stored.Body)
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // a replay must not issue a reference fetch
			got, gotPath, gotType, err := restarted.buildAsyncImageUpstreamRequest(ctx, task, stored, testAsyncImageRetryConfig(), nil)
			require.NoError(t, err)
			require.Equal(t, body, got)
			require.Equal(t, path, gotPath)
			require.Equal(t, contentType, gotType)
			require.Equal(t, payload.SourcePath, stored.SourcePath)
		})
	}
}

func TestAsyncImagePreparedRequestAmbiguousCommitAndFailureBudget(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			task, payload, body, path, contentType := preparedRequestFixture(t, service.PlatformGemini)
			repo := &preparedRequestTestRepo{generatedWorkerResultRepo: &generatedWorkerResultRepo{task: task}, writeErr: errors.New("database write failed"), commitBeforeError: committed}
			h := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo), encryptor: resultFetchTestCipher{}}
			d, stop := h.persistAsyncImagePreparedRequest(context.Background(), context.Background(), task, payload, body, path, contentType, testAsyncImageRetryConfig())
			require.Equal(t, !committed, stop)
			require.Equal(t, !committed, d.requeue)
			if committed {
				require.Equal(t, service.AsyncImageTaskStatusInvoking, repo.task.Status)
				require.Empty(t, repo.transitions)
			} else {
				require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
				require.True(t, repo.transitions[0].IncrementReferenceRetry)
				require.Empty(t, task.RequestPayload)
			}
		})
	}
	task, payload, body, path, contentType := preparedRequestFixture(t, service.PlatformGemini)
	cfg := testAsyncImageRetryConfig()
	task.ReferenceRetryCount = cfg.ReferenceFetchMaxRetries
	repo := &preparedRequestTestRepo{generatedWorkerResultRepo: &generatedWorkerResultRepo{task: task}, writeErr: errors.New("database write failed")}
	h := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo), encryptor: resultFetchTestCipher{}}
	d, stop := h.persistAsyncImagePreparedRequest(context.Background(), context.Background(), task, payload, body, path, contentType, cfg)
	require.True(t, stop)
	require.False(t, d.requeue)
	require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
	require.Equal(t, "reference_snapshot_persistence_failed", *repo.task.ErrorCode)
	// Terminal transitions continue to remove the encrypted snapshot.
	require.Empty(t, repo.task.RequestPayload)
}

type preparedRequestKeyRepo struct {
	service.APIKeyRepository
	key *service.APIKey
}

func (r *preparedRequestKeyRepo) GetByID(context.Context, int64) (*service.APIKey, error) {
	return r.key, nil
}

func TestAsyncImagePreparedRequestPersistenceFailureStopsInvocation(t *testing.T) {
	task, payload, _, _, _ := preparedRequestFixture(t, service.PlatformGemini)
	task.APIKeyID, task.UserID, task.GroupID = 7, 9, 3
	plain, err := json.Marshal(payload)
	require.NoError(t, err)
	cipher, err := (resultFetchTestCipher{}).Encrypt(string(plain))
	require.NoError(t, err)
	task.RequestPayload = []byte(cipher)
	repo := &preparedRequestTestRepo{generatedWorkerResultRepo: &generatedWorkerResultRepo{task: task}, writeErr: errors.New("database unavailable")}
	h, _, _ := newGeneratedWorkerLocalHandler(t, repo.generatedWorkerResultRepo)
	h.tasks = service.NewAsyncImageTaskService(repo)
	h.encryptor = resultFetchTestCipher{}
	group := &service.Group{ID: 3, Platform: service.PlatformGemini, Status: service.StatusActive, AllowImageGeneration: true, AllowAsyncImageGeneration: true}
	key := &service.APIKey{ID: 7, UserID: 9, GroupID: &task.GroupID, Group: group, Status: service.StatusActive, User: &service.User{ID: 9, Status: service.StatusActive}}
	h.apiKeys = service.NewAPIKeyService(&preparedRequestKeyRepo{key: key}, nil, &generatedWorkerGroupRepo{group: group}, nil, nil, nil, nil)
	// There is deliberately no gateway: reaching forward would instead fail
	// gateway_unavailable. Persistence must return before it is consulted.
	d := h.invokeAsyncImageTask(context.Background(), task, testAsyncImageRetryConfig())
	require.True(t, d.requeue)
	require.Equal(t, 1, repo.writes)
	require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
	require.Equal(t, "reference_image_fetch_retry", repo.transitions[len(repo.transitions)-1].EventType)
	require.Empty(t, repo.task.AccountAttempts, "no account or generation invocation may start")
}

func TestAsyncImagePreparedRequestScopeAndSizeLimits(t *testing.T) {
	task, _, body, path, contentType := preparedRequestFixture(t, service.PlatformGemini)
	prepared := &asyncImagePreparedRequest{Version: 1, TaskID: task.TaskID, RequestHash: task.RequestHash, Model: task.Model, Body: body, Path: path, ContentType: contentType}
	require.NoError(t, validateAsyncImagePreparedRequest(task, prepared))
	for _, mutate := range []func(*service.AsyncImageTask, *asyncImagePreparedRequest){
		func(t *service.AsyncImageTask, _ *asyncImagePreparedRequest) { t.TaskID = "other" },
		func(t *service.AsyncImageTask, _ *asyncImagePreparedRequest) { t.RequestHash = "other" },
		func(t *service.AsyncImageTask, _ *asyncImagePreparedRequest) { t.Model = "other" },
		func(t *service.AsyncImageTask, _ *asyncImagePreparedRequest) {
			t.RequestType = service.AsyncImageRequestTypeTextToImage
		},
		func(t *service.AsyncImageTask, _ *asyncImagePreparedRequest) {
			mode := "passthrough"
			t.ReferenceTransport = &mode
		},
		func(_ *service.AsyncImageTask, p *asyncImagePreparedRequest) {
			p.Path = "https://example.org/elsewhere"
		},
		func(_ *service.AsyncImageTask, p *asyncImagePreparedRequest) { p.ContentType = "text/plain" },
		func(_ *service.AsyncImageTask, p *asyncImagePreparedRequest) {
			p.Body = []byte(`{"messages":[{"content":[{"image_url":{"url":"https://example.org/image"}}]}]}`)
		},
	} {
		copyTask, copyPrepared := *task, *prepared
		mutate(&copyTask, &copyPrepared)
		require.Error(t, validateAsyncImagePreparedRequest(&copyTask, &copyPrepared))
	}
	// The boundary uses a valid JSON request padded with whitespace; validation
	// must accept the exact body cap and reject the extra byte before parsing.
	large := bytes.Repeat([]byte(" "), service.AsyncImagePreparedRequestMaxBytes+1)
	copy(large, body)
	require.True(t, asyncImagePreparedRequestEligible(task, large[:service.AsyncImagePreparedRequestMaxBytes], path, contentType))
	require.False(t, asyncImagePreparedRequestEligible(task, large, path, contentType))
	var mixed bytes.Buffer
	w := multipart.NewWriter(&mixed)
	require.NoError(t, w.WriteField("image", "https://example.org/image"))
	require.NoError(t, w.Close())
	openAITask := *task
	openAITask.Platform = service.PlatformOpenAI
	require.False(t, asyncImagePreparedRequestEligible(&openAITask, mixed.Bytes(), EndpointImagesEdits, w.FormDataContentType()))
	// Match the gateway's trimmed field names: an otherwise local file must
	// not hide a remaining URL in a whitespace-padded compatibility field.
	mixed.Reset()
	w = multipart.NewWriter(&mixed)
	file, err := w.CreateFormFile("image", "input.png")
	require.NoError(t, err)
	png, _ := generatedWorkerImageFixtures(t)
	_, err = file.Write(png)
	require.NoError(t, err)
	require.NoError(t, w.WriteField(" image_urls ", "https://example.org/image"))
	require.NoError(t, w.Close())
	require.False(t, asyncImagePreparedRequestEligible(&openAITask, mixed.Bytes(), EndpointImagesEdits, w.FormDataContentType()))
}

type expandedPreparedRequestCipher struct{ resultFetchTestCipher }

func (expandedPreparedRequestCipher) Encrypt(string) (string, error) {
	return strings.Repeat("x", service.AsyncImagePreparedRequestMaxBytes+1), nil
}

func TestAsyncImagePreparedRequestSerializationLimitsLeaveOriginalPayload(t *testing.T) {
	for _, stage := range []string{"json", "ciphertext"} {
		t.Run(stage, func(t *testing.T) {
			task, payload, body, path, contentType := preparedRequestFixture(t, service.PlatformGemini)
			task.RequestPayload = []byte("original encrypted request")
			repo := &preparedRequestTestRepo{generatedWorkerResultRepo: &generatedWorkerResultRepo{task: task}}
			h := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo), encryptor: resultFetchTestCipher{}}
			if stage == "json" {
				padded := bytes.Repeat([]byte(" "), 49<<20)
				copy(padded, body)
				body = padded
			} else {
				h.encryptor = expandedPreparedRequestCipher{}
			}
			require.LessOrEqual(t, len(body), service.AsyncImagePreparedRequestMaxBytes)
			d, stop := h.persistAsyncImagePreparedRequest(context.Background(), context.Background(), task, payload, body, path, contentType, testAsyncImageRetryConfig())
			require.False(t, stop, "outside the optional snapshot cap, preserve the original request path")
			require.False(t, d.requeue)
			require.Zero(t, repo.writes)
			require.Nil(t, payload.Prepared)
			require.Equal(t, []byte("original encrypted request"), task.RequestPayload)
		})
	}
}
