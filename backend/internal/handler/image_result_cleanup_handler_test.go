package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	saves     int
}

func (r *resultCleanupHandlerRepo) GetImageResultCleanupPolicy(context.Context) (*service.ImageResultCleanupPolicy, error) {
	return &r.policy, nil
}
func (r *resultCleanupHandlerRepo) SaveImageResultCleanupPolicy(_ context.Context, p service.ImageResultCleanupPolicy, expected int64) (*service.ImageResultCleanupPolicy, error) {
	if expected != r.policy.Revision {
		return nil, service.ErrImageResultCleanupConflict
	}
	r.saves++
	p.Revision = expected + 1
	r.policy = p
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
	repo := &resultCleanupHandlerRepo{policy: service.ImageResultCleanupPolicy{Configured: true, Enabled: true, RetentionValue: 7, RetentionUnit: "days", ScheduleMode: "interval", IntervalMinutes: 15, DailyTime: "03:00", Timezone: "Asia/Shanghai", BatchSize: 50, RequestsPerSecond: 2, Concurrency: 1, Revision: 4}}
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

func TestImageResultCleanupHandlerRequestRateValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		rate string
		want int
	}{
		{"1", http.StatusOK},
		{"5", http.StatusOK},
		{"50", http.StatusOK},
		{"100", http.StatusOK},
		{"500", http.StatusOK},
		{"1000", http.StatusOK},
		{"0", http.StatusBadRequest},
		{"-1", http.StatusBadRequest},
		{"1001", http.StatusBadRequest},
		{"1.5", http.StatusBadRequest},
		{"1000.0", http.StatusBadRequest},
		{`"100"`, http.StatusBadRequest},
		{"true", http.StatusBadRequest},
		{"null", http.StatusBadRequest},
	} {
		t.Run(tc.rate, func(t *testing.T) {
			h, repo := newResultCleanupHandlerFixture()
			policyJSON, err := json.Marshal(repo.policy)
			require.NoError(t, err)
			var input map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(policyJSON, &input))
			input["requests_per_second"] = json.RawMessage(tc.rate)
			body, err := json.Marshal(input)
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
			c.Request = httptest.NewRequest(http.MethodPut, "/cleanup-policy", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			h.AdminUpdateResultCleanupPolicy(c)
			require.Equal(t, tc.want, recorder.Code, recorder.Body.String())
			if tc.want != http.StatusOK {
				require.Zero(t, repo.saves, "invalid input must not persist policy changes")
				return
			}
			var response struct {
				Data service.ImageResultCleanupPolicy `json:"data"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, repo.policy.RequestsPerSecond, response.Data.RequestsPerSecond)
			require.Equal(t, tc.rate, strconv.Itoa(response.Data.RequestsPerSecond))
			require.Equal(t, int64(5), response.Data.Revision)
			require.Equal(t, 1, repo.saves)
		})
	}
}

func TestImageResultCleanupHandlerConcurrencyValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		value       string
		want        int
		concurrency int
	}{
		{"1", http.StatusOK, 1},
		{"4", http.StatusOK, 4},
		{"16", http.StatusOK, 16},
		{"omitted", http.StatusOK, 1},
		{"0", http.StatusBadRequest, 0},
		{"-1", http.StatusBadRequest, 0},
		{"17", http.StatusBadRequest, 0},
		{"1.5", http.StatusBadRequest, 0},
		{"4.0", http.StatusBadRequest, 0},
		{`"4"`, http.StatusBadRequest, 0},
		{"null", http.StatusBadRequest, 0},
	} {
		t.Run(tc.value, func(t *testing.T) {
			h, repo := newResultCleanupHandlerFixture()
			policyJSON, err := json.Marshal(repo.policy)
			require.NoError(t, err)
			var input map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(policyJSON, &input))
			if tc.value == "omitted" {
				delete(input, "concurrency")
			} else {
				input["concurrency"] = json.RawMessage(tc.value)
			}
			body, err := json.Marshal(input)
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
			c.Request = httptest.NewRequest(http.MethodPut, "/cleanup-policy", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			h.AdminUpdateResultCleanupPolicy(c)
			require.Equal(t, tc.want, recorder.Code, recorder.Body.String())
			if tc.want != http.StatusOK {
				require.Zero(t, repo.saves)
				return
			}
			var response struct {
				Data service.ImageResultCleanupPolicy `json:"data"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, tc.concurrency, repo.policy.Concurrency)
			require.Equal(t, tc.concurrency, response.Data.Concurrency)
			require.Equal(t, 1, repo.saves)
		})
	}
}
