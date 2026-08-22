package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	AsyncImageDialectBB = "bb"
	AsyncImageDialectSC = "sc"

	AsyncImageKindText = "text_to_image"
	AsyncImageKindEdit = "image_to_image"

	defaultAsyncImageReferenceMaxBytes     = int64(32 << 20)
	defaultAsyncImageReferenceMaxPixels    = int64(80_000_000)
	defaultAsyncImageReferenceTimeout      = 30 * time.Second
	defaultAsyncImageReferenceMaxRedirects = 3
)

// AsyncImageInputPart preserves the order of text and reference-image parts in
// downstream BB requests. SC requests are normalized to images followed by text.
type AsyncImageInputPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	URL  string `json:"url,omitempty"`
}

// AsyncImageNormalizedRequest is the internal request shared by the BB and SC
// dialects. It is not exposed as a public wire format.
type AsyncImageNormalizedRequest struct {
	Dialect     string                `json:"dialect"`
	Platform    string                `json:"platform"`
	Kind        string                `json:"kind"`
	Model       string                `json:"model"`
	Prompt      string                `json:"prompt"`
	ImageSize   string                `json:"image_size,omitempty"`
	AspectRatio string                `json:"aspect_ratio,omitempty"`
	Parts       []AsyncImageInputPart `json:"parts"`
	SourcePath  string                `json:"source_path"`
}

func (r *AsyncImageNormalizedRequest) ReferenceCount() int {
	if r == nil {
		return 0
	}
	count := 0
	for _, part := range r.Parts {
		if part.Type == "image_url" && strings.TrimSpace(part.URL) != "" {
			count++
		}
	}
	return count
}

type bbGeminiRequest struct {
	Model     string            `json:"model"`
	Stream    bool              `json:"stream"`
	Messages  []bbGeminiMessage `json:"messages"`
	ExtraBody struct {
		Google struct {
			ImageConfig struct {
				ImageSize   string `json:"image_size"`
				AspectRatio string `json:"aspect_ratio"`
			} `json:"image_config"`
		} `json:"google"`
	} `json:"extra_body"`
}

type bbGeminiMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type bbGeminiContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

// ParseBBGeminiImageRequest validates the downstream BB Chat Completions
// dialect without changing the legacy /v1/chat/completions parser.
func ParseBBGeminiImageRequest(body []byte, sourcePath string) (*AsyncImageNormalizedRequest, error) {
	var in bbGeminiRequest
	if len(bytes.TrimSpace(body)) == 0 || json.Unmarshal(body, &in) != nil {
		return nil, errors.New("invalid JSON request body")
	}
	model := strings.TrimSpace(in.Model)
	if model == "" {
		return nil, errors.New("model is required")
	}
	if in.Stream {
		return nil, errors.New("stream must be false for asynchronous image generation")
	}
	if len(in.Messages) == 0 {
		return nil, errors.New("messages must contain at least one user message")
	}

	parts := make([]AsyncImageInputPart, 0, len(in.Messages)*2)
	promptParts := make([]string, 0, len(in.Messages))
	userMessages := 0
	for _, message := range in.Messages {
		if strings.TrimSpace(strings.ToLower(message.Role)) != "user" {
			return nil, fmt.Errorf("unsupported message role %q; only user messages are accepted", message.Role)
		}
		userMessages++
		messageParts, texts, err := parseBBGeminiContent(message.Content)
		if err != nil {
			return nil, err
		}
		parts = append(parts, messageParts...)
		promptParts = append(promptParts, texts...)
	}
	if userMessages == 0 {
		return nil, errors.New("messages must contain at least one user message")
	}
	prompt := strings.TrimSpace(strings.Join(promptParts, "\n"))
	if prompt == "" {
		return nil, errors.New("a non-empty text prompt is required")
	}

	size, ratio, err := normalizeAsyncGeminiDimensions(
		in.ExtraBody.Google.ImageConfig.ImageSize,
		in.ExtraBody.Google.ImageConfig.AspectRatio,
	)
	if err != nil {
		return nil, err
	}
	kind := AsyncImageKindText
	if countAsyncImageReferences(parts) > 0 {
		kind = AsyncImageKindEdit
	}
	return &AsyncImageNormalizedRequest{
		Dialect:     AsyncImageDialectBB,
		Platform:    PlatformGemini,
		Kind:        kind,
		Model:       model,
		Prompt:      prompt,
		ImageSize:   size,
		AspectRatio: ratio,
		Parts:       parts,
		SourcePath:  strings.TrimSpace(sourcePath),
	}, nil
}

