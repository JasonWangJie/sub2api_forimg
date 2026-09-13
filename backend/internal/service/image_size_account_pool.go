package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/tidwall/gjson"
)

type imageSizeAccountPoolContextKey struct{}

// ImageAccountPoolRoute is the request-side routing identity. Model is never
// rewritten by account/channel model mappings; SizeTier is the normalized tariff tier.
type ImageAccountPoolRoute struct {
	Model    string
	SizeTier string
}

// WithImageAccountPoolRoute validates and attaches an image request's exact model
// and optional resolution. Callers should surface validation failures as bad requests.
func WithImageAccountPoolRoute(ctx context.Context, model, sizeTier string) (context.Context, error) {
	normalizedModel, err := NormalizeImageAccountPoolModel(model)
	if err != nil {
		return ctx, err
	}
	tier := ""
	if strings.TrimSpace(sizeTier) != "" {
		tier = NormalizeImageSizePoolTier(sizeTier)
	}
	return context.WithValue(ctx, imageSizeAccountPoolContextKey{}, ImageAccountPoolRoute{
		Model:    normalizedModel,
		SizeTier: tier,
	}), nil
}

// WithImageSizeAccountPoolTier attaches a resolved 1K/2K/4K tier for image account selection.
func WithImageSizeAccountPoolTier(ctx context.Context, sizeTier string) context.Context {
	tier := NormalizeImageSizePoolTier(sizeTier)
	if tier == "" {
		return ctx
	}
	route, _ := ImageAccountPoolRouteFromContext(ctx)
	route.SizeTier = tier
	return context.WithValue(ctx, imageSizeAccountPoolContextKey{}, route)
}

// ImageSizeAccountPoolTierFromContext returns the size-tier pool hint when present.
func ImageSizeAccountPoolTierFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	route, ok := ImageAccountPoolRouteFromContext(ctx)
	if !ok || route.SizeTier == "" {
		return "", false
	}
	return NormalizeImageSizePoolTier(route.SizeTier), true
}

// ImageAccountPoolRouteFromContext returns the unified routing hint when present.
func ImageAccountPoolRouteFromContext(ctx context.Context) (ImageAccountPoolRoute, bool) {
	if ctx == nil {
		return ImageAccountPoolRoute{}, false
	}
	switch value := ctx.Value(imageSizeAccountPoolContextKey{}).(type) {
	case ImageAccountPoolRoute:
		return value, value.Model != "" || value.SizeTier != ""
	case string: // compatibility with contexts produced before the unified route type
		if value == "" {
			return ImageAccountPoolRoute{}, false
		}
		return ImageAccountPoolRoute{SizeTier: NormalizeImageSizePoolTier(value)}, true
	default:
		return ImageAccountPoolRoute{}, false
	}
}

// ImageAccountPoolStore is the repository surface used by multi-dimensional schedulers.
type ImageAccountPoolStore interface {
	GetImageAccountPoolMode(ctx context.Context, groupID int64) (string, error)
	HasImageAccountPoolConfigured(ctx context.Context, groupID int64, model, sizeTier string) (bool, error)
	ListSchedulableByGroupImageAccountPool(ctx context.Context, groupID int64, model, sizeTier string, platforms []string) ([]Account, error)
	ListImageSizeAccountIDsByGroupID(ctx context.Context, groupID int64) ([]int64, error)
}

// ImageSizeAccountPoolStore is the optional repository surface used by image schedulers.
type ImageSizeAccountPoolStore interface {
	HasImageSizeTierConfigured(ctx context.Context, groupID int64, sizeTier string) (bool, error)
	ListSchedulableByGroupImageSizeTier(ctx context.Context, groupID int64, sizeTier string, platforms []string) ([]Account, error)
	ListImageSizeAccountIDsByGroupID(ctx context.Context, groupID int64) ([]int64, error)
}

// ResolveImageAccountPool resolves the one exact key selected by the group's
// current mode. Missing dimensions or missing bindings fall back to account_groups;
// an existing key remains configured even if all of its accounts are unavailable.
func ResolveImageAccountPool(ctx context.Context, repo AccountRepository, groupID int64, platforms []string) ([]Account, bool, error) {
	store := asImageAccountPoolStore(repo)
	if store == nil {
		return nil, false, nil
	}
	mode, err := store.GetImageAccountPoolMode(ctx, groupID)
	if err != nil {
		return nil, false, fmt.Errorf("get image account pool mode: %w", err)
	}
	mode = NormalizeImageAccountPoolMode(mode)
	route, _ := ImageAccountPoolRouteFromContext(ctx)

	model, tier := "", ""
	switch mode {
	case ImageAccountPoolModeResolution:
		if route.SizeTier == "" {
			return nil, false, nil
		}
		tier = NormalizeImageSizePoolTier(route.SizeTier)
	case ImageAccountPoolModeModel:
		if route.Model == "" {
			return nil, false, nil
		}
		model = route.Model
	case ImageAccountPoolModeModelResolution:
		if route.Model == "" || route.SizeTier == "" {
			return nil, false, nil
		}
		model = route.Model
		tier = NormalizeImageSizePoolTier(route.SizeTier)
	}

	configured, err := store.HasImageAccountPoolConfigured(ctx, groupID, model, tier)
	if err != nil {
		return nil, false, fmt.Errorf("check image account pool: %w", err)
	}
	if !configured {
		return nil, false, nil
	}
	accounts, err := store.ListSchedulableByGroupImageAccountPool(ctx, groupID, model, tier, platforms)
	if err != nil {
		return nil, true, fmt.Errorf("list image account pool: %w", err)
	}
	return filterImageAccountPoolGroupRequirements(ctx, groupID, accounts), true, nil
}

