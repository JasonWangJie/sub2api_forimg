package service

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strings"
)

const maxOpenAIImageDimensionProbeBytes int64 = 1 << 20

// OpenAI image workbench / async clients select resolution + aspect ratio.
// Upstream OpenAI Images still expects a concrete WxH `size` (or "auto").
var openAIImageSizeByResolutionAspect = map[string]map[string]string{
	"1K": {
		"1:1":  "1024x1024",
		"3:2":  "1536x1024",
		"2:3":  "1024x1536",
		"16:9": "1536x1024",
		"9:16": "1024x1536",
		"5:4":  "1280x1024",
		"4:5":  "1024x1280",
		"4:3":  "1360x1024",
		"3:4":  "1024x1360",
		"21:9": "2384x1024",
		"9:21": "1024x2384",
		"2:1":  "2048x1024",
		"1:2":  "1024x2048",
	},
	"2K": {
		"1:1":  "2048x2048",
		"3:2":  "2048x1152",
		"2:3":  "1152x2048",
		"16:9": "2048x1152",
		"9:16": "1152x2048",
		"5:4":  "2048x1632",
		"4:5":  "1632x2048",
		"4:3":  "2048x1536",
		"3:4":  "1536x2048",
		"21:9": "2048x880",
		"9:21": "880x2048",
		"2:1":  "2048x1024",
		"1:2":  "1024x2048",
	},
	"4K": {
		"1:1":  "4096x4096",
		"3:2":  "4096x2304",
		"2:3":  "2304x4096",
		"16:9": "4096x2304",
		"9:16": "2304x4096",
		"5:4":  "4096x3272",
		"4:5":  "3272x4096",
		"4:3":  "4096x3072",
		"3:4":  "3072x4096",
		"21:9": "4096x1752",
		"9:21": "1752x4096",
		"2:1":  "4096x2048",
		"1:2":  "2048x4096",
	},
}

// Each preset is the largest exact ratio with 16-pixel alignment within the
// GPT Image 2 family's edge and total pixel limits.
var gptImage2Size4KByAspect = map[string]string{
	"1:1":  "2880x2880",
	"3:2":  "3504x2336",
	"2:3":  "2336x3504",
	"16:9": "3840x2160",
	"9:16": "2160x3840",
	"5:4":  "3200x2560",
	"4:5":  "2560x3200",
	"4:3":  "3264x2448",
	"3:4":  "2448x3264",
	"21:9": "3808x1632",
	"9:21": "1632x3808",
	"2:1":  "3840x1920",
	"1:2":  "1920x3840",
}

func usesGPTImage2SizeLimits(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "gpt-image-2", "gpt-image-2-2026-04-21",
		"gpt-image-2.5-flare", "gpt-image-2.5-flare-2026-09-08",
		"gpt-image-2.5-sunburst", "gpt-image-2.5-sunburst-2026-09-08":
		return true
	default:
		return false
	}
}

func openAIImageWorkbenchResolutions() []string {
	return []string{"1K", "2K", "4K"}
}

func openAIImageWorkbenchAspectRatios() []string {
	return []string{
		"auto", "1:1", "2:3", "3:2", "4:5", "5:4", "4:3", "3:4",
		"16:9", "9:16", "21:9", "9:21", "2:1", "1:2",
	}
}

func normalizeOpenAIImageResolution(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "1K":
		return "1K"
	case "2K":
		return "2K"
	case "4K":
		return "4K"
	case "AUTO":
		return "auto"
	default:
		return ""
	}
}

func normalizeOpenAIImageAspectRatio(raw string) string {
	ratio := strings.TrimSpace(raw)
	if ratio == "" {
		return ""
	}
	lower := strings.ToLower(ratio)
	if lower == "auto" || ratio == "自动" {
		return "auto"
	}
	parts := strings.Split(ratio, ":")
	if len(parts) != 2 {
		return ""
	}
	left := strings.TrimSpace(parts[0])
	right := strings.TrimSpace(parts[1])
	if left == "" || right == "" {
		return ""
	}
	return left + ":" + right
}

