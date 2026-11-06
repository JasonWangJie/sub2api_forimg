package service

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

// Normalize only the request being sent to the official edits endpoint. The
// worker's saved payload stays unchanged for a later attempt at another provider.
func normalizeOfficialOpenAIImageMultipart(targetURL string, body []byte, contentType string) ([]byte, string, error) {
	target, err := url.Parse(targetURL)
	if err != nil || !strings.EqualFold(target.Scheme, "https") ||
		!strings.EqualFold(target.Hostname(), "api.openai.com") || target.User != nil ||
		(target.Port() != "" && target.Port() != "443") || target.Path != openAIImagesEditsEndpoint {
		return body, contentType, nil
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return body, contentType, nil
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, "", fmt.Errorf("official image multipart boundary is required")
	}

	// Count reference files without retaining their contents. Masks are separate.
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	imageCount := 0
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("read official image multipart: %w", err)
		}
		if part.FileName() != "" && (part.FormName() == "image" || part.FormName() == "image[]") {
			imageCount++
		}
		if _, err := io.Copy(io.Discard, part); err != nil {
			return nil, "", fmt.Errorf("read official image multipart file: %w", err)
		}
	}

	reader = multipart.NewReader(bytes.NewReader(body), boundary)
	var output bytes.Buffer
	output.Grow(len(body))
	writer := multipart.NewWriter(&output)
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("read official image multipart: %w", err)
		}
		header := cloneMultipartHeader(part.Header)
		field := part.FormName()
		isImage := field == "image" || field == "image[]"
		var prefix [512]byte
		n := 0
		if part.FileName() != "" && (isImage || field == "mask") {
			if isImage && imageCount > 1 && field != "image[]" {
				disposition, values, err := mime.ParseMediaType(header.Get("Content-Disposition"))
				if err != nil {
					return nil, "", fmt.Errorf("parse official image disposition: %w", err)
				}
				values["name"] = "image[]"
				header.Set("Content-Disposition", mime.FormatMediaType(disposition, values))
			}
			n, err = io.ReadFull(part, prefix[:])
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
				return nil, "", fmt.Errorf("read official image MIME prefix: %w", err)
			}
			// The downloader validates local references before this step. For
			// client uploads, fix only recognized types; never guess from a name.
			switch detected := http.DetectContentType(prefix[:n]); detected {
			case "image/png", "image/jpeg", "image/webp":
				header.Set("Content-Type", detected)
			}
		}
		outPart, err := writer.CreatePart(header)
		if err != nil {
			return nil, "", fmt.Errorf("write official image multipart header: %w", err)
		}
		if _, err := outPart.Write(prefix[:n]); err != nil {
			return nil, "", fmt.Errorf("write official image MIME prefix: %w", err)
		}
		if _, err := io.Copy(outPart, part); err != nil {
			return nil, "", fmt.Errorf("write official image multipart body: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("close official image multipart: %w", err)
	}
	return output.Bytes(), writer.FormDataContentType(), nil
}
