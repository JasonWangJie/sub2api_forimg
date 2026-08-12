package handler

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ImageLibraryHandler struct {
	svc         *service.ImageLibraryService
	maintenance *service.ImageLibraryMaintenanceService
}

func NewImageLibraryHandler(svc *service.ImageLibraryService, maintenance *service.ImageLibraryMaintenanceService) *ImageLibraryHandler {
	return &ImageLibraryHandler{svc: svc, maintenance: maintenance}
}

type imageLibraryImportURLRequest struct {
	ImageURL       string `json:"image_url"`
	APIKeyID       *int64 `json:"api_key_id"`
	GroupID        *int64 `json:"group_id"`
	Platform       string `json:"platform"`
	GenerationMode string `json:"generation_mode"`
	SourceType     string `json:"source_type"`
	Model          string `json:"model"`
	RequestedSize  string `json:"requested_size"`
	ActualSize     string `json:"actual_size"`
	AspectRatio    string `json:"aspect_ratio"`
	Quality        string `json:"quality"`
	Title          string `json:"title"`
	Prompt         string `json:"prompt"`
}

type imageLibraryFromTaskRequest struct {
	TaskID     string `json:"task_id"`
	ImageIndex int    `json:"image_index"`
	Title      string `json:"title"`
}

type imageLibraryUpdateRequest struct {
	Title         *string `json:"title"`
	PrivatePrompt *string `json:"private_prompt"`
}

type imagePublicationRequest struct {
	PublicTitle  string  `json:"public_title"`
	SharePrompt  bool    `json:"share_prompt"`
	PublicPrompt *string `json:"public_prompt"`
}

type imageReportRequest struct {
	Reason  string `json:"reason"`
	Details string `json:"details"`
}

type imageSubmissionCreateRequest struct {
	Title          string  `json:"title"`
	PrivatePrompt  string  `json:"private_prompt"`
	PublicTitle    string  `json:"public_title"`
	SharePrompt    bool    `json:"share_prompt"`
	PublicPrompt   *string `json:"public_prompt"`
	Platform       string  `json:"platform"`
	GenerationMode string  `json:"generation_mode"`
	SourceType     string  `json:"source_type"`
	Model          string  `json:"model"`
	RequestedSize  string  `json:"requested_size"`
	AspectRatio    string  `json:"aspect_ratio"`
	Quality        string  `json:"quality"`
	ContentType    string  `json:"content_type"`
	ByteSize       int64   `json:"byte_size"`
	ChecksumSHA256 string  `json:"checksum_sha256"`
	ClientBlobKey  string  `json:"client_blob_key"`
	APIKeyID       *int64  `json:"api_key_id"`
	GroupID        *int64  `json:"group_id"`
}

type imageSubmissionReviewRequest struct {
	Reason string `json:"reason"`
}

type imageCursorPayload struct {
	Time time.Time `json:"t"`
	ID   int64     `json:"i"`
}

func (h *ImageLibraryHandler) List(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	cursor, ok := parseImageCursor(c)
	if !ok {
		return
	}
	limit := parseImageLimit(c)
	items, err := h.svc.ListForUser(c.Request.Context(), service.ImageLibraryListParams{
		UserID: subject.UserID, Visibility: c.Query("visibility"), SourceType: c.Query("source_type"),
		Platform: c.Query("platform"), Status: c.Query("status"), Query: c.Query("q"),
		Cursor: cursor, Limit: limit,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "next_cursor": nextLibraryCursor(items, limit)})
}