func normalizeOpenAIImageWxHSize(raw string) (string, bool) {
	size := strings.NewReplacer("*", "x", "X", "x", "\u00d7", "x").Replace(strings.TrimSpace(raw))
	width, height, ok := parseImageBillingDimensions(size)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%dx%d", width, height), true
}

// MapOpenAIImageDimensions converts resolution + aspect_ratio using the legacy
// OpenAI Images presets, without model-specific overrides.
func MapOpenAIImageDimensions(resolution, aspectRatio string) (string, error) {
	return mapOpenAIImageDimensions("", resolution, aspectRatio)
}

func mapOpenAIImageDimensions(model, resolution, aspectRatio string) (string, error) {
	rawResolution := strings.TrimSpace(resolution)
	rawAspect := strings.TrimSpace(aspectRatio)
	resolution = normalizeOpenAIImageResolution(rawResolution)
	aspectRatio = normalizeOpenAIImageAspectRatio(rawAspect)
	if resolution == "" {
		return "", fmt.Errorf("unsupported_image_dimensions: unsupported resolution %q", rawResolution)
	}
	if resolution == "auto" {
		return "auto", nil
	}
	if rawAspect != "" && aspectRatio == "" {
		return "", fmt.Errorf("unsupported_image_dimensions: unsupported aspect_ratio %q", rawAspect)
	}
	if aspectRatio == "auto" {
		// Upstream OpenAI Images accepts size=auto and picks dimensions itself.
		return "auto", nil
	}
	if aspectRatio == "" {
		aspectRatio = "1:1"
	}
	byRatio, ok := openAIImageSizeByResolutionAspect[resolution]
	if !ok {
		return "", fmt.Errorf("unsupported_image_dimensions: unsupported resolution %q", rawResolution)
	}
	if resolution == ImageBillingSize4K && usesGPTImage2SizeLimits(model) {
		byRatio = gptImage2Size4KByAspect
	}
	size, ok := byRatio[aspectRatio]
	if !ok {
		return "", fmt.Errorf("unsupported_image_dimensions: unsupported aspect_ratio %q for resolution %q", aspectRatio, resolution)
	}
	return size, nil
}

