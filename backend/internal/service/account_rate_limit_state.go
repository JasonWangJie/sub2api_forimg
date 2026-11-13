package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrRateLimitStateChanged = apperrors.Conflict("RATE_LIMIT_STATE_CHANGED", "Account restrictions changed; reload the preview before clearing")

// Raw state stays inside the service/repository boundary. No credentials are read.
type AccountRateLimitState struct {
	AccountID                                                              int64
	Status                                                                 string
	Schedulable                                                            bool
	RateLimitedAt, RateLimitResetAt, OverloadUntil, TempUnschedulableUntil *time.Time
	TempUnschedulableReason                                                *string
	ModelRateLimits, QuotaScopes                                           json.RawMessage
}

type AccountRateLimitStateRepository interface {
	GetAccountRateLimitState(context.Context, int64) (*AccountRateLimitState, error)
	ClearAccountRateLimitState(context.Context, int64, string) (*AccountRateLimitState, *AccountRateLimitState, error)
	SyncAccountRateLimitSnapshot(context.Context, int64) error
}

type AccountRateLimitRestriction struct {
	Kind  string     `json:"kind"`
	Scope string     `json:"scope,omitempty"`
	Until *time.Time `json:"until,omitempty"`
}
type AccountRateLimitPreview struct {
	AccountID    int64                         `json:"account_id"`
	Version      string                        `json:"version"`
	Status       string                        `json:"status"`
	Schedulable  bool                          `json:"schedulable"`
	Restrictions []AccountRateLimitRestriction `json:"restrictions"`
	ActiveCount  int                           `json:"active_count"`
	Truncated    bool                          `json:"truncated"`
}
type AccountRateLimitCacheSync struct {
	Status           string   `json:"status"`
	FailedComponents []string `json:"failed_components"`
}
type AccountRateLimitClearResult struct {
	Before    *AccountRateLimitPreview  `json:"before"`
	After     *AccountRateLimitPreview  `json:"after"`
	CacheSync AccountRateLimitCacheSync `json:"cache_sync"`
}

