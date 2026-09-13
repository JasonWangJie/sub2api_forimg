package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type imageSizePoolRepositoryStub struct {
	AccountRepository
	configured bool
	accounts   []Account
	err        error
}

type imageAccountPoolRepositoryStub struct {
	AccountRepository
	mode         string
	configured   bool
	accounts     []Account
	err          error
	gotModel     string
	gotTier      string
	gotPlatforms []string
}

func (s *imageAccountPoolRepositoryStub) GetImageAccountPoolMode(context.Context, int64) (string, error) {
	return s.mode, s.err
}

func (s *imageAccountPoolRepositoryStub) HasImageAccountPoolConfigured(_ context.Context, _ int64, model, tier string) (bool, error) {
	s.gotModel, s.gotTier = model, tier
	return s.configured, s.err
}

func (s *imageAccountPoolRepositoryStub) ListSchedulableByGroupImageAccountPool(_ context.Context, _ int64, model, tier string, platforms []string) ([]Account, error) {
	s.gotModel, s.gotTier, s.gotPlatforms = model, tier, platforms
	return s.accounts, s.err
}

func (s *imageAccountPoolRepositoryStub) ListImageSizeAccountIDsByGroupID(context.Context, int64) ([]int64, error) {
	return nil, nil
}

func (s imageSizePoolRepositoryStub) HasImageSizeTierConfigured(context.Context, int64, string) (bool, error) {
	return s.configured, s.err
}

func (s imageSizePoolRepositoryStub) ListSchedulableByGroupImageSizeTier(context.Context, int64, string, []string) ([]Account, error) {
	return s.accounts, s.err
}

func (s imageSizePoolRepositoryStub) ListImageSizeAccountIDsByGroupID(context.Context, int64) ([]int64, error) {
	return nil, nil
}

func (s imageSizePoolRepositoryStub) GetImageAccountPoolMode(context.Context, int64) (string, error) {
	return ImageAccountPoolModeResolution, s.err
}

func (s imageSizePoolRepositoryStub) HasImageAccountPoolConfigured(context.Context, int64, string, string) (bool, error) {
	return s.configured, s.err
}

func (s imageSizePoolRepositoryStub) ListSchedulableByGroupImageAccountPool(context.Context, int64, string, string, []string) ([]Account, error) {
	return s.accounts, s.err
}

type schedulerSnapshotImageSizePoolStub struct {
	AccountRepository
	poolCalls int
}

func (s *schedulerSnapshotImageSizePoolStub) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]Account, error) {
	return []Account{{ID: 1, Platform: PlatformOpenAI}}, nil
}

func (s *schedulerSnapshotImageSizePoolStub) ListSchedulableByGroupImageSizeTier(context.Context, int64, string, []string) ([]Account, error) {
	s.poolCalls++
	return []Account{{ID: 2, Platform: PlatformOpenAI}}, nil
}

func (s *schedulerSnapshotImageSizePoolStub) HasImageSizeTierConfigured(context.Context, int64, string) (bool, error) {
	return true, nil
}

func (s *schedulerSnapshotImageSizePoolStub) ListImageSizeAccountIDsByGroupID(context.Context, int64) ([]int64, error) {
	return []int64{2}, nil
}

func TestExtractImageSizePoolTierFromRequestBody(t *testing.T) {
	require.Equal(t, ImageBillingSize4K, ExtractImageSizePoolTierFromRequestBody([]byte(`{"extra_body":{"google":{"image_config":{"image_size":"4K"}}}}`)))
	require.Equal(t, ImageBillingSize1K, ExtractImageSizePoolTierFromRequestBody([]byte(`{"resolution":"1K"}`)))
	require.Equal(t, ImageBillingSize2K, ExtractImageSizePoolTierFromRequestBody([]byte(`{"generationConfig":{"imageConfig":{"imageSize":"2K"}}}`)))
	nonSquareTier := ExtractImageSizePoolTierFromRequestBody([]byte(`{"size":"1792x1024"}`))
	require.Equal(t, ImageBillingSize1K, nonSquareTier)
	require.Equal(t, ResolveGeminiImageBillingSize("1792x1024", nil).BillingSize, nonSquareTier, "Gemini routing must use the same short-edge tier as billing")
	require.Equal(t, "", ExtractImageSizePoolTierFromRequestBody([]byte(`{"model":"x"}`)))
}

