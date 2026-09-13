package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateGroupImageAccountPoolsRejectsInvalidModelsAndKeepsIndependentBindings(t *testing.T) {
	pools := EmptyGroupImageAccountPools(ImageAccountPoolModeModelResolution)
	pools.ResolutionPools[ImageBillingSize1K] = []GroupImageSizeAccount{{AccountID: 1, Priority: 2}}
	pools.ModelPools = []ImageModelAccountPool{{
		Model:    "gpt-image-2",
		Accounts: []GroupImageSizeAccount{{AccountID: 2, Priority: 1}},
	}}
	pools.ModelResolutionPools = []ImageModelResolutionAccountPool{{
		Model: "gpt-image-2.5-sunburst",
		Resolutions: map[string][]GroupImageSizeAccount{
			ImageBillingSize1K: {},
			ImageBillingSize2K: {{AccountID: 3, Priority: 1}},
			ImageBillingSize4K: {},
		},
	}}
	ids, err := validateGroupImageAccountPools(pools)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{1, 2, 3}, ids)

	pools.ModelPools = append(pools.ModelPools, ImageModelAccountPool{Model: "gpt-image-*"})
	_, err = validateGroupImageAccountPools(pools)
	require.ErrorContains(t, err, "wildcard")
}

func TestGroupImageAccountPoolsToViewSeparatesDimensions(t *testing.T) {
	name := "account-9"
	rows := []GroupImageSizeAccount{
		{Model: "", SizeTier: ImageBillingSize1K, AccountID: 7, Priority: 2},
		{Model: "gpt-image-2", SizeTier: "", AccountID: 8, Priority: 1},
		{Model: "gpt-image-2", SizeTier: ImageBillingSize4K, AccountID: 9, Priority: 3, AccountName: &name},
	}
	view := groupImageAccountPoolsToView(ImageAccountPoolModeModelResolution, rows)
	require.Equal(t, ImageAccountPoolModeModelResolution, view.Mode)
	require.Equal(t, int64(7), view.ResolutionPools[ImageBillingSize1K][0].AccountID)
	require.Equal(t, int64(8), view.ModelPools[0].Accounts[0].AccountID)
	require.Equal(t, int64(9), view.ModelResolutionPools[0].Resolutions[ImageBillingSize4K][0].AccountID)
	require.Equal(t, &name, view.ModelResolutionPools[0].Resolutions[ImageBillingSize4K][0].AccountName)
}

func TestNormalizeImageAccountPoolModelIsExactAndByteBounded(t *testing.T) {
	model, err := NormalizeImageAccountPoolModel(" GPT-Image-2 ")
	require.NoError(t, err)
	require.Equal(t, "GPT-Image-2", model)

	_, err = NormalizeImageAccountPoolModel("")
	require.ErrorContains(t, err, "required")
	_, err = NormalizeImageAccountPoolModel("gpt-image-2\x00")
	require.ErrorContains(t, err, "control")
	_, err = NormalizeImageAccountPoolModel(string(make([]byte, 256)))
	require.Error(t, err)
}

func TestImageAccountPoolModelCandidatesExcludeUnusableWildcards(t *testing.T) {
	got := imageAccountPoolModelCandidates(PlatformOpenAI, []string{
		"gpt-image-*", "gpt-image-2", " gpt-image-2.5-flare ",
	}, nil)
	require.Equal(t, []string{"gpt-image-2", "gpt-image-2.5-flare"}, got)
}

func TestImageAccountPoolPlatformCompatibilityMatchesRuntimeScheduler(t *testing.T) {
	require.True(t, accountPlatformCompatibleWithImageSizeGroup(PlatformGemini, PlatformAntigravity))
	require.True(t, accountPlatformCompatibleWithImageSizeGroup(PlatformAntigravity, PlatformAntigravity))
	require.False(t, accountPlatformCompatibleWithImageSizeGroup(PlatformAntigravity, PlatformGemini), "forced Antigravity scheduling cannot select Gemini accounts")
	require.True(t, accountPlatformCompatibleWithImageSizeGroup(PlatformComposite, PlatformOpenAI))
	require.True(t, accountPlatformCompatibleWithImageSizeGroup(PlatformComposite, PlatformGemini))
	require.True(t, accountPlatformCompatibleWithImageSizeGroup(PlatformComposite, PlatformAntigravity))
}