func parseBBGeminiContent(raw json.RawMessage) ([]AsyncImageInputPart, []string, error) {
	if len(raw) == 0 {
		return nil, nil, errors.New("message content is required")
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, nil, errors.New("message content must not be empty")
		}
		return []AsyncImageInputPart{{Type: "text", Text: text}}, []string{text}, nil
	}

	var inputParts []bbGeminiContentPart
	if json.Unmarshal(raw, &inputParts) != nil || len(inputParts) == 0 {
		return nil, nil, errors.New("message content must be a string or a non-empty content array")
	}
	parts := make([]AsyncImageInputPart, 0, len(inputParts))
	texts := make([]string, 0, len(inputParts))
	for _, part := range inputParts {
		switch strings.TrimSpace(strings.ToLower(part.Type)) {
		case "text":
			value := strings.TrimSpace(part.Text)
			if value == "" {
				return nil, nil, errors.New("text content part must not be empty")
			}
			parts = append(parts, AsyncImageInputPart{Type: "text", Text: value})
			texts = append(texts, value)
		case "image_url":
			value := strings.TrimSpace(part.ImageURL.URL)
			if value == "" {
				return nil, nil, errors.New("image_url.url is required")
			}
			parts = append(parts, AsyncImageInputPart{Type: "image_url", URL: value})
		default:
			return nil, nil, fmt.Errorf("unsupported content part type %q", part.Type)
		}
	}
	return parts, texts, nil
}

type scImageRequest struct {
	Model       string   `json:"model"`
	Prompt      string   `json:"prompt"`
	ImageURLs   []string `json:"image_urls"`
	Resolution  string   `json:"resolution"`
	AspectRatio string   `json:"aspect_ratio"`
	// Size is a client-friendly alias:
	// - ratio strings like "3:2" map to aspect_ratio
	// - pixel dimensions like "1080x1350" map to their supported aspect ratio
	// - tier strings like "2K" map to resolution when resolution is empty
	Size string `json:"size"`
}

// ParseSCGeminiImageRequest validates the SC image-generation dialect. SC is
// intentionally Gemini-only in the first release.
func ParseSCGeminiImageRequest(body []byte, sourcePath string) (*AsyncImageNormalizedRequest, error) {
	var in scImageRequest
	if len(bytes.TrimSpace(body)) == 0 || json.Unmarshal(body, &in) != nil {
		return nil, errors.New("invalid JSON request body")
	}
	model := strings.TrimSpace(in.Model)
	prompt := strings.TrimSpace(in.Prompt)
	if model == "" {
		return nil, errors.New("model is required")
	}
	if prompt == "" {
		return nil, errors.New("prompt is required")
	}

	parts := make([]AsyncImageInputPart, 0, len(in.ImageURLs)+1)
	for _, rawURL := range in.ImageURLs {
		value := strings.TrimSpace(rawURL)
		if value == "" {
			return nil, errors.New("image_urls must not contain empty values")
		}
		parts = append(parts, AsyncImageInputPart{Type: "image_url", URL: value})
	}
	parts = append(parts, AsyncImageInputPart{Type: "text", Text: prompt})

	resolution, aspectRatio := resolveSCGeminiDimensionAliases(in.Resolution, in.AspectRatio, in.Size)
	size, ratio, err := normalizeAsyncGeminiDimensions(resolution, aspectRatio)
	if err != nil {
		return nil, err
	}
	kind := AsyncImageKindText
	if len(in.ImageURLs) > 0 {
		kind = AsyncImageKindEdit
	}
	return &AsyncImageNormalizedRequest{
		Dialect:     AsyncImageDialectSC,
		Platform:    PlatformGemini,
		Kind:        kind,
		Model:       model,
		Prompt:      prompt,
		ImageSize:   size,
		AspectRatio: ratio,
		Parts:       parts,
		SourcePath:  strings.TrimSpace(sourcePath),
	}, nil
}

func resolveSCGeminiDimensionAliases(resolution, aspectRatio, size string) (string, string) {
	resolution = strings.TrimSpace(resolution)
	aspectRatio = strings.TrimSpace(aspectRatio)
	size = strings.TrimSpace(size)
	if size == "" {
		return resolution, aspectRatio
	}
	upper := strings.ToUpper(size)
	switch upper {
	case "0.5K", "1K", "2K", "4K":
		if resolution == "" {
			resolution = size
		}
	default:
		if aspectRatio == "" {
			aspectRatio = normalizeSCGeminiPixelSizeAspectRatio(size)
		}
	}
	return resolution, aspectRatio
}