func TestWithImageSizeAccountPoolTierContext(t *testing.T) {
	ctx := WithImageSizeAccountPoolTier(context.Background(), "2k")
	tier, ok := ImageSizeAccountPoolTierFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, ImageBillingSize2K, tier)
}

func TestWithImageAccountPoolRouteUsesExactTrimmedRequestModel(t *testing.T) {
	ctx, err := WithImageAccountPoolRoute(context.Background(), " gpt-image-2.5-Flare ", "4k")
	require.NoError(t, err)
	route, ok := ImageAccountPoolRouteFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "gpt-image-2.5-Flare", route.Model)
	require.Equal(t, ImageBillingSize4K, route.SizeTier)

	_, err = WithImageAccountPoolRoute(context.Background(), "gpt-image-*", "1K")
	require.ErrorContains(t, err, "wildcard")
	_, err = WithImageAccountPoolRoute(context.Background(), "gpt-image-2\n", "1K")
	require.NoError(t, err, "leading/trailing whitespace is trimmed before control-character validation")
	_, err = WithImageAccountPoolRoute(context.Background(), "gpt-image\x00-2", "1K")
	require.ErrorContains(t, err, "control")
}

func TestResolveImageAccountPoolModesAndFallback(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		model     string
		tier      string
		wantModel string
		wantTier  string
	}{
		{name: "resolution", mode: ImageAccountPoolModeResolution, model: "gpt-image-2", tier: "2K", wantTier: "2K"},
		{name: "model", mode: ImageAccountPoolModeModel, model: "gpt-image-2", tier: "2K", wantModel: "gpt-image-2"},
		{name: "combined", mode: ImageAccountPoolModeModelResolution, model: "gpt-image-2.5-sunburst", tier: "4K", wantModel: "gpt-image-2.5-sunburst", wantTier: "4K"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, err := WithImageAccountPoolRoute(context.Background(), tt.model, tt.tier)
			require.NoError(t, err)
			repo := &imageAccountPoolRepositoryStub{mode: tt.mode, configured: true, accounts: []Account{{ID: 9}}}
			accounts, configured, err := ResolveImageAccountPool(ctx, repo, 7, []string{PlatformOpenAI})
			require.NoError(t, err)
			require.True(t, configured)
			require.Equal(t, []Account{{ID: 9}}, accounts)
			require.Equal(t, tt.wantModel, repo.gotModel)
			require.Equal(t, tt.wantTier, repo.gotTier)
		})
	}

	ctx, err := WithImageAccountPoolRoute(context.Background(), "gpt-image-2", "")
	require.NoError(t, err)
	repo := &imageAccountPoolRepositoryStub{mode: ImageAccountPoolModeModelResolution, configured: true}
	accounts, configured, err := ResolveImageAccountPool(ctx, repo, 7, nil)
	require.NoError(t, err)
	require.False(t, configured, "missing required resolution falls back to account_groups")
	require.Nil(t, accounts)

	repo = &imageAccountPoolRepositoryStub{mode: ImageAccountPoolModeModel, configured: false}
	accounts, configured, err = ResolveImageAccountPool(ctx, repo, 7, nil)
	require.NoError(t, err)
	require.False(t, configured, "an absent exact model binding falls back")
	require.Nil(t, accounts)

	repo = &imageAccountPoolRepositoryStub{mode: ImageAccountPoolModeModel, configured: true, accounts: []Account{}}
	accounts, configured, err = ResolveImageAccountPool(ctx, repo, 7, nil)
	require.NoError(t, err)
	require.True(t, configured, "a configured but unavailable pool must remain fail-closed")
	require.Empty(t, accounts)
}

