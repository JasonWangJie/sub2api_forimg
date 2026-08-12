package service

import (
	"context"
	"time"
)

const (
	ImagePlazaVisibilityPublic  = "public"
	ImagePlazaVisibilityPrivate = "private"
)

// ImagePlazaItem is a globally shared plaza image record.
type ImagePlazaItem struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	UserEmail   string    `json:"user_email,omitempty"`
	Prompt      string    `json:"prompt"`
	Title       string    `json:"title"`
	Model       string    `json:"model"`
	Size        string    `json:"size"`
	Quality     string    `json:"quality"`
	Format      string    `json:"format"`
	Background  string    `json:"background"`
	Style       string    `json:"style"`
	StoragePath string    `json:"-"`
	ContentType string    `json:"content_type"`
	FileSize    int64     `json:"file_size"`
	Visibility  string    `json:"visibility"`
	ImageURL    string    `json:"image_url"`
	CreatedAt   time.Time `json:"created_at"`
}

type ImagePlazaPublishInput struct {
	Prompt     string
	Title      string
	Model      string
	Size       string
	Quality    string
	Format     string
	Background string
	Style      string
	ImageData  []byte
	MimeType   string
}

type ImagePlazaListParams struct {
	Query    string
	Page     int
	PageSize int
}

type ImagePlazaListResult struct {
	Items    []ImagePlazaItem
	Total    int64
	Page     int
	PageSize int
}

// ImagePlazaRepository persists plaza metadata.
type ImagePlazaRepository interface {
	Create(ctx context.Context, item *ImagePlazaItem) error
	GetByID(ctx context.Context, id int64) (*ImagePlazaItem, error)
	ListPublic(ctx context.Context, params ImagePlazaListParams) (*ImagePlazaListResult, error)
	Delete(ctx context.Context, id int64, userID int64) (bool, error)
}
