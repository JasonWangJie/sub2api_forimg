package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const localObjectURLPrefix = "local:"

// LocalImageServePathPrefix is the public HTTP path for HMAC-signed local objects.
const LocalImageServePathPrefix = "/v1/images/local/"

// LocalImageStorage stores durable library/plaza (or async) bytes on disk.
type LocalImageStorage struct {
	rootDir string

	// Optional URL resolution for async image_storage.backend=local:
	// - localURL / publicBaseURL: static/CDN root → base/object_key
	// - signServeBaseURL + signKey: HMAC signed download under LocalImageServePathPrefix
	// When neither is set, SignURL returns local:object_key (plaza/library internal use).
	localURL         string
	publicBaseURL    string
	signServeBaseURL string
	signKey          []byte
}

var _ DurableImageStorage = (*LocalImageStorage)(nil)
var _ DurableImageStorageIntentResolver = (*LocalImageStorage)(nil)

func NewLocalImageStorage(rootDir string) (*LocalImageStorage, error) {
	return NewLocalImageStorageWithURLOptions(rootDir, "", "", "", nil)
}

// NewLocalImageStorageWithURLOptions constructs local storage with optional HTTP URL signing.
// localURL takes precedence over publicBaseURL when forming client-facing links.
func NewLocalImageStorageWithURLOptions(rootDir, localURL, publicBaseURL, signServeBaseURL string, signKey []byte) (*LocalImageStorage, error) {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return nil, fmt.Errorf("local image storage root is required")
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve local image storage root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("create local image storage root: %w", err)
	}
	if err := os.Chmod(abs, 0o700); err != nil {
		return nil, fmt.Errorf("secure local image storage root: %w", err)
	}
	return &LocalImageStorage{
		rootDir:          abs,
		localURL:         strings.TrimRight(strings.TrimSpace(localURL), "/"),
		publicBaseURL:    strings.TrimRight(strings.TrimSpace(publicBaseURL), "/"),
		signServeBaseURL: strings.TrimRight(strings.TrimSpace(signServeBaseURL), "/"),
		signKey:          append([]byte(nil), signKey...),
	}, nil
}

func (s *LocalImageStorage) RootDir() string {
	if s == nil {
		return ""
	}
	return s.rootDir
}

