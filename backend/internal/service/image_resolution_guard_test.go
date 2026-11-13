package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type imageResolutionRepoStub struct {
	ImageResolutionGuardRepository
	mu         sync.Mutex
	records    []ImageResolutionRecord
	cooldown   *ImageResolutionCooldown
	getCount   int
	mailStatus string
	delivered  []string
	available  time.Time
}

func (r *imageResolutionRepoStub) Record(_ context.Context, v ImageResolutionRecord) (*ImageResolutionCooldown, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, v)
	return r.cooldown, nil
}
func (r *imageResolutionRepoStub) GetCooldown(context.Context, int64, string, string) (*ImageResolutionCooldown, error) {
	r.getCount++
	return r.cooldown, nil
}
func (r *imageResolutionRepoStub) ListCooldowns(context.Context, int64) ([]ImageResolutionCooldown, error) {
	if r.cooldown == nil {
		return []ImageResolutionCooldown{}, nil
	}
	return []ImageResolutionCooldown{*r.cooldown}, nil
}
func (r *imageResolutionRepoStub) ClearCooldown(context.Context, int64, string, time.Time) error {
	r.cooldown = nil
	return nil
}
func (r *imageResolutionRepoStub) SaveMailProgress(_ context.Context, _ int64, _ string, v []string) error {
	r.delivered = append([]string{}, v...)
	return nil
}
func (r *imageResolutionRepoStub) FinishMail(_ context.Context, _ int64, _ string, status string, v []string, next time.Time, _ string) error {
	r.mailStatus = status
	r.delivered = append([]string{}, v...)
	r.available = next
	return nil
}

type imageResolutionUserStub struct {
	UserRepository
	admin *User
}

func (r imageResolutionUserStub) GetFirstAdmin(context.Context) (*User, error) { return r.admin, nil }

func resolutionObservation() ImageResolutionObservation {
	return ImageResolutionObservation{ID: "image-1", AccountID: 12, AccountName: "channel", RequestID: "req-1", Expectation: ImageResolutionExpectation{RequestedModel: "gpt-image-2", UpstreamModel: "gpt-image-2", SizeTier: "4K", ExpectedWidth: 2880, ExpectedHeight: 2880, ExpectedPixels: 2880 * 2880, RuleVersion: 1}, Outputs: []ImageResolutionOutput{{Width: 1024, Height: 1024}}, StartedAt: time.Now().UTC()}
}

func TestImageResolutionGuardAreaBoundariesAndMultiImage(t *testing.T) {
	v := resolutionObservation()
	v.Expectation.ExpectedPixels = 400
	v.Expectation.ExpectedWidth = 20
	v.Expectation.ExpectedHeight = 20
	for _, tc := range []struct {
		name    string
		outputs []ImageResolutionOutput
		want    string
	}{
		{"below", []ImageResolutionOutput{{Width: 13, Height: 23}}, "mismatch"},
		{"exactly75percent", []ImageResolutionOutput{{Width: 15, Height: 20}}, "pass"},
		{"larger", []ImageResolutionOutput{{Width: 30, Height: 20}}, "pass"},
		{"oneBad", []ImageResolutionOutput{{Width: 20, Height: 20}, {Width: 10, Height: 10}}, "mismatch"},
		{"partialUnknown", []ImageResolutionOutput{{Width: 20, Height: 20}, {}}, "unknown"},
		{"badAndUnknown", []ImageResolutionOutput{{Width: 10, Height: 10}, {}}, "mismatch"},
		{"noOutputs", nil, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) { v.Outputs = tc.outputs; require.Equal(t, tc.want, imageResolutionOutcome(v, 75)) })
	}
	v = resolutionObservation()
	v.Outputs = []ImageResolutionOutput{{Width: 2880, Height: 2880}}
	require.Equal(t, "pass", imageResolutionOutcome(v, 75))
	v.Expectation.ExpectedPixels = 0
	v.Expectation.ExpectedWidth = 0
	require.Equal(t, "unknown", imageResolutionOutcome(v, 75))
}

