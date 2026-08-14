package service

import (
	"context"
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Supported platforms for API key image billing group mappings.
const (
	APIKeyImagePlatformGemini = PlatformGemini
	APIKeyImagePlatformOpenAI = PlatformOpenAI
)

// APIKeyPlatformGroup is one (api_key, platform) → billing group mapping.
type APIKeyPlatformGroup struct {
	APIKeyID int64
	Platform string
	GroupID  int64
}

// APIKeyPlatformGroupRepository persists per-key platform billing group mappings.
type APIKeyPlatformGroupRepository interface {
	ListByAPIKeyID(ctx context.Context, apiKeyID int64) ([]APIKeyPlatformGroup, error)
	ListByAPIKeyIDs(ctx context.Context, apiKeyIDs []int64) (map[int64][]APIKeyPlatformGroup, error)
	ReplaceForAPIKey(ctx context.Context, apiKeyID int64, mappings map[string]int64) error
	ClearByGroupID(ctx context.Context, groupID int64) (int64, error)
}

// IsAPIKeyImagePlatform reports whether platform may appear in image_platform_groups.
func IsAPIKeyImagePlatform(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformGemini, PlatformOpenAI:
		return true
	default:
		return false
	}
}

// NormalizeImagePlatformGroups keeps only gemini/openai with positive group IDs.
func NormalizeImagePlatformGroups(in map[string]int64) (map[string]int64, error) {
	if in == nil {
		return map[string]int64{}, nil
	}
	out := make(map[string]int64, len(in))
	for platform, groupID := range in {
		platform = strings.ToLower(strings.TrimSpace(platform))
		if platform == "" {
			continue
		}
		if !IsAPIKeyImagePlatform(platform) {
			return nil, infraerrors.BadRequest("INVALID_IMAGE_PLATFORM", "image_platform_groups only supports gemini and openai")
		}
		if groupID <= 0 {
			continue
		}
		out[platform] = groupID
	}
	return out, nil
}

// ImagePlatformGroupsMap converts mapping rows to platform → group_id.
func ImagePlatformGroupsMap(items []APIKeyPlatformGroup) map[string]int64 {
	if len(items) == 0 {
		return map[string]int64{}
	}
	out := make(map[string]int64, len(items))
	for _, item := range items {
		platform := strings.ToLower(strings.TrimSpace(item.Platform))
		if platform == "" || item.GroupID <= 0 {
			continue
		}
		out[platform] = item.GroupID
	}
	return out
}

// ResolveAsyncImageBillingGroupID picks the billing group ID for an async image platform.
// Mapping table wins; otherwise the key's primary group is used when platforms match.
func ResolveAsyncImageBillingGroupID(apiKey *APIKey, platform string) (int64, bool) {
	if apiKey == nil {
		return 0, false
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" {
		return 0, false
	}
	if apiKey.ImagePlatformGroups != nil {
		if gid, ok := apiKey.ImagePlatformGroups[platform]; ok && gid > 0 {
			return gid, true
		}
	}
	if apiKey.GroupID != nil && *apiKey.GroupID > 0 && apiKey.Group != nil &&
		*apiKey.GroupID == apiKey.Group.ID &&
		strings.EqualFold(strings.TrimSpace(apiKey.Group.Platform), platform) {
		return apiKey.Group.ID, true
	}
	return 0, false
}

// ValidateAsyncImageBillingGroup checks a resolved billing group for async image use.
func ValidateAsyncImageBillingGroup(group *Group, expectedPlatform string) error {
	if group == nil {
		return infraerrors.New(http.StatusForbidden, "group_required", "an assigned image group is required")
	}
	expectedPlatform = strings.ToLower(strings.TrimSpace(expectedPlatform))
	if !strings.EqualFold(strings.TrimSpace(group.Platform), expectedPlatform) {
		return infraerrors.New(http.StatusForbidden, "group_platform_mismatch", "the API key group does not support this asynchronous image endpoint")
	}
	if !GroupAllowsImageGeneration(group) {
		return infraerrors.New(http.StatusForbidden, "image_generation_disabled", ImageGenerationPermissionMessage())
	}
	if !group.AllowAsyncImageGeneration {
		return infraerrors.New(http.StatusForbidden, "async_image_generation_disabled", "asynchronous image generation is not enabled for this group")
	}
	if group.Status != StatusActive {
		return infraerrors.New(http.StatusForbidden, "group_inactive", "the assigned image group is not active")
	}
	return nil
}
