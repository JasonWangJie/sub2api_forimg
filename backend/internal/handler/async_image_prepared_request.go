package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type asyncImagePreparedRequest struct {
	Version     int    `json:"version"`
	TaskID      string `json:"task_id"`
	RequestHash string `json:"request_hash"`
	Model       string `json:"model"`
	Path        string `json:"path"`
	ContentType string `json:"content_type"`
	Body        []byte `json:"body"`
}

// This is an internal snapshot of the successful builder output, never a new
// client-controlled forwarding endpoint. Only local image inputs are eligible.
func asyncImagePreparedRequestEligible(task *service.AsyncImageTask, body []byte, path, contentType string) bool {
	if task == nil || task.RequestType != service.AsyncImageRequestTypeImageToImage || task.ReferenceTransport == nil ||
		*task.ReferenceTransport != service.AsyncImageReferenceTransportLocal || task.TaskID == "" || task.RequestHash == "" ||
		len(body) == 0 || len(body) > service.AsyncImagePreparedRequestMaxBytes {
		return false
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch task.Platform {
	case service.PlatformGemini:
		if path != EndpointChatCompletions || mediaType != "application/json" || !json.Valid(body) {
			return false
		}
	case service.PlatformOpenAI:
		if path != EndpointImagesEdits || mediaType != "multipart/form-data" || params["boundary"] == "" || !asyncImagePreparedMultipartLocal(body, params["boundary"]) {
			return false
		}
	default:
		return false
	}
	input := asyncImageInputCapabilities(body, contentType)
	return input.Count > 0 && input.Transport == service.AsyncImageReferenceTransportLocal && input.TotalBytes > 0 && input.TotalPixels > 0
}

func asyncImagePreparedMultipartLocal(body []byte, boundary string) bool {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return true
		}
		if err != nil {
			return false
		}
		name := strings.TrimSpace(part.FormName())
		isImage := name == "image" || name == "images" || name == "mask" || strings.HasPrefix(name, "image[") || strings.HasPrefix(name, "images[")
		// Text reference fields could still contain URLs. Generated multipart
		// uses files for every image/mask, so do not cache these mixed forms.
		if name == "image_urls" || name == "file_id" || (isImage && part.FileName() == "") {
			_ = part.Close()
			return false
		}
		if err := part.Close(); err != nil {
			return false
		}
	}
}

func validateAsyncImagePreparedRequest(task *service.AsyncImageTask, prepared *asyncImagePreparedRequest) error {
	if prepared == nil || prepared.Version != 1 || task == nil || prepared.TaskID != task.TaskID ||
		prepared.RequestHash != task.RequestHash || prepared.Model != task.Model ||
		!asyncImagePreparedRequestEligible(task, prepared.Body, prepared.Path, prepared.ContentType) {
		return errors.New("stored local image request snapshot is invalid")
	}
	return nil
}

// Persist before entering any generation gateway. A failed write cannot leave
// an upstream request running without its promised retry-stable local inputs.
func (h *DurableAsyncImageHandler) persistAsyncImagePreparedRequest(
	ctx, parent context.Context, task *service.AsyncImageTask, payload *durableAsyncImagePayload,
	body []byte, path, contentType string, cfg service.AsyncImageRuntimeConfig,
) (asyncImageWorkerDisposition, bool) {
	if payload.Prepared != nil || !asyncImagePreparedRequestEligible(task, body, path, contentType) ||
		isMultipartContentType(payload.ContentType) {
		// Original multipart is already stored as stable bytes at submission.
		return asyncImageWorkerDisposition{}, false
	}
	repo, ok := h.tasks.Repository().(service.AsyncImagePreparedRequestRepository)
	if !ok {
		return asyncImageWorkerDisposition{}, false
	}
	prepared := &asyncImagePreparedRequest{Version: 1, TaskID: task.TaskID, RequestHash: task.RequestHash,
		Model: task.Model, Path: path, ContentType: contentType, Body: body}
	// Replace the original references rather than duplicating their URLs and
	// input bytes. SourcePath remains the original billing/audit endpoint.
	snapshot := durableAsyncImagePayload{SourcePath: payload.SourcePath, Prepared: prepared}
	// Avoid allocating an oversized JSON copy: []byte uses base64 in JSON.
	if base64.StdEncoding.EncodedLen(len(body)) > service.AsyncImagePreparedRequestMaxBytes {
		return asyncImageWorkerDisposition{}, false
	}
	plain, err := json.Marshal(snapshot)
	if err == nil && len(plain) > service.AsyncImagePreparedRequestMaxBytes {
		return asyncImageWorkerDisposition{}, false
	}
	var encrypted string
	if err == nil {
		if h.encryptor == nil {
			err = errors.New("image request encryption is unavailable")
		} else {
			encrypted, err = h.encryptor.Encrypt(string(plain))
		}
	}
	if err == nil && len(encrypted) > service.AsyncImagePreparedRequestMaxBytes {
		return asyncImageWorkerDisposition{}, false
	}
	if err == nil {
		err = repo.SaveAsyncImagePreparedRequest(ctx, task.TaskID, task.Version, []byte(encrypted))
		if err != nil {
			// Reconcile a possibly committed transaction before deciding whether
			// to retry preparation. Never call generation on an uncertain write.
			current, getErr := h.tasks.Repository().GetAsyncImageTaskByTaskID(ctx, task.TaskID)
			if getErr == nil && current != nil && current.Status == service.AsyncImageTaskStatusInvoking && current.Version == task.Version && bytes.Equal(current.RequestPayload, []byte(encrypted)) {
				err = nil
			}
		}
	}
	if err != nil {
		if errors.Is(err, service.ErrAsyncImageInvalidTransition) {
			return asyncImageWorkerDisposition{}, true
		}
		terminalCtx, cancel := asyncImageTerminalTransitionContext(parent)
		defer cancel()
		const message = "local reference snapshot could not be persisted; generation was not started"
		if canRetryAsyncImage(task, task.ReferenceRetryCount, cfg.ReferenceFetchMaxRetries, cfg.TotalMaxRetries) {
			return h.retryAsyncImageReferenceFetch(terminalCtx, task, cfg, errors.New(message)), true
		}
		h.failAsyncImageTask(terminalCtx, task, "reference_snapshot_persistence_failed", message, false)
		return asyncImageWorkerDisposition{}, true
	}
	*payload = snapshot
	task.RequestPayload = []byte(encrypted)
	return asyncImageWorkerDisposition{}, false
}
