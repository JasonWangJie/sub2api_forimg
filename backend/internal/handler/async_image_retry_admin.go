package handler

import (
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AsyncImageTaskCenterHandler) retryRepository(c *gin.Context) service.AsyncImageRetryRepository {
	provider, ok := h.tasks.(interface {
		Repository() service.AsyncImageTaskRepository
	})
	if ok {
		if repo, ok := provider.Repository().(service.AsyncImageRetryRepository); ok {
			return repo
		}
	}
	response.Error(c, http.StatusServiceUnavailable, "async image retry policy is unavailable")
	return nil
}

func (h *AsyncImageTaskCenterHandler) GetRetryPolicy(c *gin.Context) {
	repo := h.retryRepository(c)
	if repo == nil {
		return
	}
	policy, err := repo.GetAsyncImageRetryPolicy(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, policy)
}

func (h *AsyncImageTaskCenterHandler) SaveRetryPolicy(c *gin.Context) {
	repo := h.retryRepository(c)
	if repo == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.AsyncImageRetryMaxPolicyBytes)
	var req struct {
		service.AsyncImageRetryPolicy
		ExpectedRevision *int64 `json:"revision" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ExpectedRevision == nil {
		response.BadRequest(c, "invalid retry policy")
		return
	}
	policy := req.AsyncImageRetryPolicy
	policy.Revision = *req.ExpectedRevision
	if err := service.ValidateAsyncImageRetryPolicy(&policy); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	h.writeRetryPolicy(c, repo, policy.Revision, policy, nil)
}

func (h *AsyncImageTaskCenterHandler) RollbackRetryPolicy(c *gin.Context) {
	repo := h.retryRepository(c)
	if repo == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req struct {
		Revision       *int64 `json:"revision" binding:"required"`
		TargetRevision *int64 `json:"target_revision" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || *req.Revision < 0 || *req.TargetRevision < 0 {
		response.BadRequest(c, "invalid retry policy revisions")
		return
	}
	version, err := repo.GetAsyncImageRetryVersion(c.Request.Context(), *req.TargetRevision)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.writeRetryPolicy(c, repo, *req.Revision, version.Policy, req.TargetRevision)
}

func (h *AsyncImageTaskCenterHandler) writeRetryPolicy(c *gin.Context, repo service.AsyncImageRetryRepository, revision int64, policy service.AsyncImageRetryPolicy, rollbackFrom *int64) {
	ctx := c.Request.Context()
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Error(c, http.StatusUnauthorized, "administrator identity unavailable")
		return
	}
	version, err := repo.SaveAsyncImageRetryPolicy(ctx, revision, policy, subject.UserID, rollbackFrom)
	if err != nil {
		if errors.Is(err, service.ErrAsyncImageRetryRevisionConflict) {
			if current, lookupErr := repo.GetAsyncImageRetryPolicy(ctx); lookupErr == nil {
				err = infraerrors.Conflict("ASYNC_IMAGE_RETRY_REVISION_CONFLICT", "the retry policy revision has changed").
					WithMetadata(map[string]string{"current_revision": strconv.FormatInt(current.Revision, 10)})
			}
		}
		response.ErrorFrom(c, err)
		return
	}
	middleware.SetAuditAction(c, "async_image_retry_policy_"+version.Operation)
	audit := map[string]any{"revision": version.Revision}
	if rollbackFrom != nil {
		audit["target_revision"] = *rollbackFrom
	}
	middleware.SetAuditExtra(c, audit)
	response.Success(c, version.Policy)
}

func (h *AsyncImageTaskCenterHandler) ListRetryVersions(c *gin.Context) {
	repo := h.retryRepository(c)
	if repo == nil {
		return
	}
	page, size := asyncImageRetryPagination(c)
	items, total, err := repo.ListAsyncImageRetryVersions(c.Request.Context(), size, (page-1)*size)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, size)
}

func (h *AsyncImageTaskCenterHandler) GetRetryVersion(c *gin.Context) {
	repo := h.retryRepository(c)
	if repo == nil {
		return
	}
	revision, err := strconv.ParseInt(c.Param("revision"), 10, 64)
	if err != nil || revision < 0 {
		response.BadRequest(c, "invalid revision")
		return
	}
	version, err := repo.GetAsyncImageRetryVersion(c.Request.Context(), revision)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, version)
}

