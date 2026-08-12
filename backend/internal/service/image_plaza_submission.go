package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	ImageSubmissionPendingReview      = "pending_review"
	ImageSubmissionApprovedPendingSync = "approved_pending_sync"
	ImageSubmissionRejected           = "rejected"
	ImageSubmissionWithdrawn          = "withdrawn"
	ImageSubmissionSynced             = "synced"
)

type ImagePlazaSubmissionRequest struct {
	ID                  int64      `json:"-"`
	RequestID           string     `json:"id"`
	UserID              int64      `json:"user_id,omitempty"`
	Status              string     `json:"status"`
	Title               string     `json:"title"`
	PrivatePrompt       string     `json:"private_prompt,omitempty"`
	PublicTitle         string     `json:"public_title"`
	PublicPrompt        *string    `json:"public_prompt,omitempty"`
	SharePrompt         bool       `json:"share_prompt"`
	Platform            string     `json:"platform"`
	GenerationMode      string     `json:"generation_mode"`
	SourceType          string     `json:"source_type"`
	Model               string     `json:"model"`
	RequestedSize       string     `json:"requested_size"`
	AspectRatio         string     `json:"aspect_ratio"`
	Quality             string     `json:"quality"`
	ContentType         string     `json:"content_type"`
	ByteSize            int64      `json:"byte_size"`
	ChecksumSHA256      string     `json:"checksum_sha256"`
	ClientBlobKey       string     `json:"client_blob_key"`
	APIKeyID            *int64     `json:"api_key_id,omitempty"`
	GroupID             *int64     `json:"group_id,omitempty"`
	ReviewerUserID      *int64     `json:"reviewer_user_id,omitempty"`
	ReviewReason        *string    `json:"review_reason,omitempty"`
	ReviewedAt          *time.Time `json:"reviewed_at,omitempty"`
	LibraryItemID       *int64     `json:"-"`
	LibraryAssetID      *string    `json:"library_item_id,omitempty"`
	PublicationPublicID *string    `json:"publication_id,omitempty"`
	ExpiresAt           time.Time  `json:"expires_at"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	// ImageHeldClientSide tells admin/UI that pixels are not on OSS yet.
	ImageHeldClientSide bool `json:"image_held_client_side"`
}

type CreateImagePlazaSubmissionParams struct {
	UserID         int64
	Title          string
	PrivatePrompt  string
	PublicTitle    string
	PublicPrompt   *string
	SharePrompt    bool
	Platform       string
	GenerationMode string
	SourceType     string
	Model          string
	RequestedSize  string
	AspectRatio    string
	Quality        string
	ContentType    string
	ByteSize       int64
	ChecksumSHA256 string
	ClientBlobKey  string
	APIKeyID       *int64
	GroupID        *int64
	IdempotencyKey string
	RateLimit      int
	ExpiresAt      time.Time
}

type SyncImagePlazaSubmissionParams struct {
	UserID    int64
	RequestID string
	ImageData []byte
	MIMEType  string
}

type ImagePlazaSubmissionRepository interface {
	CreateSubmissionRequest(ctx context.Context, in CreateImagePlazaSubmissionParams) (*ImagePlazaSubmissionRequest, bool, error)
	ListSubmissionRequestsForUser(ctx context.Context, userID int64, status string, cursor *ImageLibraryCursor, limit int) ([]ImagePlazaSubmissionRequest, error)
	ListSubmissionRequestsAdmin(ctx context.Context, status string, cursor *ImageLibraryCursor, limit int) ([]ImagePlazaSubmissionRequest, error)
	GetSubmissionRequestForUser(ctx context.Context, userID int64, requestID string) (*ImagePlazaSubmissionRequest, error)
	TransitionSubmissionRequest(ctx context.Context, adminUserID int64, requestID, action, reason string) (*ImagePlazaSubmissionRequest, error)
	WithdrawSubmissionRequest(ctx context.Context, userID int64, requestID string) error
	MarkSubmissionSynced(ctx context.Context, userID int64, requestID, libraryAssetID, publicationPublicID string) (*ImagePlazaSubmissionRequest, error)
}

func (s *ImageLibraryService) CreateSubmissionRequest(ctx context.Context, in CreateImagePlazaSubmissionParams) (*ImagePlazaSubmissionRequest, bool, error) {
	if s == nil || s.submissions == nil || in.UserID <= 0 {
		return nil, false, apperrors.Unauthorized("UNAUTHORIZED", "unauthorized")
	}
	policy, err := s.storageSettings.LibraryRuntimeConfig(ctx)
	if err != nil {
		return nil, false, err
	}
	in.Title = cleanLibraryText(in.Title, 200)
	in.PrivatePrompt = cleanLibraryText(in.PrivatePrompt, 8000)
	in.PublicTitle = cleanLibraryText(in.PublicTitle, 200)
	if in.PublicTitle == "" {
		in.PublicTitle = in.Title
	}
	if in.PublicPrompt != nil {
		v := cleanLibraryText(*in.PublicPrompt, 8000)
		in.PublicPrompt = &v
	}
	in.Platform = cleanLibraryText(in.Platform, 32)
	in.Model = cleanLibraryText(in.Model, 255)
	in.RequestedSize = cleanLibraryText(in.RequestedSize, 32)
	in.AspectRatio = cleanLibraryText(in.AspectRatio, 32)
	in.Quality = cleanLibraryText(in.Quality, 32)
	in.ContentType = cleanLibraryText(in.ContentType, 128)
	in.ChecksumSHA256 = strings.ToLower(strings.TrimSpace(in.ChecksumSHA256))
	in.ClientBlobKey = strings.TrimSpace(in.ClientBlobKey)
	if in.ClientBlobKey == "" {
		return nil, false, apperrors.BadRequest("CLIENT_BLOB_KEY_REQUIRED", "client_blob_key is required")
	}
	if in.ByteSize <= 0 || in.ChecksumSHA256 == "" || in.ContentType == "" {
		return nil, false, apperrors.BadRequest("IMAGE_METADATA_REQUIRED", "content_type, byte_size, and checksum_sha256 are required")
	}
	mode := strings.ToLower(strings.TrimSpace(in.GenerationMode))
	if mode != "realtime" && mode != "async" && mode != "import" {
		mode = "realtime"
	}
	in.GenerationMode = mode
	source := strings.ToLower(strings.TrimSpace(in.SourceType))
	if source != "realtime_import" && source != "manual_import" && source != "async_task" && source != "legacy_plaza" {
		source = "realtime_import"
	}
	in.SourceType = source
	in.ExpiresAt = time.Now().UTC().AddDate(0, 0, policy.RetentionDays)
	in.RateLimit = policy.PublishPerMinute
	item, reused, err := s.submissions.CreateSubmissionRequest(ctx, in)
	if item != nil {
		item.ImageHeldClientSide = true
	}
	return item, reused, err
}

func (s *ImageLibraryService) ListMySubmissionRequests(ctx context.Context, userID int64, status string, cursor *ImageLibraryCursor, limit int) ([]ImagePlazaSubmissionRequest, error) {
	if s == nil || s.submissions == nil || userID <= 0 {
		return nil, apperrors.Unauthorized("UNAUTHORIZED", "unauthorized")
	}
	items, err := s.submissions.ListSubmissionRequestsForUser(ctx, userID, status, cursor, limit)
	for i := range items {
		items[i].ImageHeldClientSide = items[i].Status == ImageSubmissionPendingReview || items[i].Status == ImageSubmissionApprovedPendingSync
	}
	return items, err
}

func (s *ImageLibraryService) ListSubmissionRequestsAdmin(ctx context.Context, status string, cursor *ImageLibraryCursor, limit int) ([]ImagePlazaSubmissionRequest, error) {
	if s == nil || s.submissions == nil {
		return nil, apperrors.ServiceUnavailable("IMAGE_LIBRARY_UNAVAILABLE", "image library is unavailable")
	}
	items, err := s.submissions.ListSubmissionRequestsAdmin(ctx, status, cursor, limit)
	for i := range items {
		items[i].ImageHeldClientSide = items[i].Status == ImageSubmissionPendingReview || items[i].Status == ImageSubmissionApprovedPendingSync
	}
	return items, err
}

func (s *ImageLibraryService) TransitionSubmissionRequest(ctx context.Context, adminID int64, requestID, action, reason string) (*ImagePlazaSubmissionRequest, error) {
	if s == nil || s.submissions == nil || adminID <= 0 {
		return nil, apperrors.Unauthorized("UNAUTHORIZED", "unauthorized")
	}
	action = strings.ToLower(strings.TrimSpace(action))
	reason = strings.TrimSpace(reason)
	if action != "approve" && action != "reject" {
		return nil, apperrors.BadRequest("INVALID_SUBMISSION_ACTION", "action must be approve or reject")
	}
	if action == "reject" && reason == "" {
		return nil, apperrors.BadRequest("REVIEW_REASON_REQUIRED", "reject reason is required")
	}
	item, err := s.submissions.TransitionSubmissionRequest(ctx, adminID, requestID, action, reason)
	if item != nil {
		item.ImageHeldClientSide = item.Status == ImageSubmissionApprovedPendingSync
	}
	return item, err
}

func (s *ImageLibraryService) WithdrawSubmissionRequest(ctx context.Context, userID int64, requestID string) error {
	if s == nil || s.submissions == nil || userID <= 0 {
		return apperrors.Unauthorized("UNAUTHORIZED", "unauthorized")
	}
	return s.submissions.WithdrawSubmissionRequest(ctx, userID, requestID)
}

func (s *ImageLibraryService) SyncSubmissionRequest(ctx context.Context, in SyncImagePlazaSubmissionParams) (*ImagePlazaSubmissionRequest, *ImageLibraryItem, error) {
	if s == nil || s.submissions == nil || s.repo == nil || in.UserID <= 0 {
		return nil, nil, apperrors.Unauthorized("UNAUTHORIZED", "unauthorized")
	}
	req, err := s.submissions.GetSubmissionRequestForUser(ctx, in.UserID, in.RequestID)
	if err != nil {
		return nil, nil, err
	}
	if req.Status == ImageSubmissionSynced {
		var item *ImageLibraryItem
		if req.LibraryAssetID != nil {
			item, err = s.GetForUser(ctx, in.UserID, *req.LibraryAssetID)
			if err != nil {
				return nil, nil, err
			}
		}
		req.ImageHeldClientSide = false
		return req, item, nil
	}
	if req.Status != ImageSubmissionApprovedPendingSync {
		return nil, nil, apperrors.Conflict("SUBMISSION_NOT_READY_TO_SYNC", "only approved submissions waiting for sync can be uploaded")
	}
	if int64(len(in.ImageData)) != req.ByteSize {
		return nil, nil, apperrors.BadRequest("IMAGE_SIZE_MISMATCH", "uploaded image size does not match the approved submission")
	}
	sum := sha256Hex(in.ImageData)
	if sum != req.ChecksumSHA256 {
		return nil, nil, apperrors.BadRequest("IMAGE_CHECKSUM_MISMATCH", "uploaded image checksum does not match the approved submission")
	}

	policy, err := s.storageSettings.LibraryRuntimeConfig(ctx)
	if err != nil {
		return nil, nil, err
	}

	item, _, err := s.ImportBytes(ctx, in.UserID, ImageLibraryImportInput{
		APIKeyID: req.APIKeyID, GroupID: req.GroupID, Platform: req.Platform,
		GenerationMode: req.GenerationMode, SourceType: req.SourceType, Model: req.Model,
		RequestedSize: req.RequestedSize, AspectRatio: req.AspectRatio, Quality: req.Quality,
		Title: req.Title, Prompt: req.PrivatePrompt, ImageData: in.ImageData, DeclaredMIME: firstNonEmpty(in.MIMEType, req.ContentType),
		IdempotencyKey: "sync:" + req.RequestID,
	})
	if err != nil {
		return nil, nil, err
	}

	pub, err := s.repo.CreatePublication(ctx, CreateImagePublicationParams{
		UserID: in.UserID, AssetID: item.AssetID, PublicTitle: req.PublicTitle,
		PublicPrompt: req.PublicPrompt, SharePrompt: req.SharePrompt,
		InitialStatus: ImagePublicationPublished,
		ExpiresAt:     time.Now().UTC().AddDate(0, 0, policy.RetentionDays),
		RateLimit:     policy.PublishPerMinute,
	})
	if err != nil {
		return nil, nil, err
	}
	synced, err := s.submissions.MarkSubmissionSynced(ctx, in.UserID, req.RequestID, item.AssetID, pub.PublicID)
	if err != nil {
		return nil, nil, err
	}
	synced.ImageHeldClientSide = false
	if synced.LibraryAssetID == nil {
		synced.LibraryAssetID = &item.AssetID
	}
	if synced.PublicationPublicID == nil {
		synced.PublicationPublicID = &pub.PublicID
	}
	return synced, item, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
