//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Storage substitute only; the tested path uses the real settings service,
// pixel classifier, quality monitor and production account selector. PostgreSQL
// transaction/lease behavior has separate image_resolution_guard_postgres tests.
type resolutionSchedulingRepo struct {
	ImageResolutionGuardRepository
	mu     sync.Mutex
	states map[string]ImageResolutionCooldown
}

func resolutionSchedulingKey(id int64, model, tier string) string {
	return fmt.Sprintf("%d:%s:%s", id, model, tier)
}
func (r *resolutionSchedulingRepo) Record(_ context.Context, record ImageResolutionRecord) (*ImageResolutionCooldown, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if record.Outcome != "mismatch" {
		return nil, nil
	}
	v := record.Observation
	c := ImageResolutionCooldown{AccountID: v.AccountID, Model: v.Expectation.RequestedModel, SizeTier: v.Expectation.SizeTier, CycleID: v.ID, CooldownUntil: record.Now.Add(time.Duration(record.Settings.CooldownSeconds) * time.Second), Reason: "image_resolution_downgrade"}
	r.states[resolutionSchedulingKey(c.AccountID, c.Model, c.SizeTier)] = c
	return &c, nil
}
func (r *resolutionSchedulingRepo) GetCooldown(_ context.Context, id int64, model, tier string) (*ImageResolutionCooldown, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.states[resolutionSchedulingKey(id, model, tier)]
	if !ok {
		return nil, nil
	}
	return &v, nil
}
func (r *resolutionSchedulingRepo) ListCooldowns(_ context.Context, id int64) ([]ImageResolutionCooldown, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []ImageResolutionCooldown{}
	for _, v := range r.states {
		if v.AccountID == id {
			out = append(out, v)
		}
	}
	return out, nil
}
func (r *resolutionSchedulingRepo) ClearCooldown(_ context.Context, id int64, cycle string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range r.states {
		if v.AccountID == id && v.CycleID == cycle {
			delete(r.states, k)
		}
	}
	return nil
}

func newResolutionSchedulingTest(t *testing.T, rdb *redis.Client) (*ImageResolutionGuardService, *ImageResolutionMonitor, *resolutionSchedulingRepo, *OpenAIGatewayService) {
	t.Helper()
	repo := &resolutionSchedulingRepo{states: map[string]ImageResolutionCooldown{}}
	guard := NewImageResolutionGuardService(repo, newNotificationEmailMemorySettingRepo(), nil, nil, rdb)
	cfg := DefaultImageResolutionGuardSettings()
	cfg.Enabled = true
	cfg.EmailEnabled = false
	_, err := guard.UpdateSettings(context.Background(), &cfg)
	require.NoError(t, err)
	monitor := NewImageResolutionMonitor(guard)
	t.Cleanup(monitor.Close)
	accounts := []Account{{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0}, {ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}}
	svc := &OpenAIGatewayService{accountRepo: stubOpenAIAccountRepo{accounts: accounts}, concurrencyService: NewConcurrencyService(stubConcurrencyCache{}), imageResolutionMonitor: monitor}
	return guard, monitor, repo, svc
}

