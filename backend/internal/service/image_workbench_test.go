package service

import (
	"context"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type imageWorkbenchAPIKeyReaderStub struct {
	key *APIKey
	err error
}

func (s imageWorkbenchAPIKeyReaderStub) GetByID(context.Context, int64) (*APIKey, error) {
	return s.key, s.err
}

func (s imageWorkbenchAPIKeyReaderStub) AttachImagePlatformGroups(_ context.Context, apiKey *APIKey) error {
	if apiKey != nil && apiKey.ImagePlatformGroups == nil {
		apiKey.ImagePlatformGroups = map[string]int64{}
	}
	return nil
}

func (s imageWorkbenchAPIKeyReaderStub) GetGroupByID(_ context.Context, groupID int64) (*Group, error) {
	if s.key != nil && s.key.Group != nil && s.key.Group.ID == groupID {
		return s.key.Group, nil
	}
	return nil, ErrGroupNotFound
}

type imageWorkbenchModelCatalogStub struct {
	models []string
}

func (s imageWorkbenchModelCatalogStub) GetAvailableModels(context.Context, *int64, string) []string {
	return append([]string(nil), s.models...)
}

func imageWorkbenchTestKey(platform string) *APIKey {
	groupID := int64(20)
	return &APIKey{
		ID: 10, UserID: 7, GroupID: &groupID, Status: StatusActive,
		Group: &Group{
			ID: 20, Platform: platform, Status: StatusActive,
			AllowImageGeneration: true,
		},
	}
}

func TestImageWorkbenchCapabilitiesOpenAIAsyncUsesGroupModelList(t *testing.T) {
	key := imageWorkbenchTestKey(PlatformOpenAI)
	key.Group.AllowAsyncImageGeneration = true
	key.Group.ModelsListConfig = GroupModelsListConfig{
		Enabled: true,
		Models:  []string{"gpt-5.6", "gpt-image-2", "gpt-image-2"},
	}
	svc := NewImageWorkbenchService(
		imageWorkbenchAPIKeyReaderStub{key: key},
		imageWorkbenchModelCatalogStub{models: []string{"gpt-image-*", "gpt-5.6"}},
	)

	got, err := svc.GetCapabilities(context.Background(), 7, 10)
	require.NoError(t, err)
	require.True(t, got.Available)
	require.Equal(t, ImageWorkbenchModeAsync, got.ExecutionMode)
	require.Equal(t, ImageWorkbenchProtocolOpenAIAsync, got.Protocol)
	require.Equal(t, "/v1/images/generations_oa", got.Endpoints.Generation)
	require.Equal(t, "/v1/images/generations_oa", got.Endpoints.Edit)
	require.Equal(t, []ImageWorkbenchModel{{ID: "gpt-image-2", Label: "GPT Image 2"}}, got.Models)
	require.Equal(t, 4, got.MaxOutputImages)
	require.Equal(t, 5, got.MaxReferenceImages)
	require.NotEmpty(t, got.CapabilityVersion)
}

func TestImageWorkbenchCapabilitiesGeminiRealtimeSupportsExplicitAliasAndWildcard(t *testing.T) {
	key := imageWorkbenchTestKey(PlatformGemini)
	key.Group.ModelsListConfig = GroupModelsListConfig{
		Enabled: true,
		Models:  []string{"gemini-3-pro-image", "gemini-2.5-pro", "gemini-3.1*"},
	}
	svc := NewImageWorkbenchService(
		imageWorkbenchAPIKeyReaderStub{key: key},
		imageWorkbenchModelCatalogStub{models: []string{"gemini-3.1-flash-image", "gemini-2.5-pro"}},
	)

	got, err := svc.GetCapabilities(context.Background(), 7, 10)
	require.NoError(t, err)
	require.True(t, got.Available)
	require.Equal(t, ImageWorkbenchModeRealtime, got.ExecutionMode)
	require.Equal(t, ImageWorkbenchProtocolGeminiNative, got.Protocol)
	require.Equal(t, []string{"gemini-3-pro-image", "gemini-3.1-flash-image"}, []string{got.Models[0].ID, got.Models[1].ID})
	require.Equal(t, "/v1beta/models/{model}:generateContent", got.Endpoints.Generation)
	require.Empty(t, got.Formats)
	require.Equal(t, 1, got.MaxOutputImages)
	require.Equal(t, 5, got.MaxReferenceImages)
}

func TestImageWorkbenchCapabilitiesExpandAccountModelWildcard(t *testing.T) {
	key := imageWorkbenchTestKey(PlatformGemini)
	svc := NewImageWorkbenchService(
		imageWorkbenchAPIKeyReaderStub{key: key},
		imageWorkbenchModelCatalogStub{models: []string{"gemini-3.1*", "gemini-2.5-pro"}},
	)

	got, err := svc.GetCapabilities(context.Background(), 7, 10)
	require.NoError(t, err)
	require.True(t, got.Available)
	require.Equal(t, []ImageWorkbenchModel{{ID: "gemini-3.1-flash-image", Label: "Gemini 3.1 Flash Image"}}, got.Models)
}

func TestImageWorkbenchCapabilitiesGrokIsAlwaysRealtimeAndDoesNotAdvertiseSize(t *testing.T) {
	key := imageWorkbenchTestKey(PlatformGrok)
	key.Group.AllowAsyncImageGeneration = true
	svc := NewImageWorkbenchService(imageWorkbenchAPIKeyReaderStub{key: key}, nil)

	got, err := svc.GetCapabilities(context.Background(), 7, 10)
	require.NoError(t, err)
	require.True(t, got.Available)
	require.Equal(t, ImageWorkbenchModeRealtime, got.ExecutionMode)
	require.Equal(t, ImageWorkbenchProtocolGrokImages, got.Protocol)
	require.Empty(t, got.ImageSizes)
	require.Equal(t, 1, got.MaxOutputImages)
	require.Equal(t, 1, got.MaxReferenceImages)
}

func TestImageWorkbenchCapabilitiesExcludeAntigravityAndDisabledImages(t *testing.T) {
	for _, tt := range []struct {
		name   string
		key    *APIKey
		reason string
	}{
		{name: "antigravity", key: imageWorkbenchTestKey(PlatformAntigravity), reason: "platform_not_supported"},
		{name: "disabled", key: imageWorkbenchTestKey(PlatformGemini), reason: "image_generation_disabled"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "disabled" {
				tt.key.Group.AllowImageGeneration = false
			}
			svc := NewImageWorkbenchService(imageWorkbenchAPIKeyReaderStub{key: tt.key}, nil)
			got, err := svc.GetCapabilities(context.Background(), 7, 10)
			require.NoError(t, err)
			require.False(t, got.Available)
			require.Equal(t, tt.reason, got.UnavailableReason)
		})
	}
}

func TestImageWorkbenchCapabilitiesHideForeignKeyAsNotFound(t *testing.T) {
	key := imageWorkbenchTestKey(PlatformGemini)
	svc := NewImageWorkbenchService(imageWorkbenchAPIKeyReaderStub{key: key}, nil)

	_, err := svc.GetCapabilities(context.Background(), 99, key.ID)
	require.Error(t, err)
	require.Equal(t, 404, infraerrors.Code(err))
}

func TestImageWorkbenchCapabilityVersionChangesWithExecutionMode(t *testing.T) {
	key := imageWorkbenchTestKey(PlatformOpenAI)
	svc := NewImageWorkbenchService(imageWorkbenchAPIKeyReaderStub{key: key}, nil)
	realtime, err := svc.GetCapabilities(context.Background(), 7, 10)
	require.NoError(t, err)

	key.Group.AllowAsyncImageGeneration = true
	async, err := svc.GetCapabilities(context.Background(), 7, 10)
	require.NoError(t, err)
	require.NotEqual(t, realtime.CapabilityVersion, async.CapabilityVersion)
}
