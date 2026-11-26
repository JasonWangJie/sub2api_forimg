package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type asyncImageRetryAdminTestTasks struct {
	asyncImageTaskCenterService
	repo *asyncImageRetryAdminTestRepo
}

func (s asyncImageRetryAdminTestTasks) Repository() service.AsyncImageTaskRepository {
	return s.repo
}

type asyncImageRetryAdminTestRepo struct {
	service.AsyncImageTaskRepository
	service.AsyncImageRetryRepository
	policy service.AsyncImageRetryPolicy
	saves  int
}

func (r *asyncImageRetryAdminTestRepo) GetAsyncImageRetryPolicy(context.Context) (service.AsyncImageRetryPolicy, error) {
	return r.policy, nil
}

func (r *asyncImageRetryAdminTestRepo) SaveAsyncImageRetryPolicy(_ context.Context, expected int64, policy service.AsyncImageRetryPolicy, actorID int64, _ *int64) (service.AsyncImageRetryVersion, error) {
	if expected != r.policy.Revision {
		return service.AsyncImageRetryVersion{}, service.ErrAsyncImageRetryRevisionConflict
	}
	policy.Revision++
	r.saves++
	r.policy = policy
	return service.AsyncImageRetryVersion{Policy: policy, Revision: policy.Revision, ActorID: actorID, Operation: "save"}, nil
}

func TestAsyncImageRetryAdminRevisionAndReadOnlyPreview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &asyncImageRetryAdminTestRepo{}
	handler := &AsyncImageTaskCenterHandler{tasks: asyncImageRetryAdminTestTasks{repo: repo}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Next()
	})
	router.PUT("/", handler.SaveRetryPolicy)
	router.POST("/preview", func(c *gin.Context) { handler.PreviewRetryPolicy(c, nil) })
	invoke := func(method, path, content string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(content))
		req.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		return result
	}
	rules := `[{"id":"test","name":"Test","enabled":true,"scope":{},"match":{"http_statuses":[429]},"steps":[{"id":"stop","action":"stop","max_attempts":1}]}]`
	policy := `{"revision":0,"rules":` + rules + `}`
	require.Equal(t, http.StatusBadRequest, invoke(http.MethodPut, "/", `{"rules":[]}`).Code)
	save := invoke(http.MethodPut, "/", policy)
	require.Equal(t, http.StatusOK, save.Code, save.Body.String())
	require.Equal(t, 1, repo.saves)
	conflict := invoke(http.MethodPut, "/", policy)
	require.Equal(t, http.StatusConflict, conflict.Code, conflict.Body.String())
	require.Contains(t, conflict.Body.String(), `"current_revision":"1"`)
	require.Equal(t, 1, repo.saves)

	preview := invoke(http.MethodPost, "/preview", `{"draft":`+policy+`,"fact":{"stage":"submit","http_status":429,"message":"try later"}}`)
	require.Equal(t, http.StatusOK, preview.Code, preview.Body.String())
	require.Contains(t, preview.Body.String(), `"rule_id":"test"`)
	require.Contains(t, preview.Body.String(), `"eligibility":"unverified"`)
	require.Equal(t, 1, repo.saves)
	require.Equal(t, http.StatusBadRequest, invoke(http.MethodPost, "/preview", `{"fact":{},"event_id":1}`).Code)
}

func TestAsyncImageRetryAdminValidatesPaginationAndFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/", func(c *gin.Context) {
		filter, _, ok := readAsyncImageRetryFilter(c)
		if ok {
			require.Equal(t, 7, filter.Offset)
			require.Equal(t, 7, filter.Limit)
			require.Equal(t, int64(4), filter.GroupID)
			c.Status(http.StatusNoContent)
		}
	})
	for _, query := range []string{"group_id=-2", "http_status=700", "from=not-a-date", "group_id=0"} {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/?"+query, nil))
		require.Equal(t, http.StatusBadRequest, rr.Code, query)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/?page=2&page_size=7&group_id=4", nil))
	require.Equal(t, http.StatusNoContent, rr.Code)
}
