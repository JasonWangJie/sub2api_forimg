package service

import (
	"sort"
	"strconv"
	"strings"
)

const (
	ImageBillingSize1K = "1K"
	ImageBillingSize2K = "2K"
	ImageBillingSize4K = "4K"

	ImageSizeSourceOutput  = "output"
	ImageSizeSourceInput   = "input"
	ImageSizeSourceDefault = "default"
	ImageSizeSourceLegacy  = "legacy"
)

type ImageBillingSizeResolution struct {
	BillingSize string
	InputSize   string
	OutputSize  string
	Source      string
	Breakdown   map[string]int
}

func ClassifyImageBillingTier(size string) (string, bool) {
	return classifyImageBillingTier(size, false)
}

// ClassifyGeminiImageBillingTier maps Gemini image sizes onto the shared
// 1K/2K/4K tariff. Gemini tiers are defined by the short edge (~1024/2048/4096)
// while aspect ratio stretches the long edge, so long-edge OpenAI rules must
// not be reused or non-square 1K/2K outputs are overbilled.
func ClassifyGeminiImageBillingTier(size string) (string, bool) {
	return classifyImageBillingTier(size, true)
}

func classifyImageBillingTier(size string, useShortEdge bool) (string, bool) {
	trimmed := strings.TrimSpace(size)
	normalized := strings.ToLower(trimmed)
	switch normalized {
	case "", "auto":
		return "", false
	case "0.5k", "512x512":
		// Groups intentionally keep the existing 1K/2K/4K tariff. Gemini's
		// optional 0.5K capability therefore belongs to the lowest existing
		// tier instead of silently falling through to the 2K default.
		return ImageBillingSize1K, true
	case "1k":
		return ImageBillingSize1K, true
	case "2k":
		return ImageBillingSize2K, true
	case "4k":
		return ImageBillingSize4K, true
	case "2048x2048", "2048x1152":
		return ImageBillingSize2K, true
	case "3840x2160", "2160x3840":
		return ImageBillingSize4K, true
	}

	width, height, ok := parseImageBillingDimensions(trimmed)
	if !ok {
		return "", false
	}
	edge := width
	if useShortEdge {
		if height < edge {
			edge = height
		}
	} else if height > edge {
		edge = height
	}
	switch {
	case edge <= 1024:
		return ImageBillingSize1K, true
	case edge <= 2048:
		return ImageBillingSize2K, true
	default:
		return ImageBillingSize4K, true
	}
}

func NormalizeImageBillingTierOrDefault(size string) string {
	if tier, ok := ClassifyImageBillingTier(size); ok {
		return tier
	}
	return ImageBillingSize2K
}

func NormalizeGeminiImageBillingTierOrDefault(size string) string {
	if tier, ok := ClassifyGeminiImageBillingTier(size); ok {
		return tier
	}
	return ImageBillingSize2K
}

func ResolveImageBillingSize(inputSize string, outputSizes []string) ImageBillingSizeResolution {
	return resolveImageBillingSize(inputSize, outputSizes, false)
}

func ResolveGeminiImageBillingSize(inputSize string, outputSizes []string) ImageBillingSizeResolution {
	return resolveImageBillingSize(inputSize, outputSizes, true)
}

func isExplicitImageBillingTier(size string) bool {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "0.5k", "1k", "2k", "4k":
		return true
	default:
		return false
	}
}

func resolveImageBillingSize(inputSize string, outputSizes []string, gemini bool) ImageBillingSizeResolution {
	inputSize = strings.TrimSpace(inputSize)
	outputSizes = compactTrimmedStrings(outputSizes)
	classify := ClassifyImageBillingTier
	if gemini {
		classify = ClassifyGeminiImageBillingTier
	}

	outputSize := firstDisplayImageOutputSize(outputSizes)
	breakdown := map[string]int{}
	outputTier := ""
	for _, output := range outputSizes {
		tier, ok := classify(output)
		if !ok {
			continue
		}
		breakdown[tier]++
		if imageTierRank(tier) > imageTierRank(outputTier) {
			outputTier = tier
		}
	}

	explicitTier := ""
	if isExplicitImageBillingTier(inputSize) {
		explicitTier, _ = classify(inputSize)
	}

	// Distinct observed output tiers are authoritative when they do not exceed
	// an explicit request tier. This allows exact mixed-size charging without
	// turning decoded dimensions into an unexpected upgrade for the client.
	if len(breakdown) > 1 && (explicitTier == "" || imageTierRank(outputTier) <= imageTierRank(explicitTier)) {
		return ImageBillingSizeResolution{
			BillingSize: outputTier,
			InputSize:   inputSize,
			OutputSize:  outputSize,
			Source:      ImageSizeSourceOutput,
			Breakdown:   normalizeImageSizeBreakdown(breakdown),
		}
	}

	// Workbench / async clients select an explicit 1K/2K/4K (or 0.5K→1K) tariff.
	// Aspect-ratio mapped WxH must not reclassify a requested 1K into 2K/4K.
	if explicitTier != "" {
		explicitBreakdown := map[string]int{}
		if n := len(outputSizes); n > 0 {
			explicitBreakdown[explicitTier] = n
		}
		return ImageBillingSizeResolution{
			BillingSize: explicitTier,
			InputSize:   inputSize,
			OutputSize:  outputSize,
			Source:      ImageSizeSourceInput,
			Breakdown:   normalizeImageSizeBreakdown(explicitBreakdown),
		}
	}

	if outputTier != "" {
		return ImageBillingSizeResolution{
			BillingSize: outputTier,
			InputSize:   inputSize,
			OutputSize:  outputSize,
			Source:      ImageSizeSourceOutput,
			Breakdown:   normalizeImageSizeBreakdown(breakdown),
		}
	}

	if tier, ok := classify(inputSize); ok {
		return ImageBillingSizeResolution{
			BillingSize: tier,
			InputSize:   inputSize,
			OutputSize:  outputSize,
			Source:      ImageSizeSourceInput,
		}
	}

	return ImageBillingSizeResolution{
		BillingSize: ImageBillingSize2K,
		InputSize:   inputSize,
		OutputSize:  outputSize,
		Source:      ImageSizeSourceDefault,
	}
}