// normalizeOpenAIImagesDimensions accepts several client shapes:
//   - resolution + aspect_ratio
//   - resolution + size as aspect ratio (e.g. size="9:16")
//   - size as WxH / auto (legacy OpenAI Images)
//   - size as 1K/2K/4K (treated as resolution)
//
// Explicit aspect_ratio wins over size-as-ratio. A concrete WxH size is
// authoritative and must agree with any supplied resolution tier.
func normalizeOpenAIImagesDimensions(req *OpenAIImagesRequest) error {
	if req == nil {
		return nil
	}

	rawResolution := strings.TrimSpace(req.Resolution)
	rawAspect := strings.TrimSpace(req.AspectRatio)
	if rawResolution != "" {
		req.Resolution = normalizeOpenAIImageResolution(rawResolution)
		if req.Resolution == "" {
			return fmt.Errorf("unsupported_image_dimensions: unsupported resolution %q", rawResolution)
		}
	} else {
		req.Resolution = ""
	}
	if rawAspect != "" {
		req.AspectRatio = normalizeOpenAIImageAspectRatio(rawAspect)
		if req.AspectRatio == "" {
			return fmt.Errorf("unsupported_image_dimensions: unsupported aspect_ratio %q", rawAspect)
		}
	} else {
		req.AspectRatio = ""
	}

	size := strings.TrimSpace(req.Size)
	if size != "" {
		canonicalSize, hasPixelSize := normalizeOpenAIImageWxHSize(size)
		switch {
		case strings.EqualFold(size, "auto"):
			// An explicit auto size is intentionally billed from the resulting
			// image when it is available. Do not let a companion resolution lower
			// the pre-selection tier while the upstream can choose a larger size.
			req.Size = "auto"
			req.SizeTier = ImageBillingSize2K
			if req.Resolution != "" {
				req.NeedsSizeRewrite = true
			}
			return nil
		case hasPixelSize:
			tier := NormalizeImageBillingTierOrDefault(canonicalSize)
			if req.Resolution != "" && req.Resolution != "auto" && req.Resolution != tier {
				return fmt.Errorf(
					"unsupported_image_dimensions: size %q is %s and conflicts with resolution %q",
					canonicalSize,
					tier,
					req.Resolution,
				)
			}
			req.Size = canonicalSize
			req.SizeTier = tier
			req.NeedsSizeRewrite = canonicalSize != size || req.Resolution != ""
			// A concrete pixel size controls upstream output, account selection,
			// and billing. Resolution is only an alias and must agree with it.
			return nil
		case normalizeOpenAIImageAspectRatio(size) != "":
			// Clients may send aspect ratio in `size` (e.g. "9:16").
			if req.AspectRatio == "" {
				req.AspectRatio = normalizeOpenAIImageAspectRatio(size)
			}
			req.Size = ""
		case normalizeOpenAIImageResolution(size) != "" && normalizeOpenAIImageResolution(size) != "auto":
			// Treat size="1K"/"2K"/"4K" as resolution for convenience.
			if req.Resolution == "" {
				req.Resolution = normalizeOpenAIImageResolution(size)
			}
			req.Size = ""
		default:
			// Unknown legacy size values are owned by the upstream endpoint. Keep
			// them intact instead of blocking passthrough. Dimension aliases stay
			// strict because combining them with an unknown size is ambiguous.
			if req.Resolution == "" && req.AspectRatio == "" {
				req.Size = size
				return nil
			}
			return fmt.Errorf("unsupported_image_dimensions: unsupported size %q", size)
		}
	}

	if req.Resolution == "" && req.AspectRatio == "" {
		return nil
	}
	if req.Resolution == "" {
		return fmt.Errorf("unsupported_image_dimensions: resolution is required when aspect_ratio is set")
	}

	mapped, err := mapOpenAIImageDimensions(req.Model, req.Resolution, req.AspectRatio)
	if err != nil {
		return err
	}
	req.Size = mapped
	req.ExplicitSize = true
	req.NeedsSizeRewrite = true
	req.sizeFromAliases = true
	if req.Resolution == "auto" || req.Size == "auto" {
		req.SizeTier = ImageBillingSize2K
	} else {
		req.SizeTier = NormalizeImageBillingTierOrDefault(req.Resolution)
	}
	return nil
}

// ValidateAsyncDimensions checks the resolved size before an async image task
// is created. Synchronous image requests retain their upstream passthrough.
func (req *OpenAIImagesRequest) ValidateAsyncDimensions() error {
	if req == nil || !usesGPTImage2SizeLimits(req.Model) || req.Size == "" || strings.EqualFold(req.Size, "auto") {
		return nil
	}
	width, height, ok := parseImageBillingDimensions(req.Size)
	var reason string
	switch {
	case !ok:
		reason = "size must be WIDTHxHEIGHT or auto"
	case max(width, height) > 3840:
		reason = "the longest edge must be at most 3840 pixels"
	case width%16 != 0 || height%16 != 0:
		reason = "both edges must be multiples of 16 pixels"
	case max(width, height) > 3*min(width, height):
		reason = "the ratio of the long edge to the short edge must be at most 3:1"
	case width*height < 655_360 || width*height > 8_294_400:
		reason = "the total pixel count must be between 655,360 and 8,294,400"
	default:
		return nil
	}
	return fmt.Errorf("unsupported_image_dimensions: invalid size %q for %s: %s. Use a supported size such as 3504x2336 (3:2), 3840x2160 (16:9), or auto", req.Size, req.Model, reason)
}

