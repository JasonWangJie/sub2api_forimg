package handler

import (
	"bytes"
	"encoding/base64"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
	_ "golang.org/x/image/webp"
)

// asyncImageInputCapabilities inspects the prepared upstream body. It never
// fetches URLs or decodes image pixels; unknown remote sizes remain zero. Masks
// count as input images because they also consume the upstream's input budget.
func asyncImageInputCapabilities(body []byte, contentType string) service.ImageInputCapabilities {
	var input service.ImageInputCapabilities
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if mediaType == "multipart/form-data" {
		reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			name := strings.TrimSpace(part.FormName())
			if name == "image" || name == "images" || name == "mask" ||
				strings.HasPrefix(name, "image[") || strings.HasPrefix(name, "images[") {
				input.Count++
				input.Transport = "local"
				asyncImageMeasureInput(&input, part)
			}
			_ = part.Close()
		}
		return input
	}
	if !gjson.ValidBytes(body) {
		return input
	}
	root := gjson.ParseBytes(body)
	for _, field := range []string{"image", "images", "image_urls", "mask"} {
		asyncImageMeasureReferenceJSON(&input, root.Get(field))
	}
	// These are the request envelopes produced by the OpenAI and Gemini builders.
	// Do not traverse arbitrary strings: prompts and output image hints are not inputs.
	for _, field := range []string{"messages", "contents", "input"} {
		root.Get(field).ForEach(func(_, message gjson.Result) bool {
			for _, parts := range []string{"content", "parts"} {
				message.Get(parts).ForEach(func(_, part gjson.Result) bool {
					asyncImageMeasureInputPart(&input, part)
					return true
				})
			}
			return true
		})
	}
	return input
}

func asyncImageMeasureReferenceJSON(input *service.ImageInputCapabilities, ref gjson.Result) {
	if ref.Type == gjson.String {
		asyncImageMeasureInputURL(input, ref.String())
		return
	}
	if ref.IsArray() {
		ref.ForEach(func(_, value gjson.Result) bool {
			asyncImageMeasureReferenceJSON(input, value)
			return true
		})
		return
	}
	if ref.IsObject() {
		for _, key := range []string{"image_url", "url"} {
			if value := ref.Get(key); value.Exists() {
				asyncImageMeasureReferenceJSON(input, value)
				return
			}
		}
	}
}

func asyncImageMeasureInputPart(input *service.ImageInputCapabilities, part gjson.Result) {
	if !part.IsObject() {
		return
	}
	if ref := part.Get("image_url"); ref.Exists() {
		asyncImageMeasureReferenceJSON(input, ref)
		return
	}
	for _, key := range []string{"inlineData", "inline_data"} {
		if ref := part.Get(key); ref.IsObject() {
			mimeType := ref.Get("mimeType").String()
			if mimeType == "" {
				mimeType = ref.Get("mime_type").String()
			}
			if strings.HasPrefix(strings.ToLower(mimeType), "image/") && ref.Get("data").Type == gjson.String {
				input.Count++
				if input.Transport == "" {
					input.Transport = "local"
				}
				asyncImageMeasureInput(input, base64.NewDecoder(base64.StdEncoding, strings.NewReader(ref.Get("data").String())))
			}
			return
		}
	}
	for _, key := range []string{"fileData", "file_data"} {
		if ref := part.Get(key); ref.IsObject() {
			mimeType := ref.Get("mimeType").String()
			if mimeType == "" {
				mimeType = ref.Get("mime_type").String()
			}
			if strings.HasPrefix(strings.ToLower(mimeType), "image/") {
				uri := ref.Get("fileUri").String()
				if uri == "" {
					uri = ref.Get("file_uri").String()
				}
				asyncImageMeasureInputURL(input, uri)
			}
			return
		}
	}
}

func asyncImageMeasureInputURL(input *service.ImageInputCapabilities, uri string) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return
	}
	input.Count++
	if len(uri) < len("data:image/") || !strings.EqualFold(uri[:len("data:image/")], "data:image/") {
		// Any remaining URL requires passthrough, even if other inputs are inline.
		input.Transport = "passthrough"
		return
	}
	if input.Transport == "" {
		input.Transport = "local"
	}
	header, encoded, ok := strings.Cut(uri, ",")
	if ok && strings.HasSuffix(strings.ToLower(header), ";base64") {
		asyncImageMeasureInput(input, base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)))
	}
}

type asyncImageInputCountingReader struct {
	io.Reader
	n int64
}

func (r *asyncImageInputCountingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.n += int64(n)
	return n, err
}

func asyncImageMeasureInput(input *service.ImageInputCapabilities, source io.Reader) {
	reader := &asyncImageInputCountingReader{Reader: source}
	config, _, configErr := image.DecodeConfig(reader)
	// Drain the source to count decoded bytes without holding another image copy.
	_, readErr := io.Copy(io.Discard, reader)
	if readErr == nil {
		input.MaxBytes = max(input.MaxBytes, reader.n)
		input.TotalBytes = asyncImageSaturatingInputSum(input.TotalBytes, reader.n)
	}
	if configErr == nil && config.Width > 0 && config.Height > 0 && int64(config.Width) <= math.MaxInt64/int64(config.Height) {
		pixels := int64(config.Width) * int64(config.Height)
		input.MaxPixels = max(input.MaxPixels, pixels)
		input.TotalPixels = asyncImageSaturatingInputSum(input.TotalPixels, pixels)
	}
}

func asyncImageSaturatingInputSum(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}
