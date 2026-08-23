package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type asyncImageTaskCenterServiceStub struct {
	userID        int64
	filter        service.AsyncImageTaskFilter
	tasks         []*service.AsyncImageTask
	total         int64
	details       *service.AsyncImageTaskDetails
	results       map[string][]service.AsyncImageResult
	err           error
	resumeDetails *service.AsyncImageTaskDetails
	resumeCalls   int
	stats         service.AsyncImageTaskStats
	statsErr      error
	statsUserID   int64
	statsFilter   service.AsyncImageTaskFilter
}

func (s *asyncImageTaskCenterServiceStub) ListForUser(_ context.Context, userID int64, filter service.AsyncImageTaskFilter) ([]*service.AsyncImageTask, int64, error) {
	s.userID, s.filter = userID, filter
	return s.tasks, s.total, s.err
}

func (s *asyncImageTaskCenterServiceStub) ListForAdmin(_ context.Context, filter service.AsyncImageTaskFilter) ([]*service.AsyncImageTask, int64, error) {
	s.filter = filter
	return s.tasks, s.total, s.err
}

func (s *asyncImageTaskCenterServiceStub) GetForUser(_ context.Context, userID int64, _ string) (*service.AsyncImageTaskDetails, error) {
	s.userID = userID
	return s.details, s.err
}

func (s *asyncImageTaskCenterServiceStub) GetForAdmin(_ context.Context, _ string) (*service.AsyncImageTaskDetails, error) {
	return s.details, s.err
}

func (s *asyncImageTaskCenterServiceStub) ListResults(_ context.Context, taskID string) ([]service.AsyncImageResult, error) {
	return s.results[taskID], s.err
}

func (s *asyncImageTaskCenterServiceStub) ResumePostProcessing(_ context.Context, _ string) (*service.AsyncImageTaskDetails, error) {
	s.resumeCalls++
	if s.resumeDetails != nil {
		return s.resumeDetails, s.err
	}
	return s.details, s.err
}

func (s *asyncImageTaskCenterServiceStub) StatsForUser(_ context.Context, userID int64, filter service.AsyncImageTaskFilter) (service.AsyncImageTaskStats, error) {
	s.statsUserID, s.statsFilter = userID, filter
	return s.stats, s.statsErr
}

func (s *asyncImageTaskCenterServiceStub) StatsForAdmin(_ context.Context, _ service.AsyncImageTaskFilter) (service.AsyncImageTaskStats, error) {
	return s.stats, s.statsErr
}

func TestAsyncImageTaskCenterGetStatsReturnsUserBalanceAndTodayCounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AsyncImageTaskCenterHandler{
		tasks: &asyncImageTaskCenterServiceStub{stats: service.AsyncImageTaskStats{
			Active: 2, Completed: 7, Failed: 3, SuccessRate: 70,
		}},
	}
	taskStub := h.tasks.(*asyncImageTaskCenterServiceStub)
	apiKey := &service.APIKey{User: &service.User{ID: 42, Balance: 12.5}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/images/tasks_async/stats", nil)
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)

	h.GetStats(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var got asyncImageStatsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, "async_image.stats", got.Object)
	require.Equal(t, 12.5, got.Balance)
	require.Equal(t, int64(12), got.TodayRequests)
	require.Equal(t, int64(7), got.SuccessCount)
	require.Equal(t, int64(3), got.FailureCount)
	require.Equal(t, float64(70), got.SuccessRate)
	require.NotEmpty(t, got.Date)
	require.NotEmpty(t, got.Timezone)
	require.Equal(t, int64(42), taskStub.statsUserID)
	require.NotNil(t, taskStub.statsFilter.CreatedAfter)
	require.NotNil(t, taskStub.statsFilter.CreatedBefore)
	require.Equal(t, got.Date, taskStub.statsFilter.CreatedAfter.Format("2006-01-02"))
	require.Equal(t, got.Date, taskStub.statsFilter.CreatedBefore.Add(-24*time.Hour).Format("2006-01-02"))
}

func TestAsyncImageTaskCenterGetStatsRejectsMissingAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AsyncImageTaskCenterHandler{tasks: &asyncImageTaskCenterServiceStub{}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/images/tasks_async/stats", nil)

	h.GetStats(c)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), "authentication_error")
}

