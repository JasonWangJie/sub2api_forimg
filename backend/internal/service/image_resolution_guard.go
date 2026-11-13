package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/mail"
	"strings"
	"sync"
	"time"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const SettingKeyImageResolutionGuard = "image_resolution_guard"
const imageResolutionSettingsChannel = "settings:image-resolution-guard:invalidate"

type ImageResolutionGuardSettings struct {
	Enabled          bool     `json:"enabled"`
	MinAreaPercent   int      `json:"min_area_percent"`
	FailureThreshold int      `json:"failure_threshold"`
	CooldownSeconds  int      `json:"cooldown_seconds"`
	EmailEnabled     bool     `json:"email_enabled"`
	Recipients       []string `json:"recipients"`
}

type ImageResolutionGuardSettingsView struct {
	ImageResolutionGuardSettings
	EffectiveRecipients []string `json:"effective_recipients"`
	RecipientSource     string   `json:"recipient_source"`
	SMTPConfigured      bool     `json:"smtp_configured"`
	Warnings            []string `json:"warnings"`
}

func DefaultImageResolutionGuardSettings() ImageResolutionGuardSettings {
	return ImageResolutionGuardSettings{MinAreaPercent: 75, FailureThreshold: 1, CooldownSeconds: 600, EmailEnabled: true, Recipients: []string{}}
}

type ImageResolutionObservation struct {
	ID          string                     `json:"id"`
	AccountID   int64                      `json:"account_id"`
	AccountName string                     `json:"account_name,omitempty"`
	Platform    string                     `json:"platform,omitempty"`
	TaskID      string                     `json:"task_id,omitempty"`
	RequestID   string                     `json:"request_id,omitempty"`
	Expectation ImageResolutionExpectation `json:"expectation"`
	Outputs     []ImageResolutionOutput    `json:"outputs"`
	StartedAt   time.Time                  `json:"started_at"`
}

type ImageResolutionCooldown struct {
	AccountID       int64     `json:"account_id"`
	Model           string    `json:"model"`
	SizeTier        string    `json:"size_tier"`
	CycleID         string    `json:"cycle_id"`
	CooldownUntil   time.Time `json:"cooldown_until"`
	FailureCount    int       `json:"failure_count"`
	Reason          string    `json:"reason"`
	RequestedWidth  int       `json:"requested_width,omitempty"`
	RequestedHeight int       `json:"requested_height,omitempty"`
	ActualWidth     int       `json:"actual_width,omitempty"`
	ActualHeight    int       `json:"actual_height,omitempty"`
	AreaPercent     float64   `json:"area_percent,omitempty"`
}

type ImageResolutionRecord struct {
	Observation ImageResolutionObservation
	Outcome     string
	Settings    ImageResolutionGuardSettings
	Recipients  []string
	Now         time.Time
}

type ImageResolutionMailEvent struct {
	ID             int64
	CycleID        string
	Observation    ImageResolutionObservation
	CooldownUntil  time.Time
	MinAreaPercent int
	Recipients     []string
	Delivered      []string
	Attempts       int
	ClaimToken     string
}

type ImageResolutionGuardRepository interface {
	Record(context.Context, ImageResolutionRecord) (*ImageResolutionCooldown, error)
	GetCooldown(context.Context, int64, string, string) (*ImageResolutionCooldown, error)
	ListCooldowns(context.Context, int64) ([]ImageResolutionCooldown, error)
	ClearCooldown(context.Context, int64, string, time.Time) error
	ClaimMail(context.Context, string, int, time.Duration) ([]ImageResolutionMailEvent, error)
	SaveMailProgress(context.Context, int64, string, []string) error
	FinishMail(context.Context, int64, string, string, []string, time.Time, string) error
}

type ImageResolutionGuardMaintenanceRepository interface {
	Prune(context.Context, time.Time) (int64, error)
	FailedMailCount(context.Context) (int64, error)
}

// The database is authoritative. Redis stores positive cooldowns only; a cache
// miss goes to PostgreSQL so a missed notification cannot admit new requests.
type ImageResolutionGuardService struct {
	repo       ImageResolutionGuardRepository
	settings   SettingRepository
	email      *NotificationEmailService
	users      UserRepository
	rdb        *redis.Client
	mu         sync.Mutex
	cached     ImageResolutionGuardSettings
	cacheUntil time.Time
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	send       func(context.Context, NotificationEmailSendInput) error
}

