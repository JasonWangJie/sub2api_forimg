//go:build unit

package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type asyncImageRetryWorkerTestRepo struct {
	*referenceTimeout454SmokeRepo
	policy    service.AsyncImageRetryPolicy
	policyErr error
	state     service.AsyncImageRetryState
	events    []service.AsyncImageRetryEvent
	starts    []service.AsyncImageRetryStartParams
}

func (r *asyncImageRetryWorkerTestRepo) AppendAsyncImageEvent(context.Context, service.AsyncImageEvent) error {
	return nil
}

func (r *asyncImageRetryWorkerTestRepo) GetAsyncImageRetryPolicy(context.Context) (service.AsyncImageRetryPolicy, error) {
	return r.policy, r.policyErr
}

func (r *asyncImageRetryWorkerTestRepo) GetAsyncImageRetryState(context.Context, string) (service.AsyncImageRetryState, error) {
	return r.state, nil
}

func (r *asyncImageRetryWorkerTestRepo) SaveAsyncImageRetryDecision(ctx context.Context, p service.AsyncImageRetrySaveParams) (service.AsyncImageRetryEvent, error) {
	if p.ExpectedTaskVersion != r.task.Version || (p.Event.Decision.Revision != r.policy.Revision && p.Event.Decision.Revision != 0) {
		return service.AsyncImageRetryEvent{}, service.ErrAsyncImageInvalidTransition
	}
	if p.Transition != nil {
		if _, err := r.TransitionAsyncImageTask(ctx, *p.Transition); err != nil {
			return service.AsyncImageRetryEvent{}, err
		}
	}
	event := p.Event
	event.ID = int64(len(r.events) + 1)
	r.events = append(r.events, event)
	switch p.State {
	case service.AsyncImageRetryStatePending, service.AsyncImageRetryStateManual:
		r.state.Pending = &service.AsyncImageRetryPending{Event: event, Status: p.State, DueAt: p.DueAt}
	case service.AsyncImageRetryStateClear:
		r.state.Pending = nil
	}
	return event, nil
}

func (r *asyncImageRetryWorkerTestRepo) StartAsyncImageRetryAction(_ context.Context, p service.AsyncImageRetryStartParams) error {
	if p.FirstGeneration && p.EventID == 0 && p.Generation && p.ExpectedTaskVersion == r.task.Version {
		r.starts = append(r.starts, p)
		return nil
	}
	if p.ExpectedTaskVersion != r.task.Version || p.Revision != r.policy.Revision || p.EventID < 1 || p.EventID > int64(len(r.events)) {
		return service.ErrAsyncImageRetryActionChanged
	}
	event := &r.events[p.EventID-1]
	now := time.Now().UTC()
	if event.StartedAt == nil {
		event.StartedAt = &now
	}
	if p.Generation {
		event.GenerationStartedAt, event.TargetAccountID = &now, &p.AccountID
	}
	if r.state.Pending != nil && r.state.Pending.Event.ID == p.EventID {
		r.state.Pending.Status, r.state.Pending.Event = service.AsyncImageRetryStateStarted, *event
	}
	r.starts = append(r.starts, p)
	return nil
}

func TestAsyncImageRetryWorkerPauseAllowsFirstDispatchAfterPreparation(t *testing.T) {
	task := &service.AsyncImageTask{TaskID: "asyncimg_first_after_prep", Version: 3, RetryCount: 1, ReferenceRetryCount: 1,
		Status: service.AsyncImageTaskStatusInvoking, SubmittedAt: time.Now().UTC(),
		AccountAttempts: []byte(`[{"account_id":17,"status":"selected"}]`)}
	base := &generatedWorkerResultRepo{task: task}
	repo := &asyncImageRetryWorkerTestRepo{referenceTimeout454SmokeRepo: &referenceTimeout454SmokeRepo{openAIReferenceURLTimeoutRepo: &openAIReferenceURLTimeoutRepo{&referenceLocalFallbackRepo{base}}},
		policy: service.AsyncImageRetryPolicy{Revision: 2, PauseGenerationRetries: true}}
	retry := &asyncImageWorkerRetry{store: repo, task: task}
	require.NoError(t, retry.beforeDispatch(context.Background(), 17))
	require.Len(t, repo.starts, 1)
	require.True(t, repo.starts[0].FirstGeneration)
	require.ErrorIs(t, retry.beforeDispatch(context.Background(), 18), service.ErrAsyncImageRetryDispatchBlocked)
	require.Equal(t, "generation_retries_paused", retry.blocked)
}

