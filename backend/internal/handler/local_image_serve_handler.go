package handler

import (
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ServeLocalObject serves HMAC-signed local async image objects without API key auth.
// GET /v1/images/local/*object_key?exp=&sig=
func (h *DurableAsyncImageHandler) ServeLocalObject(c *gin.Context) {
	if h == nil || h.storage == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	objectKey := strings.TrimPrefix(c.Param("object_key"), "/")
	objectKey = strings.TrimLeft(strings.ReplaceAll(strings.TrimSpace(objectKey), `\`, `/`), "/")
	if objectKey == "" || strings.Contains(objectKey, "..") {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	exp := c.Query("exp")
	sig := c.Query("sig")

	cfg, err := h.storage.EffectiveRuntimeStorageConfig(c.Request.Context())
	if err != nil || cfg == nil || cfg.NormalizedBackend() != config.ImageStorageBackendLocal {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if len(cfg.SignKey) == 0 {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if err := service.VerifyLocalImageURLSignature(objectKey, exp, sig, cfg.SignKey, time.Now()); err != nil {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	storage, enabled, err := h.storage.DurableStorage(c.Request.Context())
	if err != nil || !enabled || storage == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	ref := service.ObjectRef{
		Provider:  config.ImageStorageProviderLocal,
		Bucket:    "local",
		ObjectKey: objectKey,
	}
	body, err := storage.Read(c.Request.Context(), ref)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	defer func() { _ = body.Close() }()

	contentType := "application/octet-stream"
	if meta, headErr := storage.Head(c.Request.Context(), ref); headErr == nil && meta.ContentType != "" {
		contentType = meta.ContentType
	} else if ext := path.Ext(objectKey); ext != "" {
		switch strings.ToLower(ext) {
		case ".png":
			contentType = "image/png"
		case ".jpg", ".jpeg":
			contentType = "image/jpeg"
		case ".webp":
			contentType = "image/webp"
		case ".gif":
			contentType = "image/gif"
		}
	}
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("Content-Type", contentType)
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, body)
}
