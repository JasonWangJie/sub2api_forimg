package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/gin-gonic/gin"
)

const (
	asyncImageBBTaskPath          = "/v1/images/tasks_async/"
	asyncImageSCMultipartOverhead = int64(1 << 20)
	asyncImageMaxSignedUploadBody = int64(1<<63 - 1)
)

type durableAsyncImagePayload struct {
	SourcePath  string                               `json:"source_path"`
	ContentType string                               `json:"content_type"`
	Body        []byte                               `json:"body,omitempty"`
	Normalized  *service.AsyncImageNormalizedRequest `json:"normalized,omitempty"`
}

// DurableAsyncImageHandler implements the new BB/SC compatibility surface.
// The legacy Redis-only AsyncImageHandler remains separately wired and keeps
// its routes and response contracts unchanged.
type DurableAsyncImageHandler struct {
	tasks                  *service.AsyncImageTaskService
	queue                  service.AsyncImageQueue
	storage                *service.ImageStorageSettingService
	encryptor              service.SecretEncryptor
	gateway                *GatewayHandler
	openAI                 *OpenAIGatewayHandler
	apiKeys                *service.APIKeyService
	subscriptions          *service.SubscriptionService
	accounts               *service.AccountService
	library                *service.ImageLibraryService
	referenceMu            sync.Mutex
	referenceGate          *service.AsyncImageFetchGate
	referenceCache         *service.AsyncImageReferenceCache
	referenceGateLimit     int
	referenceCacheTTL      time.Duration
	referenceCacheMaxBytes int64

	startOnce sync.Once
	stopOnce  sync.Once
	stop      context.CancelFunc
	runtimeWG sync.WaitGroup
}

func NewDurableAsyncImageHandler(
	tasks *service.AsyncImageTaskService,
	queue service.AsyncImageQueue,
	storage *service.ImageStorageSettingService,
	encryptor service.SecretEncryptor,
	gateway *GatewayHandler,
	openAI *OpenAIGatewayHandler,
	apiKeys *service.APIKeyService,
	subscriptions *service.SubscriptionService,
	accounts *service.AccountService,
	library *service.ImageLibraryService,
) *DurableAsyncImageHandler {
	h := &DurableAsyncImageHandler{
		tasks: tasks, queue: queue, storage: storage, encryptor: encryptor,
		gateway: gateway, openAI: openAI, apiKeys: apiKeys,
		subscriptions: subscriptions, accounts: accounts, library: library,
	}
	h.Start()
	return h
}

// Start launches the durable outbox dispatcher, queue workers, and recovery
// scanner. It is idempotent so tests and alternate server bootstraps may call
// it safely.
func (h *DurableAsyncImageHandler) Start() {
	if h == nil || h.tasks == nil || h.queue == nil || h.storage == nil {
		return
	}
	h.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		h.stop = cancel
		h.startRuntime(ctx)
	})
}

func (h *DurableAsyncImageHandler) Stop() {
	if h == nil {
		return
	}
	h.stopOnce.Do(func() {
		if h.stop != nil {
			h.stop()
		}
		h.runtimeWG.Wait()
	})
}

func (h *DurableAsyncImageHandler) SubmitGeminiBB(c *gin.Context) {
	h.submit(c, service.AsyncImageProtocolBB, service.PlatformGemini)
}

func (h *DurableAsyncImageHandler) SubmitOpenAIBB(c *gin.Context) {
	h.submit(c, service.AsyncImageProtocolBB, service.PlatformOpenAI)
}

func (h *DurableAsyncImageHandler) SubmitGeminiSC(c *gin.Context) {
	h.submit(c, service.AsyncImageProtocolSC, service.PlatformGemini)
}

