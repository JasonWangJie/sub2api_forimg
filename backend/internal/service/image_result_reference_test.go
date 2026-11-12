package service

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestResolveImageResultReferences(t *testing.T) {
	cfg := ImageResultReferenceConfig{Provider: "custom_s3", Bucket: "images", StorageIdentity: "identity", ResultPrefix: "images/results/", URLBases: []string{"https://cdn.example/base", "https://account.r2.cloudflarestorage.com/images"}}
	for _, tc := range []struct {
		name    string
		urls    []string
		want    int
		invalid bool
	}{
		{"public", []string{"https://cdn.example/base/images/results/a.png"}, 1, false},
		{"signed", []string{"https://account.r2.cloudflarestorage.com/images/images/results/a.png?X-Amz-Signature=opaque"}, 1, false},
		{"duplicate", []string{"https://cdn.example/base/images/results/a.png?x=1", "https://cdn.example/base/images/results/a.png?x=2"}, 1, false},
		{"encoded_key", []string{"https://cdn.example/base/images/results/a%20b.png"}, 1, false},
		{"other_origin", []string{"https://cdn.example.evil/base/images/results/a.png"}, 0, false},
		{"other_path", []string{"https://cdn.example/base-other/images/results/a.png"}, 0, false},
		{"input", []string{"https://cdn.example/base/images/inputs/a.png"}, 0, false},
		{"traversal", []string{"https://cdn.example/base/images/results/%2e%2e/a.png"}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refs, err := ResolveImageResultReferences(cfg, tc.urls)
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, refs, tc.want)
			for _, ref := range refs {
				require.Equal(t, "identity", ref.StorageIdentity)
				require.Equal(t, "images", ref.Bucket)
			}
		})
	}
}

func TestImageStorageIdentityExcludesCredentialsAndPublicURL(t *testing.T) {
	cfg := config.ImageStorageConfig{Backend: "oss", Provider: "custom_s3", Endpoint: "https://account.r2.cloudflarestorage.com", Region: "auto", Bucket: "images", ForcePathStyle: true}
	identity := ImageStorageIdentityFingerprint(&cfg)
	require.Len(t, identity, 64)
	cfg.AccessKeyID = "rotated"
	cfg.SecretAccessKey = "secret"
	cfg.PublicBaseURL = "https://new-cdn.example"
	require.Equal(t, identity, ImageStorageIdentityFingerprint(&cfg))
	cfg.Endpoint = "https://another-account.r2.cloudflarestorage.com"
	require.NotEqual(t, identity, ImageStorageIdentityFingerprint(&cfg))
}