func resolutionSchedulingContext(t *testing.T, async bool, model, tier string) context.Context {
	t.Helper()
	ctx := WithOpenAIImageGenerationIntent(context.Background())
	if async {
		ctx = WithAsyncImageAccountAttemptCapture(context.Background(), &AsyncImageAccountAttemptCapture{})
	}
	ctx, err := WithImageAccountPoolRoute(ctx, model, tier)
	require.NoError(t, err)
	return ctx
}
func observeResolutionSchedulingMismatch(t *testing.T, guard *ImageResolutionGuardService, id int64, model, tier string) {
	t.Helper()
	v := ImageResolutionObservation{ID: fmt.Sprintf("cool-account-%d-%d", id, time.Now().UnixNano()), AccountID: id, StartedAt: time.Now(), Expectation: ImageResolutionExpectation{RequestedModel: model, UpstreamModel: model, SizeTier: tier, ExpectedWidth: 2880, ExpectedHeight: 2880, ExpectedPixels: 2880 * 2880, RuleVersion: 1}, Outputs: []ImageResolutionOutput{{Width: 1024, Height: 1024}}}
	require.NoError(t, guard.Observe(context.Background(), v))
}
func requireResolutionSchedulingSelection(t *testing.T, svc *OpenAIGatewayService, ctx context.Context, model string, want int64) {
	t.Helper()
	groupID := int64(1)
	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", model, nil)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, want, selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestImageResolutionSchedulingSkipsCoolingAcrossSyncAsyncAndRestoresAfterClear(t *testing.T) {
	guard, monitor, _, svc := newResolutionSchedulingTest(t, nil)
	model := "gpt-image-2"
	syncCtx := resolutionSchedulingContext(t, false, model, "4K")
	asyncCtx := resolutionSchedulingContext(t, true, model, "4K")
	// A is the normal highest-priority candidate. A successful sync image whose
	// actual pixels are low opens a quality cooldown shared with async selection.
	requireResolutionSchedulingSelection(t, svc, syncCtx, model, 1)
	syncBody := `{"data":[{"b64_json":"` + resolutionMonitorPNG(t, 64, 64) + `"}]}`
	syncUpstream := &resolutionMonitorUpstreamStub{response: syncBody, contentType: "application/json"}
	syncResponse, err := monitor.Do(syncUpstream, resolutionMonitorRequest(t, syncCtx, `{"model":"gpt-image-2","size":"2880x2880"}`), "", &Account{ID: 1, Platform: PlatformOpenAI})
	require.NoError(t, err)
	delivered, err := io.ReadAll(syncResponse.Body)
	require.NoError(t, err)
	require.Equal(t, syncBody, string(delivered))
	require.NoError(t, syncResponse.Body.Close())
	require.Equal(t, 1, syncUpstream.calls)
	requireResolutionSchedulingSelection(t, svc, syncCtx, model, 2)
	requireResolutionSchedulingSelection(t, svc, asyncCtx, model, 2)
	for _, ctx := range []context.Context{syncCtx, asyncCtx} {
		selected, err := svc.getSchedulableAccount(ctx, 1)
		require.NoError(t, err)
		require.Nil(t, selected, "sticky account rechecks must also reject A")
	}
	// Cooling is exact account/request-model/tier; text, other models and other
	// requested resolutions retain A's original scheduling eligibility.
	requireResolutionSchedulingSelection(t, svc, resolutionSchedulingContext(t, false, model, "2K"), model, 1)
	requireResolutionSchedulingSelection(t, svc, resolutionSchedulingContext(t, false, "gpt-image-1", "4K"), "gpt-image-1", 1)
	requireResolutionSchedulingSelection(t, svc, context.Background(), "gpt-4.1", 1)
	// The send gate remains a second barrier if A was selected just before the
	// concurrent cooldown began. It must reject before any upstream HTTP send.
	upstream := &resolutionMonitorUpstreamStub{response: `{"data":[]}`, contentType: "application/json"}
	req := resolutionMonitorRequest(t, syncCtx, `{"model":"gpt-image-2","size":"2880x2880"}`)
	_, err = monitor.Admit(req, &Account{ID: 1})
	require.True(t, isImageResolutionCooling(err))
	require.Zero(t, upstream.calls)
	_, err = monitor.Do(upstream, req, "", &Account{ID: 1})
	require.True(t, isImageResolutionCooling(err))
	require.Zero(t, upstream.calls)
	observeResolutionSchedulingMismatch(t, guard, 2, model, "4K")
	groupID := int64(1)
	selection, err := svc.SelectAccountWithLoadAwareness(asyncCtx, &groupID, "", model, nil)
	require.Error(t, err)
	require.Nil(t, selection, "an entirely cooled pool must not reselect a blocked account")
	cfg := DefaultImageResolutionGuardSettings()
	cfg.Enabled = false
	cfg.EmailEnabled = false
	_, err = guard.UpdateSettings(context.Background(), &cfg)
	require.NoError(t, err)
	requireResolutionSchedulingSelection(t, svc, asyncCtx, model, 1)
	cfg.Enabled = true
	_, err = guard.UpdateSettings(context.Background(), &cfg)
	require.NoError(t, err)
	selection, err = svc.SelectAccountWithLoadAwareness(asyncCtx, &groupID, "", model, nil)
	require.Error(t, err)
	require.Nil(t, selection, "re-enabling must honor the original unexpired cooldown")
	items, err := guard.ListCooldowns(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NoError(t, guard.ClearCooldown(context.Background(), 1, items[0].CycleID))
	requireResolutionSchedulingSelection(t, svc, syncCtx, model, 1)
	requireResolutionSchedulingSelection(t, svc, asyncCtx, model, 1)
	admitted, err := monitor.Admit(req, &Account{ID: 1})
	require.NoError(t, err)
	response, err := monitor.Do(upstream, admitted, "", &Account{ID: 1})
	require.NoError(t, err)
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, 1, upstream.calls)
}

