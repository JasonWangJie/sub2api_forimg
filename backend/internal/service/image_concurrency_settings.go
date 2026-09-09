package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const (
	ImageConcurrencySettingsSourceSystem = "system_settings"
	ImageConcurrencySettingsSourceYAML   = "config_yaml"
	imageConcurrencyPublishTTL           = 2 * time.Second
)

// ImageConcurrencySettings is persisted as one JSON value so readers never
// observe a partially updated concurrency policy.
type ImageConcurrencySettings struct {
	Enabled               bool   `json:"enabled"`
	MaxConcurrentRequests int    `json:"max_concurrent_requests"`
	OverflowMode          string `json:"overflow_mode"`
	WaitTimeoutSeconds    int    `json:"wait_timeout_seconds"`
	MaxWaitingRequests    int    `json:"max_waiting_requests"`
}

type ImageConcurrencySettingsView struct {
	Configured bool                     `json:"configured"`
	Source     string                   `json:"source"`
	Effective  ImageConcurrencySettings `json:"effective"`
	Fallback   ImageConcurrencySettings `json:"fallback"`
}

type imageConcurrencyInvalidationPayload struct {
	Configured bool                      `json:"configured"`
	Settings   *ImageConcurrencySettings `json:"settings,omitempty"`
}

func imageConcurrencySettingsFromConfig(settings config.ImageConcurrencyConfig) ImageConcurrencySettings {
	mode := strings.ToLower(strings.TrimSpace(settings.OverflowMode))
	if mode == "" {
		mode = config.ImageConcurrencyOverflowModeReject
	}
	return ImageConcurrencySettings{
		Enabled:               settings.Enabled,
		MaxConcurrentRequests: settings.MaxConcurrentRequests,
		OverflowMode:          mode,
		WaitTimeoutSeconds:    settings.WaitTimeoutSeconds,
		MaxWaitingRequests:    settings.MaxWaitingRequests,
	}
}

func imageConcurrencyConfigFromSettings(settings ImageConcurrencySettings) config.ImageConcurrencyConfig {
	return config.ImageConcurrencyConfig{
		Enabled:               settings.Enabled,
		MaxConcurrentRequests: settings.MaxConcurrentRequests,
		OverflowMode:          settings.OverflowMode,
		WaitTimeoutSeconds:    settings.WaitTimeoutSeconds,
		MaxWaitingRequests:    settings.MaxWaitingRequests,
	}
}

func normalizeImageConcurrencySettings(settings *ImageConcurrencySettings) error {
	if settings == nil {
		return errors.New("settings cannot be nil")
	}
	settings.OverflowMode = strings.ToLower(strings.TrimSpace(settings.OverflowMode))
	if settings.MaxConcurrentRequests < 0 {
		return errors.New("max_concurrent_requests must be greater than or equal to 0")
	}
	if settings.WaitTimeoutSeconds < 0 {
		return errors.New("wait_timeout_seconds must be greater than or equal to 0")
	}
	if settings.MaxWaitingRequests < 0 {
		return errors.New("max_waiting_requests must be greater than or equal to 0")
	}
	if settings.OverflowMode != config.ImageConcurrencyOverflowModeReject && settings.OverflowMode != config.ImageConcurrencyOverflowModeWait {
		return errors.New("overflow_mode must be reject or wait")
	}
	return nil
}

func (s *SettingService) imageConcurrencyFallback() ImageConcurrencySettings {
	if s == nil || s.cfg == nil {
		return imageConcurrencySettingsFromConfig(config.ImageConcurrencyConfig{})
	}
	return imageConcurrencySettingsFromConfig(s.cfg.Gateway.ImageConcurrency)
}

func (s *SettingService) applyImageConcurrencySettings(settings ImageConcurrencySettings) {
	if s == nil || s.cfg == nil {
		return
	}
	s.cfg.SetImageConcurrencySettings(imageConcurrencyConfigFromSettings(settings))
}

// GetImageConcurrencySettings returns both the effective settings and the
// untouched config.yaml fallback shown by the admin UI.
func (s *SettingService) GetImageConcurrencySettings(ctx context.Context) (*ImageConcurrencySettingsView, error) {
	fallback := s.imageConcurrencyFallback()
	view := &ImageConcurrencySettingsView{
		Source:    ImageConcurrencySettingsSourceYAML,
		Effective: fallback,
		Fallback:  fallback,
	}
	if s == nil || s.settingRepo == nil {
		s.applyImageConcurrencySettings(fallback)
		return view, nil
	}

	raw, err := s.settingRepo.GetValue(ctx, SettingKeyImageConcurrencySettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			s.applyImageConcurrencySettings(fallback)
			return view, nil
		}
		return nil, fmt.Errorf("get image concurrency settings: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		s.applyImageConcurrencySettings(fallback)
		return view, nil
	}

	var settings ImageConcurrencySettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		s.applyImageConcurrencySettings(fallback)
		return nil, fmt.Errorf("decode image concurrency settings: %w", err)
	}
	if err := normalizeImageConcurrencySettings(&settings); err != nil {
		s.applyImageConcurrencySettings(fallback)
		return nil, fmt.Errorf("validate image concurrency settings: %w", err)
	}

	view.Configured = true
	view.Source = ImageConcurrencySettingsSourceSystem
	view.Effective = settings
	s.applyImageConcurrencySettings(settings)
	return view, nil
}

