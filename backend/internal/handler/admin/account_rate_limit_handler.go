package admin

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) GetRateLimitState(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	preview, err := h.rateLimitService.GetAccountRateLimitPreview(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, preview)
}

// ClearRateLimit requires the current preview, including for legacy clients.
// Missing previews never silently bypass the compare-and-swap protection.
func (h *AccountHandler) ClearRateLimit(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var req struct {
		ExpectedVersion string `json:"expected_version" binding:"required"`
	}
	if err = c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "A current expected_version is required")
		return
	}
	middleware.SetAuditAction(c, "admin.accounts.clear_rate_limit")
	result, err := h.rateLimitService.ClearAccountRateLimitPreview(c.Request.Context(), id, req.ExpectedVersion)
	if err != nil {
		code := "RATE_LIMIT_CLEAR_FAILED"
		auditResult := "failed"
		if errors.Is(err, service.ErrRateLimitStateChanged) {
			code = "RATE_LIMIT_STATE_CHANGED"
			auditResult = "not_committed"
		}
		middleware.SetAuditExtra(c, map[string]any{"result": auditResult, "error_code": code})
		response.ErrorFrom(c, err)
		return
	}
	var accountResponse any
	account, readErr := h.adminService.GetAccount(c.Request.Context(), id)
	if readErr != nil {
		result.CacheSync.Status = "warning"
		result.CacheSync.FailedComponents = append(result.CacheSync.FailedComponents, "account_refresh")
	} else {
		accountResponse = h.buildAccountResponseWithRuntime(c.Request.Context(), account)
	}
	details := map[string]any{"result": "committed", "rate_limit_before_version": result.Before.Version, "rate_limit_after_version": result.After.Version, "rate_limit_before_count": result.Before.ActiveCount, "rate_limit_after_count": result.After.ActiveCount, "rate_limit_cache_sync": result.CacheSync.Status, "rate_limit_cache_failed": strings.Join(result.CacheSync.FailedComponents, ","), "rate_limit_schedulable": result.After.Schedulable, "rate_limit_account_status": result.After.Status}
	types := []string{}
	seen := map[string]bool{}
	modelCount := 0
	var modelLatest time.Time
	for _, r := range result.Before.Restrictions {
		if !seen[r.Kind] {
			types = append(types, r.Kind)
			seen[r.Kind] = true
		}
		if r.Kind == "model" {
			modelCount++
			if r.Until != nil && r.Until.After(modelLatest) {
				modelLatest = *r.Until
			}
		}
		if r.Until != nil && r.Kind != "model" {
			details["rate_limit_before_"+r.Kind+"_until"] = r.Until.UTC().Format(time.RFC3339Nano)
		}
	}
	details["rate_limit_before_types"] = strings.Join(types, ",")
	details["rate_limit_before_model_count"] = modelCount
	if !modelLatest.IsZero() {
		details["rate_limit_before_model_until"] = modelLatest.UTC().Format(time.RFC3339Nano)
	}
	details["rate_limit_summary_truncated"] = result.Before.Truncated
	middleware.SetAuditExtra(c, details)
	response.Success(c, gin.H{"account": accountResponse, "before": result.Before, "after": result.After, "cache_sync": result.CacheSync})
}
