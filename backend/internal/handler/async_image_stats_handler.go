package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type asyncImageStatsResponse struct {
	Object        string  `json:"object"`
	Date          string  `json:"date"`
	Timezone      string  `json:"timezone"`
	Balance       float64 `json:"balance"`
	TodayRequests int64   `json:"today_requests"`
	SuccessCount  int64   `json:"success_count"`
	FailureCount  int64   `json:"failure_count"`
	SuccessRate   float64 `json:"success_rate"`
}

// GetStats returns the authenticated user's current balance and today's
// durable async-image task totals. The day boundary follows the configured
// server timezone, while task timestamps remain stored as PostgreSQL timestamptz.
// GET /v1/images/tasks_async/stats
func (h *AsyncImageTaskCenterHandler) GetStats(c *gin.Context) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.User == nil || apiKey.User.ID <= 0 {
		writeAsyncImageStatsError(c, http.StatusUnauthorized, "authentication_error", "invalid API key")
		return
	}

	statsService, ok := h.tasks.(service.AsyncImageTaskCenterStatsService)
	if !ok || statsService == nil {
		writeAsyncImageStatsError(c, http.StatusServiceUnavailable, "stats_unavailable", "asynchronous image statistics are unavailable")
		return
	}

	now := timezone.Now()
	start := timezone.StartOfDay(now)
	end := start.AddDate(0, 0, 1)
	stats, err := statsService.StatsForUser(c.Request.Context(), apiKey.User.ID, service.AsyncImageTaskFilter{
		CreatedAfter:  &start,
		CreatedBefore: &end,
		Limit:         1,
	})
	if err != nil {
		writeAsyncImageStatsError(c, http.StatusInternalServerError, "stats_unavailable", "failed to read asynchronous image statistics")
		return
	}

	todayRequests := stats.Active + stats.Completed + stats.Failed
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, asyncImageStatsResponse{
		Object:        "async_image.stats",
		Date:          start.Format("2006-01-02"),
		Timezone:      timezone.Name(),
		Balance:       apiKey.User.Balance,
		TodayRequests: todayRequests,
		SuccessCount:  stats.Completed,
		FailureCount:  stats.Failed,
		SuccessRate:   stats.SuccessRate,
	})
}

func writeAsyncImageStatsError(c *gin.Context, status int, code, message string) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"error": gin.H{"type": code, "code": code, "message": message}})
}
