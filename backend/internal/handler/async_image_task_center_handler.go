package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/gin-gonic/gin"
)

type asyncImageTaskCenterService interface {
	ListForUser(context.Context, int64, service.AsyncImageTaskFilter) ([]*service.AsyncImageTask, int64, error)
	ListForAdmin(context.Context, service.AsyncImageTaskFilter) ([]*service.AsyncImageTask, int64, error)
	GetForUser(context.Context, int64, string) (*service.AsyncImageTaskDetails, error)
	GetForAdmin(context.Context, string) (*service.AsyncImageTaskDetails, error)
	ListResults(context.Context, string) ([]service.AsyncImageResult, error)
	ResumePostProcessing(context.Context, string) (*service.AsyncImageTaskDetails, error)
}

type asyncImageTaskStorageAccess interface {
	DurableStorage(context.Context) (service.DurableImageStorage, bool, error)
	RuntimeConfig(context.Context) (service.AsyncImageRuntimeConfig, error)
}

// AsyncImageTaskCenterHandler serves the authenticated site task center. It is
// intentionally separate from the BB/SC downstream protocol handlers.
type AsyncImageTaskCenterHandler struct {
	tasks   asyncImageTaskCenterService
	storage asyncImageTaskStorageAccess
}

func NewAsyncImageTaskCenterHandler(
	tasks *service.AsyncImageTaskService,
	storage *service.ImageStorageSettingService,
) *AsyncImageTaskCenterHandler {
	return &AsyncImageTaskCenterHandler{tasks: tasks, storage: storage}
}

func (h *AsyncImageTaskCenterHandler) ListForUser(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not found in context")
		return
	}
	filter, page, pageSize, ok := parseAsyncImageTaskCenterFilter(c, false)
	if !ok {
		return
	}
	tasks, total, err := h.tasks.ListForUser(c.Request.Context(), subject.UserID, filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items, err := h.listViews(c.Request.Context(), tasks, false)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, pageSize)
}

func (h *AsyncImageTaskCenterHandler) GetForUser(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not found in context")
		return
	}
	details, err := h.tasks.GetForUser(c.Request.Context(), subject.UserID, strings.TrimSpace(c.Param("task_id")))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out, err := h.detailView(c.Request.Context(), details, false)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

func (h *AsyncImageTaskCenterHandler) ViewResultForUser(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not found in context")
		return
	}
	details, err := h.tasks.GetForUser(c.Request.Context(), subject.UserID, strings.TrimSpace(c.Param("task_id")))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.redirectResult(c, details, false)
}

func (h *AsyncImageTaskCenterHandler) ListForAdmin(c *gin.Context) {
	filter, page, pageSize, ok := parseAsyncImageTaskCenterFilter(c, true)
	if !ok {
		return
	}
	tasks, total, err := h.tasks.ListForAdmin(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items, err := h.listViews(c.Request.Context(), tasks, true)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, pageSize)
}

