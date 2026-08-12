//go:build embed

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectSEOSettingsRendersCompleteSafeBlock(t *testing.T) {
	template := []byte(`<html><head>
<!-- SEO_SETTINGS_START --><title>old</title><!-- SEO_SETTINGS_END -->
</head></html>`)
	settings := map[string]any{
		"site_name":             `Example & Co`,
		"site_subtitle":         "Fallback",
		"seo_indexing_enabled":  true,
		"seo_site_url":          "https://example.com/",
		"seo_title":             `Models <Fast> & "Reliable"`,
		"seo_keywords":          []string{"AI & API", "Gateway"},
		"seo_description":       `Use <one> API & ship "faster".`,
		"seo_social_image_url":  "https://cdn.example.com/card.jpg?a=1&b=2",
		"seo_verification_tags": `<meta name="google-site-verification" content="token&amp;value" />`,
	}
	settingsJSON, err := json.Marshal(settings)
	require.NoError(t, err)

	result := string(injectSEOSettings(template, settingsJSON))
	assert.Equal(t, 1, strings.Count(result, "<title>"))
	assert.Equal(t, 1, strings.Count(result, `name="description"`))
	assert.Equal(t, 1, strings.Count(result, `name="keywords"`))
	assert.Contains(t, result, `<title>Models &lt;Fast&gt; &amp; &#34;Reliable&#34;</title>`)
	assert.Contains(t, result, `content="AI &amp; API, Gateway"`)
	assert.Contains(t, result, `<link rel="canonical" href="https://example.com/" />`)
	assert.Contains(t, result, `property="og:image" content="https://cdn.example.com/card.jpg?a=1&amp;b=2"`)
	assert.Contains(t, result, `name="twitter:card" content="summary_large_image"`)
	assert.Contains(t, result, `name="google-site-verification" content="token&amp;value"`)
	assert.Contains(t, result, `nonce="`+NonceHTMLPlaceholder+`" type="application/ld+json"`)
	assert.NotContains(t, result, "TokensFree")
	assert.NotContains(t, result, "tokensfree.xyz")

	pattern := regexp.MustCompile(`<script nonce="[^\"]+" type="application/ld\+json">([^<]+)</script>`)
	match := pattern.FindStringSubmatch(result)
	require.Len(t, match, 2)
	var structuredData map[string]string
	require.NoError(t, json.Unmarshal([]byte(match[1]), &structuredData))
	assert.Equal(t, "https://schema.org", structuredData["@context"])
	assert.Equal(t, "WebSite", structuredData["@type"])
	assert.Equal(t, "https://example.com/", structuredData["url"])
}

func TestInjectSEOSettingsOmitsOptionalAndInvalidValues(t *testing.T) {
	template := []byte(`<!-- SEO_SETTINGS_START --><title>old</title><!-- SEO_SETTINGS_END -->`)
	settingsJSON := []byte(`{
		"site_name":"Example",
		"site_subtitle":"Subtitle",
		"seo_site_url":"https://example.com/path",
		"seo_social_image_url":"javascript:alert(1)",
		"seo_verification_tags":"<script>alert(1)</script>"
	}`)

	result := string(injectSEOSettings(template, settingsJSON))
	assert.Contains(t, result, `<title>Example - AI API Gateway</title>`)
	assert.Contains(t, result, `name="description" content="Subtitle"`)
	assert.Contains(t, result, `name="twitter:card" content="summary"`)
	assert.NotContains(t, result, `name="keywords"`)
	assert.NotContains(t, result, `rel="canonical"`)
	assert.NotContains(t, result, `property="og:url"`)
	assert.NotContains(t, result, `property="og:image"`)
	assert.NotContains(t, result, `name="twitter:image"`)
	assert.NotContains(t, result, `application/ld+json`)
	assert.NotContains(t, result, `<script>alert`)
}