func NewImageResolutionGuardService(repo ImageResolutionGuardRepository, settings SettingRepository, email *NotificationEmailService, users UserRepository, rdb *redis.Client) *ImageResolutionGuardService {
	s := &ImageResolutionGuardService{repo: repo, settings: settings, email: email, users: users, rdb: rdb, cached: DefaultImageResolutionGuardSettings()}
	if email != nil {
		s.send = email.Send
	}
	return s
}

func normalizeImageResolutionGuardSettings(v *ImageResolutionGuardSettings) error {
	if v == nil {
		return errors.New("settings cannot be nil")
	}
	if v.MinAreaPercent < 1 || v.MinAreaPercent > 100 {
		return errors.New("min_area_percent must be between 1 and 100")
	}
	if v.FailureThreshold < 1 || v.FailureThreshold > 100 {
		return errors.New("failure_threshold must be between 1 and 100")
	}
	if v.CooldownSeconds < 10 || v.CooldownSeconds > 86400 {
		return errors.New("cooldown_seconds must be between 10 and 86400")
	}
	if len(v.Recipients) > 20 {
		return errors.New("at most 20 recipients are allowed")
	}
	seen := map[string]bool{}
	recipients := []string{}
	for _, raw := range v.Recipients {
		addr := strings.TrimSpace(raw)
		parsed, err := mail.ParseAddress(addr)
		if err != nil || parsed.Address != addr || strings.ContainsAny(addr, "\r\n") {
			return errors.New("recipients must contain valid email addresses")
		}
		key := strings.ToLower(addr)
		if !seen[key] {
			recipients = append(recipients, addr)
			seen[key] = true
		}
	}
	v.Recipients = recipients
	return nil
}

func cloneImageResolutionSettings(v ImageResolutionGuardSettings) ImageResolutionGuardSettings {
	v.Recipients = append([]string{}, v.Recipients...)
	return v
}

func (s *ImageResolutionGuardService) runtimeSettings(ctx context.Context) (ImageResolutionGuardSettings, error) {
	if s == nil {
		return DefaultImageResolutionGuardSettings(), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Now().Before(s.cacheUntil) {
		return cloneImageResolutionSettings(s.cached), nil
	}
	v := DefaultImageResolutionGuardSettings()
	if s.settings != nil {
		raw, err := s.settings.GetValue(ctx, SettingKeyImageResolutionGuard)
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			return cloneImageResolutionSettings(s.cached), err
		}
		if err == nil && strings.TrimSpace(raw) != "" {
			if err = json.Unmarshal([]byte(raw), &v); err != nil {
				return cloneImageResolutionSettings(s.cached), fmt.Errorf("decode image resolution settings: %w", err)
			}
		}
	}
	if err := normalizeImageResolutionGuardSettings(&v); err != nil {
		return cloneImageResolutionSettings(s.cached), err
	}
	s.cached = cloneImageResolutionSettings(v)
	s.cacheUntil = time.Now().Add(5 * time.Second)
	return v, nil
}

func (s *ImageResolutionGuardService) effectiveRecipients(ctx context.Context, cfg ImageResolutionGuardSettings) ([]string, string) {
	if len(cfg.Recipients) > 0 {
		return append([]string{}, cfg.Recipients...), "override"
	}
	if s.users != nil {
		admin, err := s.users.GetFirstAdmin(ctx)
		if err == nil && admin != nil && admin.Status == StatusActive {
			addr := strings.TrimSpace(admin.Email)
			if p, e := mail.ParseAddress(addr); e == nil && p.Address == addr {
				return []string{addr}, "admin"
			}
		}
	}
	return []string{}, "none"
}

