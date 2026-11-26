package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

const asyncImageRetryPolicyWait = 5 * time.Second

type asyncImageWorkerRetryStore interface {
	GetAsyncImageRetryPolicy(context.Context) (service.AsyncImageRetryPolicy, error)
	GetAsyncImageRetryState(context.Context, string) (service.AsyncImageRetryState, error)
	SaveAsyncImageRetryDecision(context.Context, service.AsyncImageRetrySaveParams) (service.AsyncImageRetryEvent, error)
	StartAsyncImageRetryAction(context.Context, service.AsyncImageRetryStartParams) error
}

type asyncImageWorkerRetryKey struct{}

type asyncImageWorkerRetry struct {
	handler  *DurableAsyncImageHandler
	store    asyncImageWorkerRetryStore
	task     *service.AsyncImageTask
	config   service.AsyncImageRuntimeConfig
	pending  *service.AsyncImageRetryPending
	fact     service.AsyncImageRetryFact
	decision service.AsyncImageRetryDecision
	blocked  string
	sends    int
}

func (h *DurableAsyncImageHandler) asyncImageRetryStore() asyncImageWorkerRetryStore {
	if h.tasks == nil {
		return nil
	}
	store, _ := h.tasks.Repository().(asyncImageWorkerRetryStore)
	return store
}

func asyncImageWorkerRetryFromContext(ctx context.Context) *asyncImageWorkerRetry {
	retry, _ := ctx.Value(asyncImageWorkerRetryKey{}).(*asyncImageWorkerRetry)
	return retry
}

func asyncImageRetryFactForTask(task *service.AsyncImageTask, fact service.AsyncImageRetryFact) service.AsyncImageRetryFact {
	fact.TaskID, fact.Platform, fact.Model, fact.GroupID = task.TaskID, task.Platform, task.Model, task.GroupID
	fact.HasReferences = task.RequestType == service.AsyncImageRequestTypeImageToImage
	if fact.AccountID == 0 && task.AccountID != nil {
		fact.AccountID = *task.AccountID
	}
	if fact.OccurredAt.IsZero() {
		fact.OccurredAt = time.Now().UTC()
	}
	if task.PromptPreview != nil {
		preview := strings.TrimSpace(*task.PromptPreview)
		if utf8.RuneCountInString(preview) >= 12 {
			if prefix, _, echoed := strings.Cut(strings.Join(strings.Fields(fact.Message), " "), preview); echoed {
				// The stored preview can be shorter than the echoed prompt.
				fact.Message = prefix + "[redacted input]"
			}
		}
	}
	fact = service.SanitizeAsyncImageRetryFact(fact)
	if fact.FailureID == "" {
		fact.FailureID = fmt.Sprintf("%s:%d:%s:%s", task.TaskID, task.Version, fact.Stage, fact.AttemptID)
	}
	return fact
}

func (r *asyncImageWorkerRetry) onFailure(ctx context.Context, fact service.AsyncImageRetryFact) bool {
	if fact.AttemptID == "" {
		if capture := service.AsyncImageAccountAttemptCaptureFromContext(ctx); capture != nil {
			attempts := capture.Attempts()
			for i := len(attempts) - 1; i >= 0; i-- {
				if attempts[i].AccountID == fact.AccountID && attempts[i].AttemptID != "" {
					fact.AttemptID = attempts[i].AttemptID
					break
				}
			}
		}
	}
	r.fact = asyncImageRetryFactForTask(r.task, fact)
	decision, _, err := evaluateAsyncImageWorkerRetry(ctx, r.store, r.fact)
	if err != nil {
		r.blocked = "policy_unavailable"
		return true
	}
	r.decision = decision
	if decision.RuleID == "" && (r.fact.Unknown || r.fact.HasOutput) {
		r.blocked = "execution_unknown"
		if r.fact.HasOutput {
			r.blocked = "protect_existing_output"
		}
	}
	return decision.RuleID != "" || r.blocked != ""
}

