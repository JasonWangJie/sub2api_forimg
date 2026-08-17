package service

import (
	"context"
	"fmt"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func (s *adminServiceImpl) ListGroupImageSizeAccounts(ctx context.Context, groupID int64) (GroupImageSizeAccountBindingsView, error) {
	if _, err := s.requireImageSizePoolGroup(ctx, groupID); err != nil {
		return nil, err
	}
	store := asImageSizeAccountAdminStore(s.accountRepo)
	if store == nil {
		return nil, fmt.Errorf("image size account repository is not configured")
	}
	rows, err := store.ListImageSizeAccounts(ctx, groupID)
	if err != nil {
		return nil, err
	}
	return groupImageSizeAccountsToView(rows), nil
}

func (s *adminServiceImpl) ReplaceGroupImageSizeAccounts(ctx context.Context, groupID int64, bindings GroupImageSizeAccountBindings) (GroupImageSizeAccountBindingsView, error) {
	group, err := s.requireImageSizePoolGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	store := asImageSizeAccountAdminStore(s.accountRepo)
	if store == nil {
		return nil, fmt.Errorf("image size account repository is not configured")
	}
	if bindings == nil {
		bindings = GroupImageSizeAccountBindings{}
	}
	accountIDs := make([]int64, 0)
	seenAccountIDs := make(map[int64]struct{})
	for tier, entries := range bindings {
		if !IsValidImageSizeTier(tier) {
			return nil, infraerrors.BadRequest("INVALID_IMAGE_SIZE_ACCOUNT_POOL", fmt.Sprintf("invalid image size tier %q", tier))
		}
		for _, entry := range entries {
			if entry.AccountID <= 0 {
				return nil, infraerrors.BadRequest("INVALID_IMAGE_SIZE_ACCOUNT_POOL", fmt.Sprintf("invalid account_id for tier %s", tier))
			}
			if _, seen := seenAccountIDs[entry.AccountID]; !seen {
				seenAccountIDs[entry.AccountID] = struct{}{}
				accountIDs = append(accountIDs, entry.AccountID)
			}
		}
	}
	accounts, err := s.accountRepo.GetByIDs(ctx, accountIDs)
	if err != nil {
		return nil, err
	}
	accountsByID := make(map[int64]*Account, len(accounts))
	for _, account := range accounts {
		if account != nil {
			accountsByID[account.ID] = account
		}
	}
	if len(accountsByID) != len(seenAccountIDs) {
		return nil, ErrAccountNotFound
	}
	for _, accountID := range accountIDs {
		account := accountsByID[accountID]
		if !accountPlatformCompatibleWithImageSizeGroup(group.Platform, account.Platform) {
			return nil, infraerrors.BadRequest("INVALID_IMAGE_SIZE_ACCOUNT_POOL", fmt.Sprintf(
				"account %d platform %s is not compatible with group platform %s",
				account.ID, account.Platform, group.Platform,
			))
		}
	}
	if err := store.ReplaceImageSizeAccounts(ctx, groupID, bindings); err != nil {
		return nil, err
	}
	rows, err := store.ListImageSizeAccounts(ctx, groupID)
	if err != nil {
		return nil, err
	}
	return groupImageSizeAccountsToView(rows), nil
}

func (s *adminServiceImpl) requireImageSizePoolGroup(ctx context.Context, groupID int64) (*Group, error) {
	group, err := s.groupRepo.GetByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return nil, ErrGroupNotFound
	}
	switch group.Platform {
	case PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformComposite:
		return group, nil
	default:
		return nil, infraerrors.BadRequest("INVALID_IMAGE_SIZE_ACCOUNT_POOL", "image size account pools are only supported for openai/gemini/antigravity/composite groups")
	}
}

func accountPlatformCompatibleWithImageSizeGroup(groupPlatform, accountPlatform string) bool {
	switch groupPlatform {
	case PlatformOpenAI:
		return accountPlatform == PlatformOpenAI
	case PlatformGemini:
		return accountPlatform == PlatformGemini || accountPlatform == PlatformAntigravity
	case PlatformAntigravity:
		return accountPlatform == PlatformAntigravity || accountPlatform == PlatformGemini
	case PlatformComposite:
		return accountPlatform == PlatformOpenAI || accountPlatform == PlatformGemini || accountPlatform == PlatformAntigravity
	default:
		return false
	}
}

func groupImageSizeAccountsToView(rows []GroupImageSizeAccount) GroupImageSizeAccountBindingsView {
	view := GroupImageSizeAccountBindingsView{
		ImageBillingSize1K: {},
		ImageBillingSize2K: {},
		ImageBillingSize4K: {},
	}
	for _, row := range rows {
		tier := row.SizeTier
		if !IsValidImageSizeTier(tier) {
			continue
		}
		view[tier] = append(view[tier], row)
	}
	return view
}
