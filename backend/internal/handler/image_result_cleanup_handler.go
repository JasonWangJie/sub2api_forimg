package handler

import (
	"errors"
	"strconv"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *ImageLibraryHandler) resultCleanup(c *gin.Context) *service.ImageResultCleanupService {
	if h == nil || h.svc == nil || h.svc.ResultCleanup() == nil {
		response.ErrorFrom(c, apperrors.ServiceUnavailable("IMAGE_CLEANUP_UNAVAILABLE", "生成结果清理暂不可用"))
		return nil
	}
	return h.svc.ResultCleanup()
}

func imageResultCleanupError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrImageResultCleanupConflict), errors.Is(err, service.ErrImageLibraryLeaseLost):
		err = apperrors.Conflict("IMAGE_CLEANUP_CONFLICT", "清理状态已变化，请刷新后重试")
	case errors.Is(err, service.ErrImageResultCleanupPreviewExpired):
		err = apperrors.Conflict("IMAGE_CLEANUP_PREVIEW_EXPIRED", "预览已过期或已使用，请重新预览")
	}
	response.ErrorFrom(c, err)
}

func (h *ImageLibraryHandler) AdminGetResultCleanupPolicy(c *gin.Context) {
	s := h.resultCleanup(c)
	if s == nil {
		return
	}
	p, err := s.Policy(c.Request.Context())
	if err != nil {
		imageResultCleanupError(c, err)
		return
	}
	response.Success(c, p)
}

func (h *ImageLibraryHandler) AdminUpdateResultCleanupPolicy(c *gin.Context) {
	s := h.resultCleanup(c)
	if s == nil {
		return
	}
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	var p service.ImageResultCleanupPolicy
	if err := bindLimitedJSON(c, &p, 16<<10); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	saved, err := s.SavePolicy(c.Request.Context(), subject.UserID, p)
	if err != nil {
		imageResultCleanupError(c, err)
		return
	}
	response.Success(c, saved)
}

func (h *ImageLibraryHandler) AdminCancelResultCleanup(c *gin.Context) {
	h.adminControlResultCleanup(c, "cancel")
}
func (h *ImageLibraryHandler) AdminResumeResultCleanup(c *gin.Context) {
	h.adminControlResultCleanup(c, "resume")
}
func (h *ImageLibraryHandler) adminControlResultCleanup(c *gin.Context, action string) {
	s := h.resultCleanup(c)
	if s == nil {
		return
	}
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.ErrorFrom(c, apperrors.BadRequest("IMAGE_CLEANUP_INVALID_ID", "清理任务 ID 无效"))
		return
	}
	job, err := s.Control(c.Request.Context(), subject.UserID, id, action)
	if err != nil {
		imageResultCleanupError(c, err)
		return
	}
	response.Success(c, job)
}