func evaluateAsyncImageWorkerRetry(ctx context.Context, store asyncImageWorkerRetryStore, fact service.AsyncImageRetryFact) (service.AsyncImageRetryDecision, service.AsyncImageRetryPolicy, error) {
	policy, err := store.GetAsyncImageRetryPolicy(ctx)
	if err != nil {
		return service.AsyncImageRetryDecision{}, policy, err
	}
	state, err := store.GetAsyncImageRetryState(ctx, fact.TaskID)
	if err != nil {
		return service.AsyncImageRetryDecision{}, policy, err
	}
	decision := service.EvaluateAsyncImageRetry(policy, fact, state.Usage)
	return service.ProtectAsyncImageRetryDecision(fact, decision), policy, nil
}

func (r *asyncImageWorkerRetry) beforeDispatch(ctx context.Context, accountID int64) error {
	firstGeneration := r.isFirstGeneration()
	// First dispatch does not depend on policy service availability. Every
	// subsequent send is authorized against the latest policy revision.
	if firstGeneration && r.pending == nil {
		if r.start(ctx, service.AsyncImageRetryEvent{}, accountID, true) != nil {
			r.blocked = "dispatch_state_changed"
			return service.ErrAsyncImageRetryDispatchBlocked
		}
		r.sends++
		return nil
	}
	policy, err := r.store.GetAsyncImageRetryPolicy(ctx)
	if err != nil {
		r.blocked = "policy_unavailable"
		return service.ErrAsyncImageRetryDispatchBlocked
	}
	if policy.PauseGenerationRetries && !firstGeneration {
		r.blocked = "generation_retries_paused"
		return service.ErrAsyncImageRetryDispatchBlocked
	}
	if r.sends == 0 && r.pending != nil {
		event := r.pending.Event
		if event.Decision.Revision != policy.Revision || (event.Decision.Source == "custom" && !service.AsyncImageRetryAccountAllowed(event.Decision, event.Fact, accountID)) {
			r.blocked = "dispatch_eligibility_changed"
			return service.ErrAsyncImageRetryDispatchBlocked
		}
		if err := r.start(ctx, event, accountID, true); err != nil {
			r.blocked = asyncImageRetryDispatchBlockReason(err)
			return service.ErrAsyncImageRetryDispatchBlocked
		}
	} else if r.fact.FailureID != "" {
		// Record gateway-native follow-up sends only at their physical dispatch
		// point, after the real scheduler has admitted the candidate.
		decision, latest, err := evaluateAsyncImageWorkerRetry(ctx, r.store, r.fact)
		if err != nil || latest.Revision != policy.Revision || decision.RuleID != "" {
			r.decision = decision
			r.blocked = "dispatch_policy_changed"
			return service.ErrAsyncImageRetryDispatchBlocked
		}
		action := "other_account"
		if accountID == r.fact.AccountID {
			action = "same_account"
		}
		event, err := r.store.SaveAsyncImageRetryDecision(ctx, service.AsyncImageRetrySaveParams{
			ExpectedTaskVersion: r.task.Version,
			Event: service.AsyncImageRetryEvent{Fact: r.fact, Decision: service.AsyncImageRetryDecision{
				Source: "builtin", Revision: policy.Revision, RequestedAction: action, Action: action,
			}},
		})
		if err == nil {
			err = r.start(ctx, event, accountID, true)
		}
		if err != nil {
			r.blocked = asyncImageRetryDispatchBlockReason(err)
			return service.ErrAsyncImageRetryDispatchBlocked
		}
	} else if err := r.start(ctx, service.AsyncImageRetryEvent{Decision: service.AsyncImageRetryDecision{Revision: policy.Revision}}, accountID, true); err != nil {
		// An already queued retry from before the rule-center migration has no
		// recoverable failure fact, but must still honor the atomic pause fence.
		r.blocked = "dispatch_state_changed"
		return service.ErrAsyncImageRetryDispatchBlocked
	}
	r.sends++
	return nil
}

func asyncImageRetryDispatchBlockReason(err error) string {
	switch {
	case errors.Is(err, service.ErrAsyncImageRetryRevisionConflict):
		return "dispatch_policy_changed"
	case errors.Is(err, service.ErrAsyncImageRetryGenerationPaused):
		return "generation_retries_paused"
	default:
		return "dispatch_state_changed"
	}
}

