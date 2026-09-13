package service

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	ImageAccountPoolModeResolution      = "resolution"
	ImageAccountPoolModeModel           = "model"
	ImageAccountPoolModeModelResolution = "model_resolution"
)

// GroupImageSizeAccount is one account binding in a group's image size-tier pool.
type GroupImageSizeAccount struct {
	ID        int64     `json:"id"`
	GroupID   int64     `json:"group_id"`
	Model     string    `json:"model,omitempty"`
	SizeTier  string    `json:"size_tier"`
	AccountID int64     `json:"account_id"`
	Priority  int       `json:"priority"`
	CreatedAt time.Time `json:"created_at"`

	AccountName *string `json:"account_name,omitempty"`
}

// GroupImageSizeAccountBinding is the admin write shape for one tier entry.
type GroupImageSizeAccountBinding struct {
	AccountID int64 `json:"account_id"`
	Priority  int   `json:"priority"`
}

// GroupImageSizeAccountBindings is the replace payload keyed by size tier (1K/2K/4K).
type GroupImageSizeAccountBindings map[string][]GroupImageSizeAccountBinding

// ValidImageSizeTiers returns the supported image size pool tiers in display order.
func ValidImageSizeTiers() []string {
	return []string{ImageBillingSize1K, ImageBillingSize2K, ImageBillingSize4K}
}

// IsValidImageSizeTier reports whether tier is an exact pool key (1K/2K/4K).
func IsValidImageSizeTier(tier string) bool {
	switch tier {
	case ImageBillingSize1K, ImageBillingSize2K, ImageBillingSize4K:
		return true
	default:
		return false
	}
}

// NormalizeImageSizePoolTier normalizes a requested resolution into a pool key.
func NormalizeImageSizePoolTier(size string) string {
	return NormalizeImageBillingTierOrDefault(size)
}

// GroupImageSizeAccountBindingsView is the admin read shape keyed by size tier.
type GroupImageSizeAccountBindingsView map[string][]GroupImageSizeAccount

// ImageModelAccountPool is one exact-model pool in the admin API.
type ImageModelAccountPool struct {
	Model    string                  `json:"model"`
	Accounts []GroupImageSizeAccount `json:"accounts"`
}

// ImageModelResolutionAccountPool is one exact-model set of resolution pools.
type ImageModelResolutionAccountPool struct {
	Model       string                             `json:"model"`
	Resolutions map[string][]GroupImageSizeAccount `json:"resolutions"`
}

// GroupImageAccountPools is the complete persisted image routing configuration.
// ModelCandidates is populated on GET and ignored when replacing configuration.
type GroupImageAccountPools struct {
	Mode                 string                             `json:"mode"`
	ResolutionPools      map[string][]GroupImageSizeAccount `json:"resolution_pools"`
	ModelPools           []ImageModelAccountPool            `json:"model_pools"`
	ModelResolutionPools []ImageModelResolutionAccountPool  `json:"model_resolution_pools"`
	ModelCandidates      []string                           `json:"model_candidates"`
}

// ValidImageAccountPoolMode reports whether mode is one of the persisted modes.
func ValidImageAccountPoolMode(mode string) bool {
	switch mode {
	case ImageAccountPoolModeResolution, ImageAccountPoolModeModel, ImageAccountPoolModeModelResolution:
		return true
	default:
		return false
	}
}

// NormalizeImageAccountPoolMode preserves backward compatibility for pre-migration
// in-memory values and new groups by treating an empty or unknown value as resolution.
func NormalizeImageAccountPoolMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if !ValidImageAccountPoolMode(mode) {
		return ImageAccountPoolModeResolution
	}
	return mode
}

// NormalizeImageAccountPoolModel validates and trims one exact request-side model ID.
func NormalizeImageAccountPoolModel(model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", fmt.Errorf("model is required")
	}
	if !utf8.ValidString(model) || len([]byte(model)) > 255 {
		return "", fmt.Errorf("model must be valid UTF-8 and at most 255 bytes")
	}
	if strings.Contains(model, "*") {
		return "", fmt.Errorf("model must not contain wildcard characters")
	}
	for _, r := range model {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("model must not contain control characters")
		}
	}
	return model, nil
}

// EmptyGroupImageAccountPools returns a stable response shape with all tiers present.
func EmptyGroupImageAccountPools(mode string) GroupImageAccountPools {
	return GroupImageAccountPools{
		Mode: NormalizeImageAccountPoolMode(mode),
		ResolutionPools: map[string][]GroupImageSizeAccount{
			ImageBillingSize1K: {},
			ImageBillingSize2K: {},
			ImageBillingSize4K: {},
		},
		ModelPools:           []ImageModelAccountPool{},
		ModelResolutionPools: []ImageModelResolutionAccountPool{},
		ModelCandidates:      []string{},
	}
}
