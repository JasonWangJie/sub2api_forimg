package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	defaultBillingChargeMultiplier    = 1.0
	maxBillingChargeMultiplier        = 10.0
	billingChargeMultiplierCacheTTL   = 60 * time.Second
	billingChargeMultiplierErrorTTL   = 10 * time.Second
	billingChargeMultiplierDBTimeout  = 2 * time.Second
	billingChargeMultiplierPublishTTL = 2 * time.Second
	billingChargeMultiplierRefreshKey = "billing_charge_multiplier"
)

type cachedBillingChargeMultiplier struct {
	value     float64
	allGroups bool
	groupIDs  map[int64]struct{}
	expiresAt int64
}

type billingChargePolicyPayload struct {
	Multiplier float64 `json:"multiplier"`
	AllGroups  bool    `json:"all_groups"`
	GroupIDs   []int64 `json:"group_ids"`
}

// applyBillingChargeMultiplier scales ActualCost after group/user/peak multipliers.
// Invalid multipliers fall back to 1 so billing never breaks on misconfiguration.
func applyBillingChargeMultiplier(actualCost, multiplier float64) float64 {
	if actualCost <= 0 {
		return actualCost
	}
	m := normalizeBillingChargeMultiplier(multiplier)
	if m == 1 {
		return actualCost
	}
	return actualCost * m
}

// shouldApplyBillingChargeMultiplier excludes media generation from the
// system multiplier. ImageCount covers token-priced image generation, while
// BillingMode protects image/video paths whose result metadata is incomplete.
func shouldApplyBillingChargeMultiplier(cost *CostBreakdown, imageCount int, isVideoUsage bool) bool {
	if cost == nil || imageCount > 0 || isVideoUsage {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(cost.BillingMode)) {
	case string(BillingModeImage), string(BillingModeVideo):
		return false
	default:
		return true
	}
}

func normalizeBillingChargeMultiplier(value float64) float64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return defaultBillingChargeMultiplier
	}
	if value > maxBillingChargeMultiplier {
		return maxBillingChargeMultiplier
	}
	return value
}

func parseBillingChargeMultiplier(raw string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return defaultBillingChargeMultiplier
	}
	return normalizeBillingChargeMultiplier(value)
}

func parseBillingChargeMultiplierUpdate(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || validateBillingChargeMultiplier(value) != nil {
		return 0, false
	}
	return value, true
}

func validateBillingChargeMultiplier(value float64) error {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return infraerrors.BadRequest(
			"INVALID_BILLING_CHARGE_MULTIPLIER",
			"billing charge multiplier must be a finite number greater than 0",
		)
	}
	if value > maxBillingChargeMultiplier {
		return infraerrors.BadRequest(
			"INVALID_BILLING_CHARGE_MULTIPLIER",
			"billing charge multiplier must be less than or equal to 10",
		)
	}
	return nil
}

func normalizeBillingChargeMultiplierGroupIDs(groupIDs []int64) []int64 {
	if len(groupIDs) == 0 {
		return []int64{}
	}
	seen := make(map[int64]struct{}, len(groupIDs))
	normalized := make([]int64, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		if groupID <= 0 {
			continue
		}
		if _, exists := seen[groupID]; exists {
			continue
		}
		seen[groupID] = struct{}{}
		normalized = append(normalized, groupID)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return normalized
}

func parseBillingChargeMultiplierGroupIDsValue(raw string) ([]int64, bool) {
	var groupIDs []int64
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &groupIDs); err != nil {
		return []int64{}, false
	}
	if groupIDs == nil {
		return []int64{}, false
	}
	return normalizeBillingChargeMultiplierGroupIDs(groupIDs), true
}

func newCachedBillingChargeMultiplier(value float64, allGroups bool, groupIDs []int64, ttl time.Duration) *cachedBillingChargeMultiplier {
	selected := make(map[int64]struct{}, len(groupIDs))
	for _, groupID := range normalizeBillingChargeMultiplierGroupIDs(groupIDs) {
		selected[groupID] = struct{}{}
	}
	return &cachedBillingChargeMultiplier{
		value:     normalizeBillingChargeMultiplier(value),
		allGroups: allGroups,
		groupIDs:  selected,
		expiresAt: time.Now().Add(ttl).UnixNano(),
	}
}

func (c *cachedBillingChargeMultiplier) appliesToGroup(groupID *int64) bool {
	if c == nil {
		return false
	}
	if c.allGroups {
		return true
	}
	if groupID == nil || *groupID <= 0 {
		return false
	}
	_, ok := c.groupIDs[*groupID]
	return ok
}

// GetBillingChargeMultiplier returns the system charge multiplier for the
// billing hot path. A cold or expired cache performs one synchronous,
// singleflight-coalesced read so a process restart cannot silently bill with 1.
func (s *SettingService) GetBillingChargeMultiplier(ctx context.Context) float64 {
	current := s.getBillingChargeMultiplierPolicy(ctx)
	if current == nil {
		return defaultBillingChargeMultiplier
	}
	return current.value
}

