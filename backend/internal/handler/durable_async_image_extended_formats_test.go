package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeneratedWorkerGIFSVGStorageQueryAndServe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gifBuffer bytes.Buffer
	palette := color.Palette{color.Black, color.White}
	first := image.NewPaletted(image.Rect(0, 0, 2, 3), palette)
	second := image.NewPaletted(image.Rect(0, 0, 2, 3), palette)
	second.SetColorIndex(1, 1, 1)
	require.NoError(t, gif.EncodeAll(&gifBuffer, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{20, 40}, LoopCount: 0}))
	svg := []byte("<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"2\" height=\"3\"><rect width=\"2\" height=\"3\" fill=\"red\"/></svg>")
	body, err := json.Marshal(map[string]any{"data": []any{
		map[string]any{"b64_json": base64.StdEncoding.EncodeToString(gifBuffer.Bytes())},
		map[string]any{"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(svg)},
	}})
	require.NoError(t, err)
	outputs, err := extractOpenAIAsyncImageOutputs(context.Background(), body, service.AsyncImageRuntimeConfig{DownloadMaxBytes: 1 << 20, DownloadMaxPixels: 100})
	require.NoError(t, err)
	require.Len(t, outputs, 2)
	require.Equal(t, "image/gif", outputs[0].ContentType)
	require.Equal(t, "image/svg+xml", outputs[1].ContentType)
	require.Equal(t, gifBuffer.Bytes(), outputs[0].Data)
	require.Equal(t, svg, outputs[1].Data)

	repo := &generatedWorkerResultRepo{task: &service.AsyncImageTask{
		TaskID: "asyncimg_gif_svg", Version: 1, Platform: service.PlatformOpenAI, UserID: 9,
		Status: service.AsyncImageTaskStatusSucceeded, ImageCount: 2,
		BillingStatus: service.AsyncImageBillingStatusSucceeded,
		SubmittedAt:   time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}}
	for index, output := range outputs {
		width, height := output.Width, output.Height
		repo.staging = append(repo.staging, service.AsyncImageStagingObject{
			TaskID: repo.task.TaskID, ImageIndex: index, Content: output.Data,
			ContentType: output.ContentType, ByteSize: int64(len(output.Data)), Checksum: output.Checksum,
			Width: &width, Height: &height,
		})
	}
	h, _, root := newGeneratedWorkerLocalHandler(t, repo)
	signKey := []byte("generated-result-test-sign-key")
	h.storage.WithLocalDefaults(root, signKey)
	require.NoError(t, h.uploadAsyncImageStaging(context.Background(), repo.task))
	require.Len(t, repo.results, 2)
	for index, result := range repo.results {
		require.Equal(t, []string{".gif", ".svg"}[index], filepath.Ext(result.ObjectKey))
		require.Equal(t, outputs[index].ContentType, result.ContentType)
		stored, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(result.ObjectKey)))
		require.NoError(t, err)
		require.Equal(t, outputs[index].Data, stored)
		signed, err := service.SignLocalImageURL("https://api.example", result.ObjectKey, time.Now().Add(time.Minute), signKey)
		require.NoError(t, err)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Params = gin.Params{{Key: "object_key", Value: "/" + result.ObjectKey}}
		c.Request = httptest.NewRequest(http.MethodGet, signed, nil)
		h.ServeLocalObject(c)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, outputs[index].Data, recorder.Body.Bytes())
		require.Equal(t, outputs[index].ContentType, recorder.Header().Get("Content-Type"))
		require.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
		if index == 1 {
			require.Contains(t, recorder.Header().Get("Content-Security-Policy"), "sandbox")
			require.Contains(t, recorder.Header().Get("Content-Security-Policy"), "default-src 'none'")
		}
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
		require.Contains(t, recorder.Body.String(), repo.results[0].ObjectKey)
		require.Contains(t, recorder.Body.String(), repo.results[1].ObjectKey)
		require.NotContains(t, recorder.Body.String(), "base64")
	}
}
