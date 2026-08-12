package service

import (
	"encoding/json"
	"fmt"
	htmlpkg "html"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	MaxSEOTitleLength            = 200
	MaxSEODescriptionLength      = 500
	MaxSEOKeywords               = 20
	MaxSEOKeywordLength          = 100
	MaxSEOURLLength              = 2048
	MaxSEOVerificationTags       = 20
	MaxSEOVerificationTagsLength = 10 * 1024
	maxSEOVerificationNameLength = 100
	maxSEOVerificationContentLen = 2048
)

var (
	seoMetaNamePattern   = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
	reservedSEOMetaNames = map[string]struct{}{
		"application-name":    {},
		"bingbot":             {},
		"description":         {},
		"googlebot":           {},
		"keywords":            {},
		"robots":              {},
		"twitter:card":        {},
		"twitter:description": {},
		"twitter:image":       {},
		"twitter:title":       {},
		"viewport":            {},
	}
)

func NormalizeSEOSettings(settings *SystemSettings) error {
	if settings == nil {
		return infraerrors.BadRequest("INVALID_SEO_SETTINGS", "SEO settings are required")
	}

	settings.SEOTitle = strings.TrimSpace(settings.SEOTitle)
	if utf8.RuneCountInString(settings.SEOTitle) > MaxSEOTitleLength {
		return infraerrors.BadRequest("INVALID_SEO_TITLE", "SEO title is too long (max 200 characters)")
	}

	settings.SEODescription = strings.TrimSpace(settings.SEODescription)
	if utf8.RuneCountInString(settings.SEODescription) > MaxSEODescriptionLength {
		return infraerrors.BadRequest("INVALID_SEO_DESCRIPTION", "SEO description is too long (max 500 characters)")
	}

	keywords, err := NormalizeSEOKeywords(settings.SEOKeywords)
	if err != nil {
		return infraerrors.BadRequest("INVALID_SEO_KEYWORDS", err.Error())
	}
	settings.SEOKeywords = keywords

	settings.SEOSiteURL, err = normalizeSEOSiteURL(settings.SEOSiteURL)
	if err != nil {
		return infraerrors.BadRequest("INVALID_SEO_SITE_URL", err.Error())
	}
	settings.SEOSocialImageURL, err = normalizeSEOImageURL(settings.SEOSocialImageURL)
	if err != nil {
		return infraerrors.BadRequest("INVALID_SEO_SOCIAL_IMAGE_URL", err.Error())
	}
	settings.SEOVerificationTags, err = NormalizeSEOVerificationTags(settings.SEOVerificationTags)
	if err != nil {
		return infraerrors.BadRequest("INVALID_SEO_VERIFICATION_TAGS", err.Error())
	}
	return nil
}

func NormalizeSEOKeywords(values []string) ([]string, error) {
	if len(values) > MaxSEOKeywords {
		return nil, fmt.Errorf("too many SEO keywords (max %d)", MaxSEOKeywords)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		keyword := strings.TrimSpace(value)
		if keyword == "" {
			continue
		}
		if utf8.RuneCountInString(keyword) > MaxSEOKeywordLength {
			return nil, fmt.Errorf("SEO keyword is too long (max %d characters)", MaxSEOKeywordLength)
		}
		key := strings.ToLower(keyword)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, keyword)
	}
	if len(result) > MaxSEOKeywords {
		return nil, fmt.Errorf("too many SEO keywords (max %d)", MaxSEOKeywords)
	}
	return result, nil
}

func ParseSEOKeywords(raw string) []string {
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return []string{}
	}
	values, err := NormalizeSEOKeywords(values)
	if err != nil || values == nil {
		return []string{}
	}
	return values
}

func normalizeSEOSiteURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if len(value) > MaxSEOURLLength {
		return "", fmt.Errorf("SEO site URL is too long (max %d characters)", MaxSEOURLLength)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("SEO site URL must be an absolute http(s) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("SEO site URL must be a site root without credentials, path, query, or fragment")
	}
	parsed.Path = "/"
	parsed.RawPath = ""
	return parsed.String(), nil
}

func normalizeSEOImageURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if len(value) > MaxSEOURLLength {
		return "", fmt.Errorf("SEO social image URL is too long (max %d characters)", MaxSEOURLLength)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("SEO social image URL must be an absolute http(s) URL")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("SEO social image URL must not contain credentials")
	}
	return value, nil
}

func NormalizeSEOVerificationTags(raw string) (string, error) {
	if len(raw) > MaxSEOVerificationTagsLength {
		return "", fmt.Errorf("SEO verification tags are too large (max 10 KiB)")
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if err := validateSEOVerificationTokenStream(value); err != nil {
		return "", err
	}

	contextNode := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	nodes, err := html.ParseFragment(strings.NewReader(value), contextNode)
	if err != nil {
		return "", fmt.Errorf("SEO verification tags contain invalid HTML")
	}
	result := make([]string, 0, len(nodes))
	seen := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if node.Type == html.TextNode && strings.TrimSpace(node.Data) == "" {
			continue
		}
		if node.Type != html.ElementNode || node.DataAtom != atom.Meta || node.FirstChild != nil {
			return "", fmt.Errorf("only standalone meta name/content tags are allowed")
		}

		var name, content string
		var hasName, hasContent bool
		for _, attr := range node.Attr {
			if attr.Namespace != "" {
				return "", fmt.Errorf("meta tag namespaces are not allowed")
			}
			switch strings.ToLower(attr.Key) {
			case "name":
				if hasName {
					return "", fmt.Errorf("meta tag contains a duplicate name attribute")
				}
				hasName = true
				name = strings.TrimSpace(attr.Val)
			case "content":
				if hasContent {
					return "", fmt.Errorf("meta tag contains a duplicate content attribute")
				}
				hasContent = true
				content = strings.TrimSpace(attr.Val)
			default:
				return "", fmt.Errorf("meta tag attribute %q is not allowed", attr.Key)
			}
		}
		if name == "" || content == "" {
			return "", fmt.Errorf("each meta tag requires non-empty name and content attributes")
		}
		if len(name) > maxSEOVerificationNameLength || !seoMetaNamePattern.MatchString(name) {
			return "", fmt.Errorf("meta tag name is invalid")
		}
		if utf8.RuneCountInString(content) > maxSEOVerificationContentLen {
			return "", fmt.Errorf("meta tag content is too long (max %d characters)", maxSEOVerificationContentLen)
		}
		key := strings.ToLower(name)
		if _, reserved := reservedSEOMetaNames[key]; reserved {
			return "", fmt.Errorf("meta tag name %q is managed by the system", name)
		}
		if _, duplicate := seen[key]; duplicate {
			return "", fmt.Errorf("duplicate meta tag name %q", name)
		}
		seen[key] = struct{}{}
		result = append(result, `<meta name="`+htmlpkg.EscapeString(name)+`" content="`+htmlpkg.EscapeString(content)+`" />`)
		if len(result) > MaxSEOVerificationTags {
			return "", fmt.Errorf("too many SEO verification tags (max %d)", MaxSEOVerificationTags)
		}
	}
	return strings.Join(result, "\n"), nil
}

func validateSEOVerificationTokenStream(value string) error {
	tokenizer := html.NewTokenizer(strings.NewReader(value))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			if err := tokenizer.Err(); err != nil && err != io.EOF {
				return fmt.Errorf("SEO verification tags contain invalid HTML")
			}
			return nil
		case html.TextToken:
			if strings.TrimSpace(string(tokenizer.Text())) != "" {
				return fmt.Errorf("only standalone meta name/content tags are allowed")
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			if tokenizer.Token().DataAtom != atom.Meta {
				return fmt.Errorf("only standalone meta name/content tags are allowed")
			}
		default:
			return fmt.Errorf("only standalone meta name/content tags are allowed")
		}
	}
}
