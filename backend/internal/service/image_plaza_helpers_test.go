package service

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestValidateImageBytesAcceptsDecodedPNG(t *testing.T) {
	validated, err := ValidateImageBytes(testPNG(t), "image/png", 1<<20, 100)
	require.NoError(t, err)
	require.Equal(t, "image/png", validated.MIMEType)
	require.Equal(t, 2, validated.Width)
	require.NotEmpty(t, validated.SHA256)
}

func TestValidateImageBytesRejectsExecutableAndForgedMIME(t *testing.T) {
	_, err := ValidateImageBytes([]byte("<script>alert(1)</script>"), "image/png", 1<<20, 100)
	require.Error(t, err)

	_, err = ValidateImageBytes(testPNG(t), "text/html", 1<<20, 100)
	require.Error(t, err)
}

func TestValidateImageBytesRejectsTrailingPolyglot(t *testing.T) {
	data := append(testPNG(t), []byte("<script src=/x></script>")...)
	_, err := ValidateImageBytes(data, "image/png", 1<<20, 100)
	require.Error(t, err)
}

func TestDecodeImagePayloadRejectsSVGDataURI(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	raw := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(svg)
	decoded, declaredMIME, err := DecodeBase64ImagePayload(raw)
	require.NoError(t, err)
	require.Equal(t, svg, decoded)
	require.Equal(t, "image/svg+xml", declaredMIME)

	_, _, _, err = DecodeImagePayload(raw)
	require.Error(t, err)
}

func TestValidateImageBytesJPEGAndFirstEOIPolyglot(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 3)), &jpeg.Options{Quality: 85}))
	jpegData := buf.Bytes()
	validated, err := ValidateImageBytes(jpegData, "image/jpeg", 1<<20, 100)
	require.NoError(t, err)
	require.Equal(t, "image/jpeg", validated.MIMEType)

	polyglot := append(append([]byte(nil), jpegData...), []byte("<script>alert(1)</script>\xff\xd9")...)
	_, err = ValidateImageBytes(polyglot, "image/jpeg", 1<<20, 100)
	require.Error(t, err)
}

func TestWebPContainerRejectsTrailingBytes(t *testing.T) {
	container := make([]byte, 12)
	copy(container[:4], "RIFF")
	binary.LittleEndian.PutUint32(container[4:8], 4)
	copy(container[8:12], "WEBP")
	require.NoError(t, validateExactImageContainer(container, "webp"))
	require.Error(t, validateExactImageContainer(append(container, 0), "webp"))
}

func TestValidateImageBytesAcceptsDecodedWebPAndRejectsTrailingData(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	require.NoError(t, err)

	validated, err := ValidateImageBytes(data, "image/webp", 1<<20, 100)
	require.NoError(t, err)
	require.Equal(t, "image/webp", validated.MIMEType)
	require.Equal(t, 1, validated.Width)
	require.Equal(t, 1, validated.Height)

	_, err = ValidateImageBytes(append(append([]byte(nil), data...), []byte("<script>alert(1)</script>")...), "image/webp", 1<<20, 100)
	require.Error(t, err)
}

func TestValidateImageBytesEnforcesByteAndPixelLimits(t *testing.T) {
	data := testPNG(t)
	_, err := ValidateImageBytes(data, "image/png", int64(len(data)-1), 100)
	require.Error(t, err)
	_, err = ValidateImageBytes(data, "image/png", 1<<20, 3)
	require.Error(t, err)
}