func (h *DurableAsyncImageHandler) submit(c *gin.Context, protocol, expectedPlatform string) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.User == nil {
		h.writeError(c, protocol, service.ErrAsyncImageTaskNotFound)
		return
	}
	billingGroup, err := h.resolveAsyncImageBillingGroup(c.Request.Context(), apiKey, expectedPlatform)
	if err != nil {
		h.writeError(c, protocol, err)
		return
	}
	if h == nil || h.tasks == nil || h.storage == nil || h.encryptor == nil {
		h.writeProtocolError(c, protocol, http.StatusServiceUnavailable, "async_image_unavailable", "asynchronous image generation is unavailable")
		return
	}
	if _, enabled, err := h.storage.DurableStorage(c.Request.Context()); err != nil || !enabled {
		h.writeProtocolError(c, protocol, http.StatusServiceUnavailable, "storage_unavailable", "image result storage is not configured")
		return
	}
	runtimeCfg, err := h.storage.RuntimeConfig(c.Request.Context())
	if err != nil {
		h.writeProtocolError(c, protocol, http.StatusServiceUnavailable, "configuration_unavailable", "asynchronous image configuration is unavailable")
		return
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		status := http.StatusBadRequest
		if _, ok := extractMaxBytesError(err); ok {
			status = http.StatusRequestEntityTooLarge
		}
		h.writeProtocolError(c, protocol, status, "invalid_request", "failed to read request body")
		return
	}
	if len(bytes.TrimSpace(body)) == 0 {
		h.writeProtocolError(c, protocol, http.StatusBadRequest, "invalid_request", "request body is empty")
		return
	}

	payload := durableAsyncImagePayload{
		SourcePath:  c.Request.URL.Path,
		ContentType: strings.TrimSpace(c.GetHeader("Content-Type")),
	}
	var model, kind, requestedSize, aspectRatio, prompt string
	var moderationBody []byte
	moderationProtocol := service.ContentModerationProtocolOpenAIImages
	var inputReferenceURLs []string
	referenceImageCount := 0
	imageCount := 1
	switch expectedPlatform {
	case service.PlatformGemini:
		moderationProtocol = service.ContentModerationProtocolGemini
		var normalized *service.AsyncImageNormalizedRequest
		if protocol == service.AsyncImageProtocolSC {
			normalized, err = service.ParseSCGeminiImageRequest(body, c.Request.URL.Path)
		} else {
			normalized, err = service.ParseBBGeminiImageRequest(body, c.Request.URL.Path)
		}
		if err == nil {
			payload.Normalized = normalized
			model, kind, requestedSize, aspectRatio, prompt = normalized.Model, normalized.Kind, normalized.ImageSize, normalized.AspectRatio, normalized.Prompt
			moderationBody = asyncImageGeminiModerationBody(normalized)
			for _, part := range normalized.Parts {
				if part.Type == "image_url" && strings.TrimSpace(part.URL) != "" {
					inputReferenceURLs = append(inputReferenceURLs, part.URL)
				}
			}
			referenceImageCount = normalized.ReferenceCount()
		}
	case service.PlatformOpenAI:
		if h.openAI == nil || h.openAI.gatewayService == nil {
			err = errors.New("OpenAI image gateway is unavailable")
			break
		}
		var parsed *service.OpenAIImagesRequest
		parsed, err = h.openAI.gatewayService.ParseOpenAIImagesRequest(c, body)
		if err == nil && parsed.Stream {
			err = errors.New("stream must be false for asynchronous image generation")
		}
		if err == nil {
			payload.Body = append([]byte(nil), body...)
			model, prompt, imageCount = parsed.Model, parsed.Prompt, parsed.N
			requestedSize = service.OpenAIImagesBillingInputSize(parsed)
			if requestedSize == "" {
				requestedSize = parsed.Size
			}
			aspectRatio = parsed.AspectRatio
			moderationBody = parsed.ModerationBody()
			inputReferenceURLs = parsed.ReferencedImageURLs()
			referenceImageCount = parsed.ReferenceCount()
			kind = service.AsyncImageRequestTypeTextToImage
			if parsed.IsEdits() {
				kind = service.AsyncImageRequestTypeImageToImage
			}
		}
	default:
		err = errors.New("unsupported image platform")
	}
	if err != nil {
		h.writeProtocolError(c, protocol, http.StatusBadRequest, asyncImageRequestErrorCode(err), err.Error())
		return
	}
	if referenceImageCount > runtimeCfg.MaxReferenceImages {
		h.writeProtocolError(c, protocol, http.StatusBadRequest, "too_many_reference_images", "reference image count exceeds the configured limit")
		return
	}
	if modelLimit := service.AsyncImageModelReferenceLimit(expectedPlatform, model); modelLimit > 0 && referenceImageCount > modelLimit {
		h.writeProtocolError(c, protocol, http.StatusBadRequest, "too_many_reference_images_for_model", fmt.Sprintf("reference image count exceeds the model limit of %d", modelLimit))
		return
	}
	if !h.checkSecurityAuditBeforeSubmit(c, apiKey, protocol, expectedPlatform, moderationProtocol, model, moderationBody) {
		return
	}
	inputObjectIDs, err := h.tasks.ResolveOwnedInputObjectIDs(c.Request.Context(), apiKey.ID, inputReferenceURLs, time.Now().UTC())
	if err != nil {
		h.writeProtocolError(c, protocol, http.StatusBadRequest, "invalid_reference_image", "uploaded reference image is unavailable")
		return
	}
	if requestedSize == "0.5K" {
		if expectedPlatform != service.PlatformGemini || !service.AsyncImageGeminiModelSupportsHalfK(runtimeCfg, model) {
			h.writeProtocolError(c, protocol, http.StatusBadRequest, "unsupported_image_dimensions", "unsupported_image_dimensions: 0.5K is not enabled for this Gemini model")
			return
		}
	}

	plainPayload, err := json.Marshal(payload)
	if err != nil {
		h.writeProtocolError(c, protocol, http.StatusInternalServerError, "request_persistence_failed", "failed to normalize image request")
		return
	}
	ciphertext, err := h.encryptor.Encrypt(string(plainPayload))
	if err != nil {
		h.writeProtocolError(c, protocol, http.StatusServiceUnavailable, "request_encryption_failed", "image request encryption is not configured")
		return
	}
	expiresAt := time.Now().UTC().Add(time.Duration(runtimeCfg.TaskRetentionDays) * 24 * time.Hour)
	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	var idempotencyKeyPtr *string
	if idempotencyKey != "" {
		if len(idempotencyKey) > 255 {
			h.writeProtocolError(c, protocol, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key must not exceed 255 bytes")
			return
		}
		idempotencyKeyPtr = &idempotencyKey
	}
	requestHash := service.AsyncImageTaskRequestHash(expectedPlatform, protocol, c.Request.URL.Path, body)
	promptPreview := asyncImagePromptPreview(prompt, runtimeCfg)
	params := service.CreateAsyncImageTaskParams{
		UserID: apiKey.UserID, APIKeyID: apiKey.ID, GroupID: billingGroup.ID,
		Protocol: protocol, Platform: expectedPlatform, RequestType: kind,
		Model: model, ImageCount: imageCount, IdempotencyKey: idempotencyKeyPtr,
		RequestHash: requestHash, RequestPayload: []byte(ciphertext), ExpiresAt: &expiresAt,
		OutboxPayload:  json.RawMessage(`{"source":"public_submit"}`),
		InputObjectIDs: inputObjectIDs,
	}
	if requestedSize != "" {
		params.RequestedImageSize = &requestedSize
	}
	if aspectRatio != "" {
		params.AspectRatio = &aspectRatio
	}
	if promptPreview != "" {
		params.PromptPreview = &promptPreview
	}
	task, _, err := h.tasks.Create(c.Request.Context(), params)
	if err != nil {
		h.writeError(c, protocol, err)
		return
	}
	h.writeSubmitResponse(c, protocol, task, runtimeCfg)
}