func (h *ImageLibraryHandler) Import(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	policy, err := h.svc.Policy(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, policy.MaxImageBytes+(1<<20))
	if err := c.Request.ParseMultipartForm(policy.MaxImageBytes + (1 << 20)); err != nil {
		response.ErrorFrom(c, apperrors.BadRequest("INVALID_MULTIPART_IMAGE", "invalid or oversized multipart image"))
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.ErrorFrom(c, apperrors.BadRequest("IMAGE_REQUIRED", "multipart file is required"))
		return
	}
	defer func() { _ = file.Close() }()
	data, err := readMultipartImage(file, policy.MaxImageBytes)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	input := imageLibraryImportFromForm(c, data, safeDeclaredImageType(header.Header.Get("Content-Type")))
	input.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	item, reused, err := h.svc.ImportBytes(c.Request.Context(), subject.UserID, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, gin.H{"item": item, "reused": reused})
}

func (h *ImageLibraryHandler) ImportURL(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	var req imageLibraryImportURLRequest
	if err := bindLimitedJSON(c, &req, 64<<10); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	item, reused, err := h.svc.ImportURL(c.Request.Context(), subject.UserID, req.ImageURL, service.ImageLibraryImportInput{
		APIKeyID: req.APIKeyID, GroupID: req.GroupID, Platform: req.Platform,
		GenerationMode: req.GenerationMode, SourceType: req.SourceType, Model: req.Model,
		RequestedSize: req.RequestedSize, ActualSize: req.ActualSize, AspectRatio: req.AspectRatio,
		Quality: req.Quality, Title: req.Title, Prompt: req.Prompt,
		IdempotencyKey: strings.TrimSpace(c.GetHeader("Idempotency-Key")),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, gin.H{"item": item, "reused": reused})
}

func (h *ImageLibraryHandler) FromTask(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	var req imageLibraryFromTaskRequest
	if err := bindLimitedJSON(c, &req, 16<<10); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	item, reused, err := h.svc.FromTask(c.Request.Context(), subject.UserID, req.TaskID, req.ImageIndex, req.Title)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, gin.H{"item": item, "reused": reused})
}