func TestImageResolutionGuardSettingsValidationAndRecipientFallback(t *testing.T) {
	ctx := context.Background()
	settings := newNotificationEmailMemorySettingRepo()
	repo := &imageResolutionRepoStub{}
	s := NewImageResolutionGuardService(repo, settings, nil, imageResolutionUserStub{admin: &User{Email: "admin@example.com", Status: StatusActive}}, nil)
	view, err := s.Settings(ctx)
	require.NoError(t, err)
	require.False(t, view.Enabled)
	require.Equal(t, 75, view.MinAreaPercent)
	require.Equal(t, []string{"admin@example.com"}, view.EffectiveRecipients)
	require.Equal(t, "admin", view.RecipientSource)
	require.Contains(t, view.Warnings, "smtp_not_configured")
	cfg := DefaultImageResolutionGuardSettings()
	cfg.Enabled = true
	cfg.Recipients = []string{"ops@example.com", "OPS@example.com"}
	view, err = s.UpdateSettings(ctx, &cfg)
	require.NoError(t, err)
	require.Equal(t, []string{"ops@example.com"}, view.Recipients)
	require.Equal(t, "override", view.RecipientSource)
	for _, mutate := range []func(*ImageResolutionGuardSettings){func(c *ImageResolutionGuardSettings) { c.MinAreaPercent = 0 }, func(c *ImageResolutionGuardSettings) { c.MinAreaPercent = 101 }, func(c *ImageResolutionGuardSettings) { c.FailureThreshold = 101 }, func(c *ImageResolutionGuardSettings) { c.CooldownSeconds = 9 }, func(c *ImageResolutionGuardSettings) { c.CooldownSeconds = 86401 }, func(c *ImageResolutionGuardSettings) { c.Recipients = []string{"Name <ops@example.com>"} }} {
		bad := cfg
		mutate(&bad)
		_, err = s.UpdateSettings(ctx, &bad)
		require.Error(t, err)
	}
	_, err = s.UpdateSettings(ctx, nil)
	require.Error(t, err)
}

func TestImageResolutionGuardObserveDisabledAndIndependentBilling(t *testing.T) {
	ctx := context.Background()
	settings := newNotificationEmailMemorySettingRepo()
	repo := &imageResolutionRepoStub{}
	s := NewImageResolutionGuardService(repo, settings, nil, nil, nil)
	v := resolutionObservation()
	require.NoError(t, s.Observe(ctx, v))
	require.Empty(t, repo.records)
	cfg := DefaultImageResolutionGuardSettings()
	cfg.Enabled = true
	cfg.Recipients = []string{"ops@example.com"}
	_, err := s.UpdateSettings(ctx, &cfg)
	require.NoError(t, err)
	require.NoError(t, s.Observe(ctx, v))
	require.Len(t, repo.records, 1)
	require.Equal(t, "mismatch", repo.records[0].Outcome)
	require.Equal(t, []string{"ops@example.com"}, repo.records[0].Recipients)
	// The quality service records metadata only: no usage/billing service exists
	// on it, and detecting one request cannot replace its output or regenerate.
	require.Equal(t, 1024, v.Outputs[0].Width)
}

