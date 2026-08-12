package handler

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ImageWorkbenchHandler struct {
	service *service.ImageWorkbenchService
}

func NewImageWorkbenchHandler(service *service.ImageWorkbenchService) *ImageWorkbenchHandler {
	return &ImageWorkbenchHandler{service: service}
}

// GetCapabilities returns the current group-controlled image execution mode.
// GET /api/v1/user/image-workbench/capabilities/:api_key_id
func (h *ImageWorkbenchHandler) GetCapabilities(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	apiKeyID, err := strconv.ParseInt(c.Param("api_key_id"), 10, 64)
	if err != nil || apiKeyID <= 0 {
		response.BadRequest(c, "Invalid API key ID")
		return
	}
	if h == nil || h.service == nil {
		response.InternalError(c, "Image workbench service unavailable")
		return
	}
	capabilities, err := h.service.GetCapabilities(c.Request.Context(), subject.UserID, apiKeyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, capabilities)
}