// GetBillingChargeMultiplierForGroup returns the configured multiplier only
// when the API key's billing group is in scope. A nil group is included only
// in all-groups mode.
func (s *SettingService) GetBillingChargeMultiplierForGroup(ctx context.Context, groupID *int64) float64 {
	current := s.getBillingChargeMultiplierPolicy(ctx)
	if current == nil || !current.appliesToGroup(groupID) {
		return defaultBillingChargeMultiplier
	}
	return current.value
}

func (s *SettingService) getBillingChargeMultiplierPolicy(ctx context.Context) *cachedBillingChargeMultiplier {
	if s == nil {
		return nil
	}
	cached, _ := s.billingChargeMultiplierCache.Load().(*cachedBillingChargeMultiplier)
	now := time.Now().UnixNano()
	if cached != nil && now < cached.expiresAt {
		return cached
	}
	_, _, _ = s.billingChargeMultiplierSF.Do(billingChargeMultiplierRefreshKey, func() (any, error) {
		if current, _ := s.billingChargeMultiplierCache.Load().(*cachedBillingChargeMultiplier); current != nil && time.Now().UnixNano() < current.expiresAt {
			return current, nil
		}
		return s.refreshBillingChargeMultiplier(ctx), nil
	})
	if current, _ := s.billingChargeMultiplierCache.Load().(*cachedBillingChargeMultiplier); current != nil {
		return current
	}
	return nil
}

// WarmBillingChargeMultiplier synchronously loads the multiplier into cache.
func (s *SettingService) WarmBillingChargeMultiplier(ctx context.Context) float64 {
	if s == nil {
		return defaultBillingChargeMultiplier
	}
	return s.GetBillingChargeMultiplier(ctx)
}

func (s *SettingService) storeBillingChargeMultiplierCache(value float64) {
	s.storeBillingChargeMultiplierPolicyCache(value, true, nil)
}

func (s *SettingService) storeBillingChargeMultiplierPolicyCache(value float64, allGroups bool, groupIDs []int64) {
	if s == nil {
		return
	}
	s.billingChargeMultiplierMu.Lock()
	defer s.billingChargeMultiplierMu.Unlock()
	s.billingChargeMultiplierGeneration.Add(1)
	s.billingChargeMultiplierCache.Store(newCachedBillingChargeMultiplier(
		value,
		allGroups,
		groupIDs,
		billingChargeMultiplierCacheTTL,
	))
}

func (s *SettingService) refreshBillingChargeMultiplier(ctx context.Context) *cachedBillingChargeMultiplier {
	if s == nil || s.settingRepo == nil {
		return nil
	}
	generation := s.billingChargeMultiplierGeneration.Load()
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), billingChargeMultiplierDBTimeout)
	defer cancel()

	entry := newCachedBillingChargeMultiplier(defaultBillingChargeMultiplier, true, nil, billingChargeMultiplierCacheTTL)
	values, err := s.settingRepo.GetMultiple(dbCtx, []string{
		SettingKeyBillingChargeMultiplier,
		SettingKeyBillingChargeMultiplierAllGroups,
		SettingKeyBillingChargeMultiplierGroupIDs,
	})
	if err == nil {
		value := parseBillingChargeMultiplier(values[SettingKeyBillingChargeMultiplier])
		allGroups := values[SettingKeyBillingChargeMultiplierAllGroups] != "false"
		groupIDs, validGroupIDs := parseBillingChargeMultiplierGroupIDsValue(values[SettingKeyBillingChargeMultiplierGroupIDs])
		if !allGroups && !validGroupIDs {
			slog.Warn("invalid billing charge multiplier group scope; falling back to all groups")
			allGroups = true
		}
		entry = newCachedBillingChargeMultiplier(value, allGroups, groupIDs, billingChargeMultiplierCacheTTL)
	} else if !errors.Is(err, ErrSettingNotFound) {
		if prior, _ := s.billingChargeMultiplierCache.Load().(*cachedBillingChargeMultiplier); prior != nil {
			entry = newCachedBillingChargeMultiplier(prior.value, prior.allGroups, billingChargeMultiplierGroupIDs(prior), billingChargeMultiplierErrorTTL)
		} else {
			entry = newCachedBillingChargeMultiplier(defaultBillingChargeMultiplier, true, nil, billingChargeMultiplierErrorTTL)
		}
	}
	s.billingChargeMultiplierMu.Lock()
	defer s.billingChargeMultiplierMu.Unlock()
	if generation != s.billingChargeMultiplierGeneration.Load() {
		current, _ := s.billingChargeMultiplierCache.Load().(*cachedBillingChargeMultiplier)
		return current
	}
	s.billingChargeMultiplierCache.Store(entry)
	return entry
}

