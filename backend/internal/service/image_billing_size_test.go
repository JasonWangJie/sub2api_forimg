package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyImageBillingTier(t *testing.T) {
	tests := []struct {
		name     string
		size     string
		wantTier string
		wantOK   bool
	}{
		{name: "gemini half k uses existing one k tariff", size: "0.5K", wantTier: ImageBillingSize1K, wantOK: true},
		{name: "half k dimensions use existing one k tariff", size: "512x512", wantTier: ImageBillingSize1K, wantOK: true},
		{name: "explicit 2k square", size: "2048x2048", wantTier: "2K", wantOK: true},
		{name: "explicit 2k landscape", size: "2048x1152", wantTier: "2K", wantOK: true},
		{name: "explicit 4k landscape", size: "3840x2160", wantTier: "4K", wantOK: true},
		{name: "explicit 4k portrait", size: "2160x3840", wantTier: "4K", wantOK: true},
		{name: "long edge 1k", size: "1024X768", wantTier: "1K", wantOK: true},
		{name: "long edge 2k", size: "1280x768", wantTier: "2K", wantOK: true},
		{name: "long edge 4k", size: "2560x1600", wantTier: "4K", wantOK: true},
		{name: "tier string 1k", size: "1k", wantTier: "1K", wantOK: true},
		{name: "empty", size: "", wantOK: false},
		{name: "auto", size: "auto", wantOK: false},
		{name: "invalid", size: "not-a-size", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTier, gotOK := ClassifyImageBillingTier(tt.size)
			require.Equal(t, tt.wantOK, gotOK)
			require.Equal(t, tt.wantTier, gotTier)
		})
	}
}

func TestClassifyGeminiImageBillingTier(t *testing.T) {
	tests := []struct {
		name     string
		size     string
		wantTier string
		wantOK   bool
	}{
		{name: "square 1k", size: "1024x1024", wantTier: ImageBillingSize1K, wantOK: true},
		{name: "gemini 1k 16:9", size: "1344x768", wantTier: ImageBillingSize1K, wantOK: true},
		{name: "gemini 1k 9:16", size: "768x1344", wantTier: ImageBillingSize1K, wantOK: true},
		{name: "gemini 1k 21:9", size: "1536x672", wantTier: ImageBillingSize1K, wantOK: true},
		{name: "square 2k", size: "2048x2048", wantTier: ImageBillingSize2K, wantOK: true},
		{name: "gemini 2k 16:9", size: "2688x1536", wantTier: ImageBillingSize2K, wantOK: true},
		{name: "gemini 4k 16:9", size: "5376x3072", wantTier: ImageBillingSize4K, wantOK: true},
		{name: "gemini 4k square", size: "4096x4096", wantTier: ImageBillingSize4K, wantOK: true},
		{name: "tier string still honored", size: "1K", wantTier: ImageBillingSize1K, wantOK: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTier, gotOK := ClassifyGeminiImageBillingTier(tt.size)
			require.Equal(t, tt.wantOK, gotOK)
			require.Equal(t, tt.wantTier, gotTier)
		})
	}
}

func TestResolveGeminiImageBillingSizeNonSquareTiers(t *testing.T) {
	got := ResolveGeminiImageBillingSize("1K", []string{"1344x768"})
	require.Equal(t, ImageBillingSize1K, got.BillingSize)
	require.Equal(t, ImageSizeSourceInput, got.Source)
	require.Equal(t, map[string]int{ImageBillingSize1K: 1}, got.Breakdown)

	got = ResolveGeminiImageBillingSize("2K", []string{"2688x1536"})
	require.Equal(t, ImageBillingSize2K, got.BillingSize)
	require.Equal(t, ImageSizeSourceInput, got.Source)

	got = ResolveGeminiImageBillingSize("4K", []string{"5376x3072"})
	require.Equal(t, ImageBillingSize4K, got.BillingSize)
	require.Equal(t, ImageSizeSourceInput, got.Source)
}

