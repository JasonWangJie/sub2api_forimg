package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrImagePlazaNotFound  = apperrors.NotFound("IMAGE_PLAZA_NOT_FOUND", "image not found")
	ErrImagePlazaForbidden = apperrors.Forbidden("IMAGE_PLAZA_FORBIDDEN", "not allowed")
)

type imagePlazaStorageUpdater interface {
	UpdateStorage(ctx context.Context, id int64, storagePath, contentType string, fileSize int64) error
}

type ImagePlazaService struct {
	repo    ImagePlazaRepository
	dataDir string
}

func NewImagePlazaService(repo ImagePlazaRepository, cfg *config.Config) *ImagePlazaService {
	dataDir := "./data"
	if cfg != nil && strings.TrimSpace(cfg.Pricing.DataDir) != "" {
		dataDir = strings.TrimSpace(cfg.Pricing.DataDir)
	}
	return &ImagePlazaService{repo: repo, dataDir: dataDir}
}

func (s *ImagePlazaService) Publish(ctx context.Context, userID int64, in ImagePlazaPublishInput) (*ImagePlazaItem, error) {
	if userID <= 0 {
		return nil, apperrors.Unauthorized("UNAUTHORIZED", "unauthorized")
	}
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		return nil, apperrors.BadRequest("INVALID_PROMPT", "prompt is required")
	}
	model := strings.TrimSpace(in.Model)
	if model == "" {
		return nil, apperrors.BadRequest("INVALID_MODEL", "model is required")
	}
	if len(in.ImageData) == 0 {
		return nil, apperrors.BadRequest("INVALID_IMAGE", "image data is required")
	}
	validated, err := ValidateImageBytes(in.ImageData, in.MimeType, DefaultImageLibraryMaxBytes, DefaultImageLibraryMaxPixels)
	if err != nil {
		return nil, err
	}
	format := validated.Format
	mime := validated.MIMEType
	ext := extFromFormat(format)

	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = truncateRunes(prompt, 42)
	}

	item := &ImagePlazaItem{
		UserID:      userID,
		Prompt:      prompt,
		Title:       title,
		Model:       model,
		Size:        strings.TrimSpace(in.Size),
		Quality:     defaultString(strings.TrimSpace(in.Quality), "auto"),
		Format:      format,
		Background:  defaultString(strings.TrimSpace(in.Background), "auto"),
		Style:       defaultString(strings.TrimSpace(in.Style), "auto"),
		StoragePath: "pending",
		ContentType: mime,
		FileSize:    validated.SizeBytes,
		// Legacy plaza submissions are no longer made public implicitly. The
		// unified library flow creates a pending-review publication instead.
		Visibility: ImagePlazaVisibilityPrivate,
		CreatedAt:  time.Now().UTC(),
	}

	if err := s.repo.Create(ctx, item); err != nil {
		return nil, err
	}

	rel := filepath.ToSlash(filepath.Join("image_plaza", fmt.Sprintf("%d", item.UserID), fmt.Sprintf("%d.%s", item.ID, ext)))
	abs := filepath.Join(s.dataDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		_, _ = s.repo.Delete(ctx, item.ID, userID)
		return nil, apperrors.InternalServer("STORAGE_ERROR", "failed to prepare storage")
	}
	if err := os.WriteFile(abs, validated.Data, 0o644); err != nil {
		_, _ = s.repo.Delete(ctx, item.ID, userID)
		return nil, apperrors.InternalServer("STORAGE_ERROR", "failed to store image")
	}

	updater, ok := s.repo.(imagePlazaStorageUpdater)
	if !ok {
		_ = os.Remove(abs)
		_, _ = s.repo.Delete(ctx, item.ID, userID)
		return nil, apperrors.InternalServer("STORAGE_ERROR", "storage updater unavailable")
	}
	if err := updater.UpdateStorage(ctx, item.ID, rel, mime, validated.SizeBytes); err != nil {
		_ = os.Remove(abs)
		_, _ = s.repo.Delete(ctx, item.ID, userID)
		return nil, err
	}

	item.StoragePath = rel
	item.ImageURL = fmt.Sprintf("/api/v1/image-plaza/%d/content", item.ID)
	return item, nil
}

func (s *ImagePlazaService) List(ctx context.Context, params ImagePlazaListParams) (*ImagePlazaListResult, error) {
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 24
	}
	if params.PageSize > 100 {
		params.PageSize = 100
	}
	result, err := s.repo.ListPublic(ctx, params)
	if err != nil {
		return nil, err
	}
	for i := range result.Items {
		result.Items[i].ImageURL = fmt.Sprintf("/api/v1/image-plaza/%d/content", result.Items[i].ID)
		result.Items[i].StoragePath = ""
	}
	return result, nil
}

func (s *ImagePlazaService) Get(ctx context.Context, id int64) (*ImagePlazaItem, error) {
	item, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if item.Visibility != ImagePlazaVisibilityPublic {
		return nil, ErrImagePlazaNotFound
	}
	item.ImageURL = fmt.Sprintf("/api/v1/image-plaza/%d/content", item.ID)
	item.StoragePath = ""
	return item, nil
}

func (s *ImagePlazaService) OpenContent(ctx context.Context, id int64) (absPath string, contentType string, err error) {
	item, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return "", "", err
	}
	if item.Visibility != ImagePlazaVisibilityPublic {
		return "", "", ErrImagePlazaNotFound
	}
	cleanRelative := filepath.Clean(filepath.FromSlash(item.StoragePath))
	if cleanRelative == "." || filepath.IsAbs(cleanRelative) || cleanRelative == ".." || strings.HasPrefix(cleanRelative, ".."+string(filepath.Separator)) {
		return "", "", ErrImagePlazaNotFound
	}
	base, resolveErr := filepath.Abs(s.dataDir)
	if resolveErr != nil {
		return "", "", ErrImagePlazaNotFound
	}
	abs, resolveErr := filepath.Abs(filepath.Join(base, cleanRelative))
	if resolveErr != nil {
		return "", "", ErrImagePlazaNotFound
	}
	rel, resolveErr := filepath.Rel(base, abs)
	if resolveErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", ErrImagePlazaNotFound
	}
	if _, statErr := os.Stat(abs); statErr != nil {
		return "", "", ErrImagePlazaNotFound
	}
	ct := item.ContentType
	if ct == "" {
		ct = mimeFromFormat(item.Format)
	}
	return abs, ct, nil
}

func (s *ImagePlazaService) Delete(ctx context.Context, userID, id int64) error {
	item, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if item.UserID != userID {
		return ErrImagePlazaNotFound
	}
	ok, err := s.repo.Delete(ctx, id, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrImagePlazaNotFound
	}
	if item.StoragePath != "" && item.StoragePath != "pending" {
		_ = os.Remove(filepath.Join(s.dataDir, filepath.FromSlash(item.StoragePath)))
	}
	return nil
}