// Canonical JSON removes formatting differences but includes expired markers and
// exact timestamps: a newly recorded restriction must invalidate an old preview.
func (s *AccountRateLimitState) Version() string {
	decode := func(raw json.RawMessage) any {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		_ = decoder.Decode(&value)
		return value
	}
	models, quota := decode(s.ModelRateLimits), decode(s.QuotaScopes)
	utc := func(t *time.Time) *time.Time {
		if t == nil {
			return nil
		}
		v := t.UTC()
		return &v
	}
	raw, _ := json.Marshal([]any{s.AccountID, s.Status, s.Schedulable, utc(s.RateLimitedAt), utc(s.RateLimitResetAt), utc(s.OverloadUntil), utc(s.TempUnschedulableUntil), s.TempUnschedulableReason, models, quota})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (s *AccountRateLimitState) Preview(now time.Time) *AccountRateLimitPreview {
	p := &AccountRateLimitPreview{AccountID: s.AccountID, Version: s.Version(), Status: s.Status, Schedulable: s.Schedulable, Restrictions: []AccountRateLimitRestriction{}}
	add := func(kind, scope string, until *time.Time) {
		if until != nil && !until.After(now) {
			return
		}
		p.ActiveCount++
		if len(p.Restrictions) < 100 {
			p.Restrictions = append(p.Restrictions, AccountRateLimitRestriction{Kind: kind, Scope: scope, Until: until})
		} else {
			p.Truncated = true
		}
	}
	if s.RateLimitResetAt != nil {
		add("account", "", s.RateLimitResetAt)
	}
	if s.OverloadUntil != nil {
		add("overload", "", s.OverloadUntil)
	}
	if s.TempUnschedulableUntil != nil {
		add("temporary", "", s.TempUnschedulableUntil)
	}
	var models map[string]json.RawMessage
	if json.Unmarshal(s.ModelRateLimits, &models) == nil {
		keys := make([]string, 0, len(models))
		for key := range models {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			var entry struct {
				Reset *time.Time `json:"rate_limit_reset_at"`
			}
			if json.Unmarshal(models[key], &entry) == nil && entry.Reset != nil {
				add("model", safeRateLimitScope(key), entry.Reset)
			}
		}
	}
	var scopes map[string]any
	if json.Unmarshal(s.QuotaScopes, &scopes) == nil && len(scopes) > 0 {
		add("quota", "", nil)
	}
	return p
}
func safeRateLimitScope(s string) string {
	if len(s) > 100 || strings.Contains(s, "://") {
		return "model"
	}
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= 100 {
			break
		}
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._-:/", r)) {
			b.WriteRune(r)
		} else {
			return "model"
		}
	}
	if b.Len() == 0 {
		return "model"
	}
	return b.String()
}
func (s *RateLimitService) GetAccountRateLimitPreview(ctx context.Context, id int64) (*AccountRateLimitPreview, error) {
	repo, ok := s.accountRepo.(AccountRateLimitStateRepository)
	if !ok {
		return nil, apperrors.ServiceUnavailable("RATE_LIMIT_STATE_UNAVAILABLE", "Atomic restriction management unavailable")
	}
	state, err := repo.GetAccountRateLimitState(ctx, id)
	if err != nil {
		return nil, err
	}
	return state.Preview(time.Now()), nil
}
func (s *RateLimitService) ClearAccountRateLimitPreview(ctx context.Context, id int64, version string) (*AccountRateLimitClearResult, error) {
	if len(version) != 64 {
		return nil, apperrors.BadRequest("INVALID_RATE_LIMIT_VERSION", "A current preview version is required")
	}
	if _, err := hex.DecodeString(version); err != nil {
		return nil, apperrors.BadRequest("INVALID_RATE_LIMIT_VERSION", "Invalid preview version")
	}
	repo, ok := s.accountRepo.(AccountRateLimitStateRepository)
	if !ok {
		return nil, apperrors.ServiceUnavailable("RATE_LIMIT_STATE_UNAVAILABLE", "Atomic restriction management unavailable")
	}
	observed := s.observeManualRateLimitCaches(ctx, id)
	before, after, err := repo.ClearAccountRateLimitState(ctx, id, version)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &AccountRateLimitClearResult{Before: before.Preview(now), After: after.Preview(now), CacheSync: s.syncManuallyClearedRateLimitState(ctx, id, repo, before, observed)}, nil
}
func (s *RateLimitService) syncClearedRateLimitState(ctx context.Context, id int64, repo AccountRateLimitStateRepository) AccountRateLimitCacheSync {
	// Complete bounded post-commit propagation even when the HTTP caller disconnects.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result := AccountRateLimitCacheSync{Status: "synced", FailedComponents: []string{}}
	fail := func(component string) {
		result.Status = "warning"
		result.FailedComponents = append(result.FailedComponents, component)
	}
	if s.tempUnschedCache != nil {
		if err := s.tempUnschedCache.DeleteTempUnsched(ctx, id); err != nil {
			fail("temp_unsched")
		}
	}
	if s.openAI403CounterCache != nil {
		if err := s.openAI403CounterCache.ResetOpenAI403Count(ctx, id); err != nil {
			fail("openai_403")
		}
	}
	s.notifyAccountSchedulingBlockCleared(id)
	if err := repo.SyncAccountRateLimitSnapshot(ctx, id); err != nil {
		fail("scheduler_snapshot")
	}
	return result
}

