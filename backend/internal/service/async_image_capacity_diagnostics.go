package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Diagnostic snapshots never participate in account selection. Pool snapshots
// describe database state sampled on capacity failure, not the exact decisions of
// a cached scheduler. Actual filter/acquire observations are labelled separately.
type ImageCapacityReason struct {
	Code               string     `json:"code"`
	Count              int        `json:"count"`
	AccountIDs         []int64    `json:"account_ids,omitempty"`
	EarliestRecoveryAt *time.Time `json:"earliest_recovery_at,omitempty"`
}
type ImageCapacityObservation struct {
	Source              string                `json:"source"`
	ObservedAt          time.Time             `json:"observed_at"`
	PoolMode            string                `json:"pool_mode,omitempty"`
	PoolModel           string                `json:"pool_model,omitempty"`
	PoolSizeTier        string                `json:"pool_size_tier,omitempty"`
	CandidateCount      int                   `json:"candidate_count"`
	CandidatesTruncated bool                  `json:"candidates_truncated"`
	Reasons             []ImageCapacityReason `json:"reasons"`
}
type ImageCapacityDiagnostics struct {
	ObservedAt   time.Time                  `json:"observed_at"`
	GroupID      int64                      `json:"group_id"`
	Model        string                     `json:"model"`
	SizeTier     string                     `json:"size_tier"`
	Observations []ImageCapacityObservation `json:"observations"`
}
type imageCapacityCaptureKey struct{}
type imageCapacityCapture struct {
	mu       sync.Mutex
	value    ImageCapacityDiagnostics
	sampled  map[string]bool
	seen     map[string]map[int64]bool
	pools    []imageCapacityPoolDescriptor
	resolved bool
}

func WithImageCapacityDiagnostics(ctx context.Context, groupID int64, model, sizeTier string) context.Context {
	return context.WithValue(ctx, imageCapacityCaptureKey{}, &imageCapacityCapture{
		value:   ImageCapacityDiagnostics{ObservedAt: time.Now().UTC(), GroupID: groupID, Model: model, SizeTier: sizeTier},
		sampled: make(map[string]bool), seen: make(map[string]map[int64]bool),
	})
}
func imageCapacityCaptureFrom(ctx context.Context) *imageCapacityCapture {
	if ctx == nil {
		return nil
	}
	capture, _ := ctx.Value(imageCapacityCaptureKey{}).(*imageCapacityCapture)
	return capture
}

// ImageCapacitySnapshotStore deliberately returns only operational metadata.
type ImageCapacitySnapshotStore interface {
	SnapshotImageAccountCapacity(context.Context, int64, string, string, string, []string) (ImageCapacityObservation, error)
}

type imageCapacityPoolDescriptor struct {
	repo              AccountRepository
	groupID           int64
	mode, model, tier string
	platforms         []string
}

// Only retain the actual routing key here. Successful requests incur no extra
// database read. Snapshot lookup is deferred until local capacity really fails.
func captureImageCapacityPool(ctx context.Context, repo AccountRepository, groupID int64, mode, model, tier string, platforms []string) {
	c := imageCapacityCaptureFrom(ctx)
	if c == nil {
		return
	}
	key := fmt.Sprintf("%d/%s/%s/%s", groupID, mode, model, tier)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sampled[key] || len(c.sampled) >= 8 {
		return
	}
	c.sampled[key] = true
	c.pools = append(c.pools, imageCapacityPoolDescriptor{repo: repo, groupID: groupID, mode: mode, model: model, tier: tier, platforms: append([]string(nil), platforms...)})
}

// Default routing is only known when the gateway actually lists candidates.
// A configured-pool probe can fall back to a different platform or group. The
// current metadata query covers the task's group, so leave other scopes unknown.
func captureDefaultImageCapacityPool(ctx context.Context, repo AccountRepository, groupID *int64, platforms []string, simpleMode bool) {
	c := imageCapacityCaptureFrom(ctx)
	if c == nil {
		return
	}
	if simpleMode || groupID == nil || *groupID != c.value.GroupID {
		captureImageCapacityPool(ctx, nil, c.value.GroupID, "default", "", "", platforms)
		return
	}
	captureImageCapacityPool(ctx, repo, *groupID, "default", "", "", platforms)
}