func TestResolveImageAccountPoolDoesNotBypassOAuthOnlyGroup(t *testing.T) {
	groupID := int64(7)
	group := &Group{
		ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true,
		RequireOAuthOnly: true,
	}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	ctx, err := WithImageAccountPoolRoute(ctx, "gpt-image-2", "1K")
	require.NoError(t, err)
	rateMultiplier := 1.75
	repo := &imageAccountPoolRepositoryStub{
		mode:       ImageAccountPoolModeModel,
		configured: true,
		accounts: []Account{
			{ID: 1, Type: AccountTypeAPIKey, RateMultiplier: &rateMultiplier},
			{ID: 2, Type: AccountTypeOAuth, RateMultiplier: &rateMultiplier},
		},
	}

	accounts, configured, err := ResolveImageAccountPool(ctx, repo, groupID, []string{PlatformOpenAI})
	require.NoError(t, err)
	require.True(t, configured)
	require.Len(t, accounts, 1)
	require.Equal(t, int64(2), accounts[0].ID)
	require.Equal(t, rateMultiplier, accounts[0].BillingRateMultiplier(), "pool filtering must preserve account billing fields")

	repo.accounts = []Account{{ID: 1, Type: AccountTypeAPIKey}}
	accounts, configured, err = ResolveImageAccountPool(ctx, repo, groupID, []string{PlatformOpenAI})
	require.NoError(t, err)
	require.True(t, configured, "filtered OAuth-only pools must not fall back across pool boundaries")
	require.Empty(t, accounts)
}

func TestIsSingleAntigravityAccountGroupUsesConfiguredImagePool(t *testing.T) {
	groupID := int64(7)
	group := &Group{ID: groupID, Platform: PlatformAntigravity, Status: StatusActive, Hydrated: true}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	ctx, err := WithImageAccountPoolRoute(ctx, "gemini-3-pro-image-preview", "2K")
	require.NoError(t, err)
	repo := &imageAccountPoolRepositoryStub{
		mode:       ImageAccountPoolModeModel,
		configured: true,
		accounts:   []Account{{ID: 8, Platform: PlatformAntigravity, Status: StatusActive, Schedulable: true}},
	}
	svc := &GatewayService{accountRepo: repo}

	require.True(t, svc.IsSingleAntigravityAccountGroup(ctx, &groupID))
	require.Equal(t, "gemini-3-pro-image-preview", repo.gotModel)
	require.Empty(t, repo.gotTier)

	repo.accounts = append(repo.accounts, Account{ID: 9, Platform: PlatformAntigravity, Status: StatusActive, Schedulable: true})
	require.False(t, svc.IsSingleAntigravityAccountGroup(ctx, &groupID))
}

func TestForcedSchedulableAccountsContext(t *testing.T) {
	accounts := []Account{{ID: 9, Platform: PlatformOpenAI}}
	ctx := withForcedSchedulableAccounts(context.Background(), accounts)
	got, ok := forcedSchedulableAccountsFromContext(ctx)
	require.True(t, ok)
	require.Len(t, got, 1)
	require.Equal(t, int64(9), got[0].ID)
}

func TestForcedSchedulableAccountsRestrictSchedulingScope(t *testing.T) {
	groupID := int64(7)
	account := &Account{ID: 9}
	ctx := withForcedSchedulableAccounts(context.Background(), []Account{*account})

	require.True(t, (&GatewayService{}).isAccountInSchedulingScope(ctx, account, &groupID))
	require.False(t, (&GatewayService{}).isAccountInSchedulingScope(ctx, &Account{ID: 10}, &groupID))
	require.True(t, (&OpenAIGatewayService{}).openAIAccountMatchesSchedulingScope(ctx, account, &groupID))
	require.False(t, (&OpenAIGatewayService{}).openAIAccountMatchesSchedulingScope(ctx, &Account{ID: 10}, &groupID))
}

