package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/tidwall/gjson"
	_ "golang.org/x/image/webp"
)

type geminiImageOutputCounter struct {
	seenHashes map[string][sha256.Size]byte
	sizeBySlot map[string]string
	slotOrder  []string
}

func newGeminiImageOutputCounter() *geminiImageOutputCounter {
	return &geminiImageOutputCounter{
		seenHashes: make(map[string][sha256.Size]byte),
		sizeBySlot: make(map[string]string),
	}
}

func (c *geminiImageOutputCounter) Count() int {
	if c == nil {
		return 0
	}
	return len(c.slotOrder)
}

func (c *geminiImageOutputCounter) Sizes() []string {
	if c == nil || len(c.sizeBySlot) == 0 {
		return nil
	}
	sizes := make([]string, 0, len(c.sizeBySlot))
	for _, slot := range c.slotOrder {
		if size := c.sizeBySlot[slot]; size != "" {
			sizes = append(sizes, size)
		}
	}
	if len(sizes) == 0 {
		return nil
	}
	return sizes
}

func (c *geminiImageOutputCounter) AddJSONBytes(body []byte) {
	if c == nil || len(body) == 0 || !gjson.ValidBytes(body) {
		return
	}
	var response map[string]any
	if gjson.GetBytes(body, "response").IsObject() {
		body = []byte(gjson.GetBytes(body, "response").Raw)
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return
	}
	c.AddResponse(response)
}

func (c *geminiImageOutputCounter) AddResponse(response map[string]any) {
	if c == nil || response == nil {
		return
	}
	candidates, _ := response["candidates"].([]any)
	for candidateIndex, candidateRaw := range candidates {
		candidate, _ := candidateRaw.(map[string]any)
		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for partIndex, partRaw := range parts {
			part, _ := partRaw.(map[string]any)
			inline, _ := part["inlineData"].(map[string]any)
			if inline == nil {
				inline, _ = part["inline_data"].(map[string]any)
			}
			if inline == nil {
				continue
			}
			encoded := firstNonEmptyAsyncImageString(inline, "data")
			if encoded == "" {
				continue
			}
			mimeType := strings.ToLower(firstNonEmptyAsyncImageString(inline, "mimeType", "mime_type"))
			width, height, decoded := decodeGeminiInlineImageSize(encoded)
			if !strings.HasPrefix(mimeType, "image/") && !decoded {
				continue
			}
			hash := sha256.Sum256([]byte(encoded))
			slot := fmt.Sprintf("%d:%d", candidateIndex, partIndex)
			previousHash, exists := c.seenHashes[slot]
			if exists && previousHash == hash {
				continue
			}
			if !exists {
				c.slotOrder = append(c.slotOrder, slot)
			}
			c.seenHashes[slot] = hash
			if decoded && width > 0 && height > 0 {
				c.sizeBySlot[slot] = fmt.Sprintf("%dx%d", width, height)
			}
		}
	}
}

func decodeGeminiInlineImageSize(encoded string) (int, int, bool) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return 0, 0, false
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		decoder := base64.NewDecoder(encoding, strings.NewReader(encoded))
		cfg, _, err := image.DecodeConfig(decoder)
		if err == nil && cfg.Width > 0 && cfg.Height > 0 {
			return cfg.Width, cfg.Height, true
		}
	}
	return 0, 0, false
}

func applyGeminiImageOutputAccounting(result *ForwardResult, counter *geminiImageOutputCounter) {
	if result == nil || counter == nil {
		return
	}
	result.ImageCount = counter.Count()
	result.ImageOutputSizes = counter.Sizes()
	if len(result.ImageOutputSizes) > 0 {
		result.ImageOutputSize = result.ImageOutputSizes[0]
	}
	ApplyForwardImageBillingResolution(result)
}

// ResolveGeminiForwardModel returns the model that the selected account will
// place on the Gemini upstream request. Keep permission checks and forwarding
// on the same resolver so account-level aliases cannot bypass image controls.
func ResolveGeminiForwardModel(account *Account, requestedModel string) string {
	return resolveGeminiForwardModelWithContext(context.Background(), account, requestedModel, false)
}

func ResolveGeminiForwardModelForRequest(ctx context.Context, account *Account, requestedModel string) string {
	return resolveGeminiForwardModelWithContext(ctx, account, requestedModel, true)
}

func resolveGeminiForwardModelWithContext(ctx context.Context, account *Account, requestedModel string, requestAware bool) string {
	if account == nil {
		return requestedModel
	}
	if account.Platform == PlatformAntigravity && account.Type != AccountTypeAPIKey {
		if requestAware {
			return mapAntigravityModelForRequest(ctx, account, requestedModel)
		}
		return mapAntigravityModel(account, requestedModel)
	}
	if account.Type == AccountTypeAPIKey || account.Type == AccountTypeServiceAccount {
		if requestAware {
			return account.GetMappedModelForRequest(ctx, requestedModel)
		}
		return account.GetMappedModel(requestedModel)
	}
	return requestedModel
}

// IsGeminiNativeImageGenerationIntent recognizes requests that can produce
// inline image data. Reference images alone do not make a request generative.
func IsGeminiNativeImageGenerationIntent(action, model string, body []byte) bool {
	action = strings.TrimSpace(action)
	if action != "generateContent" && action != "streamGenerateContent" {
		return false
	}
	if isImageGenerationModel(model) {
		return true
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	if gjson.GetBytes(body, "generationConfig.imageConfig").IsObject() ||
		gjson.GetBytes(body, "generation_config.image_config").IsObject() {
		return true
	}
	for _, path := range []string{"generationConfig.responseModalities", "generation_config.response_modalities"} {
		modalities := gjson.GetBytes(body, path)
		if !modalities.IsArray() {
			continue
		}
		found := false
		modalities.ForEach(func(_, value gjson.Result) bool {
			found = strings.EqualFold(strings.TrimSpace(value.String()), "IMAGE")
			return !found
		})
		if found {
			return true
		}
	}
	return false
}
