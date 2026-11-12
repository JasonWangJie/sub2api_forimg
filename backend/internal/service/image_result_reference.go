package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// ImageResultReferenceConfig contains public addressing information only. It
// intentionally excludes credentials and is also safe to snapshot for cleanup.
type ImageResultReferenceConfig struct {
	Backend         string
	Provider        string
	Bucket          string
	StorageIdentity string
	URLBases        []string
	ResultPrefix    string
}

func ImageStorageIdentityFingerprint(cfg *config.ImageStorageConfig) string {
	if cfg == nil {
		return ""
	}
	identity := imageStorageIdentity{Backend: cfg.NormalizedBackend(), Provider: cfg.Provider}
	switch identity.Backend {
	case ImageStorageBackendLocal:
		identity.LocalDataDir = cfg.Local.DataDir
		if absolute, err := filepath.Abs(identity.LocalDataDir); err == nil {
			identity.LocalDataDir = filepath.Clean(absolute)
		}
	case ImageStorageBackendSuperbed:
		identity.SuperbedCat, identity.SuperbedURL = cfg.Superbed.Categories, cfg.Superbed.UploadURL
	default:
		identity.Bucket, identity.Endpoint, identity.Region, identity.ForcePathStyle = cfg.Bucket, strings.TrimRight(cfg.Endpoint, "/"), cfg.Region, cfg.ForcePathStyle
	}
	encoded, _ := json.Marshal(identity)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func (s *ImageStorageSettingService) ResultReferenceConfig(ctx context.Context) (ImageResultReferenceConfig, error) {
	if s == nil {
		return ImageResultReferenceConfig{}, fmt.Errorf("image storage settings unavailable")
	}
	cfg, err := s.effectiveConfig(ctx)
	if err != nil {
		return ImageResultReferenceConfig{}, err
	}
	if err := ResolveImageStorageProvider(cfg); err != nil {
		return ImageResultReferenceConfig{}, err
	}
	out := ImageResultReferenceConfig{Backend: cfg.NormalizedBackend(), Provider: cfg.Provider, Bucket: cfg.Bucket,
		StorageIdentity: ImageStorageIdentityFingerprint(cfg), ResultPrefix: strings.Trim(cfg.Prefix, "/") + "/results/"}
	out.ResultPrefix = strings.TrimLeft(out.ResultPrefix, "/")
	if cfg.PublicBaseURL != "" {
		out.URLBases = append(out.URLBases, strings.TrimRight(cfg.PublicBaseURL, "/"))
	}
	if out.Backend == ImageStorageBackendOSS {
		endpoint, parseErr := url.Parse(cfg.Endpoint)
		if parseErr == nil && endpoint.Host != "" {
			out.URLBases = append(out.URLBases, strings.TrimRight(endpoint.String(), "/")+"/"+url.PathEscape(cfg.Bucket))
			virtual := *endpoint
			virtual.Host = cfg.Bucket + "." + endpoint.Host
			out.URLBases = append(out.URLBases, strings.TrimRight(virtual.String(), "/"))
		}
	}
	if out.Backend == ImageStorageBackendLocal {
		out.Provider, out.Bucket = ImageStorageProviderLocal, "local"
		if cfg.Local.LocalURL != "" {
			out.URLBases = append(out.URLBases, strings.TrimRight(cfg.Local.LocalURL, "/"))
		}
		for _, base := range []string{cfg.SignServeBaseURL} {
			if base != "" {
				out.URLBases = append(out.URLBases, strings.TrimRight(base, "/")+"/v1/images/local")
			}
		}
	}
	return out, nil
}

// ResolveImageResultReferences recognizes only exact configured URL origins
// and result prefixes. External URLs retain the existing reference policy.
func ResolveImageResultReferences(cfg ImageResultReferenceConfig, urls []string) ([]ObjectRef, error) {
	refs := make([]ObjectRef, 0, len(urls))
	seen := map[string]bool{}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, ErrAsyncImageInvalidInput
		}
		for _, rawBase := range cfg.URLBases {
			base, err := url.Parse(rawBase)
			if err != nil || !strings.EqualFold(u.Scheme, base.Scheme) || !strings.EqualFold(u.Host, base.Host) || u.User != nil {
				continue
			}
			prefix := strings.TrimRight(base.Path, "/") + "/"
			if !strings.HasPrefix(u.Path, prefix) {
				continue
			}
			key := strings.TrimPrefix(u.Path, prefix)
			if !strings.HasPrefix(key, cfg.ResultPrefix) {
				continue
			}
			if strings.Contains(key, "\\") || path.Clean(key) != key {
				return nil, ErrAsyncImageInvalidInput
			}
			if !seen[key] {
				seen[key] = true
				refs = append(refs, ObjectRef{Provider: cfg.Provider, Bucket: cfg.Bucket, ObjectKey: key, StorageIdentity: cfg.StorageIdentity})
			}
			break
		}
	}
	return refs, nil
}

type imageCleanupRequestKey struct{}

// WithImageCleanupRequest makes each HEAD/DELETE consume a cleanup rate token
// and disables SDK retries, so every real request stays inside the budget.
func WithImageCleanupRequest(ctx context.Context, beforeRequest func(context.Context) error) context.Context {
	return context.WithValue(ctx, imageCleanupRequestKey{}, beforeRequest)
}

func ImageCleanupRequestGate(ctx context.Context) (func(context.Context) error, bool) {
	gate, ok := ctx.Value(imageCleanupRequestKey{}).(func(context.Context) error)
	return gate, ok
}
