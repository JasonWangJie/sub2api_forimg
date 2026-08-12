package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	_ "golang.org/x/image/webp"
)

const (
	DefaultImageLibraryMaxBytes  int64 = 20 << 20
	DefaultImageLibraryMaxPixels int64 = 40_000_000
)

// ValidatedImage contains metadata derived from the bytes themselves. Client
// supplied MIME types and filename extensions are never persisted as truth.
type ValidatedImage struct {
	Data      []byte
	MIMEType  string
	Format    string
	Width     int
	Height    int
	SHA256    string
	SizeBytes int64
}

// DecodeBase64ImagePayload decodes the legacy plaza transport without parsing
// the image container. Callers must pass the result to ValidateImageBytes;
// keeping those steps separate lets persistent rate and quota checks run first.
func DecodeBase64ImagePayload(raw string) (data []byte, declaredMIME string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "", apperrors.BadRequest("INVALID_IMAGE", "image is required")
	}

	declaredType := ""
	payload := raw
	if strings.HasPrefix(strings.ToLower(raw), "data:") {
		comma := strings.IndexByte(raw, ',')
		if comma < 0 {
			return nil, "", apperrors.BadRequest("INVALID_IMAGE", "invalid image data URL")
		}
		meta := raw[len("data:"):comma]
		parts := strings.Split(meta, ";")
		if len(parts) < 2 || !strings.EqualFold(strings.TrimSpace(parts[len(parts)-1]), "base64") {
			return nil, "", apperrors.BadRequest("INVALID_IMAGE", "image data URL must use base64 encoding")
		}
		declaredType = strings.TrimSpace(parts[0])
		payload = raw[comma+1:]
	}

	// Reject oversized input before allocating the decoded buffer. A little
	// headroom accounts for base64 padding without weakening the byte limit.
	if int64(base64.StdEncoding.DecodedLen(len(payload))) > DefaultImageLibraryMaxBytes+2 {
		return nil, "", apperrors.BadRequest("IMAGE_TOO_LARGE", "image exceeds the 20 MiB limit")
	}
	decoded, decodeErr := base64.StdEncoding.Strict().DecodeString(payload)
	if decodeErr != nil {
		return nil, "", apperrors.BadRequest("INVALID_IMAGE", "invalid base64 image")
	}
	return decoded, declaredType, nil
}

// DecodeImagePayload accepts raw base64 or a base64 data URL and performs a
// complete, bounded decode. Only PNG, JPEG, and WebP are accepted.
func DecodeImagePayload(raw string) (data []byte, mimeType string, format string, err error) {
	decoded, declaredType, err := DecodeBase64ImagePayload(raw)
	if err != nil {
		return nil, "", "", err
	}
	validated, validateErr := ValidateImageBytes(decoded, declaredType, DefaultImageLibraryMaxBytes, DefaultImageLibraryMaxPixels)
	if validateErr != nil {
		return nil, "", "", validateErr
	}
	return validated.Data, validated.MIMEType, validated.Format, nil
}

// ValidateImageBytes fully decodes an image and verifies that its container
// ends exactly where expected. This rejects HTML/JS/SVG, forged MIME types,
// decompression bombs, and common image-plus-script polyglots.
func ValidateImageBytes(data []byte, declaredType string, maxBytes, maxPixels int64) (*ValidatedImage, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultImageLibraryMaxBytes
	}
	if maxPixels <= 0 {
		maxPixels = DefaultImageLibraryMaxPixels
	}
	if len(data) == 0 {
		return nil, apperrors.BadRequest("INVALID_IMAGE", "image is empty")
	}
	if int64(len(data)) > maxBytes {
		return nil, apperrors.BadRequest("IMAGE_TOO_LARGE", "image exceeds the configured byte limit")
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, apperrors.BadRequest("UNSUPPORTED_IMAGE_FORMAT", "only valid PNG, JPEG, and WebP images are supported")
	}
	format = strings.ToLower(strings.TrimSpace(format))
	actualType := imageFormatMIME(format)
	if actualType != "image/png" && actualType != "image/jpeg" && actualType != "image/webp" {
		return nil, apperrors.BadRequest("UNSUPPORTED_IMAGE_FORMAT", "only PNG, JPEG, and WebP images are supported")
	}
	if int64(cfg.Width) > maxPixels || int64(cfg.Height) > maxPixels || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, apperrors.BadRequest("IMAGE_TOO_MANY_PIXELS", "image exceeds the configured pixel limit")
	}
	if err := validateExactImageContainer(data, format); err != nil {
		return nil, apperrors.BadRequest("INVALID_IMAGE", "image container is invalid or contains trailing data")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, apperrors.BadRequest("INVALID_IMAGE", "image data could not be fully decoded")
	}

	detectedType := http.DetectContentType(data)
	if parsed, _, parseErr := mime.ParseMediaType(detectedType); parseErr == nil {
		detectedType = parsed
	}
	if !strings.EqualFold(detectedType, actualType) {
		return nil, apperrors.BadRequest("IMAGE_MIME_MISMATCH", "image signature does not match its decoded format")
	}
	if strings.TrimSpace(declaredType) != "" {
		parsed, _, parseErr := mime.ParseMediaType(declaredType)
		if parseErr != nil || !strings.EqualFold(parsed, actualType) {
			return nil, apperrors.BadRequest("IMAGE_MIME_MISMATCH", "declared image type does not match image bytes")
		}
	}

	sum := sha256.Sum256(data)
	return &ValidatedImage{
		Data: data, MIMEType: actualType, Format: format,
		Width: cfg.Width, Height: cfg.Height,
		SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(data)),
	}, nil
}