func TestSEOAssetsAndFrontendIndexingRoutes(t *testing.T) {
	provider := &mockSettingsProvider{settings: map[string]any{
		"site_name":            "Example",
		"site_subtitle":        "Description",
		"seo_indexing_enabled": true,
		"seo_site_url":         "https://example.com/",
	}}
	server, err := NewFrontendServer(provider)
	require.NoError(t, err)

	router := gin.New()
	router.Use(server.Middleware())

	root := performSEORequest(router, "/", "")
	assert.Equal(t, http.StatusOK, root.Code)
	assert.Empty(t, root.Header().Get("X-Robots-Tag"))
	assert.NotEmpty(t, root.Header().Get("ETag"))

	privatePage := performSEORequest(router, "/login", "")
	assert.Equal(t, http.StatusOK, privatePage.Code)
	assert.Equal(t, seoNoIndexDirective, privatePage.Header().Get("X-Robots-Tag"))

	home := performSEORequest(router, "/home?next=%2Fdashboard", "")
	assert.Equal(t, http.StatusMovedPermanently, home.Code)
	assert.Equal(t, "/?next=%2Fdashboard", home.Header().Get("Location"))

	robots := performSEORequest(router, "/robots.txt", "")
	assert.Equal(t, http.StatusOK, robots.Code)
	assert.Contains(t, robots.Header().Get("Content-Type"), "text/plain")
	assert.Contains(t, robots.Body.String(), "Allow: /")
	assert.Contains(t, robots.Body.String(), "Disallow: /api/")
	assert.Contains(t, robots.Body.String(), "Sitemap: https://example.com/sitemap.xml")
	require.NotEmpty(t, robots.Header().Get("ETag"))

	robotsNotModified := performSEORequest(router, "/robots.txt", robots.Header().Get("ETag"))
	assert.Equal(t, http.StatusNotModified, robotsNotModified.Code)
	assert.Empty(t, robotsNotModified.Body.String())

	sitemap := performSEORequest(router, "/sitemap.xml", "")
	assert.Equal(t, http.StatusOK, sitemap.Code)
	assert.Contains(t, sitemap.Header().Get("Content-Type"), "application/xml")
	assert.Contains(t, sitemap.Body.String(), `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	assert.Contains(t, sitemap.Body.String(), `<loc>https://example.com/</loc>`)
}

func TestDisabledSEOBlocksIndexingAndSitemap(t *testing.T) {
	provider := &mockSettingsProvider{settings: map[string]any{
		"site_name":            "Example",
		"seo_indexing_enabled": false,
		"seo_site_url":         "https://example.com/",
	}}
	server, err := NewFrontendServer(provider)
	require.NoError(t, err)
	router := gin.New()
	router.Use(server.Middleware())

	root := performSEORequest(router, "/", "")
	assert.Equal(t, seoNoIndexDirective, root.Header().Get("X-Robots-Tag"))
	assert.Contains(t, root.Body.String(), `name="robots" content="noindex, nofollow"`)

	robots := performSEORequest(router, "/robots.txt", "")
	assert.Equal(t, "User-agent: *\nDisallow: /\n", robots.Body.String())

	sitemap := performSEORequest(router, "/sitemap.xml", "")
	assert.Equal(t, http.StatusNotFound, sitemap.Code)
}

func TestSEOHTMLCacheInvalidationAndTTL(t *testing.T) {
	provider := &mockSettingsProvider{settings: map[string]any{
		"site_name":            "Example",
		"seo_indexing_enabled": true,
		"seo_title":            "First title",
	}}
	server, err := NewFrontendServer(provider)
	require.NoError(t, err)
	router := gin.New()
	router.Use(server.Middleware())

	first := performSEORequest(router, "/", "")
	firstETag := first.Header().Get("ETag")
	assert.Contains(t, first.Body.String(), "First title")

	provider.settings = map[string]any{
		"site_name":            "Example",
		"seo_indexing_enabled": true,
		"seo_title":            "Second title",
	}
	cached := performSEORequest(router, "/", "")
	assert.Equal(t, firstETag, cached.Header().Get("ETag"))
	assert.Contains(t, cached.Body.String(), "First title")

	server.InvalidateCache()
	updated := performSEORequest(router, "/", "")
	assert.NotEqual(t, firstETag, updated.Header().Get("ETag"))
	assert.Contains(t, updated.Body.String(), "Second title")

	server.cache.mu.Lock()
	server.cache.generatedAt = time.Now().Add(-htmlCacheTTL)
	server.cache.mu.Unlock()
	assert.Nil(t, server.cache.Get())
}

func performSEORequest(router http.Handler, target, etag string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	router.ServeHTTP(w, req)
	return w
}