func asyncImageGeminiModerationBody(request *service.AsyncImageNormalizedRequest) []byte {
	if request == nil {
		return nil
	}
	parts := make([]map[string]any, 0, len(request.Parts))
	for _, part := range request.Parts {
		switch part.Type {
		case "text":
			if text := strings.TrimSpace(part.Text); text != "" {
				parts = append(parts, map[string]any{"text": text})
			}
		case "image_url":
			if imageURL := strings.TrimSpace(part.URL); imageURL != "" {
				parts = append(parts, map[string]any{"fileData": map[string]any{"fileUri": imageURL}})
			}
		}
	}
	if len(parts) == 0 {
		return nil
	}
	body, err := json.Marshal(map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": parts}},
	})
	if err != nil {
		return nil
	}
	return body
}

func (h *DurableAsyncImageHandler) checkSecurityAuditBeforeSubmit(
	c *gin.Context,
	apiKey *service.APIKey,
	responseProtocol string,
	platform string,
	moderationProtocol string,
	model string,
	body []byte,
) bool {
	if len(body) == 0 {
		c.Set(securityAuditCompletedContextKey, true)
		return true
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		h.writeProtocolError(c, responseProtocol, http.StatusInternalServerError, "api_error", "user context not found")
		return false
	}
	if platform == service.PlatformGemini {
		if h == nil || h.gateway == nil {
			return true
		}
		result := h.gateway.checkSecurityAudit(c, nil, apiKey, subject, moderationProtocol, model, body)
		if result == nil || result.AllowNextStage {
			return true
		}
		h.writeProtocolError(c, responseProtocol, securityAuditStatus(result), securityAuditErrorCode(result), securityAuditMessage(result))
		return false
	}
	if h == nil || h.openAI == nil {
		return true
	}
	result := h.openAI.checkSecurityAudit(c, nil, apiKey, subject, moderationProtocol, model, body)
	if result == nil || result.AllowNextStage {
		return true
	}
	h.writeProtocolError(c, responseProtocol, securityAuditStatus(result), securityAuditErrorCode(result), securityAuditMessage(result))
	return false
}

func (h *DurableAsyncImageHandler) resolveAsyncImageBillingGroup(ctx context.Context, apiKey *service.APIKey, expectedPlatform string) (*service.Group, error) {
	if h == nil || apiKey == nil {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "async_image_unavailable", "asynchronous image generation is unavailable")
	}
	if h.apiKeys == nil {
		if err := validateAsyncImageGroup(apiKey, expectedPlatform); err != nil {
			return nil, err
		}
		return apiKey.Group, nil
	}
	return h.apiKeys.ResolveAsyncImageBillingGroup(ctx, apiKey, expectedPlatform)
}

func validateAsyncImageGroup(apiKey *service.APIKey, expectedPlatform string) error {
	if apiKey == nil {
		return infraerrors.New(http.StatusForbidden, "group_required", "an assigned image group is required")
	}
	groupID, ok := service.ResolveAsyncImageBillingGroupID(apiKey, expectedPlatform)
	if !ok {
		return infraerrors.New(http.StatusForbidden, "group_required", "an assigned image group is required")
	}
	if apiKey.Group != nil && apiKey.Group.ID == groupID {
		return service.ValidateAsyncImageBillingGroup(apiKey.Group, expectedPlatform)
	}
	// Legacy helper used when Group is already loaded as the primary group.
	if apiKey.Group == nil || apiKey.GroupID == nil || *apiKey.GroupID != apiKey.Group.ID {
		return infraerrors.New(http.StatusForbidden, "group_required", "an assigned image group is required")
	}
	return service.ValidateAsyncImageBillingGroup(apiKey.Group, expectedPlatform)
}

func (h *DurableAsyncImageHandler) writeSubmitResponse(c *gin.Context, protocol string, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig) {
	// OpenAI and Gemini async both poll GET /v1/images/tasks_async/{task_id}.
	// /v1/tasks_sc/{task_id} remains a compatibility alias with the same body.
	queryURL := asyncImageAbsoluteURL(cfg.PublicBaseURL, asyncImageBBTaskPath+task.TaskID)
	c.Header("Cache-Control", "no-store")
	c.Header("Location", queryURL)
	c.Header("Retry-After", "3")
	// Gemini SC accept/query bodies match OpenAI async (task_id + query_url / status + data).
	if protocol == service.AsyncImageProtocolSC || task.Platform == service.PlatformOpenAI {
		c.JSON(http.StatusAccepted, gin.H{"task_id": task.TaskID, "query_url": queryURL})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"id": task.TaskID, "task_id": task.TaskID, "object": "image.task",
		"status": "queued", "query_url": queryURL,
	})
}