type asyncImageTaskStorageAccessStub struct {
	storage service.DurableImageStorage
	runtime service.AsyncImageRuntimeConfig
	enabled bool
	err     error
}

func (s *asyncImageTaskStorageAccessStub) DurableStorage(context.Context) (service.DurableImageStorage, bool, error) {
	return s.storage, s.enabled, s.err
}

func (s *asyncImageTaskStorageAccessStub) RuntimeConfig(context.Context) (service.AsyncImageRuntimeConfig, error) {
	return s.runtime, s.err
}

type asyncImageDurableStorageStub struct {
	access    service.ObjectAccess
	ref       service.ObjectRef
	signCalls int
}

type asyncImageTaskCenterCatalogStub struct {
	users    map[int64]*service.User
	apiKeys  map[int64]*service.APIKey
	groups   map[int64]*service.Group
	accounts map[int64]*service.Account
}

func (s *asyncImageTaskCenterCatalogStub) GetUser(_ context.Context, id int64) (*service.User, error) {
	if user, ok := s.users[id]; ok {
		return user, nil
	}
	return nil, service.ErrUserNotFound
}

func (s *asyncImageTaskCenterCatalogStub) GetAPIKey(_ context.Context, id int64) (*service.APIKey, error) {
	if key, ok := s.apiKeys[id]; ok {
		return key, nil
	}
	return nil, service.ErrAPIKeyNotFound
}

func (s *asyncImageTaskCenterCatalogStub) GetGroup(_ context.Context, id int64) (*service.Group, error) {
	if group, ok := s.groups[id]; ok {
		return group, nil
	}
	return nil, service.ErrGroupNotFound
}

func (s *asyncImageTaskCenterCatalogStub) GetAccount(_ context.Context, id int64) (*service.Account, error) {
	if account, ok := s.accounts[id]; ok {
		return account, nil
	}
	return nil, service.ErrAccountNotFound
}

func (s *asyncImageDurableStorageStub) Save(context.Context, string, string, []byte) (string, error) {
	return s.access.URL, nil
}

func (s *asyncImageDurableStorageStub) SaveObject(context.Context, string, string, []byte) (service.ObjectRef, error) {
	return service.ObjectRef{}, nil
}

func (s *asyncImageDurableStorageStub) SignURL(_ context.Context, ref service.ObjectRef, _ time.Duration) (service.ObjectAccess, error) {
	s.signCalls++
	s.ref = ref
	return s.access, nil
}

func (s *asyncImageDurableStorageStub) Read(context.Context, service.ObjectRef) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("image")), nil
}

func (s *asyncImageDurableStorageStub) Head(context.Context, service.ObjectRef) (service.ObjectMetadata, error) {
	return service.ObjectMetadata{}, nil
}

func (s *asyncImageDurableStorageStub) Delete(context.Context, service.ObjectRef) error { return nil }

