package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

type geminiImageGenerationIntentContextKey struct{}

func WithGeminiImageGenerationIntent(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, geminiImageGenerationIntentContextKey{}, true)
}

func GeminiImageGenerationIntentFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(geminiImageGenerationIntentContextKey{}).(bool)
	return enabled
}

type geminiAsyncImageCaptureContextKey struct{}
type geminiAsyncImageGenerationContextKey struct{}
type geminiHalfKCapabilityContextKey struct{}

type GeminiGeneratedImage struct {
	MIMEType string `json:"mime_type"`
	Data     []byte `json:"-"`
	SHA256   string `json:"sha256"`
	// SourceURL is resolved by the durable worker, after forwarding has ended.
	SourceURL string `json:"-"`
}

type GeminiImageResponseCapture struct {
	mu          sync.RWMutex
	images      []GeminiGeneratedImage
	rawResponse []byte
	err         error
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

// IsGeminiAsyncImageGeneration reports whether a request belongs to the durable
// image worker. Handlers use it to avoid retrying an already-failed account.
func IsGeminiAsyncImageGeneration(ctx context.Context) bool {
	return hasGeminiAsyncImageGeneration(ctx)
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
	c.err = nil
	c.mu.Unlock()
}

// SetError preserves a complete but unusable upstream result. Returning it to
// the worker avoids treating result parsing as an upstream 502 and regenerating.
func (c *GeminiImageResponseCapture) SetError(err error, raw []byte) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.images = nil
	c.rawResponse = append(c.rawResponse[:0], raw...)
	c.err = err
	c.mu.Unlock()
}

func (c *GeminiImageResponseCapture) Error() error {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.err
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

// Some compatible image providers return only a URL instead of Gemini JSON.
// This parsing path is used only by the private asynchronous image capture.
func extractGeminiAsyncImageResponse(body []byte) ([]GeminiGeneratedImage, error) {
	var response map[string]any
	if err := json.Unmarshal(body, &response); err == nil && response != nil {
		return ExtractGeminiGeneratedImages(response)
	}
	text := string(body)
	var jsonText string
	if json.Unmarshal(body, &jsonText) == nil {
		text = jsonText
	}
	if rawURL := generatedImageTextURL(text); rawURL != "" {
		return []GeminiGeneratedImage{{SourceURL: rawURL}}, nil
	}
	return nil, errors.New("gemini returned an invalid image response")
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
		return nil, errors.New("gemini image response is empty")
	}
	images := make([]GeminiGeneratedImage, 0, 1)
	var textParts []string
	candidates, _ := response["candidates"].([]any)
	for _, candidateRaw := range candidates {
		candidate, _ := candidateRaw.(map[string]any)
		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, partRaw := range parts {
			part, _ := partRaw.(map[string]any)
			if text := firstNonEmptyAsyncImageString(part, "text"); text != "" {
				textParts = append(textParts, text)
			}
			inline, _ := part["inlineData"].(map[string]any)
			if inline == nil {
				inline, _ = part["inline_data"].(map[string]any)
			}
			if inline != nil {
				mimeType := firstNonEmptyAsyncImageString(inline, "mimeType", "mime_type")
				encoded := firstNonEmptyAsyncImageString(inline, "data")
				if encoded != "" && (mimeType == "" || strings.HasPrefix(strings.ToLower(mimeType), "image/")) {
					generated := GeminiGeneratedImage{MIMEType: mimeType}
					if rawURL := generatedImageURLValue(encoded); rawURL != "" {
						generated.SourceURL = rawURL
					} else {
						data, err := base64.StdEncoding.DecodeString(encoded)
						if err != nil || len(data) == 0 {
							return nil, errors.New("gemini returned invalid base64 image data")
						}
						if rawURL := generatedImageURLValue(string(data)); rawURL != "" {
							generated.SourceURL = rawURL
						} else {
							sum := sha256.Sum256(data)
							generated.Data = data
							generated.SHA256 = hex.EncodeToString(sum[:])
						}
					}
					images = append(images, generated)
					continue
				}
			}
			fileData, _ := part["fileData"].(map[string]any)
			if fileData == nil {
				fileData, _ = part["file_data"].(map[string]any)
			}
			mimeType := firstNonEmptyAsyncImageString(fileData, "mimeType", "mime_type")
			if rawURL := firstNonEmptyAsyncImageString(fileData, "fileUri", "file_uri"); rawURL != "" &&
				(mimeType == "" || strings.HasPrefix(strings.ToLower(mimeType), "image/")) {
				images = append(images, GeminiGeneratedImage{MIMEType: mimeType, SourceURL: rawURL})
			}
		}
	}
	// Structured Gemini image parts win over compatibility envelopes and text,
	// so a textual link to the same output does not duplicate it or its billing.
	if len(images) == 0 {
		items, _ := response["data"].([]any)
		for _, itemRaw := range items {
			item, _ := itemRaw.(map[string]any)
			if rawURL := firstNonEmptyAsyncImageString(item, "url"); rawURL != "" {
				images = append(images, GeminiGeneratedImage{SourceURL: rawURL})
			}
		}
		if len(images) == 0 {
			if rawURL := firstNonEmptyAsyncImageString(response, "url"); rawURL != "" {
				images = append(images, GeminiGeneratedImage{SourceURL: rawURL})
			}
		}
	}
	if len(images) == 0 {
		for _, text := range textParts {
			if rawURL := generatedImageTextURL(text); rawURL != "" {
				images = append(images, GeminiGeneratedImage{SourceURL: rawURL})
			}
		}
	}
	if len(images) == 0 {
		return nil, errors.New("gemini response did not contain a generated image")
	}
	return images, nil
}

// Only complete URL values are accepted; ordinary prose and refusal messages
// containing a link must not turn into a generated image.
func generatedImageURLValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, " \t\r\n") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil ||
		(!strings.EqualFold(u.Scheme, "https") && !strings.EqualFold(u.Scheme, "http")) {
		return ""
	}
	return raw
}

func generatedImageTextURL(text string) string {
	text = strings.TrimSpace(text)
	if rawURL := generatedImageURLValue(text); rawURL != "" {
		return rawURL
	}
	if strings.HasPrefix(text, "![") && strings.HasSuffix(text, ")") && strings.Count(text, "![") == 1 {
		if closing := strings.Index(text, "]("); closing >= 2 && !strings.ContainsAny(text[2:closing], "\r\n") {
			return generatedImageURLValue(text[closing+2 : len(text)-1])
		}
	}
	return ""
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
		if image.SourceURL != "" {
			data = append(data, map[string]any{"url": image.SourceURL})
			continue
		}
		data = append(data, map[string]any{
			"b64_json": base64.StdEncoding.EncodeToString(image.Data),
		})
	}
	return map[string]any{"data": data}
}
