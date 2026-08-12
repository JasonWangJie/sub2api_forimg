package service

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyGeminiImageConfigFromChatBody(t *testing.T) {
	gemini := []byte(`{"contents":[{"role":"user","parts":[{"text":"draw"}]}],"generationConfig":{"temperature":0.2}}`)
	chat := []byte(`{"extra_body":{"google":{"image_config":{"image_size":"4k","aspect_ratio":"16:9"}}}}`)
	ctx := WithGeminiAsyncImageGeneration(context.Background())
	out, err := ApplyGeminiImageConfigFromChatBody(ctx, gemini, chat)
	require.NoError(t, err)
	require.JSONEq(t, `{
      "contents":[{"role":"user","parts":[{"text":"draw"}]}],
      "generationConfig":{
        "temperature":0.2,
        "imageConfig":{"imageSize":"4K","aspectRatio":"16:9"},
        "responseModalities":["TEXT","IMAGE"]
      }
    }`, string(out))
}

func TestApplyGeminiImageConfigHalfKRequiresInternalContextCapability(t *testing.T) {
	geminiBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"draw"}]}]}`)
	imageCtx := WithGeminiAsyncImageGeneration(context.Background())
	_, err := ApplyGeminiImageConfigFromChatBody(imageCtx, geminiBody, []byte(`{"extra_body":{"google":{"image_config":{"image_size":"0.5K"}}}}`))
	require.ErrorContains(t, err, "unsupported_image_dimensions")

	ctx := WithGeminiHalfKCapability(imageCtx)
	out, err := ApplyGeminiImageConfigFromChatBody(ctx, geminiBody, []byte(`{"extra_body":{"google":{"image_config":{"image_size":"0.5K"}}}}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"contents":[{"role":"user","parts":[{"text":"draw"}]}],"generationConfig":{"imageConfig":{"imageSize":"0.5K"},"responseModalities":["TEXT","IMAGE"]}}`, string(out))
}

func TestApplyGeminiImageConfigUntrustedChatRequestIsNoop(t *testing.T) {
	geminiBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"draw"}]}]}`)
	chatBody := []byte(`{"extra_body":{"google":{"image_config":{"image_size":"0.5K","aspect_ratio":"16:9","allow_0_5k":true}}}}`)

	out, err := ApplyGeminiImageConfigFromChatBody(context.Background(), geminiBody, chatBody)
	require.NoError(t, err)
	require.Equal(t, geminiBody, out)
}

func TestApplyGeminiImageConfigFromChatBodyNoExtensionIsNoop(t *testing.T) {
	gemini := []byte(`{"contents":[]}`)
	out, err := ApplyGeminiImageConfigFromChatBody(context.Background(), gemini, []byte(`{"model":"gemini"}`))
	require.NoError(t, err)
	require.Equal(t, string(gemini), string(out))
}

func TestExtractGeminiGeneratedImages(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("image-bytes"))
	images, err := ExtractGeminiGeneratedImages(map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{
				map[string]any{"text": "done"},
				map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": payload}},
			}},
		}},
	})
	require.NoError(t, err)
	require.Len(t, images, 1)
	require.Equal(t, "image/png", images[0].MIMEType)
	require.Equal(t, []byte("image-bytes"), images[0].Data)
	require.NotEmpty(t, images[0].SHA256)
}

func TestGeminiImageResponseCaptureClonesData(t *testing.T) {
	capture := &GeminiImageResponseCapture{}
	ctx := WithGeminiImageResponseCapture(context.Background(), capture)
	require.Same(t, capture, GeminiImageResponseCaptureFromContext(ctx))

	original := []byte("abc")
	capture.Set([]GeminiGeneratedImage{{MIMEType: "image/png", Data: original}}, []byte(`{"ok":true}`))
	original[0] = 'z'
	got := capture.Images()
	require.Equal(t, []byte("abc"), got[0].Data)
	got[0].Data[0] = 'y'
	require.Equal(t, []byte("abc"), capture.Images()[0].Data)
}