func (r *asyncImageWorkerRetry) start(ctx context.Context, event service.AsyncImageRetryEvent, accountID int64, generation bool) error {
	return r.store.StartAsyncImageRetryAction(ctx, service.AsyncImageRetryStartParams{
		TaskID: r.task.TaskID, ExpectedTaskVersion: r.task.Version,
		EventID: event.ID, Revision: event.Decision.Revision, AccountID: accountID, Generation: generation,
		FirstGeneration: generation && r.isFirstGeneration(),
	})
}

func (r *asyncImageWorkerRetry) isFirstGeneration() bool {
	return r.sends == 0 && (r.pending == nil || !r.pending.Event.Fact.Dispatched) && !asyncImageTaskHasGenerationDispatch(r.task)
}

func asyncImageTaskHasGenerationDispatch(task *service.AsyncImageTask) bool {
	if task.UpstreamRetryCount > 0 || (task.UpstreamRequestID != nil && *task.UpstreamRequestID != "") {
		return true
	}
	var attempts []service.AsyncImageAccountAttempt
	_ = json.Unmarshal(task.AccountAttempts, &attempts)
	for _, attempt := range attempts {
		if attempt.Status == service.AsyncImageAccountAttemptInvoked || attempt.Status == service.AsyncImageAccountAttemptSucceeded ||
			attempt.UpstreamRequestID != "" || (attempt.Status == service.AsyncImageAccountAttemptFailed && attempt.StatusCode > 0) {
			return true
		}
	}
	return false
}

func (r *asyncImageWorkerRetry) context(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, asyncImageWorkerRetryKey{}, r)
	if r.pending != nil && r.pending.Event.Decision.Source == "custom" && service.IsAsyncImageGenerationRetry(r.pending.Event.Decision.Action) {
		ctx = service.WithAsyncImageRetryAccountFilter(ctx, r.pending.Event.Decision, r.pending.Event.Fact)
	}
	return service.WithAsyncImageRetryHooks(ctx, service.AsyncImageRetryHooks{OnFailure: r.onFailure, BeforeDispatch: r.beforeDispatch})
}

func (h *DurableAsyncImageHandler) prepareAsyncImagePendingRetry(ctx context.Context, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, store asyncImageWorkerRetryStore) (*service.AsyncImageRetryPending, asyncImageWorkerDisposition, bool) {
	state, err := store.GetAsyncImageRetryState(ctx, task.TaskID)
	if err != nil {
		if task.Status == service.AsyncImageTaskStatusQueued && task.RetryCount > 0 {
			return nil, asyncImageWorkerDisposition{requeue: true, delay: asyncImageRetryPolicyWait}, true
		}
		return nil, asyncImageWorkerDisposition{}, false
	}
	pending := state.Pending
	if pending == nil {
		return nil, asyncImageWorkerDisposition{}, false
	}
	if pending.Status == service.AsyncImageRetryStateManual {
		return nil, asyncImageWorkerDisposition{}, true
	}
	queued := task.Status == service.AsyncImageTaskStatusQueued
	if queued == pending.Event.Fact.HasOutput {
		// A started generation action can remain attached while its result is
		// being downloaded, stored or billed. It cannot authorize later work.
		return nil, asyncImageWorkerDisposition{}, false
	}
	if queued && (!time.Now().Before(asyncImageTaskDeadline(task)) || asyncImageRemainingUpstreamAttempts(task) <= 0) {
		disposition := h.blockAsyncImagePendingRetry(ctx, store, task, cfg, pending, "generation_budget_exhausted")
		return nil, disposition, true
	}
	if due := time.Until(pending.DueAt); due > 0 {
		return nil, asyncImageWorkerDisposition{requeue: true, delay: due}, true
	}
	policy, err := store.GetAsyncImageRetryPolicy(ctx)
	if err != nil {
		if queued {
			return nil, asyncImageWorkerDisposition{requeue: true, delay: asyncImageRetryPolicyWait}, true
		}
		return nil, asyncImageWorkerDisposition{}, false
	}
	if pending.Status == service.AsyncImageRetryStateStarted && !queued {
		// The worker may have stopped after claiming a safe recovery action.
		// Existing result, storage and billing writes are idempotent on replay.
		return pending, asyncImageWorkerDisposition{}, false
	}
	if pending.Event.Decision.Revision != policy.Revision {
		fact := pending.Event.Fact
		decision := service.ProtectAsyncImageRetryDecision(fact, service.EvaluateAsyncImageRetry(policy, fact, state.Usage))
		if decision.RuleID == "" {
			if pending.Event.Decision.Source == "builtin" {
				decision = pending.Event.Decision
				decision.Revision = policy.Revision
			} else if builtin, delay := asyncImageRetryBuiltinForFact(task, cfg, fact); builtin != "" {
				decision = service.AsyncImageRetryDecision{Source: "builtin", Revision: policy.Revision, RequestedAction: builtin, Action: builtin, DelaySeconds: delay}
			} else {
				decision = pending.Event.Decision
				decision.Revision, decision.Action, decision.BlockReason = policy.Revision, "manual_review", "policy_changed"
			}
		}
		ctx = context.WithValue(ctx, asyncImageWorkerRetryKey{}, &asyncImageWorkerRetry{pending: pending})
		disposition := h.persistAsyncImageRetryDecision(ctx, store, task, cfg, fact, decision, nil, fact.SafeToRetry)
		return nil, disposition, true
	}
	if queued && policy.PauseGenerationRetries && (pending.Event.Fact.Dispatched || asyncImageTaskHasGenerationDispatch(task)) {
		return nil, asyncImageWorkerDisposition{requeue: true, delay: min(asyncImageRetryPolicyWait, max(0, time.Until(asyncImageTaskDeadline(task))))}, true
	}
	if !queued && pending.Status == service.AsyncImageRetryStatePending {
		r := &asyncImageWorkerRetry{task: task, store: store}
		if err := r.start(ctx, pending.Event, 0, false); err != nil {
			return nil, asyncImageWorkerDisposition{requeue: true, delay: asyncImageRetryPolicyWait}, true
		}
	}
	return pending, asyncImageWorkerDisposition{}, false
}

