package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type ImagePlazaHandler struct {
	svc     *service.ImagePlazaService
	library imagePlazaLibrary
}

type imagePlazaLibrary interface {
	ListPublished(ctx context.Context, viewerUserID int64, in service.ImagePublicationListParams) (*service.PublicImagePlazaListResult, error)
	ImportBytes(ctx context.Context, userID int64, in service.ImageLibraryImportInput) (*service.ImageLibraryItem, bool, error)
	Publish(ctx context.Context, in service.CreateImagePublicationParams) (*service.ImagePublication, error)
	ResolvePublishedObject(ctx context.Context, publicationID string) (service.ObjectAccess, error)
	OpenPublishedObject(ctx context.Context, publicationID string) (service.ObjectAccess, io.ReadCloser, string, error)
	DeleteLegacyPlazaIdentifier(ctx context.Context, userID int64, identifier string) (bool, error)
}

func NewImagePlazaHandler(svc *service.ImagePlazaService) *ImagePlazaHandler {
	return &ImagePlazaHandler{svc: svc}
}

func ProvideImagePlazaHandler(svc *service.ImagePlazaService, library *service.ImageLibraryService) *ImagePlazaHandler {
	return &ImagePlazaHandler{svc: svc, library: library}
}

type imagePlazaPublishRequest struct {
	Prompt     string `json:"prompt"`
	Title      string `json:"title"`
	Model      string `json:"model"`
	Size       string `json:"size"`
	Quality    string `json:"quality"`
	Format     string `json:"format"`
	Background string `json:"background"`
	Style      string `json:"style"`
	Image      string `json:"image"` // data URL or base64
}

