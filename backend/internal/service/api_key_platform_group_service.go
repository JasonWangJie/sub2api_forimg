package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// AttachImagePlatformGroups loads platform mappings onto a single API key.
func (s *APIKeyService) AttachImagePlatformGroups(ctx context.Context, apiKey *APIKey) error {
	if apiKey == nil {
		return nil
	}
	if s == nil || s.platformGroupRepo == nil {
		if apiKey.ImagePlatformGroups == nil {
			apiKey.ImagePlatformGroups = map[string]int64{}
		}
		return nil
	}
	items, err := s.platformGroupRepo.ListByAPIKeyID(ctx, apiKey.ID)
	if err != nil {
		return fmt.Errorf("list api key platform groups: %w", err)
	}
	apiKey.ImagePlatformGroups = ImagePlatformGroupsMap(items)
	return nil
}

// AttachImagePlatformGroupsBatch loads platform mappings for many API keys.
func (s *APIKeyService) AttachImagePlatformGroupsBatch(ctx context.Context, keys []APIKey) error {
	if len(keys) == 0 {
		return nil
	}
	if s == nil || s.platformGroupRepo == nil {
		for i := range keys {
			if keys[i].ImagePlatformGroups == nil {
				keys[i].ImagePlatformGroups = map[string]int64{}
			}
		}
		return nil
	}
	ids := make([]int64, 0, len(keys))
	for i := range keys {
		ids = append(ids, keys[i].ID)
	}
	byKey, err := s.platformGroupRepo.ListByAPIKeyIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("list api key platform groups: %w", err)
	}
	for i := range keys {
		keys[i].ImagePlatformGroups = ImagePlatformGroupsMap(byKey[keys[i].ID])
	}
	return nil
}

// GetGroupByID loads a billing group by ID.
func (s *APIKeyService) GetGroupByID(ctx context.Context, groupID int64) (*Group, error) {
	if s == nil || s.groupRepo == nil {
		return nil, ErrGroupNotFound
	}
	return s.groupRepo.GetByID(ctx, groupID)
}

// ResolveAsyncImageBillingGroup resolves and validates the billing group for an async image platform.
func (s *APIKeyService) ResolveAsyncImageBillingGroup(ctx context.Context, apiKey *APIKey, platform string) (*Group, error) {
	if apiKey == nil {
		return nil, infraerrors.New(http.StatusForbidden, "group_required", "an assigned image group is required")
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	if err := s.AttachImagePlatformGroups(ctx, apiKey); err != nil {
		return nil, err
	}
	groupID, ok := ResolveAsyncImageBillingGroupID(apiKey, platform)
	if !ok {
		return nil, infraerrors.New(http.StatusForbidden, "group_required", "an assigned image group is required")
	}
	if apiKey.Group != nil && apiKey.Group.ID == groupID {
		if err := ValidateAsyncImageBillingGroup(apiKey.Group, platform); err != nil {
			return nil, err
		}
		return apiKey.Group, nil
	}
	group, err := s.GetGroupByID(ctx, groupID)
	if err != nil || group == nil {
		return nil, infraerrors.New(http.StatusForbidden, "group_required", "an assigned image group is required")
	}
	if err := ValidateAsyncImageBillingGroup(group, platform); err != nil {
		return nil, err
	}
	return group, nil
}

// ReplaceImagePlatformGroups validates and replaces platform mappings for an API key.
// asAdmin skips user bind permission checks but still validates platform match and active status.
func (s *APIKeyService) ReplaceImagePlatformGroups(ctx context.Context, apiKey *APIKey, user *User, mappings map[string]int64, asAdmin bool) error {
	if s == nil || s.platformGroupRepo == nil {
		return infraerrors.New(http.StatusServiceUnavailable, "platform_groups_unavailable", "image platform group mapping is unavailable")
	}
	if apiKey == nil {
		return ErrAPIKeyNotFound
	}
	normalized, err := NormalizeImagePlatformGroups(mappings)
	if err != nil {
		return err
	}
	for platform, groupID := range normalized {
		group, err := s.groupRepo.GetByID(ctx, groupID)
		if err != nil || group == nil {
			return infraerrors.BadRequest("GROUP_NOT_FOUND", fmt.Sprintf("image platform group not found for %s", platform))
		}
		if group.Status != StatusActive {
			return infraerrors.BadRequest("GROUP_NOT_ACTIVE", "target group is not active")
		}
		if !strings.EqualFold(strings.TrimSpace(group.Platform), platform) {
			return infraerrors.BadRequest("GROUP_PLATFORM_MISMATCH", fmt.Sprintf("group platform must be %s", platform))
		}
		if asAdmin {
			if group.IsSubscriptionType() && s.userSubRepo != nil {
				if _, err := s.userSubRepo.GetActiveByUserIDAndGroupID(ctx, apiKey.UserID, groupID); err != nil {
					if errors.Is(err, ErrSubscriptionNotFound) {
						return infraerrors.BadRequest("SUBSCRIPTION_REQUIRED", "user does not have an active subscription for this group")
					}
					return err
				}
			}
			if group.IsExclusive && !group.IsSubscriptionType() && s.userRepo != nil {
				if err := s.userRepo.AddGroupToAllowedGroups(ctx, apiKey.UserID, groupID); err != nil {
					return fmt.Errorf("add group to user allowed groups: %w", err)
				}
			}
			continue
		}
		if user == nil {
			return ErrInsufficientPerms
		}
		if !s.canUserBindGroup(ctx, user, group) {
			return ErrGroupNotAllowed
		}
	}
	if err := s.platformGroupRepo.ReplaceForAPIKey(ctx, apiKey.ID, normalized); err != nil {
		return fmt.Errorf("replace api key platform groups: %w", err)
	}
	apiKey.ImagePlatformGroups = normalized
	s.InvalidateAuthCacheByKey(ctx, apiKey.Key)
	return nil
}

// SetPlatformGroupRepository injects the optional platform mapping repository.
func (s *APIKeyService) SetPlatformGroupRepository(repo APIKeyPlatformGroupRepository) {
	if s == nil {
		return
	}
	s.platformGroupRepo = repo
}