func asyncImageRetryBuiltinForFact(task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, fact service.AsyncImageRetryFact) (string, int) {
	if !fact.SafeToRetry || fact.Unknown {
		return "", 0
	}
	if fact.HasOutput {
		switch fact.Stage {
		case "download", "storage", "billing":
			return "resume_" + fact.Stage, cfg.RetryBackoffSeconds
		}
		return "", 0
	}
	if fact.Category == "reference_fetch" && canRetryAsyncImage(task, task.ReferenceRetryCount, cfg.ReferenceFetchMaxRetries, cfg.TotalMaxRetries) {
		return "retry_prepare", cfg.ReferenceFetchRetryBaseSeconds
	}
	if (fact.Category == "local_capacity" || fact.Category == "capacity") && canRetryAsyncImage(task, task.CapacityRetryCount, cfg.CapacityMaxRetries, cfg.TotalMaxRetries) {
		return "retry_prepare", cfg.CapacityRetryBaseSeconds
	}
	if fact.Stage == "submit" && shouldRetryAsyncImageUpstreamTransient(task, cfg, fact.HTTPStatus, fact.Message) {
		return "retry_prepare", cfg.UpstreamTransientRetryBaseSeconds
	}
	return "", 0
}

func (h *DurableAsyncImageHandler) blockAsyncImagePendingRetry(ctx context.Context, store asyncImageWorkerRetryStore, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, pending *service.AsyncImageRetryPending, reason string) asyncImageWorkerDisposition {
	decision := pending.Event.Decision
	decision.Action, decision.BlockReason = "manual_review", reason
	if policy, err := store.GetAsyncImageRetryPolicy(ctx); err == nil {
		decision.Revision = policy.Revision
	}
	ctx = context.WithValue(ctx, asyncImageWorkerRetryKey{}, &asyncImageWorkerRetry{pending: pending})
	return h.persistAsyncImageRetryDecision(ctx, store, task, cfg, pending.Event.Fact, decision, nil, false)
}

