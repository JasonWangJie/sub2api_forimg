package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

const defaultSuperbedUploadURL = "https://api.superbed.cn/upload"

// SuperbedImageStorage uploads async image bytes to 聚合图床 (Superbed).
// Docs: https://www.superbed.cn/help
type SuperbedImageStorage struct {
	token      string
	categories string
	uploadURL  string
	localURL   string
	httpClient *http.Client

	// recentCDN remembers Superbed CDN URLs for logical keys within this process
	// so TestConnection/Read can still fetch after upload when local_url is set.
	recentMu  sync.Mutex
	recentCDN map[string]string
}

var _ service.ImageStorage = (*SuperbedImageStorage)(nil)
var _ service.DurableImageStorage = (*SuperbedImageStorage)(nil)
var _ service.DurableImageStorageIntentResolver = (*SuperbedImageStorage)(nil)

// NewSuperbedImageStorage constructs a Superbed-backed durable image store.
func NewSuperbedImageStorage(cfg *config.ImageStorageConfig) (*SuperbedImageStorage, error) {
	if cfg == nil {
		return nil, fmt.Errorf("image storage config is nil")
	}
	token := strings.TrimSpace(cfg.Superbed.Token)
	if token == "" {
		return nil, fmt.Errorf("superbed token is required")
	}
	uploadURL := strings.TrimSpace(cfg.Superbed.UploadURL)
	if uploadURL == "" {
		uploadURL = defaultSuperbedUploadURL
	}
	u, err := url.Parse(uploadURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("invalid superbed upload_url %q", uploadURL)
	}
	return &SuperbedImageStorage{
		token:      token,
		categories: strings.TrimSpace(cfg.Superbed.Categories),
		uploadURL:  strings.TrimRight(uploadURL, "/"),
		localURL:   strings.TrimRight(strings.TrimSpace(cfg.Superbed.LocalURL), "/"),
		httpClient: &http.Client{Timeout: 120 * time.Second},
		recentCDN:  make(map[string]string),
	}, nil
}

func (s *SuperbedImageStorage) Save(ctx context.Context, key, contentType string, data []byte) (string, error) {
	ref, err := s.SaveObject(ctx, key, contentType, data)
	if err != nil {
		return "", err
	}
	access, err := s.SignURL(ctx, ref, 0)
	if err != nil {
		return "", err
	}
	return access.URL, nil
}

