package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type asyncGPTDimensionsRepo struct {
	service.AsyncImageTaskRepository
	created []service.CreateAsyncImageTaskParams
}

func (r *asyncGPTDimensionsRepo) CreateAsyncImageTask(_ context.Context, p service.CreateAsyncImageTaskParams) (*service.AsyncImageTask, bool, error) {
	r.created = append(r.created, p)
	return &service.AsyncImageTask{TaskID: p.TaskID, Platform: p.Platform}, true, nil
}

func asyncGPTDimensionsContext(w http.ResponseWriter, request *http.Request) *gin.Context {
	c, _ := gin.CreateTestContext(w)
	c.Request = request
	groupID := int64(3)
	key := &service.APIKey{
		ID: 9, UserID: 7, User: &service.User{ID: 7}, GroupID: &groupID,
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowImageGeneration: true, AllowAsyncImageGeneration: true},
	}
	c.Set(string(middleware.ContextKeyAPIKey), key)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID})
	return c
}

func TestDurableAsyncGPTImageDimensionsAtSubmission(t *testing.T) {
	const generation = "/v1/images/generations_oa"
	const edits = "/v1/images/edits_oa"
	for _, test := range []struct {
		name, path, model, explicitSize, wantKind string
	}{
		{"JSON 4K generation", generation, "gpt-image-2", "", service.AsyncImageRequestTypeTextToImage},
		{"multipart 4K edit", edits, "gpt-image-2.5-flare", "", service.AsyncImageRequestTypeImageToImage},
		{"JSON oversize generation", generation, "gpt-image-2", "4096x2304", ""},
		{"multipart oversize edit", edits, "gpt-image-2.5-sunburst", "4096x2304", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &asyncGPTDimensionsRepo{}
			cipher := resultFetchTestCipher{}
			gateway := &service.OpenAIGatewayService{}
			h := &DurableAsyncImageHandler{
				tasks: service.NewAsyncImageTaskService(repo), encryptor: cipher,
				openAI: &OpenAIGatewayHandler{gatewayService: gateway},
				storage: service.NewImageStorageSettingService(nil, nil, nil,
					func(context.Context, *config.ImageStorageConfig) (service.ImageStorage, error) {
						return &asyncImageDurableStorageStub{}, nil
					},
					config.ImageStorageConfig{
						Enabled: true, Backend: config.ImageStorageBackendLocal, Provider: config.ImageStorageProviderLocal,
						Local: config.ImageStorageLocalConfig{DataDir: t.TempDir(), LocalURL: "https://images.example.test"},
					}, config.AsyncImageConfig{MaxReferenceImages: 1}),
			}
			fields := map[string]string{"model": test.model, "prompt": "Draw a cat"}
			if test.explicitSize == "" {
				fields["resolution"], fields["aspect_ratio"] = "4K", "3:2"
			} else {
				fields["size"] = test.explicitSize
			}
			body, contentType := asyncGPTDimensionsRequestBody(t, fields, test.path == edits)
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(body))
			request.Header.Set("Content-Type", contentType)
			response := httptest.NewRecorder()
			h.SubmitOpenAIBB(asyncGPTDimensionsContext(response, request))

			if test.wantKind == "" {
				require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
				require.Equal(t, "unsupported_image_dimensions", gjson.GetBytes(response.Body.Bytes(), "error.code").String())
				require.Contains(t, response.Body.String(), "3840")
				require.Empty(t, repo.created, "invalid dimensions must not persist a task or outbox event")
				return
			}
			require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
			require.Len(t, repo.created, 1)
			queued := repo.created[0]
			require.Equal(t, test.wantKind, queued.RequestType)
			require.Equal(t, test.model, queued.Model)
			require.NotNil(t, queued.RequestedImageSize)
			require.Equal(t, "4K", *queued.RequestedImageSize)
			require.NotNil(t, queued.AspectRatio)
			require.Equal(t, "3:2", *queued.AspectRatio)

			plain, err := cipher.Decrypt(string(queued.RequestPayload))
			require.NoError(t, err)
			var payload durableAsyncImagePayload
			require.NoError(t, json.Unmarshal([]byte(plain), &payload))
			require.Equal(t, test.path, payload.SourcePath)
			require.Equal(t, contentType, payload.ContentType)
			require.Equal(t, body, payload.Body)
			cfg, err := h.storage.RuntimeConfig(context.Background())
			require.NoError(t, err)
			workerTask := &service.AsyncImageTask{Platform: queued.Platform, RequestType: queued.RequestType}
			workerBody, workerPath, workerType, err := h.buildAsyncImageUpstreamRequest(context.Background(), workerTask, &payload, cfg, nil)
			require.NoError(t, err)
			workerRequest := httptest.NewRequest(http.MethodPost, workerPath, bytes.NewReader(workerBody))
			workerRequest.Header.Set("Content-Type", workerType)
			parsed, err := gateway.ParseOpenAIImagesRequest(asyncGPTDimensionsContext(httptest.NewRecorder(), workerRequest), workerBody)
			require.NoError(t, err)
			require.Equal(t, "3504x2336", parsed.Size)
		})
	}
}

func asyncGPTDimensionsRequestBody(t *testing.T, fields map[string]string, withReference bool) ([]byte, string) {
	t.Helper()
	if !withReference {
		body, err := json.Marshal(fields)
		require.NoError(t, err)
		return body, "application/json"
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		require.NoError(t, writer.WriteField(name, value))
	}
	file, err := writer.CreateFormFile("image", "reference.png")
	require.NoError(t, err)
	png, _ := generatedWorkerImageFixtures(t)
	_, err = file.Write(png)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return body.Bytes(), writer.FormDataContentType()
}

func TestLegacyAsyncGPTImageRejectsInvalidDimensionsBeforeCreate(t *testing.T) {
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	h := NewAsyncImageHandler(service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute),
		&OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}})
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async",
		strings.NewReader(`{"model":"gpt-image-2","prompt":"Draw a cat","size":"4096x2304"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.Submit(asyncGPTDimensionsContext(response, request))
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "unsupported_image_dimensions")
	require.Contains(t, response.Body.String(), "3840")
	require.Empty(t, store.tasks)
}
