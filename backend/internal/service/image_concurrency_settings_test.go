package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type imageConcurrencySettingsRepoStub struct {
	values    map[string]string
	setErr    error
	deleteErr error
}

func (r *imageConcurrencySettingsRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, ErrSettingNotFound
}

func (r *imageConcurrencySettingsRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func (r *imageConcurrencySettingsRepoStub) Set(_ context.Context, key, value string) error {
	if r.setErr != nil {
		return r.setErr
	}
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

func (r *imageConcurrencySettingsRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (r *imageConcurrencySettingsRepoStub) SetMultiple(context.Context, map[string]string) error {
	return nil
}

func (r *imageConcurrencySettingsRepoStub) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}

func (r *imageConcurrencySettingsRepoStub) Delete(_ context.Context, key string) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	delete(r.values, key)
	return nil
}

type imageConcurrencyInvalidationStub struct {
	published chan string
}

func (s *imageConcurrencyInvalidationStub) PublishImageConcurrencySettings(ctx context.Context, value string) error {
	select {
	case s.published <- value:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *imageConcurrencyInvalidationStub) SubscribeImageConcurrencySettings(context.Context, func(string)) error {
	return errors.New("not used")
}

func testImageConcurrencyConfig() *config.Config {
	return &config.Config{Gateway: config.GatewayConfig{ImageConcurrency: config.ImageConcurrencyConfig{
		Enabled:               true,
		MaxConcurrentRequests: 50,
		OverflowMode:          config.ImageConcurrencyOverflowModeWait,
		WaitTimeoutSeconds:    360,
		MaxWaitingRequests:    120,
	}}}
}

func TestGetImageConcurrencySettingsFallsBackToYAML(t *testing.T) {
	cfg := testImageConcurrencyConfig()
	svc := NewSettingService(&imageConcurrencySettingsRepoStub{}, cfg)

	view, err := svc.GetImageConcurrencySettings(context.Background())

	require.NoError(t, err)
	require.False(t, view.Configured)
	require.Equal(t, ImageConcurrencySettingsSourceYAML, view.Source)
	require.Equal(t, 50, view.Effective.MaxConcurrentRequests)
	require.Equal(t, cfg.Gateway.ImageConcurrency, cfg.ImageConcurrencySettings())
}

func TestSetImageConcurrencySettingsPersistsAndAppliesImmediately(t *testing.T) {
	repo := &imageConcurrencySettingsRepoStub{}
	cfg := testImageConcurrencyConfig()
	svc := NewSettingService(repo, cfg)
	invalidation := &imageConcurrencyInvalidationStub{published: make(chan string, 1)}
	svc.SetImageConcurrencySettingInvalidation(invalidation)

	view, err := svc.SetImageConcurrencySettings(context.Background(), &ImageConcurrencySettings{
		Enabled:               true,
		MaxConcurrentRequests: 200,
		OverflowMode:          " WAIT ",
		WaitTimeoutSeconds:    30,
		MaxWaitingRequests:    500,
	})

	require.NoError(t, err)
	require.True(t, view.Configured)
	require.Equal(t, ImageConcurrencySettingsSourceSystem, view.Source)
	require.Equal(t, 200, cfg.ImageConcurrencySettings().MaxConcurrentRequests)
	require.Equal(t, config.ImageConcurrencyOverflowModeWait, cfg.ImageConcurrencySettings().OverflowMode)
	require.JSONEq(t, `{"enabled":true,"max_concurrent_requests":200,"overflow_mode":"wait","wait_timeout_seconds":30,"max_waiting_requests":500}`, repo.values[SettingKeyImageConcurrencySettings])

	var payload imageConcurrencyInvalidationPayload
	require.NoError(t, json.Unmarshal([]byte(<-invalidation.published), &payload))
	require.True(t, payload.Configured)
	require.Equal(t, 200, payload.Settings.MaxConcurrentRequests)
}

func TestResetImageConcurrencySettingsRestoresYAML(t *testing.T) {
	repo := &imageConcurrencySettingsRepoStub{values: map[string]string{
		SettingKeyImageConcurrencySettings: `{"enabled":true,"max_concurrent_requests":200,"overflow_mode":"reject","wait_timeout_seconds":0,"max_waiting_requests":0}`,
	}}
	cfg := testImageConcurrencyConfig()
	svc := NewSettingService(repo, cfg)
	require.NoError(t, svc.LoadImageConcurrencySettings(context.Background()))
	require.Equal(t, 200, cfg.ImageConcurrencySettings().MaxConcurrentRequests)

	view, err := svc.ResetImageConcurrencySettings(context.Background())

	require.NoError(t, err)
	require.False(t, view.Configured)
	require.Equal(t, ImageConcurrencySettingsSourceYAML, view.Source)
	require.Equal(t, 50, cfg.ImageConcurrencySettings().MaxConcurrentRequests)
	require.NotContains(t, repo.values, SettingKeyImageConcurrencySettings)
}

func TestSetImageConcurrencySettingsRejectsInvalidValuesBeforeWrite(t *testing.T) {
	repo := &imageConcurrencySettingsRepoStub{}
	svc := NewSettingService(repo, testImageConcurrencyConfig())

	_, err := svc.SetImageConcurrencySettings(context.Background(), &ImageConcurrencySettings{
		MaxConcurrentRequests: -1,
		OverflowMode:          config.ImageConcurrencyOverflowModeReject,
	})

	require.ErrorContains(t, err, "max_concurrent_requests")
	require.Empty(t, repo.values)
}

func TestApplyImageConcurrencySettingsUpdateSupportsSetAndReset(t *testing.T) {
	cfg := testImageConcurrencyConfig()
	svc := NewSettingService(&imageConcurrencySettingsRepoStub{}, cfg)

	require.True(t, svc.applyImageConcurrencySettingsUpdate(`{"configured":true,"settings":{"enabled":true,"max_concurrent_requests":12,"overflow_mode":"reject","wait_timeout_seconds":0,"max_waiting_requests":0}}`))
	require.Equal(t, 12, cfg.ImageConcurrencySettings().MaxConcurrentRequests)
	require.True(t, svc.applyImageConcurrencySettingsUpdate(`{"configured":false}`))
	require.Equal(t, 50, cfg.ImageConcurrencySettings().MaxConcurrentRequests)
	require.False(t, svc.applyImageConcurrencySettingsUpdate(`{"configured":true}`))
}