func (s *SuperbedImageStorage) ObjectIntent(key, contentType string, sizeBytes int64, checksumSHA256 string) (service.ObjectRef, error) {
	if s == nil {
		return service.ObjectRef{}, fmt.Errorf("superbed image storage is nil")
	}
	key = strings.TrimLeft(strings.ReplaceAll(strings.TrimSpace(key), `\`, `/`), "/")
	if key == "" || strings.Contains(key, "..") {
		return service.ObjectRef{}, fmt.Errorf("object key is invalid")
	}
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	checksumSHA256 = strings.ToLower(strings.TrimSpace(checksumSHA256))
	if sizeBytes <= 0 || len(checksumSHA256) != 64 {
		return service.ObjectRef{}, fmt.Errorf("object intent size and SHA-256 are required")
	}
	bucket := s.categories
	if bucket == "" {
		bucket = "_"
	}
	return service.ObjectRef{
		Provider:       config.ImageStorageProviderSuperbed,
		Bucket:         bucket,
		ObjectKey:      key,
		ContentType:    contentType,
		SizeBytes:      sizeBytes,
		ChecksumSHA256: checksumSHA256,
	}, nil
}

func (s *SuperbedImageStorage) SaveObject(ctx context.Context, key, contentType string, data []byte) (service.ObjectRef, error) {
	checksum := fmt.Sprintf("%x", sha256.Sum256(data))
	intent, err := s.ObjectIntent(key, contentType, int64(len(data)), checksum)
	if err != nil {
		return service.ObjectRef{}, err
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("token", s.token); err != nil {
		return service.ObjectRef{}, fmt.Errorf("superbed form token: %w", err)
	}
	if s.categories != "" {
		if err := writer.WriteField("categories", s.categories); err != nil {
			return service.ObjectRef{}, fmt.Errorf("superbed form categories: %w", err)
		}
	}
	filename := path.Base(key)
	if filename == "" || filename == "." || filename == "/" {
		filename = "image.bin"
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return service.ObjectRef{}, fmt.Errorf("superbed form file: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return service.ObjectRef{}, fmt.Errorf("superbed form write: %w", err)
	}
	if err := writer.Close(); err != nil {
		return service.ObjectRef{}, fmt.Errorf("superbed form close: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.uploadURL, &body)
	if err != nil {
		return service.ObjectRef{}, fmt.Errorf("superbed request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-API-Key", s.token)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return service.ObjectRef{}, fmt.Errorf("superbed upload: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return service.ObjectRef{}, fmt.Errorf("superbed read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return service.ObjectRef{}, fmt.Errorf("superbed upload status %d: %s", resp.StatusCode, truncateForErr(respBody, 256))
	}
	publicURL, err := parseSuperbedUploadURL(respBody)
	if err != nil {
		return service.ObjectRef{}, err
	}
	if s.localURL != "" {
		// Persist logical key so SignURL can join local_url/object_key for clients.
		s.rememberCDN(intent.ObjectKey, publicURL)
		return intent, nil
	}
	intent.ObjectKey = publicURL
	return intent, nil
}

func (s *SuperbedImageStorage) rememberCDN(logicalKey, cdnURL string) {
	s.recentMu.Lock()
	defer s.recentMu.Unlock()
	if s.recentCDN == nil {
		s.recentCDN = make(map[string]string)
	}
	s.recentCDN[logicalKey] = cdnURL
}

func (s *SuperbedImageStorage) lookupCDN(logicalKey string) string {
	s.recentMu.Lock()
	defer s.recentMu.Unlock()
	if s.recentCDN == nil {
		return ""
	}
	return s.recentCDN[logicalKey]
}

func parseSuperbedUploadURL(body []byte) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("superbed response json: %w", err)
	}
	candidates := []any{payload["url"], payload["Url"], payload["URL"]}
	if data, ok := payload["data"].(map[string]any); ok {
		candidates = append(candidates, data["url"], data["Url"], data["URL"])
	}
	for _, c := range candidates {
		if s, ok := c.(string); ok {
			s = strings.TrimSpace(s)
			if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
				return s, nil
			}
		}
	}
	return "", fmt.Errorf("superbed response missing url: %s", truncateForErr(body, 256))
}

func (s *SuperbedImageStorage) SignURL(_ context.Context, ref service.ObjectRef, _ time.Duration) (service.ObjectAccess, error) {
	if err := s.validateRef(ref); err != nil {
		return service.ObjectAccess{}, err
	}
	if s.localURL != "" {
		joined, err := service.JoinLocalObjectURL(s.localURL, ref.ObjectKey)
		if err != nil {
			return service.ObjectAccess{}, err
		}
		return service.ObjectAccess{URL: joined}, nil
	}
	if !strings.HasPrefix(ref.ObjectKey, "http://") && !strings.HasPrefix(ref.ObjectKey, "https://") {
		return service.ObjectAccess{}, fmt.Errorf("superbed object key is not a public url")
	}
	return service.ObjectAccess{URL: ref.ObjectKey}, nil
}

func (s *SuperbedImageStorage) resolveFetchURL(ref service.ObjectRef) (string, error) {
	key := strings.TrimSpace(ref.ObjectKey)
	if strings.HasPrefix(key, "http://") || strings.HasPrefix(key, "https://") {
		return key, nil
	}
	if cdn := s.lookupCDN(key); cdn != "" {
		return cdn, nil
	}
	if s.localURL != "" {
		return service.JoinLocalObjectURL(s.localURL, key)
	}
	return "", fmt.Errorf("superbed object key is not a public url")
}

func (s *SuperbedImageStorage) Read(ctx context.Context, ref service.ObjectRef) (io.ReadCloser, error) {
	if err := s.validateRef(ref); err != nil {
		return nil, err
	}
	urlStr, err := s.resolveFetchURL(ref)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("superbed read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("superbed read status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

func (s *SuperbedImageStorage) Head(ctx context.Context, ref service.ObjectRef) (service.ObjectMetadata, error) {
	if err := s.validateRef(ref); err != nil {
		return service.ObjectMetadata{}, err
	}
	urlStr, err := s.resolveFetchURL(ref)
	if err != nil {
		return service.ObjectMetadata{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, urlStr, nil)
	if err != nil {
		return service.ObjectMetadata{}, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return service.ObjectMetadata{}, fmt.Errorf("superbed head: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return service.ObjectMetadata{}, fmt.Errorf("superbed head status %d", resp.StatusCode)
	}
	observed := ref
	if resp.ContentLength > 0 {
		observed.SizeBytes = resp.ContentLength
	}
	return service.ObjectMetadata{ObjectRef: observed}, nil
}

func (s *SuperbedImageStorage) Delete(ctx context.Context, ref service.ObjectRef) error {
	if err := s.validateRef(ref); err != nil {
		return err
	}
	// Superbed delete API varies by plan; orphan cleanup must not fail the retention loop.
	logger.L().Debug("superbed.delete_skipped",
		zap.String("object_key", truncateForErr([]byte(ref.ObjectKey), 128)))
	_ = ctx
	return nil
}

func (s *SuperbedImageStorage) validateRef(ref service.ObjectRef) error {
	if s == nil {
		return fmt.Errorf("superbed image storage is nil")
	}
	if !strings.EqualFold(strings.TrimSpace(ref.Provider), config.ImageStorageProviderSuperbed) {
		return fmt.Errorf("superbed storage cannot serve provider %q", ref.Provider)
	}
	if strings.TrimSpace(ref.ObjectKey) == "" {
		return fmt.Errorf("object key is required")
	}
	return nil
}

func truncateForErr(b []byte, n int) string {
	s := string(b)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
