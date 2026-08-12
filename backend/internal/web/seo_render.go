//go:build embed

package web

import (
	"bytes"
	"encoding/json"
	htmlpkg "html"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	seoSettingsStartMarker = "<!-- SEO_SETTINGS_START -->"
	seoSettingsEndMarker   = "<!-- SEO_SETTINGS_END -->"
	seoIndexDirective      = "index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1"
	seoNoIndexDirective    = "noindex, nofollow"
)

type seoSettingsPayload struct {
	SiteName             string   `json:"site_name"`
	SiteSubtitle         string   `json:"site_subtitle"`
	IndexingEnabled      *bool    `json:"seo_indexing_enabled"`
	SiteURL              string   `json:"seo_site_url"`
	Title                string   `json:"seo_title"`
	Keywords             []string `json:"seo_keywords"`
	Description          string   `json:"seo_description"`
	SocialImageURL       string   `json:"seo_social_image_url"`
	VerificationTagsHTML string   `json:"seo_verification_tags"`
}

type resolvedSEOSettings struct {
	SiteName             string
	IndexingEnabled      bool
	SiteURL              string
	Title                string
	Keywords             []string
	Description          string
	SocialImageURL       string
	VerificationTagsHTML string
}

func resolveSEOSettings(settingsJSON []byte) resolvedSEOSettings {
	var payload seoSettingsPayload
	if err := json.Unmarshal(settingsJSON, &payload); err != nil {
		return defaultSEOSettings()
	}

	defaults := defaultSEOSettings()
	result := resolvedSEOSettings{
		SiteName:       firstNonEmptySEO(payload.SiteName, defaults.SiteName),
		SiteURL:        safeSEOSiteURL(payload.SiteURL),
		Keywords:       payload.Keywords,
		SocialImageURL: safeSEOImageURL(payload.SocialImageURL),
	}
	result.IndexingEnabled = payload.IndexingEnabled == nil || *payload.IndexingEnabled
	result.Title = firstNonEmptySEO(payload.Title, result.SiteName+" - AI API Gateway")
	result.Description = firstNonEmptySEO(payload.Description, payload.SiteSubtitle, defaults.Description)
	if tags, err := service.NormalizeSEOVerificationTags(payload.VerificationTagsHTML); err == nil {
		result.VerificationTagsHTML = tags
	}
	return result
}

func defaultSEOSettings() resolvedSEOSettings {
	return resolvedSEOSettings{
		SiteName:        "Sub2API",
		IndexingEnabled: true,
		Title:           "Sub2API - AI API Gateway",
		Description:     "Subscription to API Conversion Platform",
	}
}

func firstNonEmptySEO(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func safeSEOSiteURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return ""
	}
	parsed.Path = "/"
	parsed.RawPath = ""
	return parsed.String()
}

func safeSEOImageURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return ""
	}
	return parsed.String()
}

func injectSEOSettings(html, settingsJSON []byte) []byte {
	start := bytes.Index(html, []byte(seoSettingsStartMarker))
	end := bytes.Index(html, []byte(seoSettingsEndMarker))
	if start == -1 || end == -1 || end < start {
		return html
	}
	end += len(seoSettingsEndMarker)
	block := buildSEOBlock(resolveSEOSettings(settingsJSON))

	result := make([]byte, 0, len(html)-end+start+len(block))
	result = append(result, html[:start]...)
	result = append(result, block...)
	result = append(result, html[end:]...)
	return result
}

func buildSEOBlock(settings resolvedSEOSettings) []byte {
	escape := htmlpkg.EscapeString
	directive := seoIndexDirective
	if !settings.IndexingEnabled {
		directive = seoNoIndexDirective
	}

	var block strings.Builder
	block.WriteString(seoSettingsStartMarker)
	block.WriteString("\n    <title>" + escape(settings.Title) + "</title>")
	block.WriteString("\n    <meta name=\"description\" content=\"" + escape(settings.Description) + "\" />")
	if len(settings.Keywords) > 0 {
		block.WriteString("\n    <meta name=\"keywords\" content=\"" + escape(strings.Join(settings.Keywords, ", ")) + "\" />")
	}
	for _, name := range []string{"robots", "googlebot", "bingbot"} {
		block.WriteString("\n    <meta name=\"" + name + "\" content=\"" + directive + "\" />")
	}
	block.WriteString("\n    <meta name=\"application-name\" content=\"" + escape(settings.SiteName) + "\" />")
	if settings.SiteURL != "" {
		block.WriteString("\n    <link rel=\"canonical\" href=\"" + escape(settings.SiteURL) + "\" />")
	}
	block.WriteString("\n    <meta property=\"og:type\" content=\"website\" />")
	block.WriteString("\n    <meta property=\"og:site_name\" content=\"" + escape(settings.SiteName) + "\" />")
	if settings.SiteURL != "" {
		block.WriteString("\n    <meta property=\"og:url\" content=\"" + escape(settings.SiteURL) + "\" />")
	}
	block.WriteString("\n    <meta property=\"og:title\" content=\"" + escape(settings.Title) + "\" />")
	block.WriteString("\n    <meta property=\"og:description\" content=\"" + escape(settings.Description) + "\" />")
	if settings.SocialImageURL != "" {
		block.WriteString("\n    <meta property=\"og:image\" content=\"" + escape(settings.SocialImageURL) + "\" />")
	}
	card := "summary"
	if settings.SocialImageURL != "" {
		card = "summary_large_image"
	}
	block.WriteString("\n    <meta name=\"twitter:card\" content=\"" + card + "\" />")
	block.WriteString("\n    <meta name=\"twitter:title\" content=\"" + escape(settings.Title) + "\" />")
	block.WriteString("\n    <meta name=\"twitter:description\" content=\"" + escape(settings.Description) + "\" />")
	if settings.SocialImageURL != "" {
		block.WriteString("\n    <meta name=\"twitter:image\" content=\"" + escape(settings.SocialImageURL) + "\" />")
	}
	if settings.SiteURL != "" {
		structuredData, _ := json.Marshal(map[string]string{
			"@context":    "https://schema.org",
			"@type":       "WebSite",
			"name":        settings.SiteName,
			"url":         settings.SiteURL,
			"description": settings.Description,
		})
		block.WriteString("\n    <script nonce=\"" + NonceHTMLPlaceholder + "\" type=\"application/ld+json\">" + string(structuredData) + "</script>")
	}
	if settings.VerificationTagsHTML != "" {
		for _, tag := range strings.Split(settings.VerificationTagsHTML, "\n") {
			block.WriteString("\n    " + tag)
		}
	}
	block.WriteString("\n    " + seoSettingsEndMarker)
	return []byte(block.String())
}