// CaptureImageCapacityFailureSnapshot performs at most 300ms of total optional
// I/O after capacity failure. These observations describe failure-time DB state,
// not the historical state of cached candidates. Missing data remains unknown.
func CaptureImageCapacityFailureSnapshot(parent, invocationCtx context.Context) {
	c := imageCapacityCaptureFrom(invocationCtx)
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.resolved {
		c.mu.Unlock()
		return
	}
	c.resolved = true
	pools := append([]imageCapacityPoolDescriptor(nil), c.pools...)
	c.mu.Unlock()
	readCtx, cancel := context.WithTimeout(parent, 300*time.Millisecond)
	defer cancel()
	for _, p := range pools {
		observation := ImageCapacityObservation{Source: "unavailable", ObservedAt: time.Now().UTC(), PoolMode: p.mode, PoolModel: p.model, PoolSizeTier: p.tier, Reasons: []ImageCapacityReason{{Code: "unknown", Count: 1}}}
		if readCtx.Err() == nil {
			if store, ok := p.repo.(ImageCapacitySnapshotStore); ok {
				value, err := store.SnapshotImageAccountCapacity(readCtx, p.groupID, p.mode, p.model, p.tier, p.platforms)
				if err == nil {
					observation = value
				}
			}
		}
		c.mu.Lock()
		if len(c.value.Observations) < 16 {
			c.value.Observations = append(c.value.Observations, observation)
		}
		c.mu.Unlock()
	}
}

var imageCapacityReasonCodes = map[string]bool{
	"scheduling_disabled": true, "inactive": true, "expired": true, "platform_mismatch": true,
	"rate_limited": true, "overloaded": true, "temporarily_unschedulable": true, "eligible_at_snapshot": true,
	"model_not_supported": true, "model_rate_limited": true, "excluded": true, "concurrency_full": true,
	"unknown": true, "not_schedulable": true, "runtime_blocked": true, "privacy_not_set": true,
	"transport_incompatible": true, "quota": true, "window_cost": true, "rpm": true, "profit_control": true,
	"circuit_open": true, "oauth_required": true, "no_candidates": true,
}

// RecordImageCapacityDecision records a real filter or slot-acquisition failure.
// IDs and reason cardinality are bounded and reasons are an allowlist, so no
// provider errors, prompts, credentials or arbitrary strings enter the event.
func RecordImageCapacityDecision(ctx context.Context, source, code string, accountID int64, candidates int) {
	c := imageCapacityCaptureFrom(ctx)
	if c == nil {
		return
	}
	if source != "scheduler_filter" && source != "concurrency" {
		source = "unavailable"
	}
	if !imageCapacityReasonCodes[code] {
		code = "unknown"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := source + "/" + code
	if c.seen[key] == nil {
		c.seen[key] = make(map[int64]bool)
	}
	if c.seen[key][accountID] {
		return
	}
	var obs *ImageCapacityObservation
	for i := range c.value.Observations {
		if c.value.Observations[i].Source == source {
			obs = &c.value.Observations[i]
			break
		}
	}
	if obs == nil {
		if len(c.value.Observations) >= 16 {
			return
		}
		c.value.Observations = append(c.value.Observations, ImageCapacityObservation{Source: source, ObservedAt: time.Now().UTC(), Reasons: []ImageCapacityReason{}})
		obs = &c.value.Observations[len(c.value.Observations)-1]
	}
	if candidates > obs.CandidateCount {
		obs.CandidateCount = candidates
	}
	if len(c.seen[key]) >= 512 {
		obs.CandidatesTruncated = true
		return
	}
	c.seen[key][accountID] = true
	for i := range obs.Reasons {
		if obs.Reasons[i].Code == code {
			incrementImageCapacityReason(&obs.Reasons[i], accountID)
			return
		}
	}
	obs.Reasons = append(obs.Reasons, ImageCapacityReason{Code: code})
	incrementImageCapacityReason(&obs.Reasons[len(obs.Reasons)-1], accountID)
}
func incrementImageCapacityReason(reason *ImageCapacityReason, id int64) {
	reason.Count++
	if id > 0 && len(reason.AccountIDs) < 20 {
		reason.AccountIDs = append(reason.AccountIDs, id)
	}
}

func ImageCapacityDiagnosticsFromContext(ctx context.Context) *ImageCapacityDiagnostics {
	c := imageCapacityCaptureFrom(ctx)
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Deep copy so asynchronous persistence cannot race with capture updates.
	encoded, _ := json.Marshal(c.value)
	var result ImageCapacityDiagnostics
	_ = json.Unmarshal(encoded, &result)
	if len(result.Observations) == 0 {
		result.Observations = []ImageCapacityObservation{{Source: "unavailable", ObservedAt: time.Now().UTC(), Reasons: []ImageCapacityReason{{Code: "unknown", Count: 1}}}}
	}
	for i := range result.Observations {
		sort.Slice(result.Observations[i].Reasons, func(a, b int) bool {
			return result.Observations[i].Reasons[a].Code < result.Observations[i].Reasons[b].Code
		})
	}
	return &result
}