// filterImageAccountPoolGroupRequirements keeps independent pool bindings from
// bypassing restrictions normally enforced when an account is attached through
// account_groups. A configured pool that becomes empty remains fail-closed.
func filterImageAccountPoolGroupRequirements(ctx context.Context, groupID int64, accounts []Account) []Account {
	if ctx == nil || len(accounts) == 0 {
		return accounts
	}
	group, ok := ctx.Value(ctxkey.Group).(*Group)
	if !ok || group == nil || group.ID != groupID || !IsGroupContextValid(group) ||
		!group.RequireOAuthOnly || !groupSupportsOAuthOnlyFilter(group.Platform) {
		return accounts
	}
	filtered := make([]Account, 0, len(accounts))
	for i := range accounts {
		if accounts[i].Type == AccountTypeAPIKey {
			continue
		}
		filtered = append(filtered, accounts[i])
	}
	return filtered
}

// ResolveImageSizeAccountPool loads a configured tier's candidates. A missing
// tier deliberately falls back to the default account_groups pool; a configured
// tier must not silently do so when its lookup fails or no account is usable.
func ResolveImageSizeAccountPool(ctx context.Context, repo AccountRepository, groupID int64, sizeTier string, platforms []string) ([]Account, bool, error) {
	store := asImageSizeAccountPoolStore(repo)
	if store == nil {
		return nil, false, nil
	}
	configured, err := store.HasImageSizeTierConfigured(ctx, groupID, sizeTier)
	if err != nil {
		return nil, false, fmt.Errorf("check image size account pool: %w", err)
	}
	if !configured {
		return nil, false, nil
	}
	accounts, err := store.ListSchedulableByGroupImageSizeTier(ctx, groupID, sizeTier, platforms)
	if err != nil {
		return nil, true, fmt.Errorf("list image size account pool: %w", err)
	}
	return filterImageAccountPoolGroupRequirements(ctx, groupID, accounts), true, nil
}

// ImageSizeAccountAdminStore is the optional repository surface used by admin group APIs.
type ImageSizeAccountAdminStore interface {
	ListImageSizeAccounts(ctx context.Context, groupID int64) ([]GroupImageSizeAccount, error)
	ReplaceImageSizeAccounts(ctx context.Context, groupID int64, bindings GroupImageSizeAccountBindings) error
}

// ImageAccountPoolAdminStore replaces all three configurations and mode atomically.
type ImageAccountPoolAdminStore interface {
	ListImageSizeAccounts(ctx context.Context, groupID int64) ([]GroupImageSizeAccount, error)
	ReplaceImageAccountPools(ctx context.Context, groupID int64, pools GroupImageAccountPools) error
}

func asImageAccountPoolStore(repo AccountRepository) ImageAccountPoolStore {
	if repo == nil {
		return nil
	}
	store, _ := repo.(ImageAccountPoolStore)
	return store
}

func asImageSizeAccountPoolStore(repo AccountRepository) ImageSizeAccountPoolStore {
	if repo == nil {
		return nil
	}
	if store, ok := repo.(ImageSizeAccountPoolStore); ok {
		return store
	}
	return nil
}

func asImageSizeAccountAdminStore(repo AccountRepository) ImageSizeAccountAdminStore {
	if repo == nil {
		return nil
	}
	if store, ok := repo.(ImageSizeAccountAdminStore); ok {
		return store
	}
	return nil
}

func asImageAccountPoolAdminStore(repo AccountRepository) ImageAccountPoolAdminStore {
	if repo == nil {
		return nil
	}
	store, _ := repo.(ImageAccountPoolAdminStore)
	return store
}

// ExtractImageSizePoolTierFromRequestBody best-effort extracts a 1K/2K/4K tier from
// Gemini chat/native image request bodies for account-pool routing.
func ExtractImageSizePoolTierFromRequestBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	candidates := []string{
		gjson.GetBytes(body, "extra_body.google.image_config.image_size").String(),
		gjson.GetBytes(body, "generationConfig.imageConfig.imageSize").String(),
		gjson.GetBytes(body, "generation_config.image_config.image_size").String(),
		gjson.GetBytes(body, "resolution").String(),
		gjson.GetBytes(body, "image_size").String(),
		gjson.GetBytes(body, "size").String(),
	}
	for _, raw := range candidates {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		// These fields are consumed only by Gemini native/compatible image
		// entrypoints. Prefer Gemini's short-edge tariff rule so routing uses the
		// same 1K/2K/4K classification as billing for non-square images.
		if tier, ok := ClassifyGeminiImageBillingTier(raw); ok {
			return tier
		}
		if tier, ok := ClassifyImageBillingTier(raw); ok {
			return tier
		}
	}
	return ""
}
