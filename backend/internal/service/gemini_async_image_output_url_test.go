package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func generatedGeminiParts(parts ...any) map[string]any {
	return map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": parts}}}}
}

func TestExtractGeminiGeneratedImageURLShapes(t *testing.T) {
	rawURL := "https://cdn.example/output.jpeg?sig=private"
	tests := []struct {
		name string
		body map[string]any
	}{
		{"fileData", generatedGeminiParts(map[string]any{"fileData": map[string]any{"mimeType": "image/jpeg", "fileUri": rawURL}})},
		{"file_data", generatedGeminiParts(map[string]any{"file_data": map[string]any{"mime_type": "image/jpeg", "file_uri": rawURL}})},
		{"inline URL", generatedGeminiParts(map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": rawURL}})},
		{"encoded URL", generatedGeminiParts(map[string]any{"inline_data": map[string]any{"mime_type": "image/png", "data": base64.StdEncoding.EncodeToString([]byte(rawURL))}})},
		{"top URL", map[string]any{"url": rawURL}},
		{"data URL", map[string]any{"data": []any{map[string]any{"url": rawURL}}}},
		{"text URL", generatedGeminiParts(map[string]any{"text": " \n" + rawURL + "\n"})},
		{"markdown image", generatedGeminiParts(map[string]any{"text": "![result](" + rawURL + ")"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			images, err := ExtractGeminiGeneratedImages(tt.body)
			require.NoError(t, err)
			require.Len(t, images, 1)
			require.Equal(t, rawURL, images[0].SourceURL)
			require.Empty(t, images[0].Data)
			require.Empty(t, images[0].SHA256)
			require.Equal(t, rawURL, geminiCapturedImageResponse(images)["data"].([]any)[0].(map[string]any)["url"])
		})
	}
}

func TestExtractGeminiGeneratedImagesMixedStructuredPartsWinOverLinks(t *testing.T) {
	url := "https://cdn.example/image.jpg"
	body := generatedGeminiParts(
		map[string]any{"inlineData": map[string]any{"mimeType": "image/jpeg", "data": asyncImageOnePixelPNG}},
		map[string]any{"fileData": map[string]any{"mimeType": "image/jpeg", "fileUri": url}},
		map[string]any{"text": "![duplicate](" + url + ")"},
	)
	body["url"] = "https://cdn.example/fallback.png"
	body["data"] = []any{map[string]any{"url": "https://cdn.example/fallback2.png"}}
	images, err := ExtractGeminiGeneratedImages(body)
	require.NoError(t, err)
	require.Len(t, images, 2)
	require.Equal(t, mustDecodeAsyncImagePNG(t), images[0].Data)
	require.Equal(t, "image/jpeg", images[0].MIMEType, "Worker resolves the actual format")
	require.Equal(t, url, images[1].SourceURL)
	capture := &GeminiImageResponseCapture{}
	capture.Set(images, nil)
	require.Equal(t, 2, capture.Count())
	require.Equal(t, url, capture.Images()[1].SourceURL)
}

func TestExtractGeminiGeneratedImageTextDoesNotFetchProse(t *testing.T) {
	for _, text := range []string{
		"I cannot generate this. See https://cdn.example/policy.png",
		"Here is your result: ![image](https://cdn.example/image.png)",
		"![one](https://cdn.example/1.png) ![two](https://cdn.example/2.png)",
		"![one](https://cdn.example/1.png)![two](https://cdn.example/2.png)",
		"https://user:password@cdn.example/image.png",
	} {
		_, err := ExtractGeminiGeneratedImages(generatedGeminiParts(map[string]any{"text": text}))
		require.ErrorContains(t, err, "did not contain a generated image")
	}
}

func TestExtractGeminiAsyncImagePlainResponse(t *testing.T) {
	rawURL := "https://cdn.example/output.jpg?sig=private"
	for _, body := range []string{rawURL, `"` + rawURL + `"`, "![image](" + rawURL + ")"} {
		images, err := extractGeminiAsyncImageResponse([]byte(body))
		require.NoError(t, err)
		require.Len(t, images, 1)
		require.Equal(t, rawURL, images[0].SourceURL)
	}
	for _, body := range []string{`{"candidates":`, "No image was generated", "See " + rawURL} {
		_, err := extractGeminiAsyncImageResponse([]byte(body))
		require.ErrorContains(t, err, "invalid image response")
	}
}

func TestGeminiAsyncCompletedResponseDefersURLsAndParseErrorsToWorker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		response  map[string]any
		wantError bool
	}{
		{"URL is not fetched in forwarding", generatedGeminiParts(map[string]any{"fileData": map[string]any{"mimeType": "image/png", "fileUri": "https://127.0.0.1/not-fetched.png"}}), false},
		{"invalid base64 is not a retryable 502", generatedGeminiParts(map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": "invalid base64!"}}), true},
		{"completed response without an image", generatedGeminiParts(map[string]any{"text": "No image was generated"}), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := &GeminiImageResponseCapture{}
			ctx := WithGeminiImageResponseCapture(context.Background(), capture)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
			body, err := json.Marshal(tt.response)
			require.NoError(t, err)
			resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}
			svc := &GeminiMessagesCompatService{cfg: &config.Config{}}
			_, err = svc.handleChatCompletionsNonStreamingResponseFromGemini(c, resp, "gemini-3-pro-image-preview", false)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, body, capture.RawResponse())
			if tt.wantError {
				require.Error(t, capture.Error())
				require.Zero(t, capture.Count())
				require.JSONEq(t, `{"data":[]}`, recorder.Body.String())
			} else {
				require.NoError(t, capture.Error())
				require.Equal(t, 1, capture.Count())
			}
		})
	}
}

func TestGeminiImageResponseCaptureErrorClearsOnNextResult(t *testing.T) {
	capture := &GeminiImageResponseCapture{}
	_, err := ExtractGeminiGeneratedImages(nil)
	capture.SetError(err, []byte(`{}`))
	require.Error(t, capture.Error())
	capture.Set([]GeminiGeneratedImage{{SourceURL: "https://cdn.example/result.png"}}, nil)
	require.NoError(t, capture.Error())
	require.Equal(t, 1, capture.Count())
}