func TestAsyncImageTaskCenterUserListScopesAndAddsResultSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	task := &service.AsyncImageTask{
		TaskID: "asyncimg_abc", UserID: 42, APIKeyID: 7, GroupID: 8,
		AccountID: int64PointerForAsyncImageTaskTest(99), Protocol: service.AsyncImageProtocolBB,
		Platform: service.PlatformGemini, RequestType: service.AsyncImageRequestTypeTextToImage,
		Model: "gemini-image", Status: service.AsyncImageTaskStatusSucceeded,
		BillingStatus: service.AsyncImageBillingStatusSucceeded, ImageCount: 1,
		SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	result := service.AsyncImageResult{
		TaskID: task.TaskID, ImageIndex: 0, Provider: service.ImageStorageProviderQiniu,
		Bucket: "private-bucket", ObjectKey: "results/image.png", ContentType: "image/png",
		ByteSize: 1234, Checksum: "sha256", CreatedAt: now,
	}
	tasks := &asyncImageTaskCenterServiceStub{
		tasks: []*service.AsyncImageTask{task}, total: 11,
		results: map[string][]service.AsyncImageResult{task.TaskID: {result}},
	}
	durable := &asyncImageDurableStorageStub{access: service.ObjectAccess{URL: "https://cdn.example/image.png", ExpiresAt: now.Add(time.Hour)}}
	h := &AsyncImageTaskCenterHandler{
		tasks:   tasks,
		storage: &asyncImageTaskStorageAccessStub{storage: durable, enabled: true, runtime: service.AsyncImageRuntimeConfig{SignedURLExpirySeconds: 3600}},
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
	})
	router.GET("/api/v1/user/async-image-tasks", h.ListForUser)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/async-image-tasks?page=2&page_size=10&q=chair&status=succeeded&api_key_id=7", nil)
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(42), tasks.userID)
	require.Equal(t, 10, tasks.filter.Offset)
	require.Equal(t, 10, tasks.filter.Limit)
	require.Equal(t, "chair", tasks.filter.Search)
	require.NotNil(t, tasks.filter.APIKeyID)
	require.Equal(t, int64(7), *tasks.filter.APIKeyID)

	var envelope struct {
		Data struct {
			Items []map[string]any `json:"items"`
			Total int64            `json:"total"`
			Page  int              `json:"page"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, int64(11), envelope.Data.Total)
	require.Equal(t, 2, envelope.Data.Page)
	require.Len(t, envelope.Data.Items, 1)
	item := envelope.Data.Items[0]
	require.Equal(t, float64(1), item["result_count"])
	require.Equal(t, service.ImageStorageProviderQiniu, item["storage_provider"])
	require.Equal(t, "https://cdn.example/image.png", item["preview_url"])
	require.Equal(t, "/api/v1/user/async-image-tasks/asyncimg_abc/results/0/view", item["view_url"])
	require.NotContains(t, item, "account_id")
	require.Equal(t, "private-bucket", durable.ref.Bucket)
}

func TestAsyncImageTaskCenterDetailRedactsAndDoesNotExposeObjectIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	errorMessage := "upstream access_token=secret-value"
	task := &service.AsyncImageTask{
		TaskID: "asyncimg_detail", UserID: 42, APIKeyID: 7, GroupID: 8,
		AccountID: int64PointerForAsyncImageTaskTest(99), Protocol: service.AsyncImageProtocolSC,
		Platform: service.PlatformGemini, RequestType: service.AsyncImageRequestTypeImageToImage,
		Model: "gemini-image", Status: service.AsyncImageTaskStatusStorageFailed,
		BillingStatus: service.AsyncImageBillingStatusPrepared, ErrorMessage: &errorMessage,
		SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	details := &service.AsyncImageTaskDetails{
		Task:    task,
		Results: []service.AsyncImageResult{{TaskID: task.TaskID, ImageIndex: 1, Provider: service.ImageStorageProviderAliyun, Bucket: "secret-bucket", ObjectKey: "secret/key.png", ContentType: "image/png", CreatedAt: now}},
		Events:  []service.AsyncImageEvent{{ID: 1, TaskID: task.TaskID, EventType: "storage_failed", ToStatus: stringPointerForAsyncImageTaskTest(service.AsyncImageTaskStatusStorageFailed), Payload: json.RawMessage(`{"error":"access_token=another-secret"}`), CreatedAt: now}},
	}
	tasks := &asyncImageTaskCenterServiceStub{details: details}
	h := &AsyncImageTaskCenterHandler{tasks: tasks}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
	})
	router.GET("/api/v1/user/async-image-tasks/:task_id", h.GetForUser)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/user/async-image-tasks/asyncimg_detail", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	require.NotContains(t, body, "secret-value")
	require.NotContains(t, body, "another-secret")
	require.NotContains(t, body, "secret-bucket")
	require.NotContains(t, body, "secret/key.png")
	require.NotContains(t, body, "account_id")
	require.Contains(t, body, "access_token=***")
	require.NotContains(t, body, "view_url")
	require.NotContains(t, body, "preview_url")
}

func TestAsyncImageTaskCenterAdminDetailIncludesRoutingNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	task := &service.AsyncImageTask{
		TaskID: "asyncimg_names", UserID: 2, APIKeyID: 5, GroupID: 2,
		AccountID: int64PointerForAsyncImageTaskTest(9), Protocol: service.AsyncImageProtocolBB,
		Platform: service.PlatformOpenAI, RequestType: service.AsyncImageRequestTypeTextToImage,
		Model: "gpt-image-2", Status: service.AsyncImageTaskStatusFailed,
		BillingStatus: service.AsyncImageBillingStatusNotBillable,
		SubmittedAt:   now, CreatedAt: now, UpdatedAt: now,
	}
	tasks := &asyncImageTaskCenterServiceStub{details: &service.AsyncImageTaskDetails{Task: task}}
	h := &AsyncImageTaskCenterHandler{
		tasks: tasks,
		catalog: &asyncImageTaskCenterCatalogStub{
			users:    map[int64]*service.User{2: {ID: 2, Username: "alice", Email: "alice@example.com"}},
			apiKeys:  map[int64]*service.APIKey{5: {ID: 5, Name: "workbench-key"}},
			groups:   map[int64]*service.Group{2: {ID: 2, Name: "openai-image"}},
			accounts: map[int64]*service.Account{9: {ID: 9, Name: "openai-prod-1"}},
		},
	}

	router := gin.New()
	router.GET("/api/v1/admin/async-image-tasks/:task_id", h.GetForAdmin)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/async-image-tasks/asyncimg_names", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var envelope struct {
		Data struct {
			Task map[string]any `json:"task"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, "alice", envelope.Data.Task["user_email"])
	require.Equal(t, "workbench-key", envelope.Data.Task["api_key_name"])
	require.Equal(t, "openai-image", envelope.Data.Task["group_name"])
	require.Equal(t, "openai-prod-1", envelope.Data.Task["account_name"])
}

