package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
)

type ImageInputCapabilities struct {
	Transport                                    string
	Count                                        int
	MaxBytes, TotalBytes, MaxPixels, TotalPixels int64
}

// ValidateImageAccountExtra validates only the optional image-specific settings;
// other account metadata remains owned by its existing validators.
func ValidateImageAccountExtra(extra map[string]any) error {
	if group, exists := extra["image_health_group"]; exists && group != nil {
		name, ok := group.(string)
		if !ok || len(name) > 128 || name != strings.TrimSpace(name) {
			return fmt.Errorf("image_health_group must be a trimmed string of at most 128 bytes")
		}
	}
	raw, exists := extra["image_input_capabilities"]
	if !exists || raw == nil {
		return nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("invalid image_input_capabilities: %w", err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(body, &fields); err != nil || fields == nil {
		return fmt.Errorf("image_input_capabilities must be an object")
	}
	for key, value := range fields {
		switch key {
		case "transports":
			var modes []string
			if json.Unmarshal(value, &modes) != nil || len(modes) == 0 || len(modes) > 2 {
				return fmt.Errorf("image input transports must contain local and/or passthrough")
			}
			for _, mode := range modes {
				if mode != "local" && mode != "passthrough" {
					return fmt.Errorf("unsupported image input transport %q", mode)
				}
			}
		case "max_images", "max_image_bytes", "max_total_bytes", "max_image_pixels", "max_total_pixels":
			var n int64
			if json.Unmarshal(value, &n) != nil || n < 0 {
				return fmt.Errorf("%s must be a nonnegative integer", key)
			}
		default:
			return fmt.Errorf("unsupported image input capability %q", key)
		}
	}
	return nil
}

type imageInputCapabilitiesKey struct{}
type imageInputCapabilitiesState struct {
	input                  ImageInputCapabilities
	localFallbackSuggested atomic.Bool
}

func WithImageInputCapabilities(ctx context.Context, input ImageInputCapabilities) context.Context {
	return context.WithValue(ctx, imageInputCapabilitiesKey{}, &imageInputCapabilitiesState{input: input})
}

// ImageInputLocalFallbackSuggested is only admission evidence. Callers must
// independently prove that no generation request was sent before changing transport.
func ImageInputLocalFallbackSuggested(ctx context.Context) bool {
	state, _ := ctx.Value(imageInputCapabilitiesKey{}).(*imageInputCapabilitiesState)
	return state != nil && state.localFallbackSuggested.Load()
}

// ImageAccountSupportsInput checks administrator-declared limits without changing
// request content. Unmeasured URL bytes are unknown, not evidence of incompatibility.
func ImageAccountSupportsInput(ctx context.Context, account *Account) bool {
	if account == nil {
		return false
	}
	state, ok := ctx.Value(imageInputCapabilitiesKey{}).(*imageInputCapabilitiesState)
	if !ok || state.input.Count == 0 || account.Extra == nil {
		return true
	}
	input := state.input
	raw, exists := account.Extra["image_input_capabilities"]
	if !exists {
		return true
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return true
	}
	var limits struct {
		Transports  []string `json:"transports"`
		MaxImages   int      `json:"max_images"`
		MaxBytes    int64    `json:"max_image_bytes"`
		TotalBytes  int64    `json:"max_total_bytes"`
		MaxPixels   int64    `json:"max_image_pixels"`
		TotalPixels int64    `json:"max_total_pixels"`
	}
	if json.Unmarshal(b, &limits) != nil {
		return true
	}
	withinLimits := !(limits.MaxImages > 0 && input.Count > limits.MaxImages ||
		limits.MaxBytes > 0 && input.MaxBytes > limits.MaxBytes || limits.TotalBytes > 0 && input.TotalBytes > limits.TotalBytes ||
		limits.MaxPixels > 0 && input.MaxPixels > limits.MaxPixels || limits.TotalPixels > 0 && input.TotalPixels > limits.TotalPixels)
	if len(limits.Transports) > 0 && input.Transport != "" {
		matched, localAllowed := false, false
		for _, mode := range limits.Transports {
			if strings.EqualFold(mode, input.Transport) {
				matched = true
			}
			localAllowed = localAllowed || strings.EqualFold(mode, "local")
		}
		if !matched {
			if input.Transport == "passthrough" && localAllowed && withinLimits {
				state.localFallbackSuggested.Store(true)
			}
			return false
		}
	}
	return withinLimits
}

func filterImageInputCapableAccounts(ctx context.Context, accounts []Account) []Account {
	filtered := make([]Account, 0, len(accounts))
	for i := range accounts {
		if ImageAccountSupportsInput(ctx, &accounts[i]) {
			filtered = append(filtered, accounts[i])
		}
	}
	return filtered
}