func (h *ImageLibraryHandler) Get(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	id, ok := imagePathIdentifier(c, "id", "img_")
	if !ok {
		return
	}
	item, err := h.svc.GetForUser(c.Request.Context(), subject.UserID, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func (h *ImageLibraryHandler) Update(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	id, ok := imagePathIdentifier(c, "id", "img_")
	if !ok {
		return
	}
	var req imageLibraryUpdateRequest
	if err := bindLimitedJSON(c, &req, 32<<10); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	item, err := h.svc.Update(c.Request.Context(), service.UpdateImageLibraryItemParams{UserID: subject.UserID, AssetID: id, Title: req.Title, PrivatePrompt: req.PrivatePrompt})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func (h *ImageLibraryHandler) Delete(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	id, ok := imagePathIdentifier(c, "id", "img_")
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), subject.UserID, id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *ImageLibraryHandler) View(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	id, ok := imagePathIdentifier(c, "id", "img_")
	if !ok {
		return
	}
	access, reader, contentType, err := h.svc.OpenUserObject(c.Request.Context(), subject.UserID, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if reader != nil {
		defer reader.Close()
		writeImageObjectStream(c, reader, contentType)
		return
	}
	writeImageObjectAccess(c, access)
}

func (h *ImageLibraryHandler) Publish(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	id, ok := imagePathIdentifier(c, "id", "img_")
	if !ok {
		return
	}
	var req imagePublicationRequest
	if err := bindLimitedJSON(c, &req, 32<<10); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	publication, err := h.svc.Publish(c.Request.Context(), service.CreateImagePublicationParams{
		UserID: subject.UserID, AssetID: id, PublicTitle: req.PublicTitle,
		SharePrompt: req.SharePrompt, PublicPrompt: req.PublicPrompt,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, publication)
}

func (h *ImageLibraryHandler) CreateSubmissionRequest(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	var req imageSubmissionCreateRequest
	if err := bindLimitedJSON(c, &req, 64<<10); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	item, reused, err := h.svc.CreateSubmissionRequest(c.Request.Context(), service.CreateImagePlazaSubmissionParams{
		UserID: subject.UserID, Title: req.Title, PrivatePrompt: req.PrivatePrompt,
		PublicTitle: req.PublicTitle, PublicPrompt: req.PublicPrompt, SharePrompt: req.SharePrompt,
		Platform: req.Platform, GenerationMode: req.GenerationMode, SourceType: req.SourceType,
		Model: req.Model, RequestedSize: req.RequestedSize, AspectRatio: req.AspectRatio, Quality: req.Quality,
		ContentType: req.ContentType, ByteSize: req.ByteSize, ChecksumSHA256: req.ChecksumSHA256,
		ClientBlobKey: req.ClientBlobKey, APIKeyID: req.APIKeyID, GroupID: req.GroupID,
		IdempotencyKey: strings.TrimSpace(c.GetHeader("Idempotency-Key")),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if reused {
		response.Success(c, gin.H{"item": item, "reused": true})
		return
	}
	response.Created(c, gin.H{"item": item, "reused": false})
}

func (h *ImageLibraryHandler) ListMySubmissionRequests(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	cursor, ok := parseImageCursor(c)
	if !ok {
		return
	}
	limit := parseImageLimit(c)
	items, err := h.svc.ListMySubmissionRequests(c.Request.Context(), subject.UserID, c.Query("status"), cursor, limit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "next_cursor": nextSubmissionCursor(items, limit)})
}

func (h *ImageLibraryHandler) WithdrawSubmissionRequest(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	id, ok := imagePathIdentifier(c, "request_id", "imgsub_")
	if !ok {
		return
	}
	if err := h.svc.WithdrawSubmissionRequest(c.Request.Context(), subject.UserID, id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"withdrawn": true})
}

func (h *ImageLibraryHandler) SyncSubmissionRequest(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	id, ok := imagePathIdentifier(c, "request_id", "imgsub_")
	if !ok {
		return
	}
	policy, err := h.svc.Policy(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, policy.MaxImageBytes+(1<<20))
	if err := c.Request.ParseMultipartForm(policy.MaxImageBytes + (1 << 20)); err != nil {
		response.ErrorFrom(c, apperrors.BadRequest("INVALID_MULTIPART_IMAGE", "invalid or oversized multipart image"))
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.ErrorFrom(c, apperrors.BadRequest("IMAGE_REQUIRED", "image file is required"))
		return
	}
	defer func() { _ = file.Close() }()
	data, err := readMultipartImage(file, policy.MaxImageBytes)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	item, libraryItem, err := h.svc.SyncSubmissionRequest(c.Request.Context(), service.SyncImagePlazaSubmissionParams{
		UserID: subject.UserID, RequestID: id, ImageData: data, MIMEType: safeDeclaredImageType(header.Header.Get("Content-Type")),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"item": item, "library_item": libraryItem})
}

func (h *ImageLibraryHandler) AdminListSubmissionRequests(c *gin.Context) {
	cursor, ok := parseImageCursor(c)
	if !ok {
		return
	}
	limit := parseImageLimit(c)
	items, err := h.svc.ListSubmissionRequestsAdmin(c.Request.Context(), c.Query("status"), cursor, limit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "next_cursor": nextSubmissionCursor(items, limit)})
}

func (h *ImageLibraryHandler) AdminTransitionSubmissionRequest(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	id, ok := imagePathIdentifier(c, "request_id", "imgsub_")
	if !ok {
		return
	}
	action := strings.ToLower(strings.TrimSpace(c.Param("action")))
	var req imageSubmissionReviewRequest
	if c.Request.ContentLength != 0 {
		if err := bindLimitedJSON(c, &req, 16<<10); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	item, err := h.svc.TransitionSubmissionRequest(c.Request.Context(), subject.UserID, id, action, req.Reason)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func (h *ImageLibraryHandler) Withdraw(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	id, ok := imagePathIdentifier(c, "id", "img_")
	if !ok {
		return
	}
	if err := h.svc.Withdraw(c.Request.Context(), subject.UserID, id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"withdrawn": true})
}

func (h *ImageLibraryHandler) Report(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	id, ok := imagePathIdentifier(c, "publication_id", "imgpub_")
	if !ok {
		return
	}
	var req imageReportRequest
	if err := bindLimitedJSON(c, &req, 16<<10); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	report, err := h.svc.Report(c.Request.Context(), subject.UserID, id, req.Reason, req.Details)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, gin.H{"id": report.ID, "publication_id": report.PublicationID, "status": report.Status, "created_at": report.CreatedAt})
}

func (h *ImageLibraryHandler) AdminListLibrary(c *gin.Context) {
	cursor, ok := parseImageCursor(c)
	if !ok {
		return
	}
	userID, _ := strconv.ParseInt(c.Query("user_id"), 10, 64)
	limit := parseImageLimit(c)
	items, err := h.svc.AdminListLibrary(c.Request.Context(), service.ImageLibraryListParams{
		UserID: userID, Visibility: c.Query("visibility"), SourceType: c.Query("source_type"),
		Platform: c.Query("platform"), Status: c.Query("status"), Query: c.Query("q"),
		Cursor: cursor, Limit: limit,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "next_cursor": nextLibraryCursor(items, limit)})
}

func (h *ImageLibraryHandler) AdminStats(c *gin.Context) {
	stats, err := h.svc.AdminStats(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, stats)
}

func (h *ImageLibraryHandler) AdminView(c *gin.Context) {
	id, ok := imagePathIdentifier(c, "id", "img_")
	if !ok {
		return
	}
	access, reader, contentType, err := h.svc.OpenAdminObject(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if reader != nil {
		defer reader.Close()
		writeImageObjectStream(c, reader, contentType)
		return
	}
	writeImageObjectAccess(c, access)
}

func (h *ImageLibraryHandler) AdminListPublications(c *gin.Context) {
	cursor, ok := parseImageCursor(c)
	if !ok {
		return
	}
	limit := parseImageLimit(c)
	var userID *int64
	if value, err := strconv.ParseInt(c.Query("user_id"), 10, 64); err == nil && value > 0 {
		userID = &value
	}
	items, err := h.svc.AdminListPublications(c.Request.Context(), service.ImagePublicationListParams{
		UserID: userID, Status: c.Query("status"), Platform: c.Query("platform"),
		Model: c.Query("model"), AspectRatio: c.Query("aspect_ratio"),
		Query: c.Query("q"), Sort: c.Query("sort"), Cursor: cursor, Limit: limit,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "next_cursor": nextAdminPublicationCursor(items, limit)})
}

func (h *ImageLibraryHandler) AdminViewPublication(c *gin.Context) {
	id, ok := imagePathIdentifier(c, "publication_id", "imgpub_")
	if !ok {
		return
	}
	access, reader, contentType, err := h.svc.OpenAdminPublicationObject(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if reader != nil {
		defer reader.Close()
		writeImageObjectStream(c, reader, contentType)
		return
	}
	writeImageObjectAccess(c, access)
}

func (h *ImageLibraryHandler) AdminTransitionPublication(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	id, ok := imagePathIdentifier(c, "publication_id", "imgpub_")
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if c.Request.ContentLength != 0 {
		if err := bindLimitedJSON(c, &req, 16<<10); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	publication, err := h.svc.AdminTransition(c.Request.Context(), subject.UserID, id, c.Param("action"), req.Reason)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, publication)
}

func (h *ImageLibraryHandler) AdminBatchTransitionPublications(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	var req struct {
		PublicationIDs []string `json:"publication_ids"`
		Action         string   `json:"action"`
		Reason         string   `json:"reason"`
	}
	if err := bindLimitedJSON(c, &req, 64<<10); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	result, err := h.svc.AdminBatchTransition(c.Request.Context(), subject.UserID, req.PublicationIDs, req.Action, req.Reason)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *ImageLibraryHandler) AdminListReports(c *gin.Context) {
	cursor, ok := parseImageCursor(c)
	if !ok {
		return
	}
	limit := parseImageLimit(c)
	reports, err := h.svc.AdminListReports(c.Request.Context(), c.Query("status"), cursor, limit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": reports, "next_cursor": nextReportCursor(reports, limit)})
}

func (h *ImageLibraryHandler) AdminResolveReport(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	id, ok := imagePathID(c, "report_id")
	if !ok {
		return
	}
	var req struct {
		Status     string `json:"status"`
		Resolution string `json:"resolution"`
	}
	if err := bindLimitedJSON(c, &req, 16<<10); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	report, err := h.svc.AdminResolveReport(c.Request.Context(), subject.UserID, id, req.Status, req.Resolution)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, report)
}

func (h *ImageLibraryHandler) AdminListCleanupJobs(c *gin.Context) {
	jobs, err := h.svc.AdminListCleanupJobs(c.Request.Context(), parseImageLimit(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": jobs})
}

func (h *ImageLibraryHandler) AdminCreateCleanupJob(c *gin.Context) {
	subject, ok := imageLibrarySubject(c)
	if !ok {
		return
	}
	var req struct {
		Scope   string          `json:"scope"`
		Filters json.RawMessage `json:"filters"`
	}
	if err := bindLimitedJSON(c, &req, 32<<10); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	job, err := h.svc.AdminCreateCleanupJob(c.Request.Context(), subject.UserID, req.Scope, req.Filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, job)
}

func (h *ImageLibraryHandler) AdminPreviewCleanup(c *gin.Context) {
	var req struct {
		Scope   string          `json:"scope"`
		Filters json.RawMessage `json:"filters"`
	}
	if err := bindLimitedJSON(c, &req, 32<<10); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	preview, err := h.svc.AdminPreviewCleanup(c.Request.Context(), req.Scope, req.Filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, preview)
}

func (h *ImageLibraryHandler) AdminMigrationState(c *gin.Context) {
	if h == nil || h.maintenance == nil {
		response.ErrorFrom(c, apperrors.ServiceUnavailable("IMAGE_LIBRARY_MAINTENANCE_UNAVAILABLE", "image library maintenance is unavailable"))
		return
	}
	state, err := h.maintenance.MigrationState(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}

func imageLibrarySubject(c *gin.Context) (middleware2.AuthSubject, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not found in context")
		return middleware2.AuthSubject{}, false
	}
	return subject, true
}

func imagePathID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid id")
		return 0, false
	}
	return id, true
}

func imagePathIdentifier(c *gin.Context, name, prefix string) (string, bool) {
	value := strings.TrimSpace(c.Param(name))
	if !strings.HasPrefix(value, prefix) || len(value) > 64 {
		response.BadRequest(c, "invalid id")
		return "", false
	}
	for _, r := range value[len(prefix):] {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		response.BadRequest(c, "invalid id")
		return "", false
	}
	return value, true
}

func bindLimitedJSON(c *gin.Context, dst any, max int64) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return apperrors.BadRequest("INVALID_REQUEST", "invalid request body")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return apperrors.BadRequest("INVALID_REQUEST", "request body must contain one JSON object")
	}
	return nil
}

func readMultipartImage(file multipart.File, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, apperrors.BadRequest("INVALID_IMAGE", "failed to read image")
	}
	if int64(len(data)) > max {
		return nil, apperrors.BadRequest("IMAGE_TOO_LARGE", "image exceeds the configured byte limit")
	}
	return data, nil
}

func imageLibraryImportFromForm(c *gin.Context, data []byte, declared string) service.ImageLibraryImportInput {
	return service.ImageLibraryImportInput{
		APIKeyID: optionalFormInt64(c.PostForm("api_key_id")), GroupID: optionalFormInt64(c.PostForm("group_id")),
		Platform: c.PostForm("platform"), GenerationMode: c.PostForm("generation_mode"),
		SourceType: c.PostForm("source_type"), Model: c.PostForm("model"),
		RequestedSize: c.PostForm("requested_size"), ActualSize: c.PostForm("actual_size"),
		AspectRatio: c.PostForm("aspect_ratio"), Quality: c.PostForm("quality"),
		Title: c.PostForm("title"), Prompt: c.PostForm("prompt"), ImageData: data, DeclaredMIME: declared,
	}
}

func optionalFormInt64(raw string) *int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value <= 0 {
		return nil
	}
	return &value
}

func safeDeclaredImageType(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	if value == "application/octet-stream" {
		return ""
	}
	return value
}

func parseImageLimit(c *gin.Context) int {
	value, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	if value <= 0 {
		return 30
	}
	if value > 100 {
		return 100
	}
	return value
}

func parseImageCursor(c *gin.Context) (*service.ImageLibraryCursor, bool) {
	raw := strings.TrimSpace(c.Query("cursor"))
	if raw == "" {
		return nil, true
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		response.BadRequest(c, "invalid cursor")
		return nil, false
	}
	var payload imageCursorPayload
	if json.Unmarshal(data, &payload) != nil || payload.ID <= 0 || payload.Time.IsZero() {
		response.BadRequest(c, "invalid cursor")
		return nil, false
	}
	return &service.ImageLibraryCursor{CreatedAt: payload.Time, ID: payload.ID}, true
}

func encodeImageCursor(createdAt time.Time, id int64) string {
	data, _ := json.Marshal(imageCursorPayload{Time: createdAt, ID: id})
	return base64.RawURLEncoding.EncodeToString(data)
}

func nextLibraryCursor(items []service.ImageLibraryItem, limit int) string {
	if len(items) < limit || len(items) == 0 {
		return ""
	}
	last := items[len(items)-1]
	return encodeImageCursor(last.CreatedAt, last.ID)
}

func nextSubmissionCursor(items []service.ImagePlazaSubmissionRequest, limit int) string {
	if len(items) < limit || len(items) == 0 {
		return ""
	}
	last := items[len(items)-1]
	return encodeImageCursor(last.CreatedAt, last.ID)
}

func nextPublicationCursor(items []service.PublicImagePlazaItem, limit int) string {
	if len(items) < limit || len(items) == 0 {
		return ""
	}
	last := items[len(items)-1]
	return encodeImageCursor(last.PublishedAt, last.PublicationPK)
}

func nextAdminPublicationCursor(items []service.AdminImagePlazaPublication, limit int) string {
	if len(items) < limit || len(items) == 0 {
		return ""
	}
	last := items[len(items)-1]
	return encodeImageCursor(last.CreatedAt, last.PublicationPK)
}

func nextReportCursor(items []service.ImagePlazaReport, limit int) string {
	if len(items) < limit || len(items) == 0 {
		return ""
	}
	last := items[len(items)-1]
	return encodeImageCursor(last.CreatedAt, last.ID)
}

func redirectToImageObject(c *gin.Context, access service.ObjectAccess) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Redirect(http.StatusTemporaryRedirect, access.URL)
}

func writeImageObjectAccess(c *gin.Context, access service.ObjectAccess) {
	if strings.Contains(strings.ToLower(c.GetHeader("Accept")), "application/json") {
		payload := gin.H{"url": access.URL}
		if !access.ExpiresAt.IsZero() {
			payload["expires_at"] = access.ExpiresAt
		}
		response.Success(c, payload)
		return
	}
	redirectToImageObject(c, access)
}

func writeImageObjectStream(c *gin.Context, reader io.Reader, contentType string) {
	if strings.Contains(strings.ToLower(c.GetHeader("Accept")), "application/json") {
		// Local objects have no public CDN URL; return the same view endpoint for JSON clients.
		response.Success(c, gin.H{"url": c.Request.URL.Path})
		return
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Content-Type-Options", "nosniff")
	c.DataFromReader(http.StatusOK, -1, contentType, reader, nil)
}