func billingChargeMultiplierGroupIDs(cached *cachedBillingChargeMultiplier) []int64 {
	if cached == nil || len(cached.groupIDs) == 0 {
		return []int64{}
	}
	groupIDs := make([]int64, 0, len(cached.groupIDs))
	for groupID := range cached.groupIDs {
		groupIDs = append(groupIDs, groupID)
	}
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })
	return groupIDs
}

func (s *SettingService) SetBillingSettingInvalidation(invalidation BillingSettingInvalidation) {
	if s != nil {
		s.billingSettingInvalidation = invalidation
	}
}

func (s *SettingService) publishBillingChargePolicy(ctx context.Context, value float64, allGroups bool, groupIDs []int64) {
	if s == nil || s.billingSettingInvalidation == nil {
		return
	}
	payload := billingChargePolicyPayload{
		Multiplier: normalizeBillingChargeMultiplier(value),
		AllGroups:  allGroups,
		GroupIDs:   normalizeBillingChargeMultiplierGroupIDs(groupIDs),
	}
	rawBytes, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("failed to encode billing charge policy update", "error", err)
		return
	}
	publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), billingChargeMultiplierPublishTTL)
	defer cancel()
	if err := s.billingSettingInvalidation.PublishBillingChargeMultiplier(publishCtx, string(rawBytes)); err != nil {
		slog.Warn("failed to publish billing charge multiplier update", "error", err)
	}
}

func parseBillingChargePolicyUpdate(raw string) (billingChargePolicyPayload, bool) {
	var payload billingChargePolicyPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &payload); err == nil {
		if validateBillingChargeMultiplier(payload.Multiplier) != nil {
			return billingChargePolicyPayload{}, false
		}
		if !payload.AllGroups && payload.GroupIDs == nil {
			return billingChargePolicyPayload{}, false
		}
		payload.GroupIDs = normalizeBillingChargeMultiplierGroupIDs(payload.GroupIDs)
		return payload, true
	}

	// Accept the pre-scope numeric payload during rolling upgrades.
	value, ok := parseBillingChargeMultiplierUpdate(raw)
	if !ok {
		return billingChargePolicyPayload{}, false
	}
	return billingChargePolicyPayload{Multiplier: value, AllGroups: true, GroupIDs: []int64{}}, true
}

// StartBillingSettingInvalidationSubscriber keeps the process-local cache in
// sync after another instance commits an admin settings update.
func (s *SettingService) StartBillingSettingInvalidationSubscriber(ctx context.Context) {
	if s == nil || s.billingSettingInvalidation == nil {
		return
	}
	s.billingInvalidationStart.Do(func() {
		subscriberCtx, cancel := context.WithCancel(ctx)
		s.billingInvalidationCancel = cancel
		s.billingInvalidationWG.Add(1)
		go func() {
			defer s.billingInvalidationWG.Done()
			backoff := time.Second
			for {
				err := s.billingSettingInvalidation.SubscribeBillingChargeMultiplier(subscriberCtx, func(raw string) {
					if policy, ok := parseBillingChargePolicyUpdate(raw); ok {
						s.storeBillingChargeMultiplierPolicyCache(policy.Multiplier, policy.AllGroups, policy.GroupIDs)
					}
				})
				if subscriberCtx.Err() != nil {
					return
				}
				if err == nil {
					err = errors.New("billing setting invalidation subscription closed")
				}
				slog.Warn("billing setting invalidation subscriber failed; retrying", "error", err, "retry_in", backoff)
				timer := time.NewTimer(backoff)
				select {
				case <-subscriberCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				if backoff < 30*time.Second {
					backoff *= 2
					if backoff > 30*time.Second {
						backoff = 30 * time.Second
					}
				}
			}
		}()
	})
}

func (s *SettingService) StopBillingSettingInvalidationSubscriber() {
	if s == nil {
		return
	}
	s.billingInvalidationStop.Do(func() {
		if s.billingInvalidationCancel != nil {
			s.billingInvalidationCancel()
		}
		s.billingInvalidationWG.Wait()
	})
}

// ResolveBillingChargeMultiplier is a nil-safe helper for gateway services.
func ResolveBillingChargeMultiplier(settingService *SettingService, ctx context.Context) float64 {
	if settingService == nil {
		return defaultBillingChargeMultiplier
	}
	return settingService.GetBillingChargeMultiplier(ctx)
}

// ResolveBillingChargeMultiplierForGroup is the billing-path helper used by
// gateway services after resolving the API key's actual billing group.
func ResolveBillingChargeMultiplierForGroup(settingService *SettingService, ctx context.Context, groupID *int64) float64 {
	if settingService == nil {
		return defaultBillingChargeMultiplier
	}
	return settingService.GetBillingChargeMultiplierForGroup(ctx, groupID)
}
