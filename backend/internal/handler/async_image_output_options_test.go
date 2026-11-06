package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type asyncOutputOptionsRepo struct {
	asyncGPTDimensionsRepo
	service.AsyncImageInputObjectRepository
}

func (*asyncOutputOptionsRepo) FindAsyncImageInputObjectsByURLHashes(context.Context, []string) ([]service.AsyncImageInputObject, error) {
	return nil, nil
}

func asyncOutputOptionsBody(t *testing.T, fields map[string]any, encoding string) ([]byte, string) {
	t.Helper()
	if encoding != "multipart" {
		body, err := json.Marshal(fields)
		require.NoError(t, err)
		return body, "application/json"
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if key == "svg" {
			options := value.(map[string]any)
			require.NoError(t, writer.WriteField("svg[model]", options["model"].(string)))
			reasoning := options["reasoning"].(map[string]any)
			require.NoError(t, writer.WriteField("svg[reasoning][effort]", reasoning["effort"].(string)))
			continue
		}
		require.NoError(t, writer.WriteField(key, fmt.Sprint(value)))
	}
	file, err := writer.CreateFormFile("image", "input.png")
	require.NoError(t, err)
	png, _ := generatedWorkerImageFixtures(t)
	_, err = file.Write(png)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return body.Bytes(), writer.FormDataContentType()
}

func TestAsyncOpenAIOutputOptionsSubmissionAndWorker(t *testing.T) {
	for _, encoding := range []string{"json", "json_no_content_type", "multipart", "data_uri"} {
		for _, test := range []struct {
			name, format    string
			count, duration int
			invalid         bool
		}{
			{"png default", "", 0, 0, false},
			{"gif defaults", "gif", 0, 0, false},
			{"gif custom", "gif", 15, 2000, false},
			{"svg", "svg", 0, 0, false},
			{"svg author", "svg", 0, 0, false},
			{"non svg author", "png", 0, 0, true},
			{"count low", "gif", 1, 0, true},
			{"count high", "gif", 17, 0, true},
			{"duration low", "gif", 0, 19, true},
			{"duration high", "gif", 0, 2001, true},
			{"non gif count", "png", 9, 0, true},
			{"format", "bmp", 0, 0, true},
		} {
			t.Run(encoding+"/"+test.name, func(t *testing.T) {
				fields := map[string]any{"model": "gpt-image-2.6-sunburst", "prompt": "Draw an animated cat"}
				if test.name == "svg author" || test.name == "non svg author" {
					fields["svg"] = map[string]any{"model": "test-svg-author", "reasoning": map[string]any{"effort": "high"}}
				}
				if test.format != "" {
					fields["output_format"] = test.format
				}
				if test.count != 0 {
					fields["frame_count"] = test.count
				}
				if test.duration != 0 {
					fields["frame_duration_ms"] = test.duration
				}
				if encoding == "data_uri" {
					png, _ := generatedWorkerImageFixtures(t)
					fields["image_urls"] = []string{"data:image/png;base64," + base64.StdEncoding.EncodeToString(png)}
				}
				body, contentType := asyncOutputOptionsBody(t, fields, encoding)
				if encoding == "json_no_content_type" {
					contentType = ""
				}
				path := "/v1/images/generations_oa"
				if encoding == "multipart" {
					path = "/v1/images/edits_oa"
				}
				repo := &asyncOutputOptionsRepo{}
				cipher := resultFetchTestCipher{}
				gateway := &service.OpenAIGatewayService{}
				h := &DurableAsyncImageHandler{
					tasks: service.NewAsyncImageTaskService(repo), encryptor: cipher,
					openAI: &OpenAIGatewayHandler{gatewayService: gateway},
					storage: service.NewImageStorageSettingService(nil, nil, nil,
						func(context.Context, *config.ImageStorageConfig) (service.ImageStorage, error) {
							return &asyncImageDurableStorageStub{}, nil
						},
						config.ImageStorageConfig{Enabled: true, Backend: config.ImageStorageBackendLocal, Provider: config.ImageStorageProviderLocal,
							Local: config.ImageStorageLocalConfig{DataDir: t.TempDir(), LocalURL: "https://images.example.test"}}, config.AsyncImageConfig{MaxReferenceImages: 1}),
				}
				request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
				request.Header.Set("Content-Type", contentType)
				response := httptest.NewRecorder()
				h.SubmitOpenAIBB(asyncGPTDimensionsContext(response, request))
				if test.invalid {
					require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
					require.Empty(t, repo.created, "invalid options must fail before task persistence")
					return
				}
				require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
				require.Len(t, repo.created, 1)
				queued := repo.created[0]
				require.Equal(t, service.AsyncImageTaskRequestHash(service.PlatformOpenAI, service.AsyncImageProtocolBB, path, body), queued.RequestHash)
				plain, err := cipher.Decrypt(string(queued.RequestPayload))
				require.NoError(t, err)
				var payload durableAsyncImagePayload
				require.NoError(t, json.Unmarshal([]byte(plain), &payload))
				cfg, err := h.storage.RuntimeConfig(context.Background())
				require.NoError(t, err)
				task := &service.AsyncImageTask{Platform: queued.Platform, RequestType: queued.RequestType}
				workerBody, workerPath, workerType, err := h.buildAsyncImageUpstreamRequest(context.Background(), task, &payload, cfg, nil)
				require.NoError(t, err)
				if encoding == "data_uri" {
					require.True(t, isMultipartContentType(workerType))
				}
				workerRequest := httptest.NewRequest(http.MethodPost, workerPath, bytes.NewReader(workerBody))
				workerRequest.Header.Set("Content-Type", workerType)
				parsed, err := gateway.ParseOpenAIImagesRequest(asyncGPTDimensionsContext(httptest.NewRecorder(), workerRequest), workerBody)
				require.NoError(t, err)
				format := test.format
				if format == "" {
					format = "png"
				}
				require.Equal(t, format, parsed.OutputFormat)
				if test.name == "svg author" {
					require.NotNil(t, parsed.SVG)
					require.Equal(t, "test-svg-author", parsed.SVG.Model)
					require.NotNil(t, parsed.SVG.Reasoning)
					require.Equal(t, "high", parsed.SVG.Reasoning.Effort)
				} else {
					require.Nil(t, parsed.SVG, "do not invent an SVG author model")
				}
				if format == "gif" {
					count, duration := test.count, test.duration
					if count == 0 {
						count = 9
					}
					if duration == 0 {
						duration = 200
					}
					require.Equal(t, count, *parsed.FrameCount)
					require.Equal(t, duration, *parsed.FrameDurationMS)
				} else {
					require.Nil(t, parsed.FrameCount)
					require.Nil(t, parsed.FrameDurationMS)
				}
				if encoding == "data_uri" {
					// Simulate a persisted local-reference retry: the prepared bytes
					// must retain both explicit options and the frozen defaults.
					local := service.AsyncImageReferenceTransportLocal
					task.TaskID, task.RequestHash, task.Model = "snapshot-test", queued.RequestHash, queued.Model
					task.ReferenceTransport = &local
					payload.Prepared = &asyncImagePreparedRequest{Version: 1, TaskID: task.TaskID,
						RequestHash: task.RequestHash, Model: task.Model,
						Body: workerBody, Path: workerPath, ContentType: workerType}
					snapshot, err := json.Marshal(payload)
					require.NoError(t, err)
					var restored durableAsyncImagePayload
					require.NoError(t, json.Unmarshal(snapshot, &restored))
					retriedBody, retriedPath, retriedType, err := h.buildAsyncImageUpstreamRequest(context.Background(), task, &restored, cfg, nil)
					require.NoError(t, err)
					require.Equal(t, workerBody, retriedBody)
					require.Equal(t, workerPath, retriedPath)
					require.Equal(t, workerType, retriedType)
				}
			})
		}
	}
}