func (s *ImageResolutionGuardService) Settings(ctx context.Context) (*ImageResolutionGuardSettingsView, error) {
	cfg, err := s.runtimeSettings(ctx)
	if err != nil {
		return nil, err
	}
	view := &ImageResolutionGuardSettingsView{ImageResolutionGuardSettings: cfg, Warnings: []string{}}
	view.EffectiveRecipients, view.RecipientSource = s.effectiveRecipients(ctx, cfg)
	if s.email != nil && s.email.emailService != nil {
		smtp, e := s.email.emailService.GetSMTPConfig(ctx)
		view.SMTPConfigured = e == nil && smtp != nil && smtp.Host != ""
	}
	if cfg.EmailEnabled && !view.SMTPConfigured {
		view.Warnings = append(view.Warnings, "smtp_not_configured")
	}
	if cfg.EmailEnabled && len(view.EffectiveRecipients) == 0 {
		view.Warnings = append(view.Warnings, "no_effective_recipients")
	}
	if maintenance, ok := s.repo.(ImageResolutionGuardMaintenanceRepository); ok {
		if count, e := maintenance.FailedMailCount(ctx); e == nil && count > 0 {
			view.Warnings = append(view.Warnings, "mail_delivery_failed")
		}
	}
	return view, nil
}

func (s *ImageResolutionGuardService) UpdateSettings(ctx context.Context, cfg *ImageResolutionGuardSettings) (*ImageResolutionGuardSettingsView, error) {
	if err := normalizeImageResolutionGuardSettings(cfg); err != nil {
		return nil, apperrors.BadRequest("IMAGE_RESOLUTION_GUARD_INVALID_SETTINGS", err.Error())
	}
	if s == nil || s.settings == nil {
		return nil, errors.New("settings repository unavailable")
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err = s.settings.Set(ctx, SettingKeyImageResolutionGuard, string(raw)); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cached = cloneImageResolutionSettings(*cfg)
	s.cacheUntil = time.Now().Add(5 * time.Second)
	s.mu.Unlock()
	if s.rdb != nil {
		pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if e := s.rdb.Publish(pubCtx, imageResolutionSettingsChannel, "refresh").Err(); e != nil {
			slog.Warn("image resolution settings notification failed")
		}
	}
	return s.Settings(ctx)
}

func imageResolutionOutcome(v ImageResolutionObservation, percent int) string {
	expected := v.Expectation.ExpectedPixels
	if expected <= 0 && v.Expectation.ExpectedWidth > 0 && v.Expectation.ExpectedHeight > 0 {
		expected = int64(v.Expectation.ExpectedWidth) * int64(v.Expectation.ExpectedHeight)
	}
	if expected <= 0 || len(v.Outputs) == 0 {
		return "unknown"
	}
	all := true
	for _, out := range v.Outputs {
		if out.Width <= 0 || out.Height <= 0 {
			all = false
			continue
		}
		actual := new(big.Int).Mul(big.NewInt(int64(out.Width)), big.NewInt(int64(out.Height)))
		actual.Mul(actual, big.NewInt(100))
		minimum := new(big.Int).Mul(big.NewInt(expected), big.NewInt(int64(percent)))
		if actual.Cmp(minimum) < 0 {
			return "mismatch"
		}
	}
	if all {
		return "pass"
	}
	return "unknown"
}

func (s *ImageResolutionGuardService) Observe(ctx context.Context, v ImageResolutionObservation) error {
	cfg, err := s.runtimeSettings(ctx)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}
	if s.repo == nil {
		return errors.New("image resolution repository unavailable")
	}
	v.Expectation.RequestedModel = strings.TrimSpace(v.Expectation.RequestedModel)
	v.Expectation.SizeTier = strings.ToUpper(strings.TrimSpace(v.Expectation.SizeTier))
	if v.ID == "" || v.AccountID <= 0 || v.Expectation.RequestedModel == "" || v.Expectation.SizeTier == "" {
		return nil
	}
	if len(v.ID) > 512 {
		sum := sha256.Sum256([]byte(v.ID))
		v.ID = hex.EncodeToString(sum[:])
	}
	if v.StartedAt.IsZero() {
		v.StartedAt = time.Now().UTC()
	}
	outcome := imageResolutionOutcome(v, cfg.MinAreaPercent)
	var recipients []string
	if outcome == "mismatch" && cfg.EmailEnabled {
		recipients, _ = s.effectiveRecipients(ctx, cfg)
	}
	cooldown, err := s.repo.Record(ctx, ImageResolutionRecord{Observation: v, Outcome: outcome, Settings: cfg, Recipients: recipients, Now: time.Now().UTC()})
	if err != nil {
		return err
	}
	if cooldown != nil {
		s.cacheCooldown(ctx, cooldown)
	}
	return nil
}