func (s *LocalImageStorage) Save(ctx context.Context, key, contentType string, data []byte) (string, error) {
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

func (s *LocalImageStorage) SaveObject(ctx context.Context, key, contentType string, data []byte) (ObjectRef, error) {
	_ = ctx
	checksum := fmt.Sprintf("%x", sha256.Sum256(data))
	intent, err := s.ObjectIntent(key, contentType, int64(len(data)), checksum)
	if err != nil {
		return ObjectRef{}, err
	}
	fullPath, err := s.absolutePath(intent.ObjectKey)
	if err != nil {
		return ObjectRef{}, err
	}
	directory := filepath.Dir(fullPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return ObjectRef{}, fmt.Errorf("create local object directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return ObjectRef{}, fmt.Errorf("secure local object directory: %w", err)
	}
	tmp := fullPath + ".tmp"
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return ObjectRef{}, fmt.Errorf("write local object: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return ObjectRef{}, fmt.Errorf("secure local object: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return ObjectRef{}, fmt.Errorf("write local object: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return ObjectRef{}, fmt.Errorf("close local object: %w", err)
	}
	if err := os.Rename(tmp, fullPath); err != nil {
		_ = os.Remove(tmp)
		return ObjectRef{}, fmt.Errorf("commit local object: %w", err)
	}
	return intent, nil
}

func (s *LocalImageStorage) ObjectIntent(key, contentType string, sizeBytes int64, checksumSHA256 string) (ObjectRef, error) {
	if s == nil {
		return ObjectRef{}, fmt.Errorf("local image storage is nil")
	}
	key = strings.TrimLeft(strings.ReplaceAll(strings.TrimSpace(key), `\`, `/`), "/")
	if key == "" || strings.Contains(key, "..") {
		return ObjectRef{}, fmt.Errorf("object key is invalid")
	}
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	checksumSHA256 = strings.ToLower(strings.TrimSpace(checksumSHA256))
	if sizeBytes <= 0 || len(checksumSHA256) != 64 {
		return ObjectRef{}, fmt.Errorf("object intent size and SHA-256 are required")
	}
	return ObjectRef{
		Provider:       config.ImageStorageProviderLocal,
		Bucket:         "local",
		ObjectKey:      key,
		ContentType:    contentType,
		SizeBytes:      sizeBytes,
		ChecksumSHA256: checksumSHA256,
	}, nil
}

func (s *LocalImageStorage) SignURL(_ context.Context, ref ObjectRef, expiry time.Duration) (ObjectAccess, error) {
	if err := s.validateRef(ref); err != nil {
		return ObjectAccess{}, err
	}
	base := s.localURL
	if base == "" {
		base = s.publicBaseURL
	}
	if base != "" {
		joined, err := JoinLocalObjectURL(base, ref.ObjectKey)
		if err != nil {
			return ObjectAccess{}, err
		}
		return ObjectAccess{URL: joined}, nil
	}
	if len(s.signKey) > 0 && s.signServeBaseURL != "" {
		if expiry <= 0 {
			expiry = time.Hour
		}
		expiresAt := time.Now().UTC().Add(expiry)
		signed, err := SignLocalImageURL(s.signServeBaseURL, ref.ObjectKey, expiresAt, s.signKey)
		if err != nil {
			return ObjectAccess{}, err
		}
		return ObjectAccess{URL: signed, ExpiresAt: expiresAt}, nil
	}
	return ObjectAccess{URL: localObjectURLPrefix + ref.ObjectKey}, nil
}

func (s *LocalImageStorage) Read(_ context.Context, ref ObjectRef) (io.ReadCloser, error) {
	if err := s.validateRef(ref); err != nil {
		return nil, err
	}
	fullPath, err := s.absolutePath(ref.ObjectKey)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(fullPath)
	if err != nil {
		return nil, fmt.Errorf("open local object: %w", err)
	}
	return file, nil
}

func (s *LocalImageStorage) Head(_ context.Context, ref ObjectRef) (ObjectMetadata, error) {
	if err := s.validateRef(ref); err != nil {
		return ObjectMetadata{}, err
	}
	fullPath, err := s.absolutePath(ref.ObjectKey)
	if err != nil {
		return ObjectMetadata{}, err
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return ObjectMetadata{}, fmt.Errorf("stat local object: %w", err)
	}
	observed := ref
	observed.SizeBytes = info.Size()
	return ObjectMetadata{
		ObjectRef:    observed,
		LastModified: info.ModTime().UTC(),
	}, nil
}

func (s *LocalImageStorage) Delete(_ context.Context, ref ObjectRef) error {
	if err := s.validateRef(ref); err != nil {
		return err
	}
	fullPath, err := s.absolutePath(ref.ObjectKey)
	if err != nil {
		return err
	}
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete local object: %w", err)
	}
	return nil
}

func (s *LocalImageStorage) validateRef(ref ObjectRef) error {
	if s == nil {
		return fmt.Errorf("local image storage is nil")
	}
	if !strings.EqualFold(strings.TrimSpace(ref.Provider), config.ImageStorageProviderLocal) {
		return fmt.Errorf("local storage cannot serve provider %q", ref.Provider)
	}
	if strings.TrimSpace(ref.ObjectKey) == "" {
		return fmt.Errorf("object key is required")
	}
	return nil
}

func (s *LocalImageStorage) absolutePath(objectKey string) (string, error) {
	key := strings.TrimLeft(strings.ReplaceAll(strings.TrimSpace(objectKey), `\`, `/`), "/")
	if key == "" || strings.Contains(key, "..") {
		return "", fmt.Errorf("object key is invalid")
	}
	full := filepath.Join(s.rootDir, filepath.FromSlash(key))
	absRoot := s.rootDir
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absFull)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("object key escapes local storage root")
	}
	return absFull, nil
}

func escapeLocalObjectKey(key string) string {
	parts := strings.Split(strings.Trim(strings.ReplaceAll(key, `\`, `/`), "/"), "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// SignLocalImageURL builds an absolute HMAC-signed download URL.
func SignLocalImageURL(serveBaseURL, objectKey string, expiresAt time.Time, key []byte) (string, error) {
	objectKey = strings.TrimLeft(strings.ReplaceAll(strings.TrimSpace(objectKey), `\`, `/`), "/")
	if objectKey == "" || strings.Contains(objectKey, "..") {
		return "", fmt.Errorf("object key is invalid")
	}
	if len(key) == 0 {
		return "", fmt.Errorf("local image sign key is required")
	}
	serveBaseURL = strings.TrimRight(strings.TrimSpace(serveBaseURL), "/")
	if serveBaseURL == "" {
		return "", fmt.Errorf("local image serve base url is required")
	}
	exp := expiresAt.UTC().Unix()
	sig := LocalImageURLSignature(objectKey, exp, key)
	u, err := url.Parse(serveBaseURL + LocalImageServePathPrefix + escapeLocalObjectKey(objectKey))
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("exp", strconv.FormatInt(exp, 10))
	q.Set("sig", sig)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// LocalImageURLSignature returns hex(HMAC-SHA256(key, objectKey + "\n" + exp)).
func LocalImageURLSignature(objectKey string, exp int64, key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(objectKey))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyLocalImageURLSignature checks expiry and HMAC for a local download request.
func VerifyLocalImageURLSignature(objectKey, expRaw, sig string, key []byte, now time.Time) error {
	objectKey = strings.TrimLeft(strings.ReplaceAll(strings.TrimSpace(objectKey), `\`, `/`), "/")
	if objectKey == "" || strings.Contains(objectKey, "..") {
		return fmt.Errorf("object key is invalid")
	}
	exp, err := strconv.ParseInt(strings.TrimSpace(expRaw), 10, 64)
	if err != nil || exp <= 0 {
		return fmt.Errorf("invalid expiry")
	}
	if now.UTC().Unix() > exp {
		return fmt.Errorf("signed url expired")
	}
	expected := LocalImageURLSignature(objectKey, exp, key)
	if !hmac.Equal([]byte(strings.ToLower(strings.TrimSpace(sig))), []byte(expected)) {
		return fmt.Errorf("invalid signature")
	}
	return nil
}