// List GET /api/v1/image-plaza
func (h *ImagePlazaHandler) List(c *gin.Context) {
	markImagePlazaDeprecated(c)
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}

	page, pageSize := parseLegacyImagePlazaPage(c)
	if h.library != nil {
		cursor, cursorOK := parseImageCursor(c)
		if !cursorOK {
			return
		}
		cursorMode := strings.TrimSpace(c.Query("cursor")) != "" || strings.TrimSpace(c.Query("limit")) != ""
		limit, offset := parseImageLimit(c), 0
		if !cursorMode {
			cursor = nil
			limit = pageSize
			offset = (page - 1) * pageSize
		}
		result, err := h.library.ListPublished(c.Request.Context(), subject.UserID, service.ImagePublicationListParams{
			Platform: c.Query("platform"), Model: c.Query("model"), AspectRatio: c.Query("aspect_ratio"),
			Query: strings.TrimSpace(c.Query("q")), Sort: c.Query("sort"), Cursor: cursor, Limit: limit, Offset: offset,
		})
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		data := gin.H{
			"items": result.Items, "total": result.Total,
			"next_cursor": nextPublicationCursor(result.Items, limit),
		}
		if !cursorMode {
			data["page"] = page
			data["page_size"] = pageSize
		}
		response.Success(c, data)
		return
	}
	result, err := h.svc.List(c.Request.Context(), service.ImagePlazaListParams{
		Query:    strings.TrimSpace(c.Query("q")),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"items":     result.Items,
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}

func parseLegacyImagePlazaPage(c *gin.Context) (page, pageSize int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	if page <= 0 || page > 1_000_000 {
		page = 1
	}
	pageSize, _ = strconv.Atoi(c.DefaultQuery("page_size", "24"))
	if pageSize <= 0 {
		pageSize = 24
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

// Publish POST /api/v1/image-plaza
func (h *ImagePlazaHandler) Publish(c *gin.Context) {
	markImagePlazaDeprecated(c)
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}

	// Base64 expands the 20 MiB binary limit by 4/3. Bound the JSON before
	// decoding so a forged plaza request cannot force an unbounded allocation.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 28<<20)
	var req imagePlazaPublishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	data, mime, err := service.DecodeBase64ImagePayload(req.Image)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if h.library != nil {
		item, _, importErr := h.library.ImportBytes(c.Request.Context(), subject.UserID, service.ImageLibraryImportInput{
			GenerationMode: "import", SourceType: "manual_import", Model: req.Model,
			RequestedSize: req.Size, ActualSize: req.Size, Quality: req.Quality,
			Title: req.Title, Prompt: req.Prompt, ImageData: data, DeclaredMIME: mime,
			IdempotencyKey: strings.TrimSpace(c.GetHeader("Idempotency-Key")),
		})
		if importErr != nil {
			response.ErrorFrom(c, importErr)
			return
		}
		publication, publishErr := h.library.Publish(c.Request.Context(), service.CreateImagePublicationParams{
			UserID: subject.UserID, AssetID: item.AssetID, PublicTitle: req.Title, SharePrompt: false,
		})
		if publishErr != nil {
			response.ErrorFrom(c, publishErr)
			return
		}
		if actualFormat := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(item.ContentType)), "image/"); actualFormat != "" {
			format = actualFormat
		}
		size := item.ActualSize
		if size == "" {
			size = item.RequestedSize
		}
		if size == "" {
			size = strings.TrimSpace(req.Size)
		}
		response.Success(c, gin.H{
			"id": item.AssetID, "asset_id": item.AssetID, "publication_id": publication.PublicID,
			"prompt": item.PrivatePrompt, "title": item.Title, "model": item.Model,
			"size": size, "quality": item.Quality, "format": format,
			"background": req.Background, "style": req.Style,
			"content_type": item.ContentType, "file_size": item.ByteSize,
			"visibility": item.Visibility, "image_url": item.ImageURL, "created_at": item.CreatedAt,
			"item": item, "publication": publication,
		})
		return
	}

	item, err := h.svc.Publish(c.Request.Context(), subject.UserID, service.ImagePlazaPublishInput{
		Prompt:     req.Prompt,
		Title:      req.Title,
		Model:      req.Model,
		Size:       req.Size,
		Quality:    req.Quality,
		Format:     format,
		Background: req.Background,
		Style:      req.Style,
		ImageData:  data,
		MimeType:   mime,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

// Content GET /api/v1/image-plaza/:id/content (public for public items)
func (h *ImagePlazaHandler) Content(c *gin.Context) {
	markImagePlazaDeprecated(c)
	if h.library != nil {
		publicID := strings.TrimSpace(c.Param("id"))
		if !strings.HasPrefix(publicID, "imgpub_") || len(publicID) > 64 {
			response.ErrorFrom(c, service.ErrImagePublicationNotFound)
			return
		}
		access, reader, contentType, err := h.library.OpenPublishedObject(c.Request.Context(), publicID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if reader != nil {
			defer reader.Close()
			writeImageObjectStream(c, reader, contentType)
			return
		}
		redirectToImageObject(c, access)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid id")
		return
	}
	path, contentType, err := h.svc.OpenContent(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=3600")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Header("Cross-Origin-Resource-Policy", "same-site")
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"image-%d\"", id))
	if contentType != "" {
		c.Header("Content-Type", contentType)
	}
	c.File(path)
}

// Delete DELETE /api/v1/image-plaza/:id
func (h *ImagePlazaHandler) Delete(c *gin.Context) {
	markImagePlazaDeprecated(c)
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	if h.library != nil {
		rawID := strings.TrimSpace(c.Param("id"))
		handled, err := h.library.DeleteLegacyPlazaIdentifier(c.Request.Context(), subject.UserID, rawID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if handled {
			response.Success(c, gin.H{"deleted": true, "message": "ok"})
			return
		}
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid id")
		return
	}
	if err := h.svc.Delete(c.Request.Context(), subject.UserID, id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true, "message": "ok"})
}

func markImagePlazaDeprecated(c *gin.Context) {
	c.Header("Deprecation", "true")
	c.Header("Sunset", time.Now().UTC().AddDate(0, 3, 0).Format(http.TimeFormat))
}
