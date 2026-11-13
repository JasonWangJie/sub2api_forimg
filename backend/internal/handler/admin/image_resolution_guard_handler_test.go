package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type resolutionHandlerSettings struct {
	service.SettingRepository
	value  string
	writes int
}

func (s *resolutionHandlerSettings) GetValue(context.Context, string) (string, error) {
	if s.value == "" {
		return "", service.ErrSettingNotFound
	}
	return s.value, nil
}
func (s *resolutionHandlerSettings) Set(_ context.Context, _, value string) error {
	s.value = value
	s.writes++
	return nil
}

type resolutionHandlerRepo struct {
	service.ImageResolutionGuardRepository
	clearedAccount int64
	clearedCycle   string
}

func (r *resolutionHandlerRepo) ListCooldowns(context.Context, int64) ([]service.ImageResolutionCooldown, error) {
	return nil, nil
}
func (r *resolutionHandlerRepo) ClearCooldown(_ context.Context, id int64, cycle string, _ time.Time) error {
	r.clearedAccount = id
	r.clearedCycle = cycle
	return nil
}

func TestImageResolutionGuardAdminHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := &resolutionHandlerSettings{}
	repo := &resolutionHandlerRepo{}
	guard := service.NewImageResolutionGuardService(repo, settings, nil, nil, nil)
	settingHandler := NewSettingHandler(nil, nil, nil, nil, nil, nil, nil)
	settingHandler.SetImageResolutionGuardService(guard)
	accountHandler := NewAccountHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	accountHandler.SetImageResolutionGuardService(guard)
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "resolution-guard-local-http-test", ExpireHour: 1}}
	auth := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	users := service.NewUserService(&rateLimitSmokeUsers{}, nil, nil, nil)
	router := gin.New()
	group := router.Group("/api/v1/admin", gin.HandlerFunc(middleware.NewAdminAuthMiddleware(auth, users, nil, nil)))
	group.GET("/settings/image-resolution-guard", settingHandler.GetImageResolutionGuardSettings)
	group.PUT("/settings/image-resolution-guard", settingHandler.UpdateImageResolutionGuardSettings)
	group.GET("/accounts/:id/image-resolution-cooldowns", accountHandler.ListImageResolutionCooldowns)
	group.POST("/accounts/:id/image-resolution-cooldowns/:cycle_id/clear", accountHandler.ClearImageResolutionCooldown)
	tokens := map[int64]string{}
	for _, id := range []int64{1, 2} {
		role := service.RoleAdmin
		if id == 2 {
			role = service.RoleUser
		}
		token, err := auth.GenerateToken(context.Background(), &service.User{ID: id, Role: role, TokenVersion: 1, TokenVersionResolved: true})
		require.NoError(t, err)
		tokens[id] = token
	}
	do := func(method, path, body string, user int64) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if user > 0 {
			req.Header.Set("Authorization", "Bearer "+tokens[user])
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	path := "/api/v1/admin/settings/image-resolution-guard"
	for _, tc := range []struct {
		user   int64
		status int
	}{{0, 401}, {2, 403}} {
		w := do(http.MethodPut, path, `{"enabled":true}`, tc.user)
		require.Equal(t, tc.status, w.Code, w.Body.String())
	}
	require.Zero(t, settings.writes)
	w := do(http.MethodGet, path, "", 1)
	require.Equal(t, 200, w.Code)
	var view struct {
		Data service.ImageResolutionGuardSettingsView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &view))
	require.False(t, view.Data.Enabled)
	require.Equal(t, 75, view.Data.MinAreaPercent)
	for _, body := range []string{`null`, `[]`, `{"enabled":null}`, `{"min_area_percent":0}`, `{"min_area_percent":75.5}`, `{"failure_threshold":101}`, `{"cooldown_seconds":9}`, `{"recipients":["bad"]}`, `{"smtp_password":"secret"}`} {
		w = do(http.MethodPut, path, body, 1)
		require.Equal(t, 400, w.Code, body+": "+w.Body.String())
	}
	require.Zero(t, settings.writes)
	w = do(http.MethodPut, path, `{"enabled":true,"min_area_percent":75,"failure_threshold":1,"cooldown_seconds":600,"email_enabled":true,"recipients":["admin@example.test"]}`, 1)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, 1, settings.writes)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &view))
	require.True(t, view.Data.Enabled)
	require.Equal(t, "override", view.Data.RecipientSource)
	require.Contains(t, view.Data.Warnings, "smtp_not_configured")
	w = do(http.MethodGet, "/api/v1/admin/accounts/42/image-resolution-cooldowns", "", 1)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"items":[]`)
	cycle := "11111111-1111-4111-8111-111111111111"
	w = do(http.MethodPost, "/api/v1/admin/accounts/42/image-resolution-cooldowns/"+cycle+"/clear", `{}`, 2)
	require.Equal(t, 403, w.Code)
	require.Zero(t, repo.clearedAccount)
	w = do(http.MethodPost, "/api/v1/admin/accounts/42/image-resolution-cooldowns/invalid/clear", `{}`, 1)
	require.Equal(t, 400, w.Code)
	w = do(http.MethodPost, "/api/v1/admin/accounts/42/image-resolution-cooldowns/"+cycle+"/clear", `{}`, 1)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, int64(42), repo.clearedAccount)
	require.Equal(t, cycle, repo.clearedCycle)
}