func (r *asyncImageWorkerRetry) finish(ctx context.Context) (asyncImageWorkerDisposition, bool) {
	if r.blocked != "" && r.sends == 0 && r.fact.FailureID == "" {
		// Policy changes between queue pickup and the dispatch barrier are
		// resolved from the persisted fact on the next reservation.
		progress := 0
		tr := service.AsyncImageTaskTransition{TaskID: r.task.TaskID, ExpectedVersion: r.task.Version,
			FromStatuses: []string{r.task.Status}, ToStatus: service.AsyncImageTaskStatusQueued,
			Progress: &progress, ClearAccountID: true, EventType: "retry_dispatch_deferred"}
		terminalCtx, cancel := asyncImageTerminalTransitionContext(ctx)
		defer cancel()
		if _, err := r.handler.tasks.Transition(terminalCtx, enrichAsyncImageAttemptTransition(terminalCtx, r.task, tr)); err != nil {
			return asyncImageWorkerDisposition{}, true
		}
		return asyncImageWorkerDisposition{requeue: true, delay: asyncImageRetryPolicyWait}, true
	}
	if r.fact.FailureID == "" {
		return asyncImageWorkerDisposition{}, false
	}
	if r.decision.RuleID != "" || r.blocked != "" {
		if r.decision.RuleID == "" {
			if r.blocked == "generation_retries_paused" && r.fact.SafeToRetry && !r.fact.Unknown && !r.fact.HasOutput {
				r.decision.Source, r.decision.RequestedAction, r.decision.Action = "builtin", "retry_prepare", "retry_prepare"
				r.decision.DelaySeconds = int(asyncImageRetryPolicyWait / time.Second)
			} else if r.blocked == "policy_unavailable" && r.fact.SafeToRetry && !r.fact.Unknown && !r.fact.HasOutput {
				r.decision = asyncImageUnavailablePolicyDecision()
			} else if r.blocked == "dispatch_policy_changed" {
				r.decision = r.handler.refreshAsyncImageRetryDecision(ctx, r.store, r.task, r.config, r.fact, r.decision)
			} else {
				r.decision.Action, r.decision.BlockReason = "manual_review", r.blocked
			}
		}
		disposition := r.handler.persistAsyncImageRetryDecision(ctx, r.store, r.task, r.config, r.fact, r.decision, nil, false)
		return disposition, true
	}
	return asyncImageWorkerDisposition{}, false
}

func asyncImageUnavailablePolicyDecision() service.AsyncImageRetryDecision {
	return service.AsyncImageRetryDecision{Source: "none", Action: "retry_prepare", BlockReason: "policy_unavailable", DelaySeconds: int(asyncImageRetryPolicyWait / time.Second)}
}

func asyncImageBuiltinRetryFact(ctx context.Context, fallback service.AsyncImageRetryFact) service.AsyncImageRetryFact {
	if retry := asyncImageWorkerRetryFromContext(ctx); retry != nil && retry.fact.FailureID != "" && fallback.Stage == "submit" {
		return retry.fact
	}
	return fallback
}

func (h *DurableAsyncImageHandler) recordAsyncImageUnmatchedFailure(ctx context.Context, task *service.AsyncImageTask, fact service.AsyncImageRetryFact) {
	store := h.asyncImageRetryStore()
	if store == nil {
		return
	}
	fact = asyncImageRetryFactForTask(task, fact)
	policy, _ := store.GetAsyncImageRetryPolicy(ctx)
	_, _ = store.SaveAsyncImageRetryDecision(ctx, service.AsyncImageRetrySaveParams{ExpectedTaskVersion: task.Version,
		Event: service.AsyncImageRetryEvent{Fact: fact, Decision: service.AsyncImageRetryDecision{Source: "none", Revision: policy.Revision}}})
}