func TestAsyncImageRetryWorkerDefersKnownFailureDuringPolicyOutage(t *testing.T) {
	task := &service.AsyncImageTask{TaskID: "asyncimg_policy_unavailable", Version: 2,
		Status: service.AsyncImageTaskStatusInvoking, SubmittedAt: time.Now().UTC()}
	base := &generatedWorkerResultRepo{task: task}
	repo := &asyncImageRetryWorkerTestRepo{referenceTimeout454SmokeRepo: &referenceTimeout454SmokeRepo{openAIReferenceURLTimeoutRepo: &openAIReferenceURLTimeoutRepo{&referenceLocalFallbackRepo{base}}},
		policy: workerTestRetryPolicy("other_account"), policyErr: errors.New("policy temporarily unavailable")}
	h := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo)}
	retry := &asyncImageWorkerRetry{handler: h, store: repo, task: task, config: service.AsyncImageRuntimeConfig{TotalMaxRetries: 3}, sends: 1}
	ctx := context.WithValue(context.Background(), asyncImageWorkerRetryKey{}, retry)
	require.True(t, retry.onFailure(ctx, service.AsyncImageRetryFact{Stage: "submit", Category: "upstream_transient", HTTPStatus: 503,
		ErrorCode: "upstream_error", AccountID: 17, SafeToRetry: true, Dispatched: true}))
	disposition, handled := retry.finish(ctx)
	require.True(t, handled)
	require.True(t, disposition.requeue)
	require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
	require.NotNil(t, repo.state.Pending)
	require.Zero(t, repo.state.Pending.Event.Decision.Revision)
	require.Equal(t, "policy_unavailable", repo.state.Pending.Event.Decision.BlockReason)
	require.Equal(t, "upstream_error", repo.state.Pending.Event.Fact.ErrorCode)
	require.Empty(t, repo.starts)
}

func workerTestRetryPolicy(action string) service.AsyncImageRetryPolicy {
	return service.AsyncImageRetryPolicy{Revision: 1, Rules: []service.AsyncImageRetryRule{{
		ID: "upstream-timeout", Name: "Upstream timeout", Enabled: true,
		Match: service.AsyncImageRetryMatch{ErrorCodes: []string{"upstream_error"}},
		Steps: []service.AsyncImageRetryStep{{ID: "first", Action: action, MaxAttempts: 1, Backoff: "fixed"}},
	}}}
}

func TestAsyncImageRetryWorkerPendingDueSurvivesQueueDelivery(t *testing.T) {
	task := &service.AsyncImageTask{TaskID: "asyncimg_pending_policy", Version: 3, RetryCount: 1,
		Status: service.AsyncImageTaskStatusQueued, SubmittedAt: time.Now().UTC()}
	base := &generatedWorkerResultRepo{task: task}
	repo := &asyncImageRetryWorkerTestRepo{referenceTimeout454SmokeRepo: &referenceTimeout454SmokeRepo{openAIReferenceURLTimeoutRepo: &openAIReferenceURLTimeoutRepo{&referenceLocalFallbackRepo{base}}}, policy: workerTestRetryPolicy("other_account")}
	due := time.Now().Add(time.Minute)
	repo.state.Pending = &service.AsyncImageRetryPending{Status: service.AsyncImageRetryStatePending, DueAt: due}
	h := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo)}
	pending, disposition, handled := h.prepareAsyncImagePendingRetry(context.Background(), task, service.AsyncImageRuntimeConfig{}, repo)
	require.True(t, handled)
	require.Nil(t, pending)
	require.True(t, disposition.requeue)
	require.Greater(t, disposition.delay, 55*time.Second)
	require.LessOrEqual(t, disposition.delay, time.Minute)
}

func TestAsyncImageRetryWorkerTaskCenterManualHoldIsAdminOnly(t *testing.T) {
	task := &service.AsyncImageTask{TaskID: "asyncimg_manual_hold", Status: service.AsyncImageTaskStatusStorageFailed}
	repo := &asyncImageRetryWorkerTestRepo{state: service.AsyncImageRetryState{Pending: &service.AsyncImageRetryPending{
		Status: service.AsyncImageRetryStateManual,
		Event: service.AsyncImageRetryEvent{ID: 42, Fact: service.AsyncImageRetryFact{Stage: "storage"},
			Decision: service.AsyncImageRetryDecision{RuleID: "rule-42", Action: "manual_review", BlockReason: "protect_existing_output"}},
	}}}
	h := &AsyncImageTaskCenterHandler{tasks: service.NewAsyncImageTaskService(repo)}
	details := &service.AsyncImageTaskDetails{Task: task}
	admin, err := h.detailView(context.Background(), details, true)
	require.NoError(t, err)
	require.EqualValues(t, 42, admin.Task.RetryPolicyState.EventID)
	require.Equal(t, "rule-42", admin.Task.RetryPolicyState.RuleID)
	require.Nil(t, admin.Task.RetryPolicyState.DueAt)
	user, err := h.detailView(context.Background(), details, false)
	require.NoError(t, err)
	require.Nil(t, user.Task.RetryPolicyState)
}

func TestAsyncImageRetryWorkerStripsUnlabelledPromptEcho(t *testing.T) {
	preview := "这是一段用于本地测试的图片描述，用来验证文本脱敏"
	fact := asyncImageRetryFactForTask(&service.AsyncImageTask{PromptPreview: &preview}, service.AsyncImageRetryFact{
		Stage: "submit", Message: "request rejected because " + preview + "未在任务预览中展示的其余文本",
	})
	require.Equal(t, "request rejected because [redacted input]", fact.Message)
}