// LoadImageConcurrencySettings applies a persisted override during startup.
func (s *SettingService) LoadImageConcurrencySettings(ctx context.Context) error {
	_, err := s.GetImageConcurrencySettings(ctx)
	return err
}

// SetImageConcurrencySettings persists and immediately applies a complete
// policy. Active requests are not interrupted; subsequent requests use it.
func (s *SettingService) SetImageConcurrencySettings(ctx context.Context, settings *ImageConcurrencySettings) (*ImageConcurrencySettingsView, error) {
	if settings == nil {
		return nil, errors.New("settings cannot be nil")
	}
	normalized := *settings
	if err := normalizeImageConcurrencySettings(&normalized); err != nil {
		return nil, err
	}
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("setting repository is not configured")
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("encode image concurrency settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyImageConcurrencySettings, string(raw)); err != nil {
		return nil, fmt.Errorf("save image concurrency settings: %w", err)
	}

	s.applyImageConcurrencySettings(normalized)
	s.publishImageConcurrencySettings(ctx, imageConcurrencyInvalidationPayload{
		Configured: true,
		Settings:   &normalized,
	})
	return &ImageConcurrencySettingsView{
		Configured: true,
		Source:     ImageConcurrencySettingsSourceSystem,
		Effective:  normalized,
		Fallback:   s.imageConcurrencyFallback(),
	}, nil
}

// ResetImageConcurrencySettings deletes the override and restores config.yaml.
func (s *SettingService) ResetImageConcurrencySettings(ctx context.Context) (*ImageConcurrencySettingsView, error) {
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("setting repository is not configured")
	}
	if err := s.settingRepo.Delete(ctx, SettingKeyImageConcurrencySettings); err != nil && !errors.Is(err, ErrSettingNotFound) {
		return nil, fmt.Errorf("delete image concurrency settings: %w", err)
	}
	fallback := s.imageConcurrencyFallback()
	s.applyImageConcurrencySettings(fallback)
	s.publishImageConcurrencySettings(ctx, imageConcurrencyInvalidationPayload{Configured: false})
	return &ImageConcurrencySettingsView{
		Source:    ImageConcurrencySettingsSourceYAML,
		Effective: fallback,
		Fallback:  fallback,
	}, nil
}

func (s *SettingService) SetImageConcurrencySettingInvalidation(invalidation ImageConcurrencySettingInvalidation) {
	if s != nil {
		s.imageConcurrencyInvalidation = invalidation
	}
}

func (s *SettingService) publishImageConcurrencySettings(ctx context.Context, payload imageConcurrencyInvalidationPayload) {
	if s == nil || s.imageConcurrencyInvalidation == nil {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("failed to encode image concurrency settings update", "error", err)
		return
	}
	publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), imageConcurrencyPublishTTL)
	defer cancel()
	if err := s.imageConcurrencyInvalidation.PublishImageConcurrencySettings(publishCtx, string(raw)); err != nil {
		slog.Warn("failed to publish image concurrency settings update", "error", err)
	}
}

func (s *SettingService) applyImageConcurrencySettingsUpdate(raw string) bool {
	var payload imageConcurrencyInvalidationPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &payload); err != nil {
		return false
	}
	if !payload.Configured {
		s.applyImageConcurrencySettings(s.imageConcurrencyFallback())
		return true
	}
	if payload.Settings == nil {
		return false
	}
	settings := *payload.Settings
	if err := normalizeImageConcurrencySettings(&settings); err != nil {
		return false
	}
	s.applyImageConcurrencySettings(settings)
	return true
}

// StartImageConcurrencySettingInvalidationSubscriber keeps this instance in
// sync after another instance changes the admin setting.
func (s *SettingService) StartImageConcurrencySettingInvalidationSubscriber(ctx context.Context) {
	if s == nil || s.imageConcurrencyInvalidation == nil {
		return
	}
	s.imageConcurrencyInvalidationStart.Do(func() {
		subscriberCtx, cancel := context.WithCancel(ctx)
		s.imageConcurrencyInvalidationCancel = cancel
		s.imageConcurrencyInvalidationWG.Add(1)
		go func() {
			defer s.imageConcurrencyInvalidationWG.Done()
			backoff := time.Second
			for {
				err := s.imageConcurrencyInvalidation.SubscribeImageConcurrencySettings(subscriberCtx, func(raw string) {
					if !s.applyImageConcurrencySettingsUpdate(raw) {
						slog.Warn("ignored invalid image concurrency settings update")
					}
				})
				if subscriberCtx.Err() != nil {
					return
				}
				if err == nil {
					err = errors.New("image concurrency setting invalidation subscription closed")
				}
				slog.Warn("image concurrency setting invalidation subscriber failed; retrying", "error", err, "retry_in", backoff)
				timer := time.NewTimer(backoff)
				select {
				case <-subscriberCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				if backoff < 30*time.Second {
					backoff *= 2
					if backoff > 30*time.Second {
						backoff = 30 * time.Second
					}
				}
			}
		}()
	})
}

func (s *SettingService) StopImageConcurrencySettingInvalidationSubscriber() {
	if s == nil {
		return
	}
	s.imageConcurrencyInvalidationStop.Do(func() {
		if s.imageConcurrencyInvalidationCancel != nil {
			s.imageConcurrencyInvalidationCancel()
		}
		s.imageConcurrencyInvalidationWG.Wait()
	})
}