func (h *AsyncImageTaskCenterHandler) ListRetrySamples(c *gin.Context) {
	repo := h.retryRepository(c)
	if repo == nil {
		return
	}
	filter, page, ok := readAsyncImageRetryFilter(c)
	if !ok {
		return
	}
	items, total, err := repo.ListAsyncImageRetrySamples(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, filter.Limit)
}

func (h *AsyncImageTaskCenterHandler) ListRetrySampleEvents(c *gin.Context) {
	repo := h.retryRepository(c)
	if repo == nil {
		return
	}
	fingerprint := c.Param("fingerprint")
	if _, err := hex.DecodeString(fingerprint); err != nil || len(fingerprint) != 64 {
		response.BadRequest(c, "invalid fingerprint")
		return
	}
	filter, page, ok := readAsyncImageRetryFilter(c)
	if !ok {
		return
	}
	items, total, err := repo.ListAsyncImageRetryEvents(c.Request.Context(), filter, fingerprint)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, filter.Limit)
}

func (h *AsyncImageTaskCenterHandler) GetRetryEvent(c *gin.Context) {
	repo := h.retryRepository(c)
	if repo == nil {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		response.BadRequest(c, "invalid retry event ID")
		return
	}
	event, err := repo.GetAsyncImageRetryEvent(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, event)
}

func asyncImageRetryPagination(c *gin.Context) (int, int) {
	page, size := response.ParsePagination(c)
	size = min(size, 100)
	return min(page, math.MaxInt/size), size
}

func readAsyncImageRetryFilter(c *gin.Context) (service.AsyncImageRetryFilter, int, bool) {
	page, size := asyncImageRetryPagination(c)
	filter := service.AsyncImageRetryFilter{
		Platform: c.Query("platform"), Model: c.Query("model"), Stage: c.Query("stage"),
		Category: c.Query("category"), ErrorCode: c.Query("error_code"), Source: c.Query("source"),
		Limit: size, Offset: (page - 1) * size,
	}
	for key, field := range map[string]**time.Time{"from": &filter.From, "to": &filter.To} {
		if value := c.Query(key); value != "" {
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				response.BadRequest(c, key+" must be an RFC3339 timestamp")
				return filter, page, false
			}
			*field = &parsed
		}
	}
	for key, field := range map[string]*int64{"group_id": &filter.GroupID, "account_id": &filter.AccountID} {
		if value := c.Query(key); value != "" {
			id, err := strconv.ParseInt(value, 10, 64)
			if err != nil || id <= 0 {
				response.BadRequest(c, "invalid "+key)
				return filter, page, false
			}
			*field = id
		}
	}
	if value := c.Query("http_status"); value != "" {
		status, err := strconv.Atoi(value)
		if err != nil || status < 100 || status > 599 {
			response.BadRequest(c, "invalid HTTP status")
			return filter, page, false
		}
		filter.HTTPStatus = status
	}
	if filter.From != nil && filter.To != nil && !filter.From.Before(*filter.To) {
		response.BadRequest(c, "invalid time range")
		return filter, page, false
	}
	return filter, page, true
}

type asyncImageRetryPreview struct {
	Decision            service.AsyncImageRetryDecision  `json:"decision"`
	Historical          *service.AsyncImageRetryDecision `json:"historical,omitempty"`
	CandidateAccountIDs []int64                          `json:"candidate_account_ids,omitempty"`
	Eligibility         string                           `json:"eligibility"`
	EligibilityReason   string                           `json:"eligibility_reason,omitempty"`
}