func (h *DurableAsyncImageHandler) GetBB(c *gin.Context) {
	h.get(c, service.AsyncImageProtocolBB)
}

func (h *DurableAsyncImageHandler) GetSC(c *gin.Context) {
	h.get(c, service.AsyncImageProtocolSC)
}

func (h *DurableAsyncImageHandler) get(c *gin.Context, protocol string) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || h == nil || h.tasks == nil {
		h.writeProtocolError(c, protocol, http.StatusNotFound, "task_not_found", "asynchronous image task not found")
		return
	}
	details, err := h.tasks.GetForAPIKey(c.Request.Context(), apiKey.ID, c.Param("task_id"))
	if err != nil || details == nil || details.Task == nil {
		h.writeProtocolError(c, protocol, http.StatusNotFound, "task_not_found", "asynchronous image task not found")
		return
	}
	// Dialect paths are separate for submissions, but an owning API key may still
	// poll with either query URL. Always answer in the dialect of the request
	// path so a failed Gemini SC task queried via /tasks_async returns BB
	// {"status":"failed","fail_reason":...} instead of a false task_not_found.
	runtimeCfg, err := h.storage.RuntimeConfig(c.Request.Context())
	if err != nil {
		h.writeProtocolError(c, protocol, http.StatusServiceUnavailable, "configuration_unavailable", "asynchronous image configuration is unavailable")
		return
	}
	c.Header("Cache-Control", "no-store")
	if protocol == service.AsyncImageProtocolSC {
		h.writeSCQuery(c, details, runtimeCfg)
		return
	}
	h.writeBBQuery(c, details, runtimeCfg)
}

func (h *DurableAsyncImageHandler) writeBBQuery(c *gin.Context, details *service.AsyncImageTaskDetails, cfg service.AsyncImageRuntimeConfig) {
	task := details.Task
	switch asyncImagePublicStatus(task, cfg) {
	case "succeeded":
		accesses, err := h.resolveResultAccess(c.Request.Context(), details.Results, cfg)
		if err != nil {
			h.writeProtocolError(c, service.AsyncImageProtocolBB, http.StatusServiceUnavailable, "storage_unavailable", "image results are temporarily unavailable")
			return
		}
		data := make([]gin.H, 0, len(accesses))
		for _, access := range accesses {
			data = append(data, gin.H{"url": access.URL})
		}
		c.JSON(http.StatusOK, gin.H{"status": "succeeded", "task_id": task.TaskID, "data": data})
	case "failed":
		c.JSON(http.StatusOK, gin.H{
			"status": "failed", "task_id": task.TaskID,
			"error_code":  asyncImageFailureBusinessCode(task),
			"fail_reason": asyncImageFailureMessage(task),
		})
	case "queued":
		c.Header("Retry-After", "3")
		c.JSON(http.StatusOK, gin.H{"status": "queued", "task_id": task.TaskID})
	default:
		c.Header("Retry-After", "3")
		c.JSON(http.StatusOK, gin.H{"status": "processing", "task_id": task.TaskID})
	}
}

func (h *DurableAsyncImageHandler) writeSCQuery(c *gin.Context, details *service.AsyncImageTaskDetails, cfg service.AsyncImageRuntimeConfig) {
	// Gemini SC query responses use the same OpenAI async shape.
	h.writeBBQuery(c, details, cfg)
}

func (h *DurableAsyncImageHandler) resolveResultAccess(ctx context.Context, results []service.AsyncImageResult, cfg service.AsyncImageRuntimeConfig) ([]service.ObjectAccess, error) {
	if len(results) == 0 || h == nil || h.storage == nil {
		return nil, errors.New("image result manifest is empty")
	}
	storage, enabled, err := h.storage.DurableStorage(ctx)
	if err != nil || !enabled || storage == nil {
		return nil, errors.New("image storage is unavailable")
	}
	expiry := time.Duration(cfg.SignedURLExpirySeconds) * time.Second
	accesses := make([]service.ObjectAccess, 0, len(results))
	for _, result := range results {
		width, height := 0, 0
		if result.Width != nil {
			width = *result.Width
		}
		if result.Height != nil {
			height = *result.Height
		}
		access, signErr := storage.SignURL(ctx, service.ObjectRef{
			Provider: result.Provider, Bucket: result.Bucket, ObjectKey: result.ObjectKey,
			ContentType: result.ContentType, SizeBytes: result.ByteSize,
			ChecksumSHA256: result.Checksum, Width: width, Height: height,
		}, expiry)
		if signErr != nil {
			return nil, signErr
		}
		accesses = append(accesses, access)
	}
	return accesses, nil
}

