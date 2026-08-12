package service

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func encodedGeminiAccountingPNG(t *testing.T, width, height int, shade uint8) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.SetRGBA(0, 0, color.RGBA{R: shade, G: 10, B: 20, A: 255})
	var out bytes.Buffer
	require.NoError(t, png.Encode(&out, img))
	return base64.StdEncoding.EncodeToString(out.Bytes())
}

func geminiAccountingResponse(parts ...any) map[string]any {
	return map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": parts},
		}},
	}
}

func TestGeminiImageOutputCounterCollectsEveryPartAndRealDimensions(t *testing.T) {
	first := encodedGeminiAccountingPNG(t, 1024, 768, 1)
	second := encodedGeminiAccountingPNG(t, 3840, 2160, 2)
	counter := newGeminiImageOutputCounter()
	counter.AddResponse(geminiAccountingResponse(
		map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": first}},
		map[string]any{"inline_data": map[string]any{"mime_type": "image/png", "data": second}},
	))

	require.Equal(t, 2, counter.Count())
	require.Equal(t, []string{"1024x768", "3840x2160"}, counter.Sizes())

	result := &ForwardResult{ImageInputSize: "1K"}
	applyGeminiImageOutputAccounting(result, counter)
	require.Equal(t, 2, result.ImageCount)
	require.Equal(t, "1K", result.ImageSize)
	require.Equal(t, ImageSizeSourceInput, result.ImageSizeSource)
	require.Equal(t, map[string]int{"1K": 2}, result.ImageSizeBreakdown)
}

func TestGeminiImageOutputCounterTreatsChangingStreamSlotAsOneImage(t *testing.T) {
	counter := newGeminiImageOutputCounter()
	counter.AddResponse(geminiAccountingResponse(map[string]any{
		"inlineData": map[string]any{"mimeType": "image/png", "data": encodedGeminiAccountingPNG(t, 512, 512, 1)},
	}))
	counter.AddResponse(geminiAccountingResponse(map[string]any{
		"inlineData": map[string]any{"mimeType": "image/png", "data": encodedGeminiAccountingPNG(t, 2048, 1152, 2)},
	}))

	require.Equal(t, 1, counter.Count())
	require.Equal(t, []string{"2048x1152"}, counter.Sizes())
}

func TestGeminiImageOutputCounterInvalidImageUsesRequestedTierFallback(t *testing.T) {
	counter := newGeminiImageOutputCounter()
	counter.AddResponse(geminiAccountingResponse(map[string]any{
		"inlineData": map[string]any{"mimeType": "image/png", "data": "not-valid-base64"},
	}))
	result := &ForwardResult{ImageInputSize: "4K"}
	applyGeminiImageOutputAccounting(result, counter)

	require.Equal(t, 1, result.ImageCount)
	require.Empty(t, result.ImageOutputSizes)
	require.Equal(t, "4K", result.ImageSize)
	require.Equal(t, ImageSizeSourceInput, result.ImageSizeSource)
}

func TestIsGeminiNativeImageGenerationIntent(t *testing.T) {
	tests := []struct {
		name   string
		action string
		model  string
		body   string
		want   bool
	}{
		{name: "image model", action: "generateContent", model: "gemini-3-pro-image", body: `{}`, want: true},
		{name: "response modality", action: "generateContent", model: "gemini-2.5-pro", body: `{"generationConfig":{"responseModalities":["TEXT","IMAGE"]}}`, want: true},
		{name: "image config", action: "streamGenerateContent", model: "gemini-2.5-pro", body: `{"generationConfig":{"imageConfig":{"imageSize":"2K"}}}`, want: true},
		{name: "reference only", action: "generateContent", model: "gemini-2.5-pro", body: `{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png","data":"aW1n"}}]}]}`, want: false},
		{name: "count tokens", action: "countTokens", model: "gemini-3-pro-image", body: `{}`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsGeminiNativeImageGenerationIntent(tt.action, tt.model, []byte(tt.body)))
		})
	}
}

func TestCollectGeminiSSECountsImageSlotsWithoutDuplicateFinalFrame(t *testing.T) {
	partial := encodedGeminiAccountingPNG(t, 512, 512, 1)
	final := encodedGeminiAccountingPNG(t, 2048, 2048, 2)
	body := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + partial + `"}}]}}]}`,
		`data: {"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + final + `"}}]}}]}`,
		"data: [DONE]",
	}, "\n\n")
	_, _, counter, err := collectGeminiSSE(strings.NewReader(body), false)
	require.NoError(t, err)
	require.Equal(t, 1, counter.Count())
	require.Equal(t, []string{"2048x2048"}, counter.Sizes())
}

func TestHandleNativeNonStreamingResponseReturnsActualImageAccounting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini:generateContent", nil)
	encoded := encodedGeminiAccountingPNG(t, 1536, 1024, 3)
	body := `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + encoded + `"}}]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":4}}`
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}

	result, err := (&GeminiMessagesCompatService{}).handleNativeNonStreamingResponse(c, resp, false)
	require.NoError(t, err)
	require.Equal(t, 1, result.imageCounter.Count())
	require.Equal(t, []string{"1536x1024"}, result.imageCounter.Sizes())
	require.Equal(t, http.StatusOK, recorder.Code)
}
