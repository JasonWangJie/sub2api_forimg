package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type capacitySnapshotStub struct {
	imageAccountPoolRepositoryStub
	reads     int
	fail      bool
	mode      string
	platforms []string
}

func (s *capacitySnapshotStub) SnapshotImageAccountCapacity(_ context.Context, _ int64, mode, model, tier string, platforms []string) (ImageCapacityObservation, error) {
	s.reads++
	s.mode = mode
	s.platforms = append([]string(nil), platforms...)
	if s.fail {
		return ImageCapacityObservation{}, errors.New("diagnostic read unavailable")
	}
	return ImageCapacityObservation{Source: "pool_snapshot", ObservedAt: time.Now().UTC(), PoolMode: mode, PoolModel: model, PoolSizeTier: tier, CandidateCount: 2, Reasons: []ImageCapacityReason{{Code: "scheduling_disabled", Count: 2, AccountIDs: []int64{12, 39}}}}, nil
}

func (s *capacitySnapshotStub) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]Account, error) {
	return s.accounts, nil
}

func TestImageCapacityDiagnosticsPoolDoesNotAlterRouting(t *testing.T) {
	for _, configured := range []bool{true, false} {
		for _, fail := range []bool{false, true} {
			t.Run(map[bool]string{true: "configured", false: "default"}[configured]+map[bool]string{true: "_error", false: "_ok"}[fail], func(t *testing.T) {
				repo := &capacitySnapshotStub{imageAccountPoolRepositoryStub: imageAccountPoolRepositoryStub{mode: ImageAccountPoolModeResolution, configured: configured, accounts: []Account{{ID: 25}}}, fail: fail}
				ctx, _ := WithImageAccountPoolRoute(context.Background(), "gemini-image", "2K")
				// Synchronous selection has no extra diagnostic I/O.
				_, _, err := ResolveImageAccountPool(ctx, repo, 2, []string{PlatformGemini})
				require.NoError(t, err)
				require.Zero(t, repo.reads)
				ctx = WithImageCapacityDiagnostics(ctx, 2, "gemini-image", "2K")
				for i := 0; i < 2; i++ {
					accounts, found, err := ResolveImageAccountPool(ctx, repo, 2, []string{PlatformGemini})
					require.NoError(t, err)
					require.Equal(t, configured, found)
					if configured {
						require.Equal(t, int64(25), accounts[0].ID)
					}
				}
				if !configured {
					groupID := int64(2)
					_, err := (&OpenAIGatewayService{accountRepo: repo}).listSchedulableAccounts(ctx, &groupID, PlatformOpenAI)
					require.NoError(t, err)
				}
				require.Zero(t, repo.reads)
				CaptureImageCapacityFailureSnapshot(context.Background(), ctx)
				CaptureImageCapacityFailureSnapshot(context.Background(), ctx)
				require.Equal(t, 1, repo.reads)
				d := ImageCapacityDiagnosticsFromContext(ctx)
				require.Len(t, d.Observations, 1)
				if fail {
					require.Equal(t, "unavailable", d.Observations[0].Source)
				} else {
					require.Equal(t, 2, d.Observations[0].CandidateCount)
				}
				if !configured {
					require.Equal(t, "default", repo.mode)
					require.Equal(t, []string{PlatformOpenAI}, repo.platforms, "use the actual listing platform, not the initial configured-pool probe")
				}
			})
		}
	}
}

func TestImageCapacityDiagnosticsUnknownDefaultScopeDoesNotReadTaskGroup(t *testing.T) {
	for _, scope := range []string{"simple", "fallback_group", "ungrouped"} {
		t.Run(scope, func(t *testing.T) {
			repo := &capacitySnapshotStub{}
			ctx := WithImageCapacityDiagnostics(context.Background(), 2, "model", "2K")
			groupID := int64(2)
			group := &groupID
			if scope == "fallback_group" {
				groupID = 3
			}
			if scope == "ungrouped" {
				group = nil
			}
			captureDefaultImageCapacityPool(ctx, repo, group, []string{PlatformOpenAI}, scope == "simple")
			CaptureImageCapacityFailureSnapshot(context.Background(), ctx)
			require.Zero(t, repo.reads)
			require.Equal(t, "unavailable", ImageCapacityDiagnosticsFromContext(ctx).Observations[0].Source)
		})
	}
}

func TestImageCapacityDiagnosticsBoundedDeduplicatedAndCopied(t *testing.T) {
	ctx := WithImageCapacityDiagnostics(context.Background(), 2, "gemini-image", "4K")
	for i := int64(1); i <= 600; i++ {
		for j := 0; j < 2; j++ {
			RecordImageCapacityDecision(ctx, "scheduler_filter", "model_not_supported", i, 600)
		}
	}
	RecordImageCapacityDecision(ctx, "concurrency", "upstream-secret-authorization", 12, 0)
	d := ImageCapacityDiagnosticsFromContext(ctx)
	require.Len(t, d.Observations, 2)
	require.True(t, d.Observations[0].CandidatesTruncated)
	require.Equal(t, 512, d.Observations[0].Reasons[0].Count)
	require.Len(t, d.Observations[0].Reasons[0].AccountIDs, 20)
	d.Observations[0].Reasons[0].AccountIDs[0] = 9999
	require.Equal(t, int64(1), ImageCapacityDiagnosticsFromContext(ctx).Observations[0].Reasons[0].AccountIDs[0])
	encoded, err := json.Marshal(d)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "authorization")
	require.Less(t, len(encoded), 65536)
	require.Nil(t, ImageCapacityDiagnosticsFromContext(context.Background()))
}