func TestResolveImageSizeAccountPool(t *testing.T) {
	accounts := []Account{{ID: 9, Platform: PlatformOpenAI}}
	resolved, configured, err := ResolveImageSizeAccountPool(context.Background(), imageSizePoolRepositoryStub{
		configured: true,
		accounts:   accounts,
	}, 7, ImageBillingSize2K, []string{PlatformOpenAI})
	require.NoError(t, err)
	require.True(t, configured)
	require.Equal(t, accounts, resolved)

	resolved, configured, err = ResolveImageSizeAccountPool(context.Background(), imageSizePoolRepositoryStub{}, 7, ImageBillingSize2K, nil)
	require.NoError(t, err)
	require.False(t, configured)
	require.Nil(t, resolved)

	_, _, err = ResolveImageSizeAccountPool(context.Background(), imageSizePoolRepositoryStub{err: errors.New("database unavailable")}, 7, ImageBillingSize2K, nil)
	require.ErrorContains(t, err, "check image size account pool")
}

func TestSchedulerSnapshotDoesNotUnionImageSizePoolAccounts(t *testing.T) {
	repo := &schedulerSnapshotImageSizePoolStub{}
	svc := &SchedulerSnapshotService{accountRepo: repo}
	accounts, err := svc.loadAccountsFromDB(context.Background(), SchedulerBucket{
		GroupID:  7,
		Platform: PlatformOpenAI,
	}, false)
	require.NoError(t, err)
	require.Equal(t, []Account{{ID: 1, Platform: PlatformOpenAI}}, accounts)
	require.Zero(t, repo.poolCalls)
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

func TestGatewayImageSizePoolHonorsPriorityAndFailoverExclusions(t *testing.T) {
	groupID := int64(7)
	repo := imageSizePoolRepositoryStub{
		configured: true,
		accounts: []Account{
			{ID: 6, Platform: PlatformGemini, Status: StatusActive, Schedulable: true, Priority: 2, Concurrency: 1},
			{ID: 3, Platform: PlatformGemini, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1},
		},
	}
	svc := &GatewayService{accountRepo: repo}
	group := &Group{ID: groupID, Platform: PlatformGemini, Status: StatusActive, Hydrated: true}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	ctx = WithImageSizeAccountPoolTier(ctx, ImageBillingSize2K)

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "", nil, "", 0)
	require.NoError(t, err)
	require.Equal(t, int64(3), selection.Account.ID)
	selection.ReleaseFunc()

	selection, err = svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "", map[int64]struct{}{3: {}}, "", 0)
	require.NoError(t, err)
	require.Equal(t, int64(6), selection.Account.ID)
	selection.ReleaseFunc()
}

func TestGatewayImageSizePoolAllowsSamePriorityAccounts(t *testing.T) {
	groupID := int64(8)
	repo := imageSizePoolRepositoryStub{
		configured: true,
		accounts: []Account{
			{ID: 11, Platform: PlatformGemini, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1},
			{ID: 12, Platform: PlatformGemini, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1},
		},
	}
	svc := &GatewayService{accountRepo: repo}
	group := &Group{ID: groupID, Platform: PlatformGemini, Status: StatusActive, Hydrated: true}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	ctx = WithImageSizeAccountPoolTier(ctx, ImageBillingSize1K)

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Contains(t, []int64{11, 12}, selection.Account.ID)
	firstID := selection.Account.ID
	selection.ReleaseFunc()

	selection, err = svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "", map[int64]struct{}{firstID: {}}, "", 0)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Contains(t, []int64{11, 12}, selection.Account.ID)
	require.NotEqual(t, firstID, selection.Account.ID)
	selection.ReleaseFunc()
}