func validateExactImageContainer(data []byte, format string) error {
	switch format {
	case "png":
		if len(data) < 20 || !bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) {
			return apperrors.BadRequest("INVALID_IMAGE", "invalid PNG signature")
		}
		pos := 8
		for pos+12 <= len(data) {
			length := int64(binary.BigEndian.Uint32(data[pos : pos+4]))
			if length > int64(len(data)) || int64(pos)+12+length > int64(len(data)) {
				return apperrors.BadRequest("INVALID_IMAGE", "invalid PNG chunk")
			}
			kind := string(data[pos+4 : pos+8])
			pos += int(12 + length)
			if kind == "IEND" {
				if length != 0 || pos != len(data) {
					return apperrors.BadRequest("INVALID_IMAGE", "invalid PNG terminator")
				}
				return nil
			}
		}
		return apperrors.BadRequest("INVALID_IMAGE", "PNG is missing IEND")
	case "jpeg":
		if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
			return apperrors.BadRequest("INVALID_IMAGE", "invalid JPEG boundaries")
		}
		return validateJPEGEndsAtFirstEOI(data)
	case "webp":
		if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
			return apperrors.BadRequest("INVALID_IMAGE", "invalid WebP signature")
		}
		declaredLength := int64(binary.LittleEndian.Uint32(data[4:8])) + 8
		if declaredLength != int64(len(data)) {
			return apperrors.BadRequest("INVALID_IMAGE", "invalid WebP container length")
		}
		return nil
	default:
		return apperrors.BadRequest("UNSUPPORTED_IMAGE_FORMAT", "unsupported image format")
	}
}

// validateJPEGEndsAtFirstEOI walks marker segments and entropy-coded scans so
// an early EOI followed by another payload cannot be hidden by appending a
// second EOI at the physical end of the file.
func validateJPEGEndsAtFirstEOI(data []byte) error {
	pos := 2
	for pos < len(data) {
		if data[pos] != 0xff {
			return apperrors.BadRequest("INVALID_IMAGE", "invalid JPEG marker")
		}
		markerStart := pos
		for pos < len(data) && data[pos] == 0xff {
			pos++
		}
		if pos >= len(data) {
			return apperrors.BadRequest("INVALID_IMAGE", "truncated JPEG marker")
		}
		marker := data[pos]
		pos++
		switch {
		case marker == 0xd9:
			if pos != len(data) {
				return apperrors.BadRequest("INVALID_IMAGE", "JPEG contains trailing data")
			}
			return nil
		case marker == 0xd8 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7):
			continue
		case marker == 0x00:
			return apperrors.BadRequest("INVALID_IMAGE", "unexpected stuffed JPEG marker")
		}
		if pos+2 > len(data) {
			return apperrors.BadRequest("INVALID_IMAGE", "truncated JPEG segment")
		}
		segmentLength := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		if segmentLength < 2 || pos+segmentLength > len(data) {
			return apperrors.BadRequest("INVALID_IMAGE", "invalid JPEG segment length")
		}
		pos += segmentLength
		if marker != 0xda {
			continue
		}

		// Scan entropy bytes until the next real marker. FF00 is escaped image
		// data and restart markers do not end the scan.
		for pos < len(data) {
			if data[pos] != 0xff {
				pos++
				continue
			}
			next := pos + 1
			for next < len(data) && data[next] == 0xff {
				next++
			}
			if next >= len(data) {
				return apperrors.BadRequest("INVALID_IMAGE", "truncated JPEG scan")
			}
			if data[next] == 0x00 {
				pos = next + 1
				continue
			}
			if data[next] >= 0xd0 && data[next] <= 0xd7 {
				pos = next + 1
				continue
			}
			pos = markerStart
			pos = next - 1
			break
		}
	}
	return apperrors.BadRequest("INVALID_IMAGE", "JPEG is missing EOI")
}

func mimeFromFormat(format string) string {
	switch strings.ToLower(format) {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	default:
		return "image/png"
	}
}

func formatFromMime(mimeType string) string {
	switch strings.ToLower(mimeType) {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

func extFromFormat(format string) string {
	switch strings.ToLower(format) {
	case "jpg", "jpeg":
		return "jpg"
	case "webp":
		return "webp"
	default:
		return "png"
	}
}

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	r := []rune(s)
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "..."
}