func (s *ImageResolutionGuardService) Enabled(ctx context.Context) bool {
	cfg, err := s.runtimeSettings(ctx)
	return err == nil && cfg.Enabled
}

func imageResolutionCacheKey(accountID int64, model, tier string) string {
	sum := sha256.Sum256([]byte(model + "\x00" + tier))
	return fmt.Sprintf("image:resolution-quality:%d:%x", accountID, sum[:16])
}

func (s *ImageResolutionGuardService) cacheCooldown(ctx context.Context, v *ImageResolutionCooldown) {
	if s.rdb == nil || v == nil {
		return
	}
	ttl := time.Until(v.CooldownUntil)
	if ttl <= 0 {
		return
	}
	// Short positive cache bounds stale blocks after a manual clear even when
	// Redis is unavailable for the clearing instance.
	if ttl > 2*time.Second {
		ttl = 2 * time.Second
	}
	raw, _ := json.Marshal(v)
	_ = s.rdb.Set(ctx, imageResolutionCacheKey(v.AccountID, v.Model, v.SizeTier), raw, ttl).Err()
}

func (s *ImageResolutionGuardService) IsCooling(ctx context.Context, id int64, model, tier string) (bool, error) {
	cfg, err := s.runtimeSettings(ctx)
	if err != nil {
		return false, err
	}
	if !cfg.Enabled || s.repo == nil || id <= 0 || model == "" || tier == "" {
		return false, nil
	}
	model = strings.TrimSpace(model)
	tier = strings.ToUpper(strings.TrimSpace(tier))
	if s.rdb != nil {
		raw, e := s.rdb.Get(ctx, imageResolutionCacheKey(id, model, tier)).Bytes()
		if e == nil {
			var v ImageResolutionCooldown
			if json.Unmarshal(raw, &v) == nil && time.Now().Before(v.CooldownUntil) {
				return true, nil
			}
		}
	}
	v, err := s.repo.GetCooldown(ctx, id, model, tier)
	if err != nil {
		return false, err
	}
	if v == nil || !time.Now().Before(v.CooldownUntil) {
		return false, nil
	}
	s.cacheCooldown(ctx, v)
	return true, nil
}

func (s *ImageResolutionGuardService) ListCooldowns(ctx context.Context, id int64) ([]ImageResolutionCooldown, error) {
	if s == nil || s.repo == nil {
		return []ImageResolutionCooldown{}, nil
	}
	return s.repo.ListCooldowns(ctx, id)
}
func (s *ImageResolutionGuardService) ClearCooldown(ctx context.Context, id int64, cycle string) error {
	if s == nil || s.repo == nil {
		return errors.New("image resolution repository unavailable")
	}
	items, err := s.repo.ListCooldowns(ctx, id)
	if err != nil {
		return err
	}
	if err = s.repo.ClearCooldown(ctx, id, cycle, time.Now().UTC()); err != nil {
		return err
	}
	if s.rdb != nil {
		for _, item := range items {
			if item.CycleID == cycle {
				_ = s.rdb.Del(ctx, imageResolutionCacheKey(id, item.Model, item.SizeTier)).Err()
			}
		}
	}
	return nil
}

func (s *ImageResolutionGuardService) Start() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		lastPrune := time.Time{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.deliverPending(ctx)
				if time.Since(lastPrune) > time.Hour {
					if maintenance, ok := s.repo.(ImageResolutionGuardMaintenanceRepository); ok {
						if _, err := maintenance.Prune(ctx, time.Now().Add(-7*24*time.Hour)); err != nil {
							slog.Warn("image resolution event cleanup failed")
						}
					}
					lastPrune = time.Now()
				}
			}
		}
	}()
	if s.rdb != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			sub := s.rdb.Subscribe(ctx, imageResolutionSettingsChannel)
			defer sub.Close()
			ch := sub.Channel()
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-ch:
					if !ok {
						return
					}
					s.mu.Lock()
					s.cacheUntil = time.Time{}
					s.mu.Unlock()
				}
			}
		}()
	}
}
func (s *ImageResolutionGuardService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
}

