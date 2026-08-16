package service

import "time"

// GroupImageSizeAccount is one account binding in a group's image size-tier pool.
type GroupImageSizeAccount struct {
	ID        int64     `json:"id"`
	GroupID   int64     `json:"group_id"`
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
