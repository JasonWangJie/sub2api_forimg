package service

import "context"

// Reliability aggregates preserve the difference between account selection,
// actual HTTP invocations and worker retries. Old rows are never presented as
// instrumented first attempts.
type AsyncImageReliabilityStats struct {
	Total                     int64    `json:"total"`
	Unknown                   int64    `json:"unknown"`
	Expired                   int64    `json:"expired"`
	AdminTerminated           int64    `json:"admin_terminated"`
	InstrumentedTasks         int64    `json:"instrumented_tasks"`
	FirstAttemptSucceeded     int64    `json:"first_attempt_succeeded"`
	HTTPInvocations           int64    `json:"http_invocations"`
	AccountSelections         int64    `json:"account_selections"`
	MultiAccountTasks         int64    `json:"multi_account_tasks"`
	MultiAccountSucceeded     int64    `json:"multi_account_succeeded"`
	LocalFallbackTasks        int64    `json:"local_fallback_tasks"`
	LocalFallbackSucceeded    int64    `json:"local_fallback_succeeded"`
	ResultFetchRetriedTasks   int64    `json:"result_fetch_retried_tasks"`
	ResultFetchRecoveredTasks int64    `json:"result_fetch_recovered_tasks"`
	UnknownEvents             int64    `json:"unknown_events"`
	P95DurationMS             *float64 `json:"p95_duration_ms"`
	P99DurationMS             *float64 `json:"p99_duration_ms"`
	P95QueueMS                *float64 `json:"p95_queue_ms"`
	P95GenerationMS           *float64 `json:"p95_generation_ms"`
	P95PostprocessMS          *float64 `json:"p95_postprocess_ms"`
	OldestQueuedAgeMS         *float64 `json:"oldest_queued_age_ms"`
	ActualCost                float64  `json:"actual_cost"`
}

type AsyncImageReliabilityStatsRepository interface {
	AsyncImageReliabilityStats(context.Context, AsyncImageTaskFilter) (*AsyncImageReliabilityStats, error)
}
