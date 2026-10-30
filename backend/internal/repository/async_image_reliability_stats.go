package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *asyncImageTaskRepository) AsyncImageReliabilityStats(ctx context.Context, filter service.AsyncImageTaskFilter) (*service.AsyncImageReliabilityStats, error) {
	where, args := buildAsyncImageTaskFilter(filter)
	// Aggregate each task before joining events, so repeated audit rows cannot
	// multiply task counts, costs or duration samples. Reuse the exact list
	// filter, including ownership, rather than querying unscoped global totals.
	// A result-fetch finalization in older versions appended the same captured
	// attempts twice. Collapse only complete JSON duplicates with a valid,
	// nonzero RFC3339 timestamp; untimed/invalid legacy rows remain independent.
	// Validate numeric components behind the shape check: this also works on
	// supported PostgreSQL 14/15 and never casts malformed data to a timestamp.
	query := `WITH tasks AS (SELECT task_id,status,error_code,billing_status,actual_cost,
 account_attempts,attempted_account_ids,reference_transport,reference_retry_count,retry_count,
 submitted_at,started_at,upstream_succeeded_at,finished_at FROM async_image_tasks` + where + `),
events AS (
 SELECT e.task_id, COUNT(*) FILTER (WHERE event_type='result_fetch_retry') AS fetch_retries,
 COUNT(*) FILTER (WHERE to_status='execution_unknown') AS unknown_events,
 MIN(e.created_at) FILTER (WHERE event_type='invocation_started') AS first_invocation
 FROM async_image_events e JOIN tasks t ON t.task_id=e.task_id
 WHERE event_type IN ('result_fetch_retry','invocation_started') OR to_status='execution_unknown'
 GROUP BY e.task_id
),
facts AS (
 SELECT t.*, a.http_count,a.selections,COALESCE(e.fetch_retries,0) AS fetch_retries,
 COALESCE(e.unknown_events,0) AS unknown_events, COALESCE(e.first_invocation,t.started_at) AS first_invocation
 FROM tasks t
 CROSS JOIN LATERAL (
  SELECT COUNT(*) FILTER (WHERE j->>'status'='invocation_started') AS http_count,
         COUNT(*) FILTER (WHERE j->>'status'='selected') AS selections
  FROM (
   SELECT DISTINCT j, CASE
    WHEN jsonb_typeof(j->'attempted_at')='string'
     AND j->>'attempted_at' ~ '^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\.[0-9]+)?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$'
    THEN CASE WHEN
     substring(j->>'attempted_at',1,4)::integer > 0
     AND substring(j->>'attempted_at',9,2)::integer <= CASE substring(j->>'attempted_at',6,2)
      WHEN '02' THEN 28 + CASE WHEN substring(j->>'attempted_at',1,4)::integer % 4=0
       AND (substring(j->>'attempted_at',1,4)::integer % 100<>0 OR substring(j->>'attempted_at',1,4)::integer % 400=0) THEN 1 ELSE 0 END
      WHEN '04' THEN 30 WHEN '06' THEN 30 WHEN '09' THEN 30 WHEN '11' THEN 30 ELSE 31 END
     AND NOT (
      left(j->>'attempted_at',10)='0001-01-01' AND substring(j->>'attempted_at',18,2)='00'
      AND COALESCE(substring(j->>'attempted_at' FROM '\.([0-9]+)'),'0') !~ '[1-9]'
      AND substring(j->>'attempted_at',12,2)::integer * 60 + substring(j->>'attempted_at',15,2)::integer =
       CASE WHEN right(j->>'attempted_at',1)='Z' THEN 0 ELSE
        (CASE WHEN left(right(j->>'attempted_at',6),1)='-' THEN -1 ELSE 1 END) *
        (substring(right(j->>'attempted_at',6),2,2)::integer * 60 + substring(right(j->>'attempted_at',6),5,2)::integer) END
     ) THEN 0 ELSE ordinal END
    ELSE ordinal END AS occurrence
   FROM jsonb_array_elements(COALESCE(t.account_attempts,'[]'::jsonb)) WITH ORDINALITY AS attempts(j,ordinal)
  ) distinct_attempts
 ) a
 LEFT JOIN events e ON e.task_id=t.task_id
)
SELECT COUNT(*),
 COUNT(*) FILTER (WHERE status='execution_unknown'),
 COUNT(*) FILTER (WHERE status='expired'),
 COUNT(*) FILTER (WHERE error_code='admin_terminated'),
 COUNT(*) FILTER (WHERE http_count>0),
 COUNT(*) FILTER (WHERE status='succeeded' AND http_count=1 AND retry_count=0),
 COALESCE(SUM(http_count),0), COALESCE(SUM(selections),0),
 COUNT(*) FILTER (WHERE jsonb_array_length(attempted_account_ids)>1),
 COUNT(*) FILTER (WHERE status='succeeded' AND jsonb_array_length(attempted_account_ids)>1),
 COUNT(*) FILTER (WHERE reference_transport='local' AND reference_retry_count>0),
 COUNT(*) FILTER (WHERE status='succeeded' AND reference_transport='local' AND reference_retry_count>0),
 COUNT(*) FILTER (WHERE fetch_retries>0),
 COUNT(*) FILTER (WHERE status='succeeded' AND fetch_retries>0),
 COALESCE(SUM(unknown_events),0),
 percentile_cont(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM finished_at-submitted_at)*1000) FILTER (WHERE status='succeeded' AND finished_at>=submitted_at),
 percentile_cont(0.99) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM finished_at-submitted_at)*1000) FILTER (WHERE status='succeeded' AND finished_at>=submitted_at),
 percentile_cont(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM first_invocation-submitted_at)*1000) FILTER (WHERE first_invocation>=submitted_at),
 percentile_cont(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM upstream_succeeded_at-first_invocation)*1000) FILTER (WHERE upstream_succeeded_at>=first_invocation),
 percentile_cont(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM finished_at-upstream_succeeded_at)*1000) FILTER (WHERE status='succeeded' AND finished_at>=upstream_succeeded_at),
 MAX(GREATEST(0,EXTRACT(EPOCH FROM NOW()-submitted_at)*1000)) FILTER (WHERE status='queued'),
 COALESCE(SUM(actual_cost) FILTER (WHERE billing_status='succeeded'),0)
FROM facts`
	s := &service.AsyncImageReliabilityStats{}
	var p95, p99, queue, generation, postprocess, oldest sql.NullFloat64
	err := r.sql.QueryRowContext(ctx, query, args...).Scan(&s.Total, &s.Unknown, &s.Expired, &s.AdminTerminated, &s.InstrumentedTasks, &s.FirstAttemptSucceeded, &s.HTTPInvocations, &s.AccountSelections, &s.MultiAccountTasks, &s.MultiAccountSucceeded, &s.LocalFallbackTasks, &s.LocalFallbackSucceeded, &s.ResultFetchRetriedTasks, &s.ResultFetchRecoveredTasks, &s.UnknownEvents, &p95, &p99, &queue, &generation, &postprocess, &oldest, &s.ActualCost)
	if err != nil {
		return nil, err
	}
	ptr := func(v sql.NullFloat64) *float64 {
		if !v.Valid {
			return nil
		}
		return &v.Float64
	}
	s.P95DurationMS = ptr(p95)
	s.P99DurationMS = ptr(p99)
	s.P95QueueMS = ptr(queue)
	s.P95GenerationMS = ptr(generation)
	s.P95PostprocessMS = ptr(postprocess)
	s.OldestQueuedAgeMS = ptr(oldest)
	return s, nil
}