func TestAsyncImageTaskCenterAdminResumeRejectsNonPostProcessingState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	tasks := &asyncImageTaskCenterServiceStub{details: &service.AsyncImageTaskDetails{Task: &service.AsyncImageTask{
		TaskID: "asyncimg_done", Status: service.AsyncImageTaskStatusSucceeded,
		SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
	}}}
	h := &AsyncImageTaskCenterHandler{tasks: tasks}
	router := gin.New()
	router.POST("/api/v1/admin/async-image-tasks/:task_id/resume", h.ResumePostProcessing)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/admin/async-image-tasks/asyncimg_done/resume", nil))

	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Zero(t, tasks.resumeCalls)
}

func TestAsyncImageTaskCenterUserResultRedirectSignsAfterOwnershipLookup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	result := service.AsyncImageResult{TaskID: "asyncimg_view", ImageIndex: 2, Provider: service.ImageStorageProviderTencent, Bucket: "bucket", ObjectKey: "result.png", ContentType: "image/png"}
	tasks := &asyncImageTaskCenterServiceStub{details: &service.AsyncImageTaskDetails{
		Task: &service.AsyncImageTask{
			TaskID: "asyncimg_view", UserID: 42, Status: service.AsyncImageTaskStatusSucceeded,
			BillingStatus: service.AsyncImageBillingStatusSucceeded, SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
		},
		Results: []service.AsyncImageResult{result},
	}}
	durable := &asyncImageDurableStorageStub{access: service.ObjectAccess{URL: "https://objects.example/signed.png"}}
	h := &AsyncImageTaskCenterHandler{
		tasks:   tasks,
		storage: &asyncImageTaskStorageAccessStub{storage: durable, enabled: true, runtime: service.AsyncImageRuntimeConfig{SignedURLExpirySeconds: 600}},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
	})
	router.GET("/api/v1/user/async-image-tasks/:task_id/results/:image_index/view", h.ViewResultForUser)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/user/async-image-tasks/asyncimg_view/results/2/view", nil))

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "https://objects.example/signed.png", recorder.Header().Get("Location"))
	require.Equal(t, int64(42), tasks.userID)
	require.Equal(t, result.ObjectKey, durable.ref.ObjectKey)
}