func OpenAIImagesBillingInputSize(req *OpenAIImagesRequest) string {
	return openAIImagesBillingInputSize(req)
}

func openAIImagesBillingInputSize(req *OpenAIImagesRequest) string {
	if req == nil {
		return ""
	}
	if req.sizeFromAliases && !strings.EqualFold(strings.TrimSpace(req.Size), "auto") {
		if resolution := strings.TrimSpace(req.Resolution); resolution != "" && resolution != "auto" {
			return resolution
		}
	}
	if size := strings.TrimSpace(req.Size); size != "" {
		if strings.EqualFold(size, "auto") {
			return "auto"
		}
		if _, ok := normalizeOpenAIImageWxHSize(size); ok {
			return size
		}
	}
	if resolution := strings.TrimSpace(req.Resolution); resolution != "" && resolution != "auto" {
		return resolution
	}
	return strings.TrimSpace(req.Size)
}

func detectOpenAIImageResultSize(encoded string) string {
	payload := strings.TrimSpace(encoded)
	if strings.HasPrefix(strings.ToLower(payload), "data:") {
		comma := strings.IndexByte(payload, ',')
		if comma < 0 || comma+1 >= len(payload) {
			return ""
		}
		payload = strings.TrimSpace(payload[comma+1:])
	}
	if payload == "" {
		return ""
	}

	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		decoded := base64.NewDecoder(encoding, strings.NewReader(payload))
		buffered := bufio.NewReader(io.LimitReader(decoded, maxOpenAIImageDimensionProbeBytes))
		prefix, _ := buffered.Peek(30)
		if width, height, ok := detectOpenAIWebPDimensions(prefix); ok {
			return fmt.Sprintf("%dx%d", width, height)
		}
		cfg, _, err := image.DecodeConfig(buffered)
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
			continue
		}
		return fmt.Sprintf("%dx%d", cfg.Width, cfg.Height)
	}
	return ""
}

func detectOpenAIWebPDimensions(header []byte) (int, int, bool) {
	if len(header) < 16 || string(header[:4]) != "RIFF" || string(header[8:12]) != "WEBP" {
		return 0, 0, false
	}

	switch string(header[12:16]) {
	case "VP8X":
		if len(header) < 30 {
			return 0, 0, false
		}
		width := 1 + int(header[24]) + int(header[25])<<8 + int(header[26])<<16
		height := 1 + int(header[27]) + int(header[28])<<8 + int(header[29])<<16
		return width, height, width > 0 && height > 0
	case "VP8 ":
		if len(header) < 30 || string(header[23:26]) != "\x9d\x01\x2a" {
			return 0, 0, false
		}
		width := int(binary.LittleEndian.Uint16(header[26:28]) & 0x3fff)
		height := int(binary.LittleEndian.Uint16(header[28:30]) & 0x3fff)
		return width, height, width > 0 && height > 0
	case "VP8L":
		if len(header) < 25 || header[20] != 0x2f {
			return 0, 0, false
		}
		width := 1 + int(header[21]) + int(header[22]&0x3f)<<8
		height := 1 + int(header[22]>>6) + int(header[23])<<2 + int(header[24]&0x0f)<<10
		return width, height, width > 0 && height > 0
	default:
		return 0, 0, false
	}
}

func reconcileOpenAIResponsesImageResultSizes(results []openAIResponsesImageResult, firstMeta *openAIResponsesImageResult) {
	for i := range results {
		// ChatGPT OAuth can normalize requested controls to "auto". The final
		// image bytes are authoritative for response metadata and tier billing.
		if actualSize := detectOpenAIImageResultSize(results[i].Result); actualSize != "" {
			results[i].Size = actualSize
		}
	}
	if firstMeta == nil || len(results) == 0 {
		return
	}
	if size := strings.TrimSpace(results[0].Size); size != "" {
		firstMeta.Size = size
	}
}