func (h *DurableAsyncImageHandler) UploadSC(c *gin.Context) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusUnauthorized, "authentication_error", "invalid API key")
		return
	}
	if _, err := h.resolveAsyncImageBillingGroup(c.Request.Context(), apiKey, service.PlatformGemini); err != nil {
		h.writeError(c, service.AsyncImageProtocolSC, err)
		return
	}
	if h == nil || h.tasks == nil || h.storage == nil {
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusServiceUnavailable, "upload_unavailable", "image upload service is unavailable")
		return
	}
	storage, enabled, err := h.storage.DurableStorage(c.Request.Context())
	if err != nil || !enabled || storage == nil {
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusServiceUnavailable, "storage_unavailable", "image input storage is not configured")
		return
	}
	cfg, err := h.storage.RuntimeConfig(c.Request.Context())
	if err != nil {
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusServiceUnavailable, "configuration_unavailable", "asynchronous image configuration is unavailable")
		return
	}
	multipartLimit := cfg.DownloadMaxBytes
	if multipartLimit <= asyncImageMaxSignedUploadBody-asyncImageSCMultipartOverhead {
		multipartLimit += asyncImageSCMultipartOverhead
	} else {
		multipartLimit = asyncImageMaxSignedUploadBody
	}
	if c.Request.ContentLength > multipartLimit {
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusRequestEntityTooLarge, "upload_too_large", "uploaded image exceeds the configured size limit")
		return
	}
	settings, err := h.storage.Get(c.Request.Context())
	if err != nil {
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusServiceUnavailable, "configuration_unavailable", "image storage configuration is unavailable")
		return
	}
	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	var idempotencyKeyPtr *string
	if idempotencyKey != "" {
		if len(idempotencyKey) > 255 {
			h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key must not exceed 255 bytes")
			return
		}
		idempotencyKeyPtr = &idempotencyKey
	}
	admission, err := h.tasks.AdmitInputUpload(c.Request.Context(), service.AdmitAsyncImageUploadParams{
		UserID: apiKey.UserID, APIKeyID: apiKey.ID, UploadPerMinute: cfg.UploadPerMinute,
	})
	if err != nil {
		if errors.Is(err, service.ErrAsyncImageUploadRateLimited) {
			c.Header("Retry-After", "60")
		}
		h.writeError(c, service.AsyncImageProtocolSC, err)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, multipartLimit)
	fileHeader, err := c.FormFile("file")
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusRequestEntityTooLarge, "upload_too_large", "uploaded image exceeds the configured size limit")
			return
		}
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusBadRequest, "invalid_upload", "multipart field file is required")
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusBadRequest, "invalid_upload", "failed to read uploaded image")
		return
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, cfg.DownloadMaxBytes+1))
	if err != nil || int64(len(data)) > cfg.DownloadMaxBytes {
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusRequestEntityTooLarge, "upload_too_large", "uploaded image exceeds the configured size limit")
		return
	}
	declaredType := fileHeader.Header.Get("Content-Type")
	requestFilename := asyncImageUploadFilename(fileHeader.Filename)
	requestHash := service.AsyncImageUploadRequestHash(data, declaredType, requestFilename)
	reservation, err := h.tasks.ReserveInputUpload(c.Request.Context(), service.ReserveAsyncImageUploadParams{
		AdmissionID: admission.AdmissionID, UserID: apiKey.UserID, APIKeyID: apiKey.ID, IdempotencyKey: idempotencyKeyPtr,
		RequestHash: requestHash, ByteSize: int64(len(data)),
		UploadPerMinute: cfg.UploadPerMinute, MaxInputBytesPerKey: cfg.MaxInputBytesPerKey,
	})
	if err != nil {
		if errors.Is(err, service.ErrAsyncImageUploadRateLimited) || errors.Is(err, service.ErrAsyncImageUploadInProgress) {
			c.Header("Retry-After", "60")
		}
		h.writeError(c, service.AsyncImageProtocolSC, err)
		return
	}
	if reservation.Reused && reservation.InputObject != nil {
		if err := h.writeSCUploadResponse(c, storage, apiKey, reservation.InputObject, true); err != nil {
			if infraerrors.Reason(err) != "" {
				h.writeError(c, service.AsyncImageProtocolSC, err)
			} else {
				h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusBadGateway, "upload_failed", "failed to create uploaded image URL")
			}
		}
		return
	}
	if reservation.Reservation == nil {
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusServiceUnavailable, "upload_unavailable", "image upload admission control is unavailable")
		return
	}
	uploadID := reservation.Reservation.ReservationID
	validated, err := (service.AsyncImageReferenceDownloader{MaxBytes: cfg.DownloadMaxBytes}).ValidateBytes(data, declaredType)
	if err != nil {
		h.failSCUploadReservation(uploadID, requestHash, "validation_failed")
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusBadRequest, "invalid_upload", err.Error())
		return
	}
	filename := requestFilename
	if filename == "" {
		filename = "image" + asyncImageExtension(validated.MIMEType)
	}
	key := strings.TrimSuffix(settings.Prefix, "/") + "/inputs/" + fmt.Sprintf("%s/%d/%s%s", service.ImageObjectDatePartition(time.Now()), apiKey.ID, uploadID, asyncImageExtension(validated.MIMEType))
	intentResolver, ok := storage.(service.DurableImageStorageIntentResolver)
	if !ok {
		h.failSCUploadReservation(uploadID, requestHash, "intent_unsupported")
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusServiceUnavailable, "upload_unavailable", "image storage cannot persist upload intent")
		return
	}
	intent, err := intentResolver.ObjectIntent(key, validated.MIMEType, int64(len(validated.Data)), validated.SHA256)
	if err != nil {
		h.failSCUploadReservation(uploadID, requestHash, "intent_invalid")
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusServiceUnavailable, "upload_unavailable", "failed to prepare image upload intent")
		return
	}
	if err := h.tasks.SetInputUploadObjectIntent(c.Request.Context(), service.SetAsyncImageUploadObjectIntentParams{
		ReservationID: uploadID, UserID: apiKey.UserID, APIKeyID: apiKey.ID,
		RequestHash: requestHash, ObjectRef: intent,
	}); err != nil {
		h.failSCUploadReservation(uploadID, requestHash, "intent_persistence_failed")
		h.writeError(c, service.AsyncImageProtocolSC, err)
		return
	}
	uploadCtx, cancelUpload := context.WithTimeout(c.Request.Context(), time.Duration(cfg.UploadTimeoutSeconds)*time.Second)
	ref, err := storage.SaveObject(uploadCtx, key, validated.MIMEType, validated.Data)
	cancelUpload()
	if err != nil {
		h.failSCUploadReservation(uploadID, requestHash, "object_upload_failed")
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusBadGateway, "upload_failed", "failed to store uploaded image")
		return
	}
	if ref.Provider != intent.Provider || ref.Bucket != intent.Bucket || ref.ObjectKey != intent.ObjectKey ||
		ref.ContentType != intent.ContentType || ref.SizeBytes != intent.SizeBytes || ref.ChecksumSHA256 != intent.ChecksumSHA256 {
		h.failSCUploadReservation(uploadID, requestHash, "object_identity_mismatch")
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = storage.Delete(cleanupCtx, ref)
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusBadGateway, "upload_failed", "stored image identity did not match the reserved object")
		return
	}
	ref.Width, ref.Height = validated.Width, validated.Height
	retention := time.Duration(cfg.InputRetentionHours) * time.Hour
	access, err := storage.SignURL(c.Request.Context(), ref, retention)
	if err != nil {
		h.failSCUploadReservation(uploadID, requestHash, "url_signing_failed")
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = storage.Delete(cleanupCtx, ref)
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusBadGateway, "upload_failed", "failed to create uploaded image URL")
		return
	}
	createdAt := time.Now().UTC()
	object, err := h.tasks.CompleteInputUpload(c.Request.Context(), service.CompleteAsyncImageUploadParams{
		ReservationID: uploadID, UserID: apiKey.UserID, APIKeyID: apiKey.ID, RequestHash: requestHash,
		ObjectRef: ref, URLHash: service.AsyncImageInputURLHash(access.URL),
		Filename: filename, ExpiresAt: createdAt.Add(retention),
	})
	if err != nil {
		// Only delete the OSS object when PostgreSQL confirms that the active
		// reservation was released. A commit timeout may mean Complete already
		// succeeded; deleting in that state would corrupt a durable input object.
		released := h.failSCUploadReservation(uploadID, requestHash, "persistence_failed")
		if released {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = storage.Delete(cleanupCtx, ref)
		}
		if infraerrors.Code(err) == http.StatusServiceUnavailable {
			h.writeError(c, service.AsyncImageProtocolSC, err)
		} else {
			h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusInternalServerError, "upload_failed", "failed to persist uploaded image")
		}
		return
	}
	if err := h.registerAsyncImageInputURLAliases(c.Request.Context(), apiKey, object, access.URL, object.ExpiresAt); err != nil {
		h.writeProtocolError(c, service.AsyncImageProtocolSC, http.StatusInternalServerError, "upload_failed", "failed to persist uploaded image access identity")
		return
	}
	h.writeSCUploadJSON(c, object, access.URL, false)
}