func (h *AsyncImageTaskCenterHandler) PreviewRetryPolicy(c *gin.Context, worker *DurableAsyncImageHandler) {
	middleware.SkipAudit(c)
	repo := h.retryRepository(c)
	if repo == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.AsyncImageRetryMaxPolicyBytes+(8<<10))
	var req struct {
		Draft   *service.AsyncImageRetryPolicy `json:"draft"`
		Fact    *service.AsyncImageRetryFact   `json:"fact"`
		EventID int64                          `json:"event_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.EventID < 0 || (req.Fact == nil) == (req.EventID == 0) {
		response.BadRequest(c, "provide exactly one diagnostic fact or event ID")
		return
	}
	ctx := c.Request.Context()
	if req.Draft == nil {
		policy, err := repo.GetAsyncImageRetryPolicy(ctx)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		req.Draft = &policy
	}
	if err := service.ValidateAsyncImageRetryPolicy(req.Draft); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Fact != nil {
		fact := service.SanitizeAsyncImageRetryFact(*req.Fact)
		response.Success(c, asyncImageRetryPreview{
			Decision:    service.EvaluateAsyncImageRetry(*req.Draft, fact, service.AsyncImageRetryUsage{}),
			Eligibility: "unverified", EligibilityReason: "text_preview",
		})
		return
	}
	event, err := repo.GetAsyncImageRetryEvent(ctx, req.EventID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	state, err := repo.GetAsyncImageRetryState(ctx, event.Fact.TaskID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	decision := service.ProtectAsyncImageRetryDecision(event.Fact, service.EvaluateAsyncImageRetry(*req.Draft, event.Fact, state.Usage))
	result := asyncImageRetryPreview{Decision: decision, Historical: &event.Decision, Eligibility: "unverified"}
	result.Eligibility, result.EligibilityReason, result.CandidateAccountIDs = h.previewRetryEligibility(c, worker, event.Fact, decision, req.Draft.PauseGenerationRetries)
	response.Success(c, result)
}

func (h *AsyncImageTaskCenterHandler) previewRetryEligibility(c *gin.Context, worker *DurableAsyncImageHandler, fact service.AsyncImageRetryFact, decision service.AsyncImageRetryDecision, paused bool) (string, string, []int64) {
	if decision.BlockReason != "" && decision.Action == "manual_review" {
		return "blocked", decision.BlockReason, nil
	}
	if !service.IsAsyncImageGenerationRetry(decision.Action) {
		if strings.HasPrefix(decision.Action, "resume_") {
			details, err := h.tasks.GetForAdmin(c.Request.Context(), fact.TaskID)
			if err != nil || details == nil || details.Task == nil {
				return "unverified", "task_context_unavailable", nil
			}
			return "candidate_snapshot", "post_processing_stage_snapshot", nil
		}
		return "unverified", "no_generation_selection", nil
	}
	if paused {
		return "blocked", "generation_paused", nil
	}
	catalog, ok := h.catalog.(*asyncImageTaskCenterServiceCatalog)
	if !ok || catalog.accounts == nil || worker == nil {
		return "unverified", "task_context_unavailable", nil
	}
	ctx := c.Request.Context()
	details, err := h.tasks.GetForAdmin(ctx, fact.TaskID)
	if err != nil || details == nil || details.Task == nil {
		return "unverified", "task_context_unavailable", nil
	}
	apiKey, _, err := worker.reloadAsyncImageIdentity(ctx, details.Task, true)
	if err != nil {
		return "blocked", "task_eligibility_changed", nil
	}
	candidates, err := catalog.accounts.AsyncImageRetryCandidateSnapshot(ctx, details.Task, apiKey.Group, decision, fact)
	if err != nil {
		return "unverified", "account_pool_unavailable", nil
	}
	permanentlyExcluded := asyncImageFixedCapabilityAccountIDs(details.Task)
	validCandidates := candidates[:0]
	for _, id := range candidates {
		if _, excluded := permanentlyExcluded[id]; !excluded {
			validCandidates = append(validCandidates, id)
		}
	}
	if len(validCandidates) == 0 {
		return "blocked", "no_eligible_target", nil
	}
	reason := "live_health_and_capacity_unverified"
	if details.Task.Status == service.AsyncImageTaskStatusSucceeded ||
		details.Task.Status == service.AsyncImageTaskStatusFailed || strings.EqualFold(details.Task.Status, service.AsyncImageTaskStatusExecutionUnknown) {
		reason = "historical_task"
	}
	return "candidate_snapshot", reason, validCandidates
}