func TestImageResolutionGuardRedisLossDisableReenableAndClear(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	repo := &imageResolutionRepoStub{cooldown: &ImageResolutionCooldown{AccountID: 12, Model: "model", SizeTier: "4K", CycleID: "cycle", CooldownUntil: time.Now().Add(time.Minute)}}
	s := NewImageResolutionGuardService(repo, newNotificationEmailMemorySettingRepo(), nil, nil, client)
	cfg := DefaultImageResolutionGuardSettings()
	cfg.Enabled = true
	_, err := s.UpdateSettings(ctx, &cfg)
	require.NoError(t, err)
	open, err := s.IsCooling(ctx, 12, "model", "4K")
	require.NoError(t, err)
	require.True(t, open)
	require.Equal(t, 1, repo.getCount)
	open, err = s.IsCooling(ctx, 12, "model", "4K")
	require.NoError(t, err)
	require.True(t, open)
	require.Equal(t, 1, repo.getCount)
	server.FlushAll()
	open, err = s.IsCooling(ctx, 12, "model", "4K")
	require.NoError(t, err)
	require.True(t, open)
	require.Equal(t, 2, repo.getCount)
	cfg.Enabled = false
	_, err = s.UpdateSettings(ctx, &cfg)
	require.NoError(t, err)
	open, err = s.IsCooling(ctx, 12, "model", "4K")
	require.NoError(t, err)
	require.False(t, open)
	require.NotNil(t, repo.cooldown)
	cfg.Enabled = true
	_, err = s.UpdateSettings(ctx, &cfg)
	require.NoError(t, err)
	open, err = s.IsCooling(ctx, 12, "model", "4K")
	require.NoError(t, err)
	require.True(t, open)
	require.NoError(t, s.ClearCooldown(ctx, 12, "cycle"))
	open, err = s.IsCooling(ctx, 12, "model", "4K")
	require.NoError(t, err)
	require.False(t, open)
}

func TestImageResolutionGuardMailRetryAndPerRecipientProgress(t *testing.T) {
	ctx := context.Background()
	repo := &imageResolutionRepoStub{}
	s := NewImageResolutionGuardService(repo, nil, nil, nil, nil)
	cfg := DefaultImageResolutionGuardSettings()
	cfg.Enabled = true
	s.cached = cfg
	s.cacheUntil = time.Now().Add(time.Hour)
	event := ImageResolutionMailEvent{ID: 1, CycleID: "stable-cycle", ClaimToken: "claim", Observation: resolutionObservation(), CooldownUntil: time.Now().Add(time.Minute), MinAreaPercent: 75, Recipients: []string{"ok@example.com", "fail@example.com"}, Attempts: 1}
	sent := map[string]int{}
	s.send = func(_ context.Context, in NotificationEmailSendInput) error {
		sent[in.RecipientEmail]++
		require.Equal(t, "stable-cycle", in.SourceID)
		if in.RecipientEmail == "fail@example.com" {
			return errors.New("smtp failure")
		}
		return nil
	}
	before := time.Now()
	s.deliverEvent(ctx, cfg, event)
	require.Equal(t, "pending", repo.mailStatus)
	require.Equal(t, []string{"ok@example.com"}, repo.delivered)
	require.WithinDuration(t, before.Add(time.Minute), repo.available, time.Second)
	event.Attempts = 2
	event.Delivered = repo.delivered
	s.deliverEvent(ctx, cfg, event)
	require.Equal(t, 1, sent["ok@example.com"])
	require.Equal(t, 2, sent["fail@example.com"])
	require.Equal(t, "pending", repo.mailStatus)
	require.WithinDuration(t, time.Now().Add(5*time.Minute), repo.available, time.Second)
	event.Attempts = 3
	s.deliverEvent(ctx, cfg, event)
	require.Equal(t, "failed", repo.mailStatus)
	cfg.EmailEnabled = false
	s.deliverEvent(ctx, cfg, event)
	require.Equal(t, "skipped", repo.mailStatus)
}

func TestImageResolutionGuardDisableDuringMailDelivery(t *testing.T) {
	ctx := context.Background()
	repo := &imageResolutionRepoStub{}
	s := NewImageResolutionGuardService(repo, newNotificationEmailMemorySettingRepo(), nil, nil, nil)
	cfg := DefaultImageResolutionGuardSettings()
	cfg.Enabled = true
	_, err := s.UpdateSettings(ctx, &cfg)
	require.NoError(t, err)
	calls := 0
	s.send = func(_ context.Context, _ NotificationEmailSendInput) error {
		calls++
		cfg.EmailEnabled = false
		_, err := s.UpdateSettings(ctx, &cfg)
		require.NoError(t, err)
		return nil
	}
	s.deliverEvent(ctx, ImageResolutionGuardSettings{Enabled: true, EmailEnabled: true}, ImageResolutionMailEvent{ID: 1, ClaimToken: "claim", Recipients: []string{"first@example.com", "second@example.com"}})
	require.Equal(t, 1, calls)
	require.Equal(t, "skipped", repo.mailStatus)
	require.Equal(t, []string{"first@example.com"}, repo.delivered)
}