func (h *DurableAsyncImageHandler) writeSCUploadResponse(c *gin.Context, storage service.DurableImageStorage, apiKey *service.APIKey, object *service.AsyncImageInputObject, replayed bool) error {
	if object == nil || apiKey == nil || object.APIKeyID != apiKey.ID || object.UserID != apiKey.UserID {
		return service.ErrAsyncImageUploadReservationInvalid
	}
	now := time.Now().UTC()
	expiry := object.ExpiresAt.Sub(now)
	if !object.ExpiresAt.After(now) || expiry <= 0 {
		return service.ErrAsyncImageUploadReservationInvalid
	}
	access, err := storage.SignURL(c.Request.Context(), object.ObjectRef, expiry)
	if err != nil {
		return err
	}
	aliasExpiry := object.ExpiresAt
	if !access.ExpiresAt.IsZero() && access.ExpiresAt.Before(aliasExpiry) {
		aliasExpiry = access.ExpiresAt
	}
	if err := h.registerAsyncImageInputURLAliases(c.Request.Context(), apiKey, object, access.URL, aliasExpiry); err != nil {
		return err
	}
	h.writeSCUploadJSON(c, object, access.URL, replayed)
	return nil
}

func (h *DurableAsyncImageHandler) registerAsyncImageInputURLAliases(
	ctx context.Context,
	apiKey *service.APIKey,
	object *service.AsyncImageInputObject,
	rawURL string,
	expiresAt time.Time,
) error {
	if h == nil || h.tasks == nil || apiKey == nil || object == nil {
		return service.ErrAsyncImageInvalidInput
	}
	for _, hash := range service.AsyncImageInputURLHashes(rawURL) {
		if hash == object.URLHash {
			continue
		}
		if err := h.tasks.RegisterInputURLAlias(ctx, service.RegisterAsyncImageInputURLAliasParams{
			InputObjectID: object.ID, UserID: apiKey.UserID, APIKeyID: apiKey.ID,
			URLHash: hash, ExpiresAt: expiresAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (h *DurableAsyncImageHandler) writeSCUploadJSON(c *gin.Context, object *service.AsyncImageInputObject, rawURL string, replayed bool) {
	if replayed {
		c.Header("X-Idempotency-Replayed", "true")
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"url": rawURL, "filename": object.Filename, "content_type": object.ObjectRef.ContentType,
		"bytes": object.ObjectRef.SizeBytes, "created_at": object.CreatedAt.Unix(),
	})
}

func (h *DurableAsyncImageHandler) failSCUploadReservation(reservationID, requestHash, reason string) bool {
	if h == nil || h.tasks == nil {
		return false
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	released, _ := h.tasks.FailInputUpload(cleanupCtx, reservationID, requestHash, reason)
	return released
}

func asyncImagePublicStatus(task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig) string {
	if task == nil {
		return "failed"
	}
	switch task.Status {
	case service.AsyncImageTaskStatusQueued:
		return "queued"
	case service.AsyncImageTaskStatusSucceeded:
		if asyncImageResultsReleasable(task) {
			return "succeeded"
		}
	case service.AsyncImageTaskStatusFailed, service.AsyncImageTaskStatusExecutionUnknown, service.AsyncImageTaskStatusExpired:
		return "failed"
	case service.AsyncImageTaskStatusStorageFailed:
		if task.StorageRetryCount >= cfg.StorageRetryAttempts {
			return "failed"
		}
	case service.AsyncImageTaskStatusBillingFailed:
		if task.BillingRetryCount >= cfg.BillingRetryAttempts {
			return "failed"
		}
	}
	return "processing"
}

// Result objects are customer-visible only after both the task state and the
// durable billing state confirm completion. Keeping this check independent of
// the normal state-transition invariant prevents a partial or manually
// repaired row from exposing an unbilled image.
func asyncImageResultsReleasable(task *service.AsyncImageTask) bool {
	return task != nil &&
		task.Status == service.AsyncImageTaskStatusSucceeded &&
		(task.BillingStatus == service.AsyncImageBillingStatusSucceeded ||
			task.BillingStatus == service.AsyncImageBillingStatusNotBillable)
}

func asyncImageFailureMessage(task *service.AsyncImageTask) string {
	if task == nil {
		return "image generation failed"
	}
	if task.Status == service.AsyncImageTaskStatusExecutionUnknown {
		return "generation outcome is unknown after an interrupted upstream request"
	}
	if task.ErrorCode != nil {
		switch strings.TrimSpace(*task.ErrorCode) {
		case "invalid_reference_image":
			if task.ErrorMessage != nil && strings.TrimSpace(*task.ErrorMessage) != "" {
				return strings.TrimSpace(*task.ErrorMessage)
			}
			return "reference image could not be processed"
		case "unsupported_image_dimensions":
			if task.ErrorMessage != nil && strings.TrimSpace(*task.ErrorMessage) != "" {
				return strings.TrimSpace(*task.ErrorMessage)
			}
			return "reference image dimensions are not supported"
		case "storage_failed", "storage_unavailable":
			return "image result storage failed"
		case "billing_failed":
			return "image billing confirmation failed"
		case "eligibility_failed":
			return "image generation eligibility changed before execution"
		case "local_capacity_exhausted", "upstream_capacity_exhausted", "upstream_failed", "gateway_unavailable", "upstream_invalid_output":
			if task.ErrorMessage != nil && strings.TrimSpace(*task.ErrorMessage) != "" {
				return strings.TrimSpace(*task.ErrorMessage)
			}
			if strings.TrimSpace(*task.ErrorCode) == "local_capacity_exhausted" {
				return "image generation could not be scheduled because no account capacity became available"
			}
			return "upstream image generation failed"
		case "execution_timeout":
			if task.ErrorMessage != nil && strings.TrimSpace(*task.ErrorMessage) != "" {
				return strings.TrimSpace(*task.ErrorMessage)
			}
			return "image generation timed out"
		}
	}
	// Keep a safely stored upstream message visible when a new internal code
	// has not been added to the classifier yet.
	if task.ErrorMessage != nil && strings.TrimSpace(*task.ErrorMessage) != "" {
		return strings.TrimSpace(*task.ErrorMessage)
	}
	return "image generation failed"
}

const (
	asyncImageBusinessCodeContentPolicy = 601
	asyncImageBusinessCodeReference     = 602
	asyncImageBusinessCodeCapacity      = 603
	asyncImageBusinessCodeInput         = 604
	asyncImageBusinessCodeRateLimit     = 605
	asyncImageBusinessCodeUpstream      = 606
	asyncImageBusinessCodeImageOutput   = 607
	asyncImageBusinessCodeTimeout       = 608
	asyncImageBusinessCodePostProcess   = 609
	// Stable escape hatch for an upstream failure outside known categories.
	asyncImageBusinessCodeUnclassified = 610
)

// asyncImageFailureBusinessCode is a stable application-level classification.
// It deliberately remains separate from HTTP status: task polling always uses
// HTTP 200, while this integer lets clients distinguish actionable failures.
func asyncImageFailureBusinessCode(task *service.AsyncImageTask) int {
	if task == nil {
		return asyncImageBusinessCodeUpstream
	}
	code := ""
	if task.ErrorCode != nil {
		code = strings.TrimSpace(*task.ErrorCode)
	}
	switch code {
	case "content_policy_violation", "policy_violation", "safety_policy_violation":
		return asyncImageBusinessCodeContentPolicy
	case "invalid_reference_image":
		if task.ErrorMessage != nil {
			referenceMessage := strings.ToLower(strings.TrimSpace(*task.ErrorMessage))
			if containsAnyAsyncImageFailure(referenceMessage,
				"image_too_many_pixels", "too many pixels", "pixel limit", "configured size limit",
				"image_mime_mismatch", "mime mismatch", "declared image type", "dimensions are not supported") {
				return asyncImageBusinessCodeInput
			}
		}
		return asyncImageBusinessCodeReference
	case "unsupported_image_dimensions", "request_decryption_failed", "eligibility_failed":
		return asyncImageBusinessCodeInput
	case "local_capacity_exhausted", "upstream_capacity_exhausted":
		return asyncImageBusinessCodeCapacity
	case "upstream_invalid_output":
		return asyncImageBusinessCodeImageOutput
	case "gateway_unavailable":
		return asyncImageBusinessCodeUpstream
	case "execution_timeout", "execution_unknown", "account_attempt_timeout":
		return asyncImageBusinessCodeTimeout
	case "storage_failed", "storage_unavailable", "billing_failed":
		return asyncImageBusinessCodePostProcess
	}

	message := ""
	if task.ErrorMessage != nil {
		message = strings.ToLower(strings.TrimSpace(*task.ErrorMessage))
	}
	// Content-policy and third-party-similarity blocks are actionable prompt
	// failures even when the upstream only reports a generic 400.
	if containsAnyAsyncImageFailure(message,
		"third-party", "third party", "similarity", "content policy", "safety policy",
		"policy violation", "第三方", "相似性", "防护限制", "内容政策", "内容安全", "安全策略", "安全政策", "内容审核", "安全拦截", "违反政策", "违反了我们的政策") {
		return asyncImageBusinessCodeContentPolicy
	}
	if containsAnyAsyncImageFailure(message,
		"image_url fetch failed", "image url fetch failed", "download openai reference image",
		"reference image fetch", "curl: (28)", "curl: (35)", "curl: (56)", "proxyerror", "tls", "dns", "name resolution", "connection reset", "connection closed") {
		return asyncImageBusinessCodeReference
	}
	if containsAnyAsyncImageFailure(message, "all available accounts exhausted", "capacity unavailable", "no account capacity") {
		return asyncImageBusinessCodeCapacity
	}
	if containsAnyAsyncImageFailure(message, "rate limit", "rate limited", "http 429", "status 429") {
		return asyncImageBusinessCodeRateLimit
	}
	if containsAnyAsyncImageFailure(message, "invalid request", "invalid prompt", "prompt is required", "unsupported_image_dimensions") {
		return asyncImageBusinessCodeInput
	}
	if containsAnyAsyncImageFailure(message, "timed out", "timeout", "http 408", "status 408") {
		return asyncImageBusinessCodeTimeout
	}
	if containsAnyAsyncImageFailure(message, "http 502", "http 503", "http 504", "status 502", "status 503", "status 504", "temporarily unavailable", "gateway unavailable") {
		return asyncImageBusinessCodeUpstream
	}
	if code == "upstream_failed" && message == "" {
		return asyncImageBusinessCodeUpstream
	}
	return asyncImageBusinessCodeUnclassified
}

func containsAnyAsyncImageFailure(value string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(value, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func asyncImageAbsoluteURL(baseURL, path string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return path
	}
	return baseURL + path
}

func asyncImagePromptPreview(prompt string, cfg service.AsyncImageRuntimeConfig) string {
	if !cfg.PromptPreviewEnabled {
		return ""
	}
	prompt = logredact.RedactText(strings.Join(strings.Fields(strings.TrimSpace(prompt)), " "), "api_key", "secret", "token", "authorization")
	runes := []rune(prompt)
	maxChars := cfg.PromptPreviewMaxChars
	if maxChars <= 0 {
		maxChars = 160
	}
	if len(runes) > maxChars {
		runes = runes[:maxChars]
	}
	return string(runes)
}

func asyncImageRequestErrorCode(err error) string {
	if err != nil && strings.Contains(err.Error(), "unsupported_image_dimensions") {
		return "unsupported_image_dimensions"
	}
	return "invalid_request"
}

func asyncImageExtension(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func asyncImageUploadFilename(raw string) string {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	name := strings.TrimSpace(path.Base(raw))
	if name == "." || name == ".." || name == "/" {
		return ""
	}
	var cleaned strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		if _, err := cleaned.WriteRune(r); err != nil {
			return ""
		}
	}
	name = strings.TrimSpace(cleaned.String())
	if len(name) <= 255 {
		return name
	}
	cleaned.Reset()
	for _, r := range name {
		if cleaned.Len()+len(string(r)) > 255 {
			break
		}
		if _, err := cleaned.WriteRune(r); err != nil {
			return ""
		}
	}
	return strings.TrimSpace(cleaned.String())
}

func (h *DurableAsyncImageHandler) writeError(c *gin.Context, protocol string, err error) {
	status := infraerrors.Code(err)
	if status <= 0 {
		status = http.StatusInternalServerError
	}
	code := strings.ToLower(strings.TrimSpace(infraerrors.Reason(err)))
	if code == "" {
		code = "async_image_error"
	}
	message := strings.TrimSpace(infraerrors.Message(err))
	if message == "" {
		message = "asynchronous image request failed"
	}
	if status >= http.StatusInternalServerError {
		attrs := []any{"path", c.FullPath(), "method", c.Request.Method, "status", status, "code", code, "error", err}
		if cause := errors.Unwrap(err); cause != nil {
			attrs = append(attrs, "cause", cause)
		}
		slog.Warn("async_image.request_failed", attrs...)
	}
	h.writeProtocolError(c, protocol, status, code, message)
}

func (h *DurableAsyncImageHandler) writeProtocolError(c *gin.Context, protocol string, status int, code, message string) {
	_ = protocol
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"error": gin.H{"type": code, "code": code, "message": message}})
}