func (h *AsyncImageTaskCenterHandler) GetForAdmin(c *gin.Context) {
	details, err := h.tasks.GetForAdmin(c.Request.Context(), strings.TrimSpace(c.Param("task_id")))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out, err := h.detailView(c.Request.Context(), details, true)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

func (h *AsyncImageTaskCenterHandler) ViewResultForAdmin(c *gin.Context) {
	details, err := h.tasks.GetForAdmin(c.Request.Context(), strings.TrimSpace(c.Param("task_id")))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.redirectResult(c, details, true)
}

// ResumePostProcessing never creates a new generation and never permits an
// execution_unknown/failed task to be retried. The service writes a durable
// post-processing-only outbox record before moving the task state.
func (h *AsyncImageTaskCenterHandler) ResumePostProcessing(c *gin.Context) {
	taskID := strings.TrimSpace(c.Param("task_id"))
	details, err := h.tasks.GetForAdmin(c.Request.Context(), taskID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if details == nil || details.Task == nil || (details.Task.Status != service.AsyncImageTaskStatusStorageFailed && details.Task.Status != service.AsyncImageTaskStatusBillingFailed) {
		response.ErrorFrom(c, service.ErrAsyncImagePostProcessingResumeNotAllowed)
		return
	}
	details, err = h.tasks.ResumePostProcessing(c.Request.Context(), taskID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out, err := h.detailView(c.Request.Context(), details, true)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

type asyncImageTaskCenterView struct {
	ID                  string     `json:"id"`
	TaskID              string     `json:"task_id"`
	UserID              *int64     `json:"user_id,omitempty"`
	APIKeyID            int64      `json:"api_key_id"`
	GroupID             int64      `json:"group_id"`
	AccountID           *int64     `json:"account_id,omitempty"`
	Protocol            string     `json:"protocol"`
	Platform            string     `json:"platform"`
	RequestType         string     `json:"request_type"`
	Model               string     `json:"model"`
	Status              string     `json:"status"`
	BillingStatus       string     `json:"billing_status"`
	Progress            int        `json:"progress"`
	RequestedSize       *string    `json:"requested_size,omitempty"`
	RequestedImageSize  *string    `json:"requested_image_size,omitempty"`
	ActualSize          *string    `json:"actual_size,omitempty"`
	ActualImageSize     *string    `json:"actual_image_size,omitempty"`
	AspectRatio         *string    `json:"aspect_ratio,omitempty"`
	ImageCount          int        `json:"image_count"`
	ResultCount         int        `json:"result_count"`
	StorageProvider     string     `json:"storage_provider,omitempty"`
	PreviewURL          string     `json:"preview_url,omitempty"`
	ViewURL             string     `json:"view_url,omitempty"`
	ActualCost          *float64   `json:"actual_cost,omitempty"`
	Currency            string     `json:"currency"`
	PromptSummary       *string    `json:"prompt_summary,omitempty"`
	PromptPreview       *string    `json:"prompt_preview,omitempty"`
	UpstreamRequestID   *string    `json:"upstream_request_id,omitempty"`
	RetryCount          int        `json:"retry_count"`
	ErrorCode           *string    `json:"error_code,omitempty"`
	ErrorMessage        *string    `json:"error_message,omitempty"`
	CanResume           bool       `json:"can_resume"`
	DurationMS          *int64     `json:"duration_ms,omitempty"`
	SubmittedAt         time.Time  `json:"submitted_at"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	UpstreamSucceededAt *time.Time `json:"upstream_succeeded_at,omitempty"`
	FinishedAt          *time.Time `json:"finished_at,omitempty"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type asyncImageTaskResultView struct {
	ID          string     `json:"id"`
	ImageIndex  int        `json:"image_index"`
	Index       int        `json:"index"`
	Provider    string     `json:"provider"`
	ContentType string     `json:"content_type"`
	ByteSize    int64      `json:"byte_size"`
	SizeBytes   int64      `json:"size_bytes"`
	Checksum    string     `json:"checksum"`
	Width       *int       `json:"width,omitempty"`
	Height      *int       `json:"height,omitempty"`
	URL         string     `json:"url,omitempty"`
	PreviewURL  string     `json:"preview_url,omitempty"`
	ViewURL     string     `json:"view_url,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type asyncImageTaskEventView struct {
	ID         int64     `json:"id"`
	EventType  string    `json:"event_type"`
	Status     string    `json:"status"`
	FromStatus *string   `json:"from_status,omitempty"`
	ToStatus   *string   `json:"to_status,omitempty"`
	Message    string    `json:"message,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type asyncImageTaskDetailsView struct {
	Task    asyncImageTaskCenterView   `json:"task"`
	Results []asyncImageTaskResultView `json:"results"`
	Events  []asyncImageTaskEventView  `json:"events"`
}

func (h *AsyncImageTaskCenterHandler) listViews(ctx context.Context, tasks []*service.AsyncImageTask, admin bool) ([]asyncImageTaskCenterView, error) {
	out := make([]asyncImageTaskCenterView, 0, len(tasks))
	for _, task := range tasks {
		results, err := h.tasks.ListResults(ctx, task.TaskID)
		if err != nil {
			return nil, err
		}
		view := newAsyncImageTaskCenterView(task, results, admin)
		if len(results) > 0 {
			previews, err := h.resultViews(ctx, task, results[:1], admin)
			if err != nil {
				return nil, err
			}
			if len(previews) > 0 {
				view.PreviewURL = previews[0].PreviewURL
				view.ViewURL = previews[0].ViewURL
			}
		}
		out = append(out, view)
	}
	return out, nil
}

func (h *AsyncImageTaskCenterHandler) detailView(ctx context.Context, details *service.AsyncImageTaskDetails, admin bool) (*asyncImageTaskDetailsView, error) {
	if details == nil || details.Task == nil {
		return nil, service.ErrAsyncImageTaskNotFound
	}
	results, err := h.resultViews(ctx, details.Task, details.Results, admin)
	if err != nil {
		return nil, err
	}
	events := make([]asyncImageTaskEventView, 0, len(details.Events))
	for _, event := range details.Events {
		status := event.EventType
		if event.ToStatus != nil && strings.TrimSpace(*event.ToStatus) != "" {
			status = *event.ToStatus
		}
		events = append(events, asyncImageTaskEventView{
			ID: event.ID, EventType: event.EventType, Status: status,
			FromStatus: event.FromStatus, ToStatus: event.ToStatus,
			Message: asyncImageEventMessage(event.Payload), CreatedAt: event.CreatedAt,
		})
	}
	return &asyncImageTaskDetailsView{
		Task:    newAsyncImageTaskCenterView(details.Task, details.Results, admin),
		Results: results,
		Events:  events,
	}, nil
}

func newAsyncImageTaskCenterView(task *service.AsyncImageTask, results []service.AsyncImageResult, admin bool) asyncImageTaskCenterView {
	provider := ""
	if len(results) > 0 {
		provider = results[0].Provider
	}
	var userID *int64
	var accountID *int64
	if admin {
		value := task.UserID
		userID = &value
		accountID = task.AccountID
	}
	var durationMS *int64
	if task.FinishedAt != nil {
		value := task.FinishedAt.Sub(task.SubmittedAt).Milliseconds()
		if value >= 0 {
			durationMS = &value
		}
	}
	errorMessage := redactAsyncImageTaskText(task.ErrorMessage)
	promptPreview := redactAsyncImageTaskText(task.PromptPreview)
	return asyncImageTaskCenterView{
		ID: task.TaskID, TaskID: task.TaskID, UserID: userID,
		APIKeyID: task.APIKeyID, GroupID: task.GroupID, AccountID: accountID,
		Protocol: task.Protocol, Platform: task.Platform, RequestType: task.RequestType,
		Model: task.Model, Status: task.Status, BillingStatus: task.BillingStatus, Progress: task.Progress,
		RequestedSize: task.RequestedImageSize, RequestedImageSize: task.RequestedImageSize,
		ActualSize: task.ActualImageSize, ActualImageSize: task.ActualImageSize,
		AspectRatio: task.AspectRatio, ImageCount: task.ImageCount, ResultCount: len(results),
		StorageProvider: provider, ActualCost: task.ActualCost, Currency: task.Currency,
		PromptSummary: promptPreview, PromptPreview: promptPreview,
		UpstreamRequestID: task.UpstreamRequestID, RetryCount: task.RetryCount,
		ErrorCode: task.ErrorCode, ErrorMessage: errorMessage,
		CanResume:  admin && (task.Status == service.AsyncImageTaskStatusStorageFailed || task.Status == service.AsyncImageTaskStatusBillingFailed),
		DurationMS: durationMS, SubmittedAt: task.SubmittedAt, StartedAt: task.StartedAt,
		UpstreamSucceededAt: task.UpstreamSucceededAt, FinishedAt: task.FinishedAt,
		ExpiresAt: task.ExpiresAt, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	}
}

func (h *AsyncImageTaskCenterHandler) resultViews(ctx context.Context, task *service.AsyncImageTask, results []service.AsyncImageResult, admin bool) ([]asyncImageTaskResultView, error) {
	out := make([]asyncImageTaskResultView, 0, len(results))
	taskID := ""
	if task != nil {
		taskID = task.TaskID
	}
	allowResultAccess := admin || asyncImageResultsReleasable(task)
	var storage service.DurableImageStorage
	var expiry time.Duration
	if allowResultAccess && len(results) > 0 && h.storage != nil {
		resolved, enabled, err := h.storage.DurableStorage(ctx)
		if err == nil && enabled {
			storage = resolved
			runtime, err := h.storage.RuntimeConfig(ctx)
			if err == nil {
				expiry = time.Duration(runtime.SignedURLExpirySeconds) * time.Second
			} else {
				storage = nil
			}
		}
	}
	for _, result := range results {
		view := asyncImageTaskResultView{
			ID:         fmt.Sprintf("%s:%d", taskID, result.ImageIndex),
			ImageIndex: result.ImageIndex, Index: result.ImageIndex, Provider: result.Provider,
			ContentType: result.ContentType, ByteSize: result.ByteSize, SizeBytes: result.ByteSize,
			Checksum: result.Checksum, Width: result.Width, Height: result.Height,
			CreatedAt: result.CreatedAt,
		}
		if allowResultAccess {
			view.ViewURL = asyncImageTaskResultViewPath(admin, taskID, result.ImageIndex)
		}
		if storage != nil {
			access, err := storage.SignURL(ctx, objectRefFromAsyncImageResult(result), expiry)
			if err == nil && validateAsyncImageAccessURL(access.URL) == nil {
				view.URL = access.URL
				view.PreviewURL = access.URL
				if !access.ExpiresAt.IsZero() {
					expiresAt := access.ExpiresAt
					view.ExpiresAt = &expiresAt
				}
			}
		}
		out = append(out, view)
	}
	return out, nil
}

func (h *AsyncImageTaskCenterHandler) redirectResult(c *gin.Context, details *service.AsyncImageTaskDetails, admin bool) {
	if details == nil || details.Task == nil || (!admin && !asyncImageResultsReleasable(details.Task)) {
		response.NotFound(c, "Image result not found")
		return
	}
	index, err := strconv.Atoi(strings.TrimSpace(c.Param("image_index")))
	if err != nil || index < 0 {
		response.BadRequest(c, "Invalid image index")
		return
	}
	var result *service.AsyncImageResult
	for i := range details.Results {
		if details.Results[i].ImageIndex == index {
			result = &details.Results[i]
			break
		}
	}
	if result == nil {
		response.NotFound(c, "Image result not found")
		return
	}
	if h.storage == nil {
		response.InternalError(c, "Image storage is unavailable")
		return
	}
	storage, enabled, err := h.storage.DurableStorage(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !enabled || storage == nil {
		response.NotFound(c, "Image result is unavailable")
		return
	}
	runtime, err := h.storage.RuntimeConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	access, err := storage.SignURL(c.Request.Context(), objectRefFromAsyncImageResult(*result), time.Duration(runtime.SignedURLExpirySeconds)*time.Second)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := validateAsyncImageAccessURL(access.URL); err != nil {
		response.InternalError(c, "Image storage returned an invalid access URL")
		return
	}
	c.Header("Cache-Control", "private, no-store")
	if strings.Contains(strings.ToLower(c.GetHeader("Accept")), "application/json") {
		response.Success(c, access)
		return
	}
	c.Redirect(http.StatusFound, access.URL)
}

func objectRefFromAsyncImageResult(result service.AsyncImageResult) service.ObjectRef {
	ref := service.ObjectRef{
		Provider: result.Provider, Bucket: result.Bucket, ObjectKey: result.ObjectKey,
		ContentType: result.ContentType, SizeBytes: result.ByteSize, ChecksumSHA256: result.Checksum,
	}
	if result.Width != nil {
		ref.Width = *result.Width
	}
	if result.Height != nil {
		ref.Height = *result.Height
	}
	return ref
}

func asyncImageTaskResultViewPath(admin bool, taskID string, imageIndex int) string {
	scope := "user"
	if admin {
		scope = "admin"
	}
	return fmt.Sprintf("/api/v1/%s/async-image-tasks/%s/results/%d/view", scope, url.PathEscape(taskID), imageIndex)
}

func validateAsyncImageAccessURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
		return fmt.Errorf("invalid image storage access URL")
	}
	return nil
}

func asyncImageEventMessage(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var value struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return ""
	}
	message := value.Message
	if message == "" {
		message = value.Error
	}
	return truncateAsyncImageTaskText(logredact.RedactText(message), 500)
}

func redactAsyncImageTaskText(value *string) *string {
	if value == nil {
		return nil
	}
	redacted := truncateAsyncImageTaskText(logredact.RedactText(*value), 500)
	if redacted == "" {
		return nil
	}
	return &redacted
}

func truncateAsyncImageTaskText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func parseAsyncImageTaskCenterFilter(c *gin.Context, admin bool) (service.AsyncImageTaskFilter, int, int, bool) {
	page, pageSize := response.ParsePagination(c)
	if pageSize > 100 {
		pageSize = 100
	}
	filter := service.AsyncImageTaskFilter{
		TaskID: strings.TrimSpace(c.Query("task_id")), Protocol: strings.TrimSpace(c.Query("protocol")),
		Platform: strings.TrimSpace(c.Query("platform")), RequestType: strings.TrimSpace(c.Query("request_type")),
		Status: strings.TrimSpace(c.Query("status")), BillingStatus: strings.TrimSpace(c.Query("billing_status")),
		Model: strings.TrimSpace(c.Query("model")), Search: strings.TrimSpace(c.Query("q")),
		StorageProvider: strings.TrimSpace(c.Query("storage_provider")),
		SortBy:          strings.TrimSpace(c.Query("sort_by")), SortOrder: strings.TrimSpace(c.Query("sort_order")),
		Limit: pageSize, Offset: (page - 1) * pageSize,
	}
	if filter.Status != "" && !service.IsAsyncImageTaskStatus(strings.ToLower(filter.Status)) {
		response.BadRequest(c, "Invalid task status")
		return service.AsyncImageTaskFilter{}, 0, 0, false
	}
	if filter.Platform != "" && filter.Platform != service.PlatformGemini && filter.Platform != service.PlatformOpenAI {
		response.BadRequest(c, "Invalid task platform")
		return service.AsyncImageTaskFilter{}, 0, 0, false
	}
	if filter.RequestType != "" && filter.RequestType != service.AsyncImageRequestTypeTextToImage && filter.RequestType != service.AsyncImageRequestTypeImageToImage {
		response.BadRequest(c, "Invalid request type")
		return service.AsyncImageTaskFilter{}, 0, 0, false
	}
	if filter.SortOrder != "" && filter.SortOrder != "asc" && filter.SortOrder != "desc" {
		response.BadRequest(c, "Invalid sort order")
		return service.AsyncImageTaskFilter{}, 0, 0, false
	}

	var ok bool
	if filter.APIKeyID, ok = parseAsyncImageTaskIDFilter(c, "api_key_id"); !ok {
		return service.AsyncImageTaskFilter{}, 0, 0, false
	}
	if filter.GroupID, ok = parseAsyncImageTaskIDFilter(c, "group_id"); !ok {
		return service.AsyncImageTaskFilter{}, 0, 0, false
	}
	if filter.AccountID, ok = parseAsyncImageTaskIDFilter(c, "account_id"); !ok {
		return service.AsyncImageTaskFilter{}, 0, 0, false
	}
	if !admin && filter.AccountID != nil {
		response.Forbidden(c, "account_id is only available to administrators")
		return service.AsyncImageTaskFilter{}, 0, 0, false
	}
	if admin {
		if filter.UserID, ok = parseAsyncImageTaskIDFilter(c, "user_id"); !ok {
			return service.AsyncImageTaskFilter{}, 0, 0, false
		}
	}

	userTZ := strings.TrimSpace(c.Query("timezone"))
	if value := strings.TrimSpace(c.Query("start_date")); value != "" {
		parsed, err := timezone.ParseInUserLocation("2006-01-02", value, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid start_date format, use YYYY-MM-DD")
			return service.AsyncImageTaskFilter{}, 0, 0, false
		}
		filter.CreatedAfter = &parsed
	}
	if value := strings.TrimSpace(c.Query("end_date")); value != "" {
		parsed, err := timezone.ParseInUserLocation("2006-01-02", value, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid end_date format, use YYYY-MM-DD")
			return service.AsyncImageTaskFilter{}, 0, 0, false
		}
		parsed = parsed.AddDate(0, 0, 1)
		filter.CreatedBefore = &parsed
	}
	return filter, page, pageSize, true
}

func parseAsyncImageTaskIDFilter(c *gin.Context, name string) (*int64, bool) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		response.BadRequest(c, "Invalid "+name)
		return nil, false
	}
	return &value, true
}