// normalizeSCGeminiPixelSizeAspectRatio translates client-friendly WxH input
// into the ratio accepted by Gemini. Gemini does not accept arbitrary pixel
// output dimensions, so the pixel values select an aspect ratio only.
func normalizeSCGeminiPixelSizeAspectRatio(size string) string {
	value := strings.NewReplacer("*", "x", "X", "x", "\u00d7", "x").Replace(strings.TrimSpace(size))
	width, height, ok := parseImageBillingDimensions(value)
	if !ok {
		return size
	}
	return canonicalAsyncGeminiAspectRatio(width, height)
}

func greatestCommonDivisor(left, right int) int {
	for right != 0 {
		left, right = right, left%right
	}
	return left
}

func canonicalAsyncGeminiAspectRatio(width, height int) string {
	gcd := greatestCommonDivisor(width, height)
	if gcd == 0 {
		return ""
	}
	width, height = width/gcd, height/gcd
	// Gemini names the ultrawide 7:3 ratio as 21:9. Preserve that canonical
	// spelling so equivalent pixel sizes such as 2520x1080 are accepted.
	if width == 7 && height == 3 {
		return "21:9"
	}
	// Symmetric ultra-tall: 3:7 → 9:21.
	if width == 3 && height == 7 {
		return "9:21"
	}
	return strconv.Itoa(width) + ":" + strconv.Itoa(height)
}

func normalizeAsyncGeminiAspectRatio(raw string) string {
	ratio := strings.ToLower(strings.TrimSpace(raw))
	if ratio == "自动" {
		return "auto"
	}
	parts := strings.Split(ratio, ":")
	if len(parts) != 2 {
		return ratio
	}
	width, widthErr := strconv.Atoi(strings.TrimSpace(parts[0]))
	height, heightErr := strconv.Atoi(strings.TrimSpace(parts[1]))
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return ratio
	}
	return canonicalAsyncGeminiAspectRatio(width, height)
}

func normalizeAsyncGeminiDimensions(rawSize, rawRatio string) (string, string, error) {
	size := strings.ToUpper(strings.TrimSpace(rawSize))
	if size != "" && size != "0.5K" && size != "1K" && size != "2K" && size != "4K" {
		return "", "", fmt.Errorf("unsupported_image_dimensions: unsupported image size %q", rawSize)
	}

	ratio := normalizeAsyncGeminiAspectRatio(rawRatio)
	if ratio == "auto" {
		// auto means omit upstream aspectRatio and lets the model decide.
		return size, "", nil
	}
	if ratio == "" {
		return size, "", nil
	}
	allowed := map[string]struct{}{
		"1:1": {}, "2:3": {}, "3:2": {}, "3:4": {}, "4:3": {},
		"4:5": {}, "5:4": {}, "9:16": {}, "16:9": {}, "21:9": {}, "9:21": {},
	}
	if _, ok := allowed[ratio]; !ok {
		return "", "", fmt.Errorf("unsupported_image_dimensions: unsupported aspect ratio %q", rawRatio)
	}
	return size, ratio, nil
}

func countAsyncImageReferences(parts []AsyncImageInputPart) int {
	count := 0
	for _, part := range parts {
		if part.Type == "image_url" && strings.TrimSpace(part.URL) != "" {
			count++
		}
	}
	return count
}

