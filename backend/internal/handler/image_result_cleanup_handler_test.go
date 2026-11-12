package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type resultCleanupHandlerRepo struct {
	service.ImageLibraryRepository
	service.ImageResultCleanupRepository
	tokenHash string
	adminID   int64
	policy    service.ImageResultCleanupPolicy
}

func (r *resultCleanupHandlerRepo) GetImageResultCleanupPolicy(context.Context) (*service.ImageResultCleanupPolicy, error) {
	return &r.policy, nil
}
func (r *resultCleanupHandlerRepo) CreateImageResultCleanupJobFromPreview(_ context.Context, hash string, adminID int64, _ time.Time) (*service.ImageResultCleanupJob, error) {
	r.tokenHash, r.adminID = hash, adminID
	return &service.ImageResultCleanupJob{ID: 12, Scope: "async_results", Status: "pending"}, nil
}
func (r *resultCleanupHandlerRepo) ControlImageResultCleanupJob(context.Context, int64, string) (*service.ImageResultCleanupJob, error) {
	return nil, service.ErrImageLibraryLeaseLost
}

func newResultCleanupHandlerFixture() (*ImageLibraryHandler, *resultCleanupHandlerRepo) {
	repo := &resultCleanupHandlerRepo{policy: service.ImageResultCleanupPolicy{Configured: true, Enabled: true, RetentionValue: 7, RetentionUnit: "days", ScheduleMode: "interval", IntervalMinutes: 15, DailyTime: "03:00", Timezone: "Asia/Shanghai", BatchSize: 50, RequestsPerSecond: 2, Revision: 4}}
	settings := service.NewImageStorageSettingService(nil, nil, nil, nil, config.ImageStorageConfig{})
	return NewImageLibraryHandler(service.NewImageLibraryService(repo, settings, nil), nil), repo
}

func TestImageResultCleanupHandlerRequiresPreviewAndIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, token   string
		authenticated bool
		want          int
	}{
		{"authentication", "", false, http.StatusUnauthorized},
		{"preview_required", "", true, http.StatusBadRequest},
		{"fixed_preview", strings.Repeat("a", 64), true, http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, repo := newResultCleanupHandlerFixture()
			body, _ := json.Marshal(gin.H{"scope": "async_results", "filters": gin.H{"preview_token": tc.token}})
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/cleanup-jobs", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			if tc.authenticated {
				c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
			}
			h.AdminCreateCleanupJob(c)
			require.Equal(t, tc.want, recorder.Code)
			if tc.want == http.StatusCreated {
				require.Equal(t, int64(7), repo.adminID)
				require.Len(t, repo.tokenHash, 64)
				require.NotEqual(t, tc.token, repo.tokenHash)
			}
		})
	}
}

func TestImageResultCleanupHandlerRejectsStalePolicyAndControl(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, repo := newResultCleanupHandlerFixture()
	repo.policy.Revision = 4
	input := repo.policy
	input.Revision = 0
	body, _ := json.Marshal(input)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
	c.Request = httptest.NewRequest(http.MethodPut, "/cleanup-policy", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.AdminUpdateResultCleanupPolicy(c)
	require.Equal(t, http.StatusConflict, recorder.Code)
	recorder = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
	c.Params = gin.Params{{Key: "id", Value: "12"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/cleanup-jobs/12/cancel", nil)
	h.AdminCancelResultCleanup(c)
	require.Equal(t, http.StatusConflict, recorder.Code)
}