func ApplyOpenAIImageBillingResolution(result *OpenAIForwardResult) {
	if result == nil || result.ImageCount <= 0 {
		return
	}
	inputSize := strings.TrimSpace(result.ImageInputSize)
	if inputSize == "" && strings.TrimSpace(result.ImageSize) != ImageBillingSize2K {
		inputSize = strings.TrimSpace(result.ImageSize)
	}
	outputSizes := result.ImageOutputSizes
	if len(outputSizes) == 0 && strings.TrimSpace(result.ImageOutputSize) != "" {
		outputSizes = []string{result.ImageOutputSize}
	}
	resolved := ResolveImageBillingSize(inputSize, outputSizes)
	applyImageBillingResolution(
		&result.ImageSize,
		&result.ImageInputSize,
		&result.ImageOutputSize,
		&result.ImageSizeSource,
		&result.ImageSizeBreakdown,
		resolved,
	)
}

func ApplyForwardImageBillingResolution(result *ForwardResult) {
	if result == nil || result.ImageCount <= 0 {
		return
	}
	inputSize := strings.TrimSpace(result.ImageInputSize)
	if inputSize == "" && strings.TrimSpace(result.ImageSize) != ImageBillingSize2K {
		inputSize = strings.TrimSpace(result.ImageSize)
	}
	outputSizes := result.ImageOutputSizes
	if len(outputSizes) == 0 && strings.TrimSpace(result.ImageOutputSize) != "" {
		outputSizes = []string{result.ImageOutputSize}
	}
	// ForwardResult image accounting is Gemini-family (sync + async).
	// Explicit 1K/2K/4K requests keep that tariff; WxH-only requests still use
	// short-edge output classification.
	resolved := ResolveGeminiImageBillingSize(inputSize, outputSizes)
	applyImageBillingResolution(
		&result.ImageSize,
		&result.ImageInputSize,
		&result.ImageOutputSize,
		&result.ImageSizeSource,
		&result.ImageSizeBreakdown,
		resolved,
	)
}

func applyImageBillingResolution(
	billingSize *string,
	inputSize *string,
	outputSize *string,
	source *string,
	breakdown *map[string]int,
	resolved ImageBillingSizeResolution,
) {
	*billingSize = resolved.BillingSize
	*inputSize = resolved.InputSize
	*outputSize = resolved.OutputSize
	*source = resolved.Source
	*breakdown = resolved.Breakdown
}

func parseImageBillingDimensions(size string) (int, int, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, false
	}
	height, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, false
	}
	if width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func compactTrimmedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func firstDisplayImageOutputSize(outputSizes []string) string {
	for _, output := range outputSizes {
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func imageTierRank(tier string) int {
	switch strings.ToUpper(strings.TrimSpace(tier)) {
	case ImageBillingSize1K:
		return 1
	case ImageBillingSize2K:
		return 2
	case ImageBillingSize4K:
		return 3
	default:
		return 0
	}
}

func normalizeImageSizeBreakdown(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for _, tier := range []string{ImageBillingSize1K, ImageBillingSize2K, ImageBillingSize4K} {
		if count := in[tier]; count > 0 {
			out[tier] = count
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func SortedImageBillingBreakdownKeys(breakdown map[string]int) []string {
	keys := make([]string, 0, len(breakdown))
	for key := range breakdown {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := imageTierRank(keys[i]), imageTierRank(keys[j])
		if left == right {
			return keys[i] < keys[j]
		}
		return left < right
	})
	return keys
}

// resolveImageBillingCounts turns the observed size breakdown into an exact
// per-tier charge plan. Any images without a decoded output size retain the
// existing fallback tier. An inconsistent breakdown is ignored instead of
// allowing malformed accounting metadata to change the total image count.
func resolveImageBillingCounts(imageCount int, fallbackSize string, breakdown map[string]int) map[string]int {
	if imageCount <= 0 {
		return nil
	}
	normalized := normalizeImageSizeBreakdown(breakdown)
	observed := 0
	for _, count := range normalized {
		observed += count
	}
	if observed > imageCount {
		normalized = nil
		observed = 0
	}
	if normalized == nil {
		normalized = make(map[string]int, 1)
	}
	if remaining := imageCount - observed; remaining > 0 {
		tier := NormalizeImageBillingTierOrDefault(fallbackSize)
		normalized[tier] += remaining
	}
	return normalized
}

func addCostBreakdown(total *CostBreakdown, part *CostBreakdown) {
	if total == nil || part == nil {
		return
	}
	total.InputCost += part.InputCost
	total.ImageInputCost += part.ImageInputCost
	total.OutputCost += part.OutputCost
	total.ImageOutputCost += part.ImageOutputCost
	total.CacheCreationCost += part.CacheCreationCost
	total.CacheReadCost += part.CacheReadCost
	total.TotalCost += part.TotalCost
	total.ActualCost += part.ActualCost
	total.LongContextBillingApplied = total.LongContextBillingApplied || part.LongContextBillingApplied
	if total.BillingMode == "" {
		total.BillingMode = part.BillingMode
	}
}
