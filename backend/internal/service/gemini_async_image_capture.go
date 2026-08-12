package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

type geminiAsyncImageCaptureContextKey struct{}
type geminiAsyncImageGenerationContextKey struct{}
type geminiHalfKCapabilityContextKey struct{}

type GeminiGeneratedImage struct {
	MIMEType string `json:"mime_type"`
	Data     []byte `json:"-"`
	SHA256   string `json:"sha256"`
}

type GeminiImageResponseCapture struct {
	mu          sync.RWMutex
	images      []GeminiGeneratedImage
	rawResponse []byte
}

func WithGeminiImageResponseCapture(ctx context.Context, capture *GeminiImageResponseCapture) context.Context {
	if capture == nil {
		return ctx
	}
	return context.WithValue(ctx, geminiAsyncImageCaptureContextKey{}, capture)
}

func GeminiImageResponseCaptureFromContext(ctx context.Context) *GeminiImageResponseCapture {
	if ctx == nil {
		return nil
	}
	capture, _ := ctx.Value(geminiAsyncImageCaptureContextKey{}).(*GeminiImageResponseCapture)
	return capture
}

// WithGeminiAsyncImageGeneration allows the durable worker to translate the
// compatibility request's image_config into Gemini generationConfig. Public
// Chat Completions requests cannot manufacture this private context value.
func WithGeminiAsyncImageGeneration(ctx context.Context) context.Context {
	return context.WithValue(ctx, geminiAsyncImageGenerationContextKey{}, true)
}

func hasGeminiAsyncImageGeneration(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(geminiAsyncImageGenerationContextKey{}).(bool)
	return enabled
}

// WithGeminiHalfKCapability marks an internal worker request as eligible to
// forward Gemini's optional 0.5K image size. The marker lives only in the Go
// context so downstream JSON cannot grant itself this capability.
func WithGeminiHalfKCapability(ctx context.Context) context.Context {
	return context.WithValue(ctx, geminiHalfKCapabilityContextKey{}, true)
}

func hasGeminiHalfKCapability(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(geminiHalfKCapabilityContextKey{}).(bool)
	return enabled
}

func (c *GeminiImageResponseCapture) Set(images []GeminiGeneratedImage, raw []byte) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.images = cloneGeminiGeneratedImages(images)
	c.rawResponse = append(c.rawResponse[:0], raw...)
	c.mu.Unlock()
}

func (c *GeminiImageResponseCapture) Images() []GeminiGeneratedImage {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneGeminiGeneratedImages(c.images)
}

func (c *GeminiImageResponseCapture) Count() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.images)
}

func (c *GeminiImageResponseCapture) RawResponse() []byte {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]byte(nil), c.rawResponse...)
}

func cloneGeminiGeneratedImages(images []GeminiGeneratedImage) []GeminiGeneratedImage {
	out := make([]GeminiGeneratedImage, len(images))
	for i := range images {
		out[i] = images[i]
		out[i].Data = append([]byte(nil), images[i].Data...)
	}
	return out
}

// ApplyGeminiImageConfigFromChatBody adds the downstream image-generation
// extension to a standard Gemini request. With no extra_body this is a no-op,
// preserving the legacy Chat Completions behavior.
func ApplyGeminiImageConfigFromChatBody(ctx context.Context, geminiBody, chatBody []byte) ([]byte, error) {
	if !hasGeminiAsyncImageGeneration(ctx) {
		return geminiBody, nil
	}
	var envelope struct {
		ExtraBody struct {
			Google struct {
				ImageConfig struct {
					ImageSize   string `json:"image_size"`
					AspectRatio string `json:"aspect_ratio"`
				} `json:"image_config"`
			} `json:"google"`
		} `json:"extra_body"`
	}
	if len(chatBody) == 0 || json.Unmarshal(chatBody, &envelope) != nil {
		return geminiBody, nil
	}
	size := strings.ToUpper(strings.TrimSpace(envelope.ExtraBody.Google.ImageConfig.ImageSize))
	ratio := strings.TrimSpace(envelope.ExtraBody.Google.ImageConfig.AspectRatio)
	if size == "" && ratio == "" {
		return geminiBody, nil
	}
	if size == "0.5K" && !hasGeminiHalfKCapability(ctx) {
		return nil, fmt.Errorf("unsupported_image_dimensions: 0.5K is not enabled for this Gemini model")
	}
	if size != "" && size != "0.5K" && size != "1K" && size != "2K" && size != "4K" {
		return nil, fmt.Errorf("unsupported_image_dimensions: unsupported image size %q", size)
	}

	var request map[string]any
	if err := json.Unmarshal(geminiBody, &request); err != nil {
		return nil, fmt.Errorf("parse Gemini request: %w", err)
	}
	generationConfig, _ := request["generationConfig"].(map[string]any)
	if generationConfig == nil {
		generationConfig = make(map[string]any)
	}
	imageConfig, _ := generationConfig["imageConfig"].(map[string]any)
	if imageConfig == nil {
		imageConfig = make(map[string]any)
	}
	if size != "" {
		imageConfig["imageSize"] = size
	}
	if ratio != "" && !strings.EqualFold(ratio, "auto") && ratio != "自动" {
		imageConfig["aspectRatio"] = ratio
	}
	generationConfig["imageConfig"] = imageConfig
	generationConfig["responseModalities"] = []any{"TEXT", "IMAGE"}
	request["generationConfig"] = generationConfig
	return json.Marshal(request)
}

func ExtractGeminiGeneratedImages(response map[string]any) ([]GeminiGeneratedImage, error) {
	if response == nil {
		return nil, errors.New("Gemini image response is empty")
	}
	images := make([]GeminiGeneratedImage, 0, 1)
	candidates, _ := response["candidates"].([]any)
	for _, candidateRaw := range candidates {
		candidate, _ := candidateRaw.(map[string]any)
		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, partRaw := range parts {
			part, _ := partRaw.(map[string]any)
			inline, _ := part["inlineData"].(map[string]any)
			if inline == nil {
				inline, _ = part["inline_data"].(map[string]any)
			}
			if inline == nil {
				continue
			}
			mimeType := firstNonEmptyAsyncImageString(inline, "mimeType", "mime_type")
			encoded := firstNonEmptyAsyncImageString(inline, "data")
			if !strings.HasPrefix(strings.ToLower(mimeType), "image/") || encoded == "" {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || len(data) == 0 {
				return nil, errors.New("Gemini returned invalid base64 image data")
			}
			sum := sha256.Sum256(data)
			images = append(images, GeminiGeneratedImage{
				MIMEType: mimeType,
				Data:     data,
				SHA256:   hex.EncodeToString(sum[:]),
			})
		}
	}
	if len(images) == 0 {
		return nil, errors.New("Gemini response did not contain a generated image")
	}
	return images, nil
}

func firstNonEmptyAsyncImageString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func geminiCapturedImageResponse(images []GeminiGeneratedImage) map[string]any {
	data := make([]any, 0, len(images))
	for _, image := range images {
		data = append(data, map[string]any{
			"b64_json": base64.StdEncoding.EncodeToString(image.Data),
		})
	}
	return map[string]any{"data": data}
}
