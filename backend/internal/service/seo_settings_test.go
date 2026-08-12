package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeSEOSettings(t *testing.T) {
	settings := &SystemSettings{
		SEOIndexingEnabled: true,
		SEOSiteURL:         "  https://example.com  ",
		SEOTitle:           "  Example & Search  ",
		SEOKeywords:        []string{" API ", "Gateway", "api", ""},
		SEODescription:     "  A useful description.  ",
		SEOSocialImageURL:  " https://cdn.example.com/card.jpg?v=1 ",
		SEOVerificationTags: `<meta content="google-token&amp;value" name="google-site-verification">
<meta name="msvalidate.01" content="bing-token" />`,
	}

	require.NoError(t, NormalizeSEOSettings(settings))
	assert.Equal(t, "https://example.com/", settings.SEOSiteURL)
	assert.Equal(t, "Example & Search", settings.SEOTitle)
	assert.Equal(t, []string{"API", "Gateway"}, settings.SEOKeywords)
	assert.Equal(t, "A useful description.", settings.SEODescription)
	assert.Equal(t, "https://cdn.example.com/card.jpg?v=1", settings.SEOSocialImageURL)
	assert.Equal(t, `<meta name="google-site-verification" content="google-token&amp;value" />
<meta name="msvalidate.01" content="bing-token" />`, settings.SEOVerificationTags)
}

func TestNormalizeSEOSettingsRejectsInvalidURLsAndLimits(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SystemSettings)
	}{
		{name: "relative site URL", mutate: func(s *SystemSettings) { s.SEOSiteURL = "/home" }},
		{name: "site URL credentials", mutate: func(s *SystemSettings) { s.SEOSiteURL = "https://user:pass@example.com/" }},
		{name: "site URL path", mutate: func(s *SystemSettings) { s.SEOSiteURL = "https://example.com/app" }},
		{name: "site URL query", mutate: func(s *SystemSettings) { s.SEOSiteURL = "https://example.com/?a=1" }},
		{name: "site URL fragment", mutate: func(s *SystemSettings) { s.SEOSiteURL = "https://example.com/#top" }},
		{name: "site URL scheme", mutate: func(s *SystemSettings) { s.SEOSiteURL = "ftp://example.com/" }},
		{name: "social image credentials", mutate: func(s *SystemSettings) { s.SEOSocialImageURL = "https://user@example.com/card.jpg" }},
		{name: "title limit", mutate: func(s *SystemSettings) { s.SEOTitle = strings.Repeat("标", MaxSEOTitleLength+1) }},
		{name: "description limit", mutate: func(s *SystemSettings) { s.SEODescription = strings.Repeat("d", MaxSEODescriptionLength+1) }},
		{name: "keyword count", mutate: func(s *SystemSettings) {
			s.SEOKeywords = make([]string, MaxSEOKeywords+1)
			for i := range s.SEOKeywords {
				s.SEOKeywords[i] = fmt.Sprintf("keyword-%d", i)
			}
		}},
		{name: "keyword length", mutate: func(s *SystemSettings) { s.SEOKeywords = []string{strings.Repeat("k", MaxSEOKeywordLength+1)} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &SystemSettings{}
			tt.mutate(settings)
			assert.Error(t, NormalizeSEOSettings(settings))
		})
	}
}

func TestNormalizeSEOKeywords(t *testing.T) {
	keywords, err := NormalizeSEOKeywords([]string{" Go ", "API", "go", " api ", "Gateway"})
	require.NoError(t, err)
	assert.Equal(t, []string{"Go", "API", "Gateway"}, keywords)

	raw, err := json.Marshal(keywords)
	require.NoError(t, err)
	assert.Equal(t, keywords, ParseSEOKeywords(string(raw)))
	assert.Empty(t, ParseSEOKeywords("not-json"))
}

func TestNormalizeSEOVerificationTagsAllowsTwentyTagsSeparatedByWhitespace(t *testing.T) {
	values := make([]string, MaxSEOVerificationTags)
	for i := range values {
		values[i] = fmt.Sprintf(`<meta name="verify.%d" content="token-%d" />`, i, i)
	}

	result, err := NormalizeSEOVerificationTags(strings.Join(values, "\n\n  "))
	require.NoError(t, err)
	assert.Equal(t, MaxSEOVerificationTags, strings.Count(result, "<meta "))
}

func TestNormalizeSEOVerificationTagsRejectsUnsafeFragments(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "script", raw: `<script>alert(1)</script>`},
		{name: "style", raw: `<style>body{display:none}</style>`},
		{name: "link", raw: `<link rel="stylesheet" href="https://example.com/x.css">`},
		{name: "iframe", raw: `<iframe src="https://example.com"></iframe>`},
		{name: "http equiv", raw: `<meta http-equiv="refresh" content="0;url=https://example.com">`},
		{name: "event handler", raw: `<meta name="verify" content="token" onload="alert(1)">`},
		{name: "extra attribute", raw: `<meta name="verify" content="token" id="x">`},
		{name: "nested node", raw: `<div><meta name="verify" content="token"></div>`},
		{name: "reserved description", raw: `<meta name="description" content="override">`},
		{name: "reserved robots", raw: `<meta name="ROBOTS" content="index">`},
		{name: "reserved twitter title", raw: `<meta name="twitter:title" content="override">`},
		{name: "duplicate name", raw: `<meta name="verify" content="one"><meta name="VERIFY" content="two">`},
		{name: "duplicate empty name attribute", raw: `<meta name="" name="verify" content="token">`},
		{name: "duplicate empty content attribute", raw: `<meta name="verify" content="" content="token">`},
		{name: "empty content", raw: `<meta name="verify" content="">`},
		{name: "invalid name", raw: `<meta name="verify tag" content="token">`},
		{name: "malicious close", raw: `</head><script>alert(1)</script><head><meta name="verify" content="token">`},
		{name: "stray closing tag", raw: `</head><meta name="verify" content="token">`},
		{name: "too much HTML", raw: strings.Repeat(" ", MaxSEOVerificationTagsLength) + `<meta name="verify" content="token">`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeSEOVerificationTags(tt.raw)
			assert.Error(t, err)
		})
	}
}

func TestParseSettingsSEODefaultsAndRoundTrip(t *testing.T) {
	service := &SettingService{cfg: &config.Config{}}
	defaults := service.parseSettings(map[string]string{})
	assert.True(t, defaults.SEOIndexingEnabled)
	assert.Empty(t, defaults.SEOSiteURL)
	assert.Empty(t, defaults.SEOKeywords)

	parsed := service.parseSettings(map[string]string{
		SettingKeySEOIndexingEnabled:  "false",
		SettingKeySEOSiteURL:          "https://example.com/",
		SettingKeySEOTitle:            "Example",
		SettingKeySEOKeywords:         `["API","Gateway"]`,
		SettingKeySEODescription:      "Description",
		SettingKeySEOSocialImageURL:   "https://example.com/card.jpg",
		SettingKeySEOVerificationTags: `<meta name="google-site-verification" content="token" />`,
	})
	assert.False(t, parsed.SEOIndexingEnabled)
	assert.Equal(t, "https://example.com/", parsed.SEOSiteURL)
	assert.Equal(t, "Example", parsed.SEOTitle)
	assert.Equal(t, []string{"API", "Gateway"}, parsed.SEOKeywords)
	assert.Equal(t, "Description", parsed.SEODescription)
	assert.Equal(t, "https://example.com/card.jpg", parsed.SEOSocialImageURL)
	assert.Contains(t, parsed.SEOVerificationTags, "google-site-verification")
}