// Manual error recovery never clears restrictions, even if a new limit arrives
// after the preflight read. Automated successful-test recovery is separate.
func (s *RateLimitService) RecoverAccountErrorManually(ctx context.Context, id int64) error {
	preview, err := s.GetAccountRateLimitPreview(ctx, id)
	if err != nil {
		return err
	}
	if preview.ActiveCount > 0 {
		return apperrors.Conflict("RATE_LIMIT_PREVIEW_REQUIRED", "Preview and clear account restrictions before recovering its error state")
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if account.Status != StatusError {
		return nil
	}
	if err = s.accountRepo.ClearError(ctx, id); err != nil {
		return err
	}
	if s.tokenCacheInvalidator != nil && account.IsOAuth() {
		_ = s.tokenCacheInvalidator.InvalidateToken(ctx, account)
	}
	s.ResetOpenAI403Counter(ctx, id)
	// Do not clear a runtime cooldown created concurrently with this operation.
	return nil
}

// Optional observation interfaces keep manual clears from deleting restrictions
// created after the preview transaction. Adapters without CAS are left intact.
type observedTempUnschedCache interface {
	SnapshotTempUnsched(context.Context, int64) (string, error)
	DeleteTempUnschedIfObserved(context.Context, int64, string) (bool, error)
}
type observedRuntimeBlocker interface {
	SnapshotAccountSchedulingBlock(int64) (uint64, time.Time)
	ClearAccountSchedulingBlockIfObserved(int64, uint64) bool
}
type observed403Counter interface {
	SnapshotOpenAI403Count(context.Context, int64) (string, error)
}
type manualRateLimitCacheObservation struct {
	tempToken         string
	tempReady         bool
	runtimeGeneration uint64
	runtimeUntil      time.Time
	runtimeReady      bool
	counterPresent    bool
}

func (s *RateLimitService) observeManualRateLimitCaches(ctx context.Context, id int64) manualRateLimitCacheObservation {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	o := manualRateLimitCacheObservation{}
	if cache, ok := s.tempUnschedCache.(observedTempUnschedCache); ok {
		token, err := cache.SnapshotTempUnsched(ctx, id)
		o.tempToken = token
		o.tempReady = err == nil
	}
	if blocker, ok := s.runtimeBlocker.(observedRuntimeBlocker); ok {
		o.runtimeGeneration, o.runtimeUntil = blocker.SnapshotAccountSchedulingBlock(id)
		o.runtimeReady = true
	}
	if s.openAI403CounterCache != nil {
		o.counterPresent = true
		if cache, ok := s.openAI403CounterCache.(observed403Counter); ok {
			token, err := cache.SnapshotOpenAI403Count(ctx, id)
			o.counterPresent = err != nil || token != ""
		}
	}
	return o
}
func (s *RateLimitService) syncManuallyClearedRateLimitState(ctx context.Context, id int64, repo AccountRateLimitStateRepository, before *AccountRateLimitState, o manualRateLimitCacheObservation) AccountRateLimitCacheSync {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result := AccountRateLimitCacheSync{Status: "synced", FailedComponents: []string{}}
	fail := func(component string) {
		result.Status = "warning"
		result.FailedComponents = append(result.FailedComponents, component)
	}
	if s.tempUnschedCache != nil {
		if cache, ok := s.tempUnschedCache.(observedTempUnschedCache); ok && o.tempReady {
			cleared, err := cache.DeleteTempUnschedIfObserved(ctx, id, o.tempToken)
			if err != nil || !cleared {
				fail("temp_unsched")
			}
		} else {
			fail("temp_unsched")
		}
	}
	if s.runtimeBlocker != nil {
		if blocker, ok := s.runtimeBlocker.(observedRuntimeBlocker); ok && o.runtimeReady && runtimeLimitMatchesDatabase(before, o.runtimeUntil) {
			if !blocker.ClearAccountSchedulingBlockIfObserved(id, o.runtimeGeneration) {
				fail("runtime_block")
			}
		} else {
			fail("runtime_block")
		}
	}
	// Counter values can return to the same number after reset (ABA). Until this
	// cache has generation tokens, manual clearing preserves any existing count.
	if o.counterPresent {
		fail("openai_403")
	}
	if err := repo.SyncAccountRateLimitSnapshot(ctx, id); err != nil {
		fail("scheduler_snapshot")
	}
	return result
}

func runtimeLimitMatchesDatabase(state *AccountRateLimitState, until time.Time) bool {
	if until.IsZero() {
		return true
	} // no observed block; CAS still preserves a later one
	for _, stored := range []*time.Time{state.RateLimitResetAt, state.OverloadUntil, state.TempUnschedulableUntil} {
		if stored != nil {
			difference := until.Sub(*stored)
			if difference >= -time.Microsecond && difference <= time.Microsecond {
				return true
			}
		}
	}
	return false
}