func TestLegacyAsyncOpenAIOutputOptions(t *testing.T) {
	for _, encoding := range []string{"json", "json_no_content_type", "multipart"} {
		for _, scenario := range []struct {
			format  string
			invalid bool
		}{{"gif", false}, {"gif", true}, {"svg", false}, {"svg", true}} {
			t.Run(fmt.Sprintf("%s/%s/invalid=%t", encoding, scenario.format, scenario.invalid), func(t *testing.T) {
				fields := map[string]any{"model": "gpt-image-2.6-flare", "prompt": "a cat", "output_format": scenario.format}
				if scenario.format == "svg" {
					fields["svg"] = map[string]any{"model": "test-svg-author", "reasoning": map[string]any{"effort": "high"}}
				}
				if scenario.invalid {
					fields["frame_count"] = 17
				}
				body, contentType := asyncOutputOptionsBody(t, fields, encoding)
				if encoding == "json_no_content_type" {
					contentType = ""
				}
				store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
				h := NewAsyncImageHandler(service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute), &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}})
				type forwarded struct {
					body              []byte
					contentType, path string
				}
				sent := make(chan forwarded, 1)
				h.execute = func(_ string, c *gin.Context) {
					data, _ := io.ReadAll(c.Request.Body)
					sent <- forwarded{data, c.GetHeader("Content-Type"), c.Request.URL.Path}
					c.JSON(http.StatusOK, gin.H{"data": []gin.H{{"url": "https://images.example.test/cat.gif"}}})
				}
				path := "/v1/images/generations/async"
				if encoding == "multipart" {
					path = "/v1/images/edits/async"
				}
				request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
				request.Header.Set("Content-Type", contentType)
				response := httptest.NewRecorder()
				h.Submit(asyncGPTDimensionsContext(response, request))
				if scenario.invalid {
					require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
					require.Empty(t, store.tasks)
					return
				}
				require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
				select {
				case actual := <-sent:
					req := httptest.NewRequest(http.MethodPost, actual.path, bytes.NewReader(actual.body))
					req.Header.Set("Content-Type", actual.contentType)
					parsed, err := (&service.OpenAIGatewayService{}).ParseOpenAIImagesRequest(asyncGPTDimensionsContext(httptest.NewRecorder(), req), actual.body)
					require.NoError(t, err)
					require.Equal(t, scenario.format, parsed.OutputFormat)
					if scenario.format == "gif" {
						require.Equal(t, 9, *parsed.FrameCount)
						require.Equal(t, 200, *parsed.FrameDurationMS)
					} else {
						require.NotNil(t, parsed.SVG)
						require.Equal(t, "test-svg-author", parsed.SVG.Model)
						require.Equal(t, "high", parsed.SVG.Reasoning.Effort)
					}
				case <-time.After(time.Second):
					t.Fatal("legacy worker did not forward the request")
				}
				taskID := gjson.GetBytes(response.Body.Bytes(), "task_id").String()
				require.Eventually(t, func() bool {
					task, err := store.Get(context.Background(), taskID)
					return err == nil && task.Status == service.ImageTaskStatusCompleted
				}, time.Second, 10*time.Millisecond)
			})
		}
	}
}
