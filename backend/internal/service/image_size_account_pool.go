package service

import (
	"context"
	"strings"

	"github.com/tidwall/gjson"
)

type imageSizeAccountPoolContextKey struct{}

// WithImageSizeAccountPoolTier attaches a resolved 1K/2K/4K tier for image account selection.
func WithImageSizeAccountPoolTier(ctx context.Context, sizeTier string) context.Context {
	tier := NormalizeImageSizePoolTier(sizeTier)
	if tier == "" {
		return ctx
	}
	return context.WithValue(ctx, imageSizeAccountPoolContextKey{}, tier)
}

// ImageSizeAccountPoolTierFromContext returns the size-tier pool hint when present.
func ImageSizeAccountPoolTierFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	tier, ok := ctx.Value(imageSizeAccountPoolContextKey{}).(string)
	if !ok || tier == "" {
		return "", false
	}
	return NormalizeImageSizePoolTier(tier), true
}

// ImageSizeAccountPoolStore is the optional repository surface used by image schedulers.
type ImageSizeAccountPoolStore interface {
	HasImageSizeTierConfigured(ctx context.Context, groupID int64, sizeTier string) (bool, error)
	ListSchedulableByGroupImageSizeTier(ctx context.Context, groupID int64, sizeTier string, platforms []string) ([]Account, error)
	ListImageSizeAccountIDsByGroupID(ctx context.Context, groupID int64) ([]int64, error)
}

// ImageSizeAccountAdminStore is the optional repository surface used by admin group APIs.
type ImageSizeAccountAdminStore interface {
	ListImageSizeAccounts(ctx context.Context, groupID int64) ([]GroupImageSizeAccount, error)
	ReplaceImageSizeAccounts(ctx context.Context, groupID int64, bindings GroupImageSizeAccountBindings) error
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
		if tier, ok := ClassifyImageBillingTier(raw); ok {
			return tier
		}
		if tier, ok := ClassifyGeminiImageBillingTier(raw); ok {
			return tier
		}
	}
	return ""
}