func TestAsyncImageTaskCenterUserHidesUnreleasedResultAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name          string
		status        string
		billingStatus string
	}{
		{"storage_failed", service.AsyncImageTaskStatusStorageFailed, service.AsyncImageBillingStatusPrepared},
		{"billing_pending", service.AsyncImageTaskStatusBillingPending, service.AsyncImageBillingStatusApplying},
		{"billing_failed", service.AsyncImageTaskStatusBillingFailed, service.AsyncImageBillingStatusFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now().UTC()
			taskID := "asyncimg_" + test.name
			task := &service.AsyncImageTask{
				TaskID: taskID, UserID: 42, APIKeyID: 7, GroupID: 8,
				Status: test.status, BillingStatus: test.billingStatus,
				SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
			}
			result := service.AsyncImageResult{
				TaskID: taskID, ImageIndex: 0, Provider: service.ImageStorageProviderAliyun,
				Bucket: "private", ObjectKey: "unreleased.png", ContentType: "image/png", CreatedAt: now,
			}
			tasks := &asyncImageTaskCenterServiceStub{
				tasks: []*service.AsyncImageTask{task}, total: 1,
				details: &service.AsyncImageTaskDetails{Task: task, Results: []service.AsyncImageResult{result}},
				results: map[string][]service.AsyncImageResult{taskID: {result}},
			}
			durable := &asyncImageDurableStorageStub{access: service.ObjectAccess{URL: "https://objects.example/unreleased.png"}}
			h := &AsyncImageTaskCenterHandler{
				tasks: tasks,
				storage: &asyncImageTaskStorageAccessStub{
					storage: durable, enabled: true,
					runtime: service.AsyncImageRuntimeConfig{SignedURLExpirySeconds: 600},
				},
			}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
				c.Next()
			})
			router.GET("/api/v1/user/async-image-tasks", h.ListForUser)
			router.GET("/api/v1/user/async-image-tasks/:task_id", h.GetForUser)
			router.GET("/api/v1/user/async-image-tasks/:task_id/results/:image_index/view", h.ViewResultForUser)

			for _, path := range []string{
				"/api/v1/user/async-image-tasks",
				"/api/v1/user/async-image-tasks/" + taskID,
			} {
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
				require.Equal(t, http.StatusOK, recorder.Code)
				require.NotContains(t, recorder.Body.String(), "view_url")
				require.NotContains(t, recorder.Body.String(), "preview_url")
				require.NotContains(t, recorder.Body.String(), "https://objects.example")
			}

			viewRecorder := httptest.NewRecorder()
			router.ServeHTTP(viewRecorder, httptest.NewRequest(http.MethodGet,
				"/api/v1/user/async-image-tasks/"+taskID+"/results/0/view", nil))
			require.Equal(t, http.StatusNotFound, viewRecorder.Code)
			require.Zero(t, durable.signCalls)
		})
	}
}

func TestAsyncImageTaskCenterAdminCanInspectUnreleasedResultOnAdminPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	task := &service.AsyncImageTask{
		TaskID: "asyncimg_admin_diagnostic", UserID: 42,
		Status: service.AsyncImageTaskStatusBillingFailed, BillingStatus: service.AsyncImageBillingStatusFailed,
		SubmittedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	result := service.AsyncImageResult{
		TaskID: task.TaskID, ImageIndex: 0, Provider: service.ImageStorageProviderTencent,
		Bucket: "diagnostic", ObjectKey: "result.png", ContentType: "image/png", CreatedAt: now,
	}
	tasks := &asyncImageTaskCenterServiceStub{details: &service.AsyncImageTaskDetails{Task: task, Results: []service.AsyncImageResult{result}}}
	durable := &asyncImageDurableStorageStub{access: service.ObjectAccess{URL: "https://objects.example/admin-signed.png"}}
	h := &AsyncImageTaskCenterHandler{
		tasks: tasks,
		storage: &asyncImageTaskStorageAccessStub{
			storage: durable, enabled: true,
			runtime: service.AsyncImageRuntimeConfig{SignedURLExpirySeconds: 600},
		},
	}
	router := gin.New()
	router.GET("/api/v1/admin/async-image-tasks/:task_id", h.GetForAdmin)
	router.GET("/api/v1/admin/async-image-tasks/:task_id/results/:image_index/view", h.ViewResultForAdmin)

	detailRecorder := httptest.NewRecorder()
	router.ServeHTTP(detailRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/async-image-tasks/"+task.TaskID, nil))
	require.Equal(t, http.StatusOK, detailRecorder.Code)
	require.Contains(t, detailRecorder.Body.String(), "/api/v1/admin/async-image-tasks/"+task.TaskID+"/results/0/view")

	viewRecorder := httptest.NewRecorder()
	router.ServeHTTP(viewRecorder, httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/async-image-tasks/"+task.TaskID+"/results/0/view", nil))
	require.Equal(t, http.StatusFound, viewRecorder.Code)
	require.Equal(t, "https://objects.example/admin-signed.png", viewRecorder.Header().Get("Location"))
}

func int64PointerForAsyncImageTaskTest(value int64) *int64 { return &value }

func stringPointerForAsyncImageTaskTest(value string) *string { return &value }
