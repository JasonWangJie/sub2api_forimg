package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractImageSizePoolTierFromRequestBody(t *testing.T) {
	require.Equal(t, ImageBillingSize4K, ExtractImageSizePoolTierFromRequestBody([]byte(`{"extra_body":{"google":{"image_config":{"image_size":"4K"}}}}`)))
	require.Equal(t, ImageBillingSize1K, ExtractImageSizePoolTierFromRequestBody([]byte(`{"resolution":"1K"}`)))
	require.Equal(t, ImageBillingSize2K, ExtractImageSizePoolTierFromRequestBody([]byte(`{"generationConfig":{"imageConfig":{"imageSize":"2K"}}}`)))
	require.Equal(t, "", ExtractImageSizePoolTierFromRequestBody([]byte(`{"model":"x"}`)))
}

func TestWithImageSizeAccountPoolTierContext(t *testing.T) {
	ctx := WithImageSizeAccountPoolTier(context.Background(), "2k")
	tier, ok := ImageSizeAccountPoolTierFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, ImageBillingSize2K, tier)
}

func TestForcedSchedulableAccountsContext(t *testing.T) {
	accounts := []Account{{ID: 9, Platform: PlatformOpenAI}}
	ctx := withForcedSchedulableAccounts(context.Background(), accounts)
	got, ok := forcedSchedulableAccountsFromContext(ctx)
	require.True(t, ok)
	require.Len(t, got, 1)
	require.Equal(t, int64(9), got[0].ID)
}

func TestIsValidImageSizeTier(t *testing.T) {
	require.True(t, IsValidImageSizeTier(ImageBillingSize1K))
	require.True(t, IsValidImageSizeTier(ImageBillingSize4K))
	require.False(t, IsValidImageSizeTier("2k"))
	require.False(t, IsValidImageSizeTier(""))
	require.Equal(t, []string{ImageBillingSize1K, ImageBillingSize2K, ImageBillingSize4K}, ValidImageSizeTiers())
}

func TestOpenAIListSchedulableAccountsHonorsForcedSizePool(t *testing.T) {
	svc := &OpenAIGatewayService{}
	forced := []Account{
		{ID: 9, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1},
		{ID: 8, Platform: PlatformGrok, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1},
	}
	ctx := withForcedSchedulableAccounts(context.Background(), forced)
	accounts, err := svc.listSchedulableAccounts(ctx, nil, PlatformOpenAI)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, int64(9), accounts[0].ID)
}