func TestResolveImageBillingSizeExplicitTierWinsOverOutputPixels(t *testing.T) {
	// OpenAI 1K 16:9 maps to 1536x1024; long-edge classification would bump to 2K.
	got := ResolveImageBillingSize("1K", []string{"1536x1024"})
	require.Equal(t, ImageBillingSize1K, got.BillingSize)
	require.Equal(t, ImageSizeSourceInput, got.Source)
	require.Equal(t, map[string]int{ImageBillingSize1K: 1}, got.Breakdown)

	got = ResolveImageBillingSize("2K", []string{"2048x1152"})
	require.Equal(t, ImageBillingSize2K, got.BillingSize)

	got = ResolveImageBillingSize("0.5K", []string{"512x512"})
	require.Equal(t, ImageBillingSize1K, got.BillingSize)
	require.Equal(t, ImageSizeSourceInput, got.Source)

	got = ResolveImageBillingSize("4K", []string{"1024x1024", "3840x2160"})
	require.Equal(t, ImageBillingSize4K, got.BillingSize)
	require.Equal(t, ImageSizeSourceOutput, got.Source)
	require.Equal(t, map[string]int{ImageBillingSize1K: 1, ImageBillingSize4K: 1}, got.Breakdown)

	got = ResolveGeminiImageBillingSize("1K", []string{"1024x768", "3840x2160"})
	require.Equal(t, ImageBillingSize1K, got.BillingSize)
	require.Equal(t, ImageSizeSourceInput, got.Source)
	require.Equal(t, map[string]int{ImageBillingSize1K: 2}, got.Breakdown)
}

func TestApplyForwardImageBillingResolutionUsesGeminiShortEdge(t *testing.T) {
	result := &ForwardResult{
		ImageCount:       1,
		ImageInputSize:   "1K",
		ImageOutputSizes: []string{"1344x768"},
	}
	ApplyForwardImageBillingResolution(result)
	require.Equal(t, ImageBillingSize1K, result.ImageSize)
	require.Equal(t, ImageSizeSourceInput, result.ImageSizeSource)
}

func TestResolveImageBillingSize(t *testing.T) {
	tests := []struct {
		name          string
		inputSize     string
		outputSizes   []string
		wantBilling   string
		wantOutput    string
		wantSource    string
		wantBreakdown map[string]int
	}{
		{
			name:          "output wins over input",
			inputSize:     "1024x1024",
			outputSizes:   []string{"3840x2160"},
			wantBilling:   "4K",
			wantOutput:    "3840x2160",
			wantSource:    ImageSizeSourceOutput,
			wantBreakdown: map[string]int{"4K": 1},
		},
		{
			name:        "input fallback",
			inputSize:   "1024x1024",
			wantBilling: "1K",
			wantSource:  ImageSizeSourceInput,
		},
		{
			name:        "auto defaults",
			inputSize:   "auto",
			wantBilling: "2K",
			wantSource:  ImageSizeSourceDefault,
		},
		{
			name:        "empty defaults",
			inputSize:   "",
			wantBilling: "2K",
			wantSource:  ImageSizeSourceDefault,
		},
		{
			name:        "invalid defaults",
			inputSize:   "largest",
			wantBilling: "2K",
			wantSource:  ImageSizeSourceDefault,
		},
		{
			name:          "mixed output chooses highest tier",
			inputSize:     "1024x1024",
			outputSizes:   []string{"1024x1024", "3840x2160", "1280x720"},
			wantBilling:   "4K",
			wantOutput:    "1024x1024",
			wantSource:    ImageSizeSourceOutput,
			wantBreakdown: map[string]int{"1K": 1, "2K": 1, "4K": 1},
		},
		{
			name:        "unparseable output falls back to parseable input",
			inputSize:   "2048x1152",
			outputSizes: []string{"auto"},
			wantBilling: "2K",
			wantOutput:  "auto",
			wantSource:  ImageSizeSourceInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveImageBillingSize(tt.inputSize, tt.outputSizes)
			require.Equal(t, tt.wantBilling, got.BillingSize)
			require.Equal(t, tt.inputSize, got.InputSize)
			require.Equal(t, tt.wantOutput, got.OutputSize)
			require.Equal(t, tt.wantSource, got.Source)
			require.Equal(t, tt.wantBreakdown, got.Breakdown)
		})
	}
}

func TestResolveImageBillingCountsPreservesExactCountAndFallback(t *testing.T) {
	require.Equal(t,
		map[string]int{ImageBillingSize1K: 1, ImageBillingSize4K: 1},
		resolveImageBillingCounts(2, ImageBillingSize4K, map[string]int{ImageBillingSize1K: 1, ImageBillingSize4K: 1}),
	)
	require.Equal(t,
		map[string]int{ImageBillingSize1K: 1, ImageBillingSize2K: 1},
		resolveImageBillingCounts(2, ImageBillingSize2K, map[string]int{ImageBillingSize1K: 1}),
	)
	require.Equal(t,
		map[string]int{ImageBillingSize4K: 2},
		resolveImageBillingCounts(2, ImageBillingSize4K, map[string]int{ImageBillingSize1K: 3}),
		"an inconsistent breakdown must not alter the authoritative image count",
	)
}
