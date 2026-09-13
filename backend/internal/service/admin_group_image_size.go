package service

import (
	"context"
	"fmt"
	"strings"

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
		if group.RequireOAuthOnly && groupSupportsOAuthOnlyFilter(group.Platform) && account.Type == AccountTypeAPIKey {
			return nil, infraerrors.BadRequest("INVALID_IMAGE_SIZE_ACCOUNT_POOL", fmt.Sprintf(
				"account %d is an API key account but group %s requires OAuth accounts", account.ID, group.Name,
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

// GetGroupImageAccountPools returns all three independently persisted pool sets.
func (s *adminServiceImpl) GetGroupImageAccountPools(ctx context.Context, groupID int64) (GroupImageAccountPools, error) {
	group, err := s.requireImageSizePoolGroup(ctx, groupID)
	if err != nil {
		return GroupImageAccountPools{}, err
	}
	store := asImageAccountPoolAdminStore(s.accountRepo)
	if store == nil {
		return GroupImageAccountPools{}, fmt.Errorf("image account pool repository is not configured")
	}
	rows, err := store.ListImageSizeAccounts(ctx, groupID)
	if err != nil {
		return GroupImageAccountPools{}, err
	}
	view := groupImageAccountPoolsToView(group.ImageAccountPoolMode, rows)
	candidates, err := s.GetGroupModelsListCandidates(ctx, groupID, group.Platform)
	if err != nil {
		return GroupImageAccountPools{}, err
	}
	view.ModelCandidates = imageAccountPoolModelCandidates(group.Platform, candidates, rows)
	return view, nil
}

// ReplaceGroupImageAccountPools validates account/platform membership and replaces
// the mode plus all dimensional bindings in one repository transaction.
func (s *adminServiceImpl) ReplaceGroupImageAccountPools(ctx context.Context, groupID int64, pools GroupImageAccountPools) (GroupImageAccountPools, error) {
	group, err := s.requireImageSizePoolGroup(ctx, groupID)
	if err != nil {
		return GroupImageAccountPools{}, err
	}
	if !ValidImageAccountPoolMode(pools.Mode) {
		return GroupImageAccountPools{}, infraerrors.BadRequest("INVALID_IMAGE_ACCOUNT_POOL", fmt.Sprintf("invalid image account pool mode %q", pools.Mode))
	}
	accountIDs, err := validateGroupImageAccountPools(pools)
	if err != nil {
		return GroupImageAccountPools{}, infraerrors.BadRequest("INVALID_IMAGE_ACCOUNT_POOL", err.Error())
	}
	accounts, err := s.accountRepo.GetByIDs(ctx, accountIDs)
	if err != nil {
		return GroupImageAccountPools{}, err
	}
	accountsByID := make(map[int64]*Account, len(accounts))
	for _, account := range accounts {
		if account != nil {
			accountsByID[account.ID] = account
		}
	}
	if len(accountsByID) != len(accountIDs) {
		return GroupImageAccountPools{}, ErrAccountNotFound
	}
	for _, accountID := range accountIDs {
		account := accountsByID[accountID]
		if !accountPlatformCompatibleWithImageSizeGroup(group.Platform, account.Platform) {
			return GroupImageAccountPools{}, infraerrors.BadRequest("INVALID_IMAGE_ACCOUNT_POOL", fmt.Sprintf(
				"account %d platform %s is not compatible with group platform %s", account.ID, account.Platform, group.Platform,
			))
		}
		if group.RequireOAuthOnly && groupSupportsOAuthOnlyFilter(group.Platform) && account.Type == AccountTypeAPIKey {
			return GroupImageAccountPools{}, infraerrors.BadRequest("INVALID_IMAGE_ACCOUNT_POOL", fmt.Sprintf(
				"account %d is an API key account but group %s requires OAuth accounts", account.ID, group.Name,
			))
		}
	}
	store := asImageAccountPoolAdminStore(s.accountRepo)
	if store == nil {
		return GroupImageAccountPools{}, fmt.Errorf("image account pool repository is not configured")
	}
	if err := store.ReplaceImageAccountPools(ctx, groupID, pools); err != nil {
		return GroupImageAccountPools{}, err
	}
	return s.GetGroupImageAccountPools(ctx, groupID)
}

func validateGroupImageAccountPools(pools GroupImageAccountPools) ([]int64, error) {
	for tier := range pools.ResolutionPools {
		if !IsValidImageSizeTier(tier) {
			return nil, fmt.Errorf("invalid image size tier %q", tier)
		}
	}
	models := make(map[string]struct{})
	combinedModels := make(map[string]struct{})
	accountIDs := make(map[int64]struct{})
	validateAccounts := func(accounts []GroupImageSizeAccount) error {
		seen := make(map[int64]struct{}, len(accounts))
		for _, account := range accounts {
			if account.AccountID <= 0 {
				return fmt.Errorf("account_id must be positive")
			}
			if _, duplicate := seen[account.AccountID]; duplicate {
				continue
			}
			seen[account.AccountID] = struct{}{}
			accountIDs[account.AccountID] = struct{}{}
		}
		return nil
	}
	for _, accounts := range pools.ResolutionPools {
		if err := validateAccounts(accounts); err != nil {
			return nil, err
		}
	}
	for _, pool := range pools.ModelPools {
		model, err := NormalizeImageAccountPoolModel(pool.Model)
		if err != nil {
			return nil, err
		}
		if _, duplicate := models[model]; duplicate {
			return nil, fmt.Errorf("duplicate model %q in model_pools", model)
		}
		models[model] = struct{}{}
		if err := validateAccounts(pool.Accounts); err != nil {
			return nil, err
		}
	}
	for _, pool := range pools.ModelResolutionPools {
		model, err := NormalizeImageAccountPoolModel(pool.Model)
		if err != nil {
			return nil, err
		}
		if _, duplicate := combinedModels[model]; duplicate {
			return nil, fmt.Errorf("duplicate model %q in model_resolution_pools", model)
		}
		combinedModels[model] = struct{}{}
		for tier := range pool.Resolutions {
			if !IsValidImageSizeTier(tier) {
				return nil, fmt.Errorf("invalid image size tier %q", tier)
			}
		}
		for _, accounts := range pool.Resolutions {
			if err := validateAccounts(accounts); err != nil {
				return nil, err
			}
		}
	}
	ids := make([]int64, 0, len(accountIDs))
	for id := range accountIDs {
		ids = append(ids, id)
	}
	return ids, nil
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
		// Antigravity groups and dedicated /antigravity routes use a forced
		// single-platform scheduler; a Gemini account could never be selected.
		return accountPlatform == PlatformAntigravity
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
		if row.Model != "" {
			continue
		}
		tier := row.SizeTier
		if !IsValidImageSizeTier(tier) {
			continue
		}
		view[tier] = append(view[tier], row)
	}
	return view
}

func groupImageAccountPoolsToView(mode string, rows []GroupImageSizeAccount) GroupImageAccountPools {
	view := EmptyGroupImageAccountPools(mode)
	modelIndex := make(map[string]int)
	combinedIndex := make(map[string]int)
	for _, row := range rows {
		switch {
		case row.Model == "" && IsValidImageSizeTier(row.SizeTier):
			view.ResolutionPools[row.SizeTier] = append(view.ResolutionPools[row.SizeTier], row)
		case row.Model != "" && row.SizeTier == "":
			idx, ok := modelIndex[row.Model]
			if !ok {
				idx = len(view.ModelPools)
				modelIndex[row.Model] = idx
				view.ModelPools = append(view.ModelPools, ImageModelAccountPool{Model: row.Model, Accounts: []GroupImageSizeAccount{}})
			}
			view.ModelPools[idx].Accounts = append(view.ModelPools[idx].Accounts, row)
		case row.Model != "" && IsValidImageSizeTier(row.SizeTier):
			idx, ok := combinedIndex[row.Model]
			if !ok {
				idx = len(view.ModelResolutionPools)
				combinedIndex[row.Model] = idx
				view.ModelResolutionPools = append(view.ModelResolutionPools, ImageModelResolutionAccountPool{
					Model: row.Model,
					Resolutions: map[string][]GroupImageSizeAccount{
						ImageBillingSize1K: {}, ImageBillingSize2K: {}, ImageBillingSize4K: {},
					},
				})
			}
			view.ModelResolutionPools[idx].Resolutions[row.SizeTier] = append(view.ModelResolutionPools[idx].Resolutions[row.SizeTier], row)
		}
	}
	return view
}

func imageAccountPoolModelCandidates(platform string, candidates []string, rows []GroupImageSizeAccount) []string {
	values := make([]string, 0, len(candidates)+len(rows))
	for _, model := range candidates {
		model = strings.TrimSpace(model)
		if _, err := NormalizeImageAccountPoolModel(model); err != nil {
			continue
		}
		isImage := strings.HasPrefix(strings.ToLower(model), "gpt-image-") || isImageGenerationModel(model)
		if platform == PlatformOpenAI && !strings.HasPrefix(strings.ToLower(model), "gpt-image-") {
			continue
		}
		if (platform == PlatformGemini || platform == PlatformAntigravity) && !isImageGenerationModel(model) {
			continue
		}
		if platform == PlatformComposite && !isImage {
			continue
		}
		if model != "" {
			values = append(values, model)
		}
	}
	for _, row := range rows {
		if row.Model != "" {
			values = append(values, row.Model)
		}
	}
	return compactUniqueStrings(values)
}
