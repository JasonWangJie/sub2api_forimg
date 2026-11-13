package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *SettingHandler) SetImageResolutionGuardService(guard *service.ImageResolutionGuardService) {
	h.imageResolutionGuard = guard
}

func (h *AccountHandler) SetImageResolutionGuardService(guard *service.ImageResolutionGuardService) {
	h.imageResolutionGuard = guard
}

func (h *SettingHandler) GetImageResolutionGuardSettings(c *gin.Context) {
	if h.imageResolutionGuard == nil {
		response.Error(c, http.StatusServiceUnavailable, "Image resolution guard is unavailable")
		return
	}
	settings, err := h.imageResolutionGuard.Settings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *SettingHandler) UpdateImageResolutionGuardSettings(c *gin.Context) {
	if h.imageResolutionGuard == nil {
		response.Error(c, http.StatusServiceUnavailable, "Image resolution guard is unavailable")
		return
	}
	// Seed defaults so older clients omitting an added field retain its default.
	settings := service.DefaultImageResolutionGuardSettings()
	var fields map[string]json.RawMessage
	if err := c.ShouldBindJSON(&fields); err != nil {
		response.BadRequest(c, "Invalid image resolution guard settings: "+err.Error())
		return
	}
	if fields == nil {
		response.BadRequest(c, "Settings must be a JSON object")
		return
	}
	for name, value := range fields {
		switch name {
		case "enabled", "min_area_percent", "failure_threshold", "cooldown_seconds", "email_enabled", "recipients":
		default:
			response.BadRequest(c, "Unknown setting: "+name)
			return
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			response.BadRequest(c, "Setting cannot be null: "+name)
			return
		}
	}
	raw, _ := json.Marshal(fields)
	if err := json.Unmarshal(raw, &settings); err != nil {
		response.BadRequest(c, "Invalid image resolution guard settings: "+err.Error())
		return
	}
	view, err := h.imageResolutionGuard.UpdateSettings(c.Request.Context(), &settings)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}

func (h *AccountHandler) imageResolutionGuardAccountID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	if h.imageResolutionGuard == nil {
		response.Error(c, http.StatusServiceUnavailable, "Image resolution guard is unavailable")
		return 0, false
	}
	if h.adminService != nil {
		if _, err := h.adminService.GetAccount(c.Request.Context(), id); err != nil {
			response.ErrorFrom(c, err)
			return 0, false
		}
	}
	return id, true
}

func (h *AccountHandler) ListImageResolutionCooldowns(c *gin.Context) {
	id, ok := h.imageResolutionGuardAccountID(c)
	if !ok {
		return
	}
	items, err := h.imageResolutionGuard.ListCooldowns(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if items == nil {
		items = []service.ImageResolutionCooldown{}
	}
	response.Success(c, gin.H{"items": items})
}

func (h *AccountHandler) ClearImageResolutionCooldown(c *gin.Context) {
	id, ok := h.imageResolutionGuardAccountID(c)
	if !ok {
		return
	}
	cycleID := strings.TrimSpace(c.Param("cycle_id"))
	if _, err := uuid.Parse(cycleID); err != nil {
		response.BadRequest(c, "Invalid cooldown cycle ID")
		return
	}
	if err := h.imageResolutionGuard.ClearCooldown(c.Request.Context(), id, cycleID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"cleared": true})
}