func TestImageCapacityDiagnosticsUnknownIsExplicit(t *testing.T) {
	d := ImageCapacityDiagnosticsFromContext(WithImageCapacityDiagnostics(context.Background(), 2, "model", "1K"))
	require.Equal(t, "unavailable", d.Observations[0].Source)
	require.Equal(t, "unknown", d.Observations[0].Reasons[0].Code)
}

func TestImageCapacityDiagnosticsCancelledSnapshotDoesNotQuery(t *testing.T) {
	repo := &capacitySnapshotStub{imageAccountPoolRepositoryStub: imageAccountPoolRepositoryStub{mode: ImageAccountPoolModeResolution, configured: true}}
	ctx, _ := WithImageAccountPoolRoute(context.Background(), "model", "2K")
	ctx = WithImageCapacityDiagnostics(ctx, 2, "model", "2K")
	_, _, err := ResolveImageAccountPool(ctx, repo, 2, nil)
	require.NoError(t, err)
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	CaptureImageCapacityFailureSnapshot(parent, ctx)
	require.Zero(t, repo.reads)
	require.Equal(t, "unavailable", ImageCapacityDiagnosticsFromContext(ctx).Observations[0].Source)
}

func TestImageCapacityDiagnosticsDoesNotTreatSingleAccountProbeAsSelection(t *testing.T) {
	repo := &capacitySnapshotStub{imageAccountPoolRepositoryStub: imageAccountPoolRepositoryStub{mode: ImageAccountPoolModeResolution, configured: true, accounts: []Account{{ID: 12, Platform: PlatformAntigravity, Status: StatusActive, Schedulable: true}}}}
	groupID := int64(2)
	ctx, _ := WithImageAccountPoolRoute(context.Background(), "model", "2K")
	ctx = WithImageCapacityDiagnostics(ctx, groupID, "model", "2K")
	svc := &GatewayService{accountRepo: repo}
	_ = svc.IsSingleAntigravityAccountGroup(ctx, &groupID)
	CaptureImageCapacityFailureSnapshot(context.Background(), ctx)
	require.Zero(t, repo.reads, "a single-platform capability probe is not the actual mixed-platform scheduling pool")
	require.Equal(t, "unavailable", ImageCapacityDiagnosticsFromContext(ctx).Observations[0].Source)
}

func TestImageCapacityDiagnosticsRecordsRealOpenAIFilter(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(101202)
	ctx := WithImageCapacityDiagnostics(context.Background(), groupID, "grok-4.5", "2K")
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{{ID: 38111, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1}}}, cache: &schedulerTestGatewayCache{}, cfg: &config.Config{}, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService("true"), concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
	selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "grok-4.5", nil, OpenAIUpstreamTransportAny, false)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selection)
	d := ImageCapacityDiagnosticsFromContext(ctx)
	require.Equal(t, "scheduler_filter", d.Observations[0].Source)
	require.Equal(t, "model_not_supported", d.Observations[0].Reasons[0].Code)
	require.Equal(t, []int64{38111}, d.Observations[0].Reasons[0].AccountIDs)
}

func TestImageCapacityDiagnosticsRecordsRealGeminiFilter(t *testing.T) {
	groupID := int64(7)
	repo := imageSizePoolRepositoryStub{configured: true, accounts: []Account{{ID: 6, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"model_mapping": map[string]any{"only-this-model": "only-this-model"}}}}}
	svc := &GatewayService{accountRepo: repo}
	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: groupID, Platform: PlatformGemini, Status: StatusActive, Hydrated: true})
	ctx = WithImageSizeAccountPoolTier(ctx, ImageBillingSize2K)
	ctx = WithImageCapacityDiagnostics(ctx, groupID, "not-supported", "2K")
	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "not-supported", nil, "", 0)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selection)
	d := ImageCapacityDiagnosticsFromContext(ctx)
	require.Equal(t, "scheduler_filter", d.Observations[0].Source)
	require.Equal(t, "model_not_supported", d.Observations[0].Reasons[0].Code)
}

type capacityAcquireCache struct {
	ConcurrencyCache
	err error
}

func (c capacityAcquireCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return false, c.err
}

func TestImageCapacityDiagnosticsConcurrencyErrorIsNotFull(t *testing.T) {
	for _, platform := range []string{"openai", "gemini"} {
		for _, failed := range []bool{false, true} {
			t.Run(platform+map[bool]string{true: "_unavailable", false: "_full"}[failed], func(t *testing.T) {
				ctx := WithImageCapacityDiagnostics(context.Background(), 2, "model", "2K")
				cache := capacityAcquireCache{}
				if failed {
					cache.err = errors.New("redis connection unavailable")
				}
				concurrency := NewConcurrencyService(cache)
				if platform == "openai" {
					_, _ = (&OpenAIGatewayService{concurrencyService: concurrency}).tryAcquireAccountSlot(ctx, 25, 1)
				} else {
					_, _ = (&GatewayService{concurrencyService: concurrency}).tryAcquireAccountSlot(ctx, 25, 1)
				}
				d := ImageCapacityDiagnosticsFromContext(ctx)
				require.Equal(t, "concurrency", d.Observations[0].Source)
				require.Equal(t, map[bool]string{true: "unknown", false: "concurrency_full"}[failed], d.Observations[0].Reasons[0].Code)
			})
		}
	}
}