func TestImageResolutionSchedulingRemoteClearPositiveCacheExpiresWithinTwoSeconds(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	guard, _, repo, svc := newResolutionSchedulingTest(t, client)
	ctx := resolutionSchedulingContext(t, true, "gpt-image-2", "4K")
	observeResolutionSchedulingMismatch(t, guard, 1, "gpt-image-2", "4K")
	requireResolutionSchedulingSelection(t, svc, ctx, "gpt-image-2", 2)
	key := imageResolutionCacheKey(1, "gpt-image-2", "4K")
	require.LessOrEqual(t, server.TTL(key), 2*time.Second)
	require.Positive(t, server.TTL(key))
	// Model a clear committed by another instance while its Redis DEL failed.
	// A may conservatively remain skipped only until the 2s positive cache TTL.
	items, err := guard.ListCooldowns(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NoError(t, repo.ClearCooldown(context.Background(), 1, items[0].CycleID, time.Now()))
	requireResolutionSchedulingSelection(t, svc, ctx, "gpt-image-2", 2)
	server.FastForward(2 * time.Second)
	requireResolutionSchedulingSelection(t, svc, ctx, "gpt-image-2", 1)
	// Losing Redis state never loses a durable active cooldown: miss -> DB.
	observeResolutionSchedulingMismatch(t, guard, 1, "gpt-image-2", "4K")
	server.FlushAll()
	requireResolutionSchedulingSelection(t, svc, ctx, "gpt-image-2", 2)
	repo.mu.Lock()
	expired := repo.states[resolutionSchedulingKey(1, "gpt-image-2", "4K")]
	expired.CooldownUntil = time.Now().Add(-time.Second)
	repo.states[resolutionSchedulingKey(1, "gpt-image-2", "4K")] = expired
	repo.mu.Unlock()
	server.FastForward(2 * time.Second)
	requireResolutionSchedulingSelection(t, svc, ctx, "gpt-image-2", 1)
}

func TestImageResolutionSchedulingAdvancedTopKExcludesCoolingBeforeRanking(t *testing.T) {
	guard, _, _, svc := newResolutionSchedulingTest(t, nil)
	ctx := resolutionSchedulingContext(t, true, "gpt-image-2", "4K")
	observeResolutionSchedulingMismatch(t, guard, 1, "gpt-image-2", "4K")
	accounts := []Account{{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0}, {ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 5}}
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: accounts}
	svc.cache = &schedulerTestGatewayCache{}
	svc.cfg = &config.Config{}
	svc.cfg.Gateway.OpenAIWS.LBTopK = 1
	svc.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 1
	svc.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Load = 1
	svc.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Queue = 1
	svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
	svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{})
	groupID := int64(1)
	selection, decision, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "gpt-image-2", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(2), selection.Account.ID)
	require.Equal(t, 1, decision.CandidateCount, "cooled A must be removed before TopK=1 can crowd out usable B")
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestImageResolutionSchedulingGatewayAndGeminiCandidatesShareExactCooldown(t *testing.T) {
	guard, monitor, _, _ := newResolutionSchedulingTest(t, nil)
	model := "gemini-3-pro-image-preview"
	ctx := WithGeminiImageGenerationIntent(context.Background())
	ctx, err := WithImageAccountPoolRoute(ctx, model, "4K")
	require.NoError(t, err)
	accounts := []Account{{ID: 1, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0}, {ID: 2, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}}
	gateway := &GatewayService{accountRepo: stubOpenAIAccountRepo{accounts: accounts}, imageResolutionMonitor: monitor}
	compat := &GeminiMessagesCompatService{imageResolutionMonitor: monitor}
	observeResolutionSchedulingMismatch(t, guard, 1, model, "4K")
	for _, base := range []context.Context{ctx, WithAsyncImageAccountAttemptCapture(ctx, &AsyncImageAccountAttemptCapture{})} {
		filtered := gateway.filterImageCircuitOpenAccounts(base, accounts)
		require.Len(t, filtered, 1)
		require.Equal(t, int64(2), filtered[0].ID)
		sticky, err := gateway.getSchedulableAccount(base, 1)
		require.NoError(t, err)
		require.Nil(t, sticky)
		selected := compat.selectBestGeminiAccount(base, accounts, model, nil, PlatformGemini, false)
		require.NotNil(t, selected)
		require.Equal(t, int64(2), selected.ID)
	}
	observeResolutionSchedulingMismatch(t, guard, 2, model, "4K")
	require.Empty(t, gateway.filterImageCircuitOpenAccounts(ctx, accounts))
	require.Nil(t, compat.selectBestGeminiAccount(ctx, accounts, model, nil, PlatformGemini, false))
	items, err := guard.ListCooldowns(context.Background(), 1)
	require.NoError(t, err)
	require.NoError(t, guard.ClearCooldown(context.Background(), 1, items[0].CycleID))
	selected := compat.selectBestGeminiAccount(ctx, accounts, model, nil, PlatformGemini, false)
	require.NotNil(t, selected)
	require.Equal(t, int64(1), selected.ID)
}
