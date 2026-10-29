package handler

import (
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func writeAsyncImageAttemptBudgetError(c *gin.Context, err error) bool {
	if !errors.Is(err, service.ErrAsyncImageAttemptBudgetExhausted) {
		return false
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
		"type": "upstream_error", "code": "async_image_attempt_budget_exhausted",
		"message": "image generation upstream attempt budget exhausted",
	}})
	return true
}