func TestImageResolutionGuardOfficialMailTemplates(t *testing.T) {
	s := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil)
	for _, locale := range []string{"zh", "en"} {
		template, err := s.GetTemplate(context.Background(), NotificationEmailEventImageResolutionMismatch, locale)
		require.NoError(t, err)
		require.Contains(t, template.HTML, "{{actual_dimensions}}")
		preview, err := s.PreviewTemplate(context.Background(), NotificationEmailPreviewInput{Event: NotificationEmailEventImageResolutionMismatch, Locale: locale, Subject: template.Subject, HTML: template.HTML, Variables: map[string]string{"account_name": "<script>"}})
		require.NoError(t, err)
		require.NotContains(t, preview.HTML, "<script>")
		require.Contains(t, preview.HTML, "&lt;script&gt;")
	}
}

func TestImageResolutionGuardRealSMTPDeliveryAndDeduplication(t *testing.T) {
	ctx := context.Background()
	settings := newNotificationEmailMemorySettingRepo()
	smtp := startNotificationEmailTestSMTPServer(t)
	require.NoError(t, settings.SetMultiple(ctx, smtp.settings()))
	mailer := NewNotificationEmailService(settings, NewEmailService(settings, nil))
	repo := &imageResolutionRepoStub{}
	s := NewImageResolutionGuardService(repo, settings, mailer, nil, nil)
	cfg := DefaultImageResolutionGuardSettings()
	cfg.Enabled = true
	cfg.Recipients = []string{"one@example.com", "two@example.com"}
	view, err := s.UpdateSettings(ctx, &cfg)
	require.NoError(t, err)
	require.True(t, view.SMTPConfigured)
	event := ImageResolutionMailEvent{ID: 1, CycleID: "smtp-cycle", ClaimToken: "claim", Observation: resolutionObservation(), CooldownUntil: time.Now().Add(time.Minute), MinAreaPercent: 75, Recipients: cfg.Recipients, Attempts: 1}
	event.Observation.AccountName = "<script>channel</script>"
	s.deliverEvent(ctx, cfg, event)
	require.Equal(t, "sent", repo.mailStatus)
	require.EqualValues(t, 2, smtp.messageCount())
	body := smtp.lastMessageBody(t)
	require.Contains(t, body, "1024x1024")
	require.Contains(t, body, "2880x2880")
	require.Contains(t, body, "12.64%")
	require.NotContains(t, body, "<script>")
	s.deliverEvent(ctx, cfg, event)
	require.EqualValues(t, 2, smtp.messageCount())
}

func TestImageResolutionGuardSettingsPubSubAndTTLRefresh(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	settings := newNotificationEmailMemorySettingRepo()
	first := NewImageResolutionGuardService(nil, settings, nil, nil, client)
	second := NewImageResolutionGuardService(nil, settings, nil, nil, client)
	require.False(t, second.Enabled(ctx))
	second.Start()
	t.Cleanup(second.Stop)
	require.Eventually(t, func() bool {
		counts, err := client.PubSubNumSub(ctx, imageResolutionSettingsChannel).Result()
		return err == nil && counts[imageResolutionSettingsChannel] > 0
	}, time.Second, 10*time.Millisecond)
	cfg := DefaultImageResolutionGuardSettings()
	cfg.Enabled = true
	_, err := first.UpdateSettings(ctx, &cfg)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return second.Enabled(ctx) }, time.Second, 10*time.Millisecond)
	// Simulate a missed invalidation: the short cache expiration still reloads
	// persisted configuration rather than leaving another instance enabled.
	require.NoError(t, settings.Set(ctx, SettingKeyImageResolutionGuard, `{"enabled":false}`))
	second.mu.Lock()
	second.cacheUntil = time.Now().Add(-time.Second)
	second.mu.Unlock()
	require.False(t, second.Enabled(ctx))
}