func (h *DurableAsyncImageHandler) tryAsyncImageRetryPolicy(ctx context.Context, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, fact service.AsyncImageRetryFact, transition *service.AsyncImageTaskTransition, builtin string, builtinDelay time.Duration, canResume bool) (asyncImageWorkerDisposition, bool) {
	store := h.asyncImageRetryStore()
	if store == nil {
		return asyncImageWorkerDisposition{}, false
	}
	fact = asyncImageRetryFactForTask(task, fact)
	decision, policy, err := evaluateAsyncImageWorkerRetry(ctx, store, fact)
	if err != nil {
		if fact.HasOutput {
			return asyncImageWorkerDisposition{}, false
		}
		decision = asyncImageUnavailablePolicyDecision()
		if !fact.SafeToRetry || fact.Unknown {
			decision.Action = "manual_review"
		}
		return h.persistAsyncImageRetryDecision(ctx, store, task, cfg, fact, decision, transition, false), true
	}
	if decision.RuleID == "" {
		if builtin == "" {
			_, _ = store.SaveAsyncImageRetryDecision(ctx, service.AsyncImageRetrySaveParams{ExpectedTaskVersion: task.Version, Event: service.AsyncImageRetryEvent{Fact: fact, Decision: decision}})
			return asyncImageWorkerDisposition{}, false
		}
		decision.Source, decision.RequestedAction, decision.Action = "builtin", builtin, builtin
		decision.Revision, decision.DelaySeconds = policy.Revision, int((builtinDelay+time.Second-1)/time.Second)
	} else if !fact.HasOutput {
		// Account and reference transport choices come from the custom action.
		// A predefined fallback transition must not carry its own choices over.
		transition = nil
	}
	return h.persistAsyncImageRetryDecision(ctx, store, task, cfg, fact, decision, transition, canResume), true
}

func (h *DurableAsyncImageHandler) persistAsyncImageRetryDecision(ctx context.Context, store asyncImageWorkerRetryStore, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, fact service.AsyncImageRetryFact, decision service.AsyncImageRetryDecision, transition *service.AsyncImageTaskTransition, canResume bool) asyncImageWorkerDisposition {
	return h.saveAsyncImageRetryDecision(ctx, store, task, cfg, fact, decision, transition, canResume, true)
}

func (h *DurableAsyncImageHandler) refreshAsyncImageRetryDecision(ctx context.Context, store asyncImageWorkerRetryStore, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, fact service.AsyncImageRetryFact, previous service.AsyncImageRetryDecision) service.AsyncImageRetryDecision {
	decision, policy, err := evaluateAsyncImageWorkerRetry(ctx, store, fact)
	if err != nil {
		if fact.SafeToRetry && !fact.Unknown && !fact.HasOutput {
			return asyncImageUnavailablePolicyDecision()
		}
		return service.AsyncImageRetryDecision{Source: "none", Action: "manual_review", BlockReason: "policy_unavailable"}
	}
	if decision.RuleID != "" {
		return decision
	}
	if previous.Source == "builtin" {
		previous.Revision = policy.Revision
		return previous
	}
	if builtin, delay := asyncImageRetryBuiltinForFact(task, cfg, fact); builtin != "" {
		return service.AsyncImageRetryDecision{Source: "builtin", Revision: policy.Revision, RequestedAction: builtin, Action: builtin, DelaySeconds: delay}
	}
	previous.Revision, previous.Action, previous.BlockReason = policy.Revision, "manual_review", "policy_changed"
	return previous
}