func (s *ImageResolutionGuardService) deliverPending(ctx context.Context) {
	if s.repo == nil {
		return
	}
	cfg, err := s.runtimeSettings(ctx)
	if err != nil {
		return
	}
	claim := uuid.NewString()
	events, err := s.repo.ClaimMail(ctx, claim, 1, 2*time.Minute)
	if err != nil {
		slog.Warn("image resolution mail claim failed")
		return
	}
	for _, event := range events {
		s.deliverEvent(ctx, cfg, event)
	}
}

func (s *ImageResolutionGuardService) deliverEvent(ctx context.Context, cfg ImageResolutionGuardSettings, event ImageResolutionMailEvent) {
	if !cfg.Enabled || !cfg.EmailEnabled {
		_ = s.repo.FinishMail(ctx, event.ID, event.ClaimToken, "skipped", event.Delivered, time.Now(), "disabled")
		return
	}
	delivered := append([]string{}, event.Delivered...)
	failed := false
	if len(event.Recipients) == 0 || s.send == nil {
		failed = true
	}
	for _, recipient := range event.Recipients {
		already := false
		for _, done := range delivered {
			if strings.EqualFold(done, recipient) {
				already = true
				break
			}
		}
		if already {
			continue
		}
		current, err := s.runtimeSettings(ctx)
		if err != nil {
			return
		}
		if !current.Enabled || !current.EmailEnabled {
			_ = s.repo.FinishMail(ctx, event.ID, event.ClaimToken, "skipped", delivered, time.Now(), "disabled")
			return
		}
		if s.send == nil {
			continue
		}
		if err := s.repo.SaveMailProgress(ctx, event.ID, event.ClaimToken, delivered); err != nil {
			return
		}
		sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = s.send(sendCtx, NotificationEmailSendInput{Event: NotificationEmailEventImageResolutionMismatch, RecipientEmail: recipient, RecipientName: emailRecipientName(recipient), SourceType: "image_resolution_guard", SourceID: event.CycleID, Variables: imageResolutionMailVariables(event)})
		cancel()
		if err != nil {
			failed = true
			continue
		}
		delivered = append(delivered, recipient)
		if err = s.repo.SaveMailProgress(ctx, event.ID, event.ClaimToken, delivered); err != nil {
			return
		}
	}
	status := "sent"
	next := time.Now()
	reason := ""
	if failed {
		status = "pending"
		reason = "mail_delivery_failed"
		if event.Attempts >= 3 {
			status = "failed"
		} else if event.Attempts == 1 {
			next = next.Add(time.Minute)
		} else {
			next = next.Add(5 * time.Minute)
		}
	}
	if err := s.repo.FinishMail(ctx, event.ID, event.ClaimToken, status, delivered, next, reason); err != nil {
		slog.Warn("image resolution mail result persistence failed")
	}
}

func imageResolutionMailVariables(event ImageResolutionMailEvent) map[string]string {
	v := event.Observation
	dimensions := []string{}
	ratios := []string{}
	expected := v.Expectation.ExpectedPixels
	if expected <= 0 {
		expected = int64(v.Expectation.ExpectedWidth) * int64(v.Expectation.ExpectedHeight)
	}
	for _, out := range v.Outputs {
		if out.Width > 0 && out.Height > 0 {
			dimensions = append(dimensions, fmt.Sprintf("%dx%d", out.Width, out.Height))
			if expected > 0 {
				ratios = append(ratios, fmt.Sprintf("%.2f%%", 100*float64(out.Width)*float64(out.Height)/float64(expected)))
			}
		}
	}
	return map[string]string{"account_id": fmt.Sprint(v.AccountID), "account_name": v.AccountName, "platform": v.Platform, "requested_model": v.Expectation.RequestedModel, "upstream_model": v.Expectation.UpstreamModel, "requested_tier": v.Expectation.SizeTier, "expected_dimensions": fmt.Sprintf("%dx%d (%d pixels)", v.Expectation.ExpectedWidth, v.Expectation.ExpectedHeight, expected), "actual_dimensions": strings.Join(dimensions, ", "), "area_percent": strings.Join(ratios, ", "), "min_area_percent": fmt.Sprint(event.MinAreaPercent), "task_id": v.TaskID, "request_id": v.RequestID, "cooldown_until": event.CooldownUntil.UTC().Format(time.RFC3339)}
}