type AsyncImageReference struct {
	MIMEType string `json:"mime_type"`
	Data     []byte `json:"-"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	SHA256   string `json:"sha256"`
}

type AsyncImageReferenceDownloadError struct {
	Phase      string
	StatusCode int
	RetryAfter time.Duration
	Err        error
}

func (e *AsyncImageReferenceDownloadError) Error() string {
	if e == nil {
		return "reference image download failed"
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("download reference image: unexpected HTTP status %d", e.StatusCode)
	}
	if e.Err != nil {
		return fmt.Sprintf("%s reference image: %v", strings.TrimSpace(e.Phase), e.Err)
	}
	return "reference image download failed"
}

func (e *AsyncImageReferenceDownloadError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func ParseAsyncImageRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func (r *AsyncImageReference) DataURI() string {
	if r == nil {
		return ""
	}
	return "data:" + r.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(r.Data)
}

type AsyncImageReferenceDownloader struct {
	MaxBytes     int64
	MaxPixels    int64
	Timeout      time.Duration
	MaxRedirects int
	Resolver     *net.Resolver
	Budget       *AsyncImageReferenceBudget
	BoundLoader  AsyncImageBoundReferenceLoader
}

type AsyncImageBoundReferenceLoader func(ctx context.Context, rawURL string) (reference *AsyncImageReference, handled bool, err error)

type AsyncImageReferenceBudget struct {
	MaxImages      int
	MaxTotalBytes  int64
	MaxTotalPixels int64
	images         int
	totalBytes     int64
	totalPixels    int64
}

func (b *AsyncImageReferenceBudget) consume(reference *AsyncImageReference) error {
	if b == nil || reference == nil {
		return nil
	}
	nextImages := b.images + 1
	nextBytes := b.totalBytes + int64(len(reference.Data))
	nextPixels := b.totalPixels + int64(reference.Width)*int64(reference.Height)
	if b.MaxImages > 0 && nextImages > b.MaxImages {
		return errors.New("reference image count exceeds the configured limit")
	}
	if b.MaxTotalBytes > 0 && nextBytes > b.MaxTotalBytes {
		return errors.New("reference image bytes exceed the configured aggregate limit")
	}
	if b.MaxTotalPixels > 0 && nextPixels > b.MaxTotalPixels {
		return errors.New("reference image pixels exceed the configured aggregate limit")
	}
	b.images, b.totalBytes, b.totalPixels = nextImages, nextBytes, nextPixels
	return nil
}

// consumeURL counts a remote reference that is passed through without local
// download. Byte/pixel budgets are enforced by the upstream fetcher instead.
func (b *AsyncImageReferenceBudget) consumeURL() error {
	if b == nil {
		return nil
	}
	nextImages := b.images + 1
	if b.MaxImages > 0 && nextImages > b.MaxImages {
		return errors.New("reference image count exceeds the configured limit")
	}
	b.images = nextImages
	return nil
}

// ValidateBytes applies the same MIME, decoder, pixel, and byte limits used
// for remote reference images to bytes received from multipart uploads.
func (d AsyncImageReferenceDownloader) ValidateBytes(data []byte, declaredType string) (*AsyncImageReference, error) {
	if int64(len(data)) > d.maxBytes() {
		return nil, errors.New("reference image exceeds the configured size limit")
	}
	return d.validateImage(data, declaredType)
}

// ValidateRemoteURL checks that a reference URL is absolute HTTPS and resolves
// to a public host. It does not download or decode the image body; Gemini
// fetches HTTPS references itself via fileData.fileUri.
func (d AsyncImageReferenceDownloader) ValidateRemoteURL(ctx context.Context, rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" {
		return errors.New("reference image URL must be an absolute HTTPS URL or an image data URI")
	}
	return validateAsyncImagePublicHost(ctx, d.resolver(), parsed.Hostname())
}

func (d AsyncImageReferenceDownloader) AcceptRemoteURL(ctx context.Context, rawURL string) error {
	if err := d.ValidateRemoteURL(ctx, rawURL); err != nil {
		return err
	}
	return d.Budget.consumeURL()
}

// ValidatePassthroughURL validates only the URL shape. The provider performs
// the actual fetch for passthrough transport, so local DNS/SSRF resolution is
// intentionally skipped in this mode.
func (d AsyncImageReferenceDownloader) ValidatePassthroughURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" {
		return errors.New("reference image URL must be an absolute HTTPS URL or an image data URI")
	}
	if ip, parseErr := netip.ParseAddr(strings.Trim(parsed.Hostname(), "[]")); parseErr == nil && !isAsyncImagePublicIP(ip) {
		return errors.New("reference image URL points to a blocked network address")
	}
	return d.Budget.consumeURL()
}

func (d AsyncImageReferenceDownloader) Download(ctx context.Context, rawURL string) (*AsyncImageReference, error) {
	rawURL = strings.TrimSpace(rawURL)
	if d.BoundLoader != nil {
		reference, handled, err := d.BoundLoader(ctx, rawURL)
		if err != nil {
			return nil, &AsyncImageReferenceDownloadError{Phase: "load bound", Err: err}
		}
		if handled {
			return d.accept(reference)
		}
	}
	if strings.HasPrefix(strings.ToLower(rawURL), "data:") {
		reference, err := d.decodeDataURI(rawURL)
		if err != nil {
			return nil, err
		}
		return d.accept(reference)
	}
	if err := d.ValidateRemoteURL(ctx, rawURL); err != nil {
		return nil, &AsyncImageReferenceDownloadError{Phase: "validate", Err: err}
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, errors.New("reference image URL must be an absolute HTTPS URL or an image data URI")
	}

	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("reference image downloader requires an HTTP transport")
	}
	transport := baseTransport.Clone()
	transport.Proxy = nil
	transport.DialContext = d.safeDialContext
	client := &http.Client{
		Transport: transport,
		Timeout:   d.timeout(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= d.maxRedirects() {
				return errors.New("reference image redirect limit exceeded")
			}
			if req.URL == nil || !strings.EqualFold(req.URL.Scheme, "https") {
				return errors.New("reference image redirects must use HTTPS")
			}
			return validateAsyncImagePublicHost(req.Context(), d.resolver(), req.URL.Hostname())
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/webp,image/png,image/jpeg")
	resp, err := client.Do(req)
	if err != nil {
		return nil, &AsyncImageReferenceDownloadError{Phase: "download", Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &AsyncImageReferenceDownloadError{
			Phase: "response", StatusCode: resp.StatusCode,
			RetryAfter: ParseAsyncImageRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}
	if resp.ContentLength > d.maxBytes() {
		return nil, errors.New("reference image exceeds the configured size limit")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, d.maxBytes()+1))
	if err != nil {
		return nil, &AsyncImageReferenceDownloadError{Phase: "read", Err: err}
	}
	if int64(len(data)) > d.maxBytes() {
		return nil, errors.New("reference image exceeds the configured size limit")
	}
	reference, err := d.validateImage(data, resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}
	return d.accept(reference)
}

func (d AsyncImageReferenceDownloader) accept(reference *AsyncImageReference) (*AsyncImageReference, error) {
	if reference == nil {
		return nil, errors.New("reference image is unavailable")
	}
	if err := d.Budget.consume(reference); err != nil {
		return nil, err
	}
	return reference, nil
}

func (d AsyncImageReferenceDownloader) decodeDataURI(raw string) (*AsyncImageReference, error) {
	comma := strings.IndexByte(raw, ',')
	if comma <= len("data:") {
		return nil, errors.New("invalid image data URI")
	}
	meta := raw[len("data:"):comma]
	if !strings.HasSuffix(strings.ToLower(meta), ";base64") {
		return nil, errors.New("image data URI must use base64 encoding")
	}
	contentType := strings.TrimSpace(meta[:len(meta)-len(";base64")])
	decodedLen := base64.StdEncoding.DecodedLen(len(raw) - comma - 1)
	if int64(decodedLen) > d.maxBytes() {
		return nil, errors.New("reference image exceeds the configured size limit")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(raw[comma+1:])
	if err != nil {
		return nil, errors.New("invalid base64 image data URI")
	}
	return d.validateImage(data, contentType)
}

func (d AsyncImageReferenceDownloader) validateImage(data []byte, declaredType string) (*AsyncImageReference, error) {
	validated, err := ValidateImageBytes(data, declaredType, d.maxBytes(), d.maxPixels())
	if err != nil {
		return nil, err
	}
	return &AsyncImageReference{
		MIMEType: validated.MIMEType,
		Data:     validated.Data,
		Width:    validated.Width,
		Height:   validated.Height,
		SHA256:   validated.SHA256,
	}, nil
}

func imageFormatMIME(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

func (d AsyncImageReferenceDownloader) safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := d.resolver().LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("reference image host could not be resolved: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("reference image host could not be resolved")
	}
	var lastErr error
	for _, ip := range addresses {
		if !isAsyncImagePublicIP(ip) {
			continue
		}
		dialer := net.Dialer{Timeout: d.timeout()}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("reference image host resolves only to blocked addresses")
}

func validateAsyncImagePublicHost(ctx context.Context, resolver *net.Resolver, host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return errors.New("reference image URL host is required")
	}
	if parsed, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		if !isAsyncImagePublicIP(parsed) {
			return errors.New("reference image URL points to a blocked network address")
		}
		return nil
	}
	addresses, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("reference image host could not be resolved: %w", err)
	}
	if len(addresses) == 0 {
		return errors.New("reference image host could not be resolved")
	}
	for _, ip := range addresses {
		if !isAsyncImagePublicIP(ip) {
			return errors.New("reference image host resolves to a blocked network address")
		}
	}
	return nil
}

var asyncImageBlockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2002::/16"),
}

func isAsyncImagePublicIP(ip netip.Addr) bool {
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip.Is4In6() {
		ip = ip.Unmap()
	}
	for _, prefix := range asyncImageBlockedPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func (d AsyncImageReferenceDownloader) resolver() *net.Resolver {
	if d.Resolver != nil {
		return d.Resolver
	}
	return net.DefaultResolver
}

func (d AsyncImageReferenceDownloader) maxBytes() int64 {
	if d.MaxBytes > 0 {
		return d.MaxBytes
	}
	return defaultAsyncImageReferenceMaxBytes
}

func (d AsyncImageReferenceDownloader) maxPixels() int64 {
	if d.MaxPixels > 0 {
		return d.MaxPixels
	}
	return defaultAsyncImageReferenceMaxPixels
}

func (d AsyncImageReferenceDownloader) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return defaultAsyncImageReferenceTimeout
}

func (d AsyncImageReferenceDownloader) maxRedirects() int {
	if d.MaxRedirects > 0 {
		return d.MaxRedirects
	}
	return defaultAsyncImageReferenceMaxRedirects
}

// BuildGeminiAsyncChatBody produces a Chat Completions request for Gemini async
// image generation. HTTPS reference URLs are passed through for upstream
// fileData.fileUri fetching; data URIs are validated locally and kept as
// inline-compatible image_url values. Compatibility code maps HTTPS URLs to
// Gemini fileData and data URIs to inlineData.
func BuildGeminiAsyncChatBody(ctx context.Context, req *AsyncImageNormalizedRequest, downloader AsyncImageReferenceDownloader) ([]byte, error) {
	return BuildGeminiAsyncChatBodyWithTransport(ctx, req, downloader, AsyncImageReferenceTransportPassthrough)
}

func BuildGeminiAsyncChatBodyWithTransport(ctx context.Context, req *AsyncImageNormalizedRequest, downloader AsyncImageReferenceDownloader, transportMode string) ([]byte, error) {
	if req == nil {
		return nil, errors.New("normalized image request is required")
	}
	content := make([]any, 0, len(req.Parts))
	for _, part := range req.Parts {
		switch part.Type {
		case "text":
			content = append(content, map[string]any{"type": "text", "text": part.Text})
		case "image_url":
			rawURL := strings.TrimSpace(part.URL)
			if rawURL == "" {
				return nil, errors.New("invalid reference image: empty url")
			}
			local := strings.EqualFold(strings.TrimSpace(transportMode), AsyncImageReferenceTransportLocal)
			if strings.HasPrefix(strings.ToLower(rawURL), "data:") || local {
				imageRef, err := downloader.Download(ctx, rawURL)
				if err != nil {
					return nil, fmt.Errorf("invalid reference image: %w", err)
				}
				rawURL = imageRef.DataURI()
			} else {
				if err := downloader.ValidatePassthroughURL(rawURL); err != nil {
					return nil, fmt.Errorf("invalid reference image: %w", err)
				}
				if err := downloader.Budget.consumeURL(); err != nil {
					return nil, err
				}
			}
			content = append(content, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": rawURL},
			})
		default:
			return nil, fmt.Errorf("unsupported normalized content part %q", part.Type)
		}
	}
	imageConfig := map[string]any{}
	if req.ImageSize != "" {
		imageConfig["image_size"] = req.ImageSize
	}
	if req.AspectRatio != "" {
		imageConfig["aspect_ratio"] = req.AspectRatio
	}
	out := map[string]any{
		"model":  req.Model,
		"stream": false,
		"messages": []any{map[string]any{
			"role":    "user",
			"content": content,
		}},
		"extra_body": map[string]any{
			"google": map[string]any{"image_config": imageConfig},
		},
	}
	return json.Marshal(out)
}

func AsyncImageTaskRequestHash(platform, dialect, sourcePath string, body []byte) string {
	h := sha256.New()
	_, _ = io.WriteString(h, strings.TrimSpace(platform))
	_, _ = io.WriteString(h, "\x00")
	_, _ = io.WriteString(h, strings.TrimSpace(dialect))
	_, _ = io.WriteString(h, "\x00")
	_, _ = io.WriteString(h, strings.TrimSpace(sourcePath))
	_, _ = io.WriteString(h, "\x00")
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// AsyncImageSignedURLExpiryUnix returns zero for a public URL and an absolute
// expiry for signed URLs. Keeping this helper here makes the SC response's
// expires_at semantics explicit and testable.
func AsyncImageSignedURLExpiryUnix(now time.Time, expiry time.Duration, public bool) int64 {
	if public || expiry <= 0 {
		return 0
	}
	return now.Add(expiry).Unix()
}
