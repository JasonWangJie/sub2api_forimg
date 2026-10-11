package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type generatedImageRoundTripFunc func(*http.Request) (*http.Response, error)

func (f generatedImageRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGeneratedImageDownloadNormalizesMIMEAndValidatesBytes(t *testing.T) {
	pngData := mustDecodeAsyncImagePNG(t)
	webpData, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	require.NoError(t, err)
	var jpegBuffer bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpegBuffer, image.NewRGBA(image.Rect(0, 0, 2, 3)), nil))
	tests := []struct {
		name       string
		data       []byte
		header     string
		maxBytes   int64
		maxPixels  int64
		wantMIME   string
		wantWidth  int
		wantHeight int
	}{
		{"PNG declared JPEG", pngData, "image/jpeg", 1 << 20, 100, "image/png", 1, 1},
		{"JPEG declared PNG", jpegBuffer.Bytes(), "image/png", 1 << 20, 100, "image/jpeg", 2, 3},
		{"WebP declared JPEG", webpData, "image/jpeg", 1 << 20, 100, "image/webp", 1, 1},
		{"generic Content-Type", pngData, "application/octet-stream", 1 << 20, 100, "image/png", 1, 1},
		{"HTML disguised as PNG", []byte("<html>not an image</html>"), "image/png", 1 << 20, 100, "", 0, 0},
		{"SVG", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), "image/svg+xml", 1 << 20, 100, "", 0, 0},
		{"trailing script", append(append([]byte(nil), pngData...), []byte("<script>bad</script>")...), "image/png", 1 << 20, 100, "", 0, 0},
		{"byte limit without Content-Length", pngData, "image/png", int64(len(pngData) - 1), 100, "", 0, 0},
		{"pixel limit", jpegBuffer.Bytes(), "image/jpeg", 1 << 20, 5, "", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: generatedImageRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				require.Empty(t, req.Header.Get("Authorization"))
				require.Empty(t, req.Header.Get("x-goog-api-key"))
				return &http.Response{StatusCode: 200, ContentLength: -1, Header: http.Header{"Content-Type": []string{tt.header}}, Body: io.NopCloser(bytes.NewReader(tt.data))}, nil
			})}
			d := AsyncImageReferenceDownloader{MaxBytes: tt.maxBytes, MaxPixels: tt.maxPixels}
			u, err := url.Parse("https://cdn.example/result?sig=secret")
			require.NoError(t, err)
			ref, err := d.fetchRemoteImage(context.Background(), u, client, true)
			if tt.wantMIME == "" {
				require.Error(t, err)
				var downloadErr *AsyncImageReferenceDownloadError
				require.ErrorAs(t, err, &downloadErr)
				require.Equal(t, "validate", downloadErr.Phase)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantMIME, ref.MIMEType)
			require.Equal(t, tt.data, ref.Data)
			require.Equal(t, tt.wantWidth, ref.Width)
			require.Equal(t, tt.wantHeight, ref.Height)
			require.NotEmpty(t, ref.SHA256)
		})
	}
}

func TestGeneratedImageDownloadDoesNotRelaxReferenceMIME(t *testing.T) {
	d := AsyncImageReferenceDownloader{MaxBytes: 1 << 20, MaxPixels: 100,
		BoundLoader: func(context.Context, string) (*AsyncImageReference, bool, error) {
			t.Fatal("an output must not resolve through the input ownership loader")
			return nil, false, nil
		},
	}
	dataURI := "data:image/jpeg;base64," + asyncImageOnePixelPNG
	ref, err := d.DownloadGenerated(context.Background(), dataURI)
	require.NoError(t, err)
	require.Equal(t, "image/png", ref.MIMEType)
	d.BoundLoader = nil
	_, err = d.Download(context.Background(), dataURI)
	require.ErrorContains(t, err, "declared image type does not match")
	_, err = d.validateDownloadedImage(mustDecodeAsyncImagePNG(t), "image/jpeg")
	require.ErrorContains(t, err, "declared image type does not match")
}

func TestGeneratedImageDownloadRejectsUnsafeURLsAndRedirects(t *testing.T) {
	d := AsyncImageReferenceDownloader{MaxRedirects: 3}
	for _, rawURL := range []string{"http://1.1.1.1/image.png", "https://127.0.0.1/image.png", "https://[::1]/image.png", "https://169.254.169.254/image.png", "file:///tmp/image.png", "https://user:secret@1.1.1.1/image.png"} {
		_, err := d.DownloadGenerated(context.Background(), rawURL)
		require.Error(t, err, rawURL)
	}
	for _, rawURL := range []string{"http://1.1.1.1/image.png", "https://127.0.0.1/image.png", "https://10.0.0.1/image.png"} {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		require.NoError(t, err)
		require.Error(t, d.checkRedirect(req, []*http.Request{{}}))
	}
	req, err := http.NewRequest(http.MethodGet, "https://1.1.1.1/image.png", nil)
	require.NoError(t, err)
	require.ErrorContains(t, d.checkRedirect(req, []*http.Request{{}, {}, {}}), "redirect limit")
	req, err = http.NewRequest(http.MethodGet, "https://user:secret@1.1.1.1/image.png", nil)
	require.NoError(t, err)
	require.ErrorContains(t, d.checkGeneratedRedirect(req, []*http.Request{{}}), "credentials")
}

func TestGeneratedImageDownloadExpiredURLAndTimeoutDoNotExposeSignature(t *testing.T) {
	u, err := url.Parse("https://cdn.example/result?sig=do-not-leak")
	require.NoError(t, err)
	d := AsyncImageReferenceDownloader{MaxBytes: 1 << 20}
	client := &http.Client{Transport: generatedImageRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("expired signature"))}, nil
	})}
	_, err = d.fetchRemoteImage(context.Background(), u, client, true)
	require.Error(t, err)
	safeErr := &generatedImageDownloadError{cause: err}
	require.Contains(t, safeErr.Error(), "HTTP status 403")
	require.NotContains(t, safeErr.Error(), "do-not-leak")

	client = &http.Client{Timeout: 10 * time.Millisecond, Transport: generatedImageRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	_, err = d.fetchRemoteImage(context.Background(), u, client, true)
	require.Error(t, err)
	safeErr = &generatedImageDownloadError{cause: err}
	require.True(t, errors.Is(safeErr, context.DeadlineExceeded))
	require.NotContains(t, safeErr.Error(), "do-not-leak")
	require.Contains(t, safeErr.Error(), "download failed")
}

func TestGeneratedImageDataURIPreservesByteLimit(t *testing.T) {
	d := AsyncImageReferenceDownloader{MaxBytes: 4}
	_, err := d.DownloadGenerated(context.Background(), "data:image/png;base64,"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 8)))
	require.Error(t, err)
	pngData := mustDecodeAsyncImagePNG(t)
	d.MaxBytes = int64(len(pngData))
	ref, err := d.DownloadGenerated(context.Background(), "data:image/jpeg;base64,"+asyncImageOnePixelPNG)
	require.NoError(t, err)
	require.Equal(t, pngData, ref.Data)
}