func (h *DurableAsyncImageHandler) saveAsyncImageRetryDecision(ctx context.Context, store asyncImageWorkerRetryStore, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig, fact service.AsyncImageRetryFact, decision service.AsyncImageRetryDecision, transition *service.AsyncImageTaskTransition, canResume, retryRevisionConflict bool) asyncImageWorkerDisposition {
	ctx, cancel := asyncImageTerminalTransitionContext(ctx)
	defer cancel()
	originalTransition := transition
	now := time.Now().UTC()
	due := now.Add(time.Duration(decision.DelaySeconds) * time.Second)
	state := "pending"
	if fact.Unknown {
		decision.Action, decision.BlockReason = "manual_review", "execution_unknown"
	}
	if fact.HasOutput && decision.Action == "stop" {
		decision.Action, decision.BlockReason = "manual_review", "output_protected"
	}
	if decision.Action == "local_same_account" || decision.Action == "local_other_account" {
		if !fact.HasReferences || (task.Platform != service.PlatformOpenAI && task.Platform != service.PlatformGemini) {
			decision.Action, decision.BlockReason = "manual_review", "local_references_unavailable"
		}
	}
	if !fact.HasOutput && decision.Action != "manual_review" && decision.Action != "stop" &&
		(task.RetryCount >= cfg.TotalMaxRetries || asyncImageRemainingUpstreamAttempts(task) <= 0 || !due.Before(asyncImageTaskDeadline(task))) {
		decision.Action, decision.BlockReason = "manual_review", "generation_budget_exhausted"
	}
	if fact.HasOutput && !canResume && decision.Action != "manual_review" {
		decision.Action, decision.BlockReason = "manual_review", "recovery_budget_exhausted"
	}
	if decision.Action == "manual_review" || decision.Action == "stop" {
		state = "clear"
		if decision.Action == "manual_review" {
			state = "manual"
		}
		if !fact.HasOutput || fact.Stage == "submit" {
			message := fact.Message
			if message == "" {
				message = "automatic image recovery stopped"
			}
			code := "upstream_failed"
			if fact.Stage == "prepare" {
				code = "invalid_reference_image"
			}
			tr := service.AsyncImageTaskTransition{TaskID: task.TaskID, ExpectedVersion: task.Version,
				FromStatuses: []string{task.Status}, ToStatus: service.AsyncImageTaskStatusFailed,
				ErrorCode: &code, ErrorMessage: &message, FinishedAt: &now, ClearRequestPayload: true,
				EventType: "task_failed", EventPayload: asyncImageFailureEvent(code, message, task.Status)}
			if fact.Unknown || fact.HasOutput {
				code = "execution_unknown"
				tr.ToStatus, tr.EventType = service.AsyncImageTaskStatusExecutionUnknown, "execution_unknown"
			}
			if tr.ToStatus == service.AsyncImageTaskStatusExecutionUnknown {
				pending := "pending"
				tr.ReconciliationStatus = &pending
			}
			transition = &tr
		}
	} else if transition == nil && !fact.HasOutput && task.Status == service.AsyncImageTaskStatusInvoking {
		progress := 0
		code := "generation_retry_scheduled"
		transition = &service.AsyncImageTaskTransition{TaskID: task.TaskID, ExpectedVersion: task.Version,
			FromStatuses: []string{task.Status}, ToStatus: service.AsyncImageTaskStatusQueued,
			Progress: &progress, ErrorCode: &code, ErrorMessage: &fact.Message,
			IncrementRetry: true, ClearAccountID: true, EventType: "retry_rule_scheduled"}
	}
	if transition != nil {
		tr := enrichAsyncImageAttemptTransition(ctx, task, *transition)
		transition = &tr
	}
	var pendingEventID int64
	if retry := asyncImageWorkerRetryFromContext(ctx); retry != nil && retry.pending != nil {
		pendingEventID = retry.pending.Event.ID
	}
	_, err := store.SaveAsyncImageRetryDecision(ctx, service.AsyncImageRetrySaveParams{
		ExpectedTaskVersion: task.Version, Event: service.AsyncImageRetryEvent{Fact: fact, Decision: decision},
		State: state, DueAt: due, Transition: transition, ExpectedPendingEventID: pendingEventID,
	})
	if err != nil {
		if retryRevisionConflict && errors.Is(err, service.ErrAsyncImageRetryRevisionConflict) {
			decision = h.refreshAsyncImageRetryDecision(ctx, store, task, cfg, fact, decision)
			if decision.Source == "custom" && !fact.HasOutput {
				originalTransition = nil
			}
			return h.saveAsyncImageRetryDecision(ctx, store, task, cfg, fact, decision, originalTransition, canResume, false)
		}
		logger.L().Warn("async_image.retry_decision_persist_failed", zap.String("task_id", task.TaskID), zap.Error(err))
		if errors.Is(err, service.ErrAsyncImageInvalidTransition) || errors.Is(err, service.ErrAsyncImageRetryActionChanged) {
			return asyncImageWorkerDisposition{}
		}
		if task.Status == service.AsyncImageTaskStatusInvoking && (!fact.HasOutput || fact.Stage == "submit") {
			if fact.Unknown || fact.HasOutput {
				h.markAsyncImageExecutionUnknown(ctx, task, fact.Message)
			} else {
				h.failAsyncImageTask(ctx, task, "upstream_failed", "automatic image recovery could not be scheduled", false)
			}
			return asyncImageWorkerDisposition{}
		}
		return asyncImageWorkerDisposition{requeue: true, delay: asyncImageRetryPolicyWait}
	}
	if state == "pending" {
		return asyncImageWorkerDisposition{requeue: true, delay: max(0, time.Until(due))}
	}
	return asyncImageWorkerDisposition{}
}
