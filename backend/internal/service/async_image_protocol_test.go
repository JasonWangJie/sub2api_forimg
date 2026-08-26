package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"io"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const asyncImageOnePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestParseBBGeminiImageRequest(t *testing.T) {
	body := []byte(`{
        "model":"gemini-3-pro-image-preview",
        "stream":false,
        "messages":[{"role":"user","content":[
          {"type":"image_url","image_url":{"url":"https://images.example/ref.png"}},
          {"type":"text","text":"paint a quiet harbor"}
        ]}],
        "extra_body":{"google":{"image_config":{"image_size":"4k","aspect_ratio":"16:9"}}}
      }`)

	req, err := ParseBBGeminiImageRequest(body, "/v1/chat/completions_gm")
	require.NoError(t, err)
	require.Equal(t, PlatformGemini, req.Platform)
	require.Equal(t, AsyncImageKindEdit, req.Kind)
	require.Equal(t, "4K", req.ImageSize)
	require.Equal(t, "16:9", req.AspectRatio)
	require.Equal(t, "paint a quiet harbor", req.Prompt)
	require.Equal(t, 1, req.ReferenceCount())
}

func TestParseBBGeminiImageRequestRejectsStreamingAndUnsupportedRole(t *testing.T) {
	_, err := ParseBBGeminiImageRequest([]byte(`{"model":"gemini-image","stream":true,"messages":[{"role":"user","content":"x"}]}`), "")
	require.ErrorContains(t, err, "stream must be false")

	_, err = ParseBBGeminiImageRequest([]byte(`{"model":"gemini-image","messages":[{"role":"system","content":"x"}]}`), "")
	require.ErrorContains(t, err, "unsupported message role")
}

func TestAsyncImageModelReferenceLimitUsesKnownGeminiCapabilities(t *testing.T) {
	require.Equal(t, 3, AsyncImageModelReferenceLimit(PlatformGemini, "gemini-2.5-flash-image"))
	require.Equal(t, 14, AsyncImageModelReferenceLimit(PlatformGemini, "gemini-3-pro-image-preview"))
	require.Zero(t, AsyncImageModelReferenceLimit(PlatformGemini, "custom-gemini-image"))
	require.Zero(t, AsyncImageModelReferenceLimit(PlatformOpenAI, "gpt-image-2"))
}

func TestParseSCGeminiImageRequestDimensions(t *testing.T) {
	req, err := ParseSCGeminiImageRequest([]byte(`{
        "model":"nano-banana-2","prompt":"modern living room",
        "image_urls":["https://images.example/ref.png"],
        "resolution":"2K","aspect_ratio":"auto"
      }`), "/v1/images/generations_sc")
	require.NoError(t, err)
	require.Equal(t, AsyncImageKindEdit, req.Kind)
	require.Equal(t, "2K", req.ImageSize)
	require.Empty(t, req.AspectRatio, "auto is represented by omitting the upstream ratio")

	halfK, err := ParseSCGeminiImageRequest([]byte(`{"model":"m","prompt":"p","resolution":"0.5K"}`), "")
	require.NoError(t, err)
	require.Equal(t, "0.5K", halfK.ImageSize)

	textAuto, err := ParseSCGeminiImageRequest([]byte(`{"model":"m","prompt":"p","aspect_ratio":"auto"}`), "")
	require.NoError(t, err)
	require.Equal(t, AsyncImageKindText, textAuto.Kind)
	require.Empty(t, textAuto.AspectRatio)

	tall, err := ParseSCGeminiImageRequest([]byte(`{"model":"m","prompt":"p","aspect_ratio":"9:21","resolution":"1K"}`), "")
	require.NoError(t, err)
	require.Equal(t, "9:21", tall.AspectRatio)
	require.Equal(t, "1K", tall.ImageSize)
}

func TestParseSCGeminiImageRequestSizeAlias(t *testing.T) {
	req, err := ParseSCGeminiImageRequest([]byte(`{
        "image_urls":[
          "https://images.example/ref1.jpg",
          "https://images.example/ref2.jpg"
        ],
        "model":"gemini-3-pro-image-preview",
        "prompt":"remove cars from image 1, ground details follow image 2",
        "resolution":"4K",
        "size":"3:2"
      }`), "/v1/images/generations_sc")
	require.NoError(t, err)
	require.Equal(t, AsyncImageKindEdit, req.Kind)
	require.Equal(t, "4K", req.ImageSize)
	require.Equal(t, "3:2", req.AspectRatio)
	require.Equal(t, 2, req.ReferenceCount())

	tierAsSize, err := ParseSCGeminiImageRequest([]byte(`{
        "model":"gemini-3-pro-image-preview",
        "prompt":"quiet street",
        "size":"2K"
      }`), "")
	require.NoError(t, err)
	require.Equal(t, "2K", tierAsSize.ImageSize)
	require.Empty(t, tierAsSize.AspectRatio)

	// Explicit aspect_ratio wins over size ratio alias.
	preferAspect, err := ParseSCGeminiImageRequest([]byte(`{
        "model":"m","prompt":"p","aspect_ratio":"16:9","size":"3:2"
      }`), "")
	require.NoError(t, err)
	require.Equal(t, "16:9", preferAspect.AspectRatio)
}

func TestParseSCGeminiImageRequestPixelSizeAlias(t *testing.T) {
	for _, size := range []string{"1080x1350", "1080X1350", "1080*1350", "1080\u00d71350"} {
		t.Run(size, func(t *testing.T) {
			req, err := ParseSCGeminiImageRequest([]byte(`{
        "image_urls":["https://images.example/ref.png"],
        "model":"gemini-3-pro-image-preview",
        "prompt":"change the background",
        "size":"`+size+`"
      }`), "/v1/images/generations_sc")
			require.NoError(t, err)
			require.Equal(t, AsyncImageKindEdit, req.Kind)
			require.Empty(t, req.ImageSize)
			require.Equal(t, "4:5", req.AspectRatio)
		})
	}

	_, err := ParseSCGeminiImageRequest([]byte(`{
      "model":"gemini-3-pro-image-preview",
      "prompt":"change the background",
      "size":"1000x900"
    }`), "/v1/images/generations_sc")
	require.ErrorContains(t, err, `unsupported aspect ratio "10:9"`)

	ultrawide, err := ParseSCGeminiImageRequest([]byte(`{
      "image_urls":["https://images.example/ref.png"],
      "model":"gemini-3-pro-image-preview",
      "prompt":"make it cinematic",
      "size":"2520x1080"
    }`), "/v1/images/generations_sc")
	require.NoError(t, err)
	require.Equal(t, "21:9", ultrawide.AspectRatio)

	ultratall, err := ParseSCGeminiImageRequest([]byte(`{
      "model":"gemini-3-pro-image-preview",
      "prompt":"make it tall",
      "size":"1080x2520"
    }`), "/v1/images/generations_sc")
	require.NoError(t, err)
	require.Equal(t, "9:21", ultratall.AspectRatio)
}

func TestAsyncImageReferenceDownloaderDataURI(t *testing.T) {
	downloader := AsyncImageReferenceDownloader{MaxBytes: 1 << 20, MaxPixels: 100}
	ref, err := downloader.Download(context.Background(), "data:image/png;base64,"+asyncImageOnePixelPNG)
	require.NoError(t, err)
	require.Equal(t, "image/png", ref.MIMEType)
	require.Equal(t, 1, ref.Width)
	require.Equal(t, 1, ref.Height)
	require.NotEmpty(t, ref.SHA256)
	require.Equal(t, asyncImageOnePixelPNG, base64.StdEncoding.EncodeToString(ref.Data))
}

func TestAsyncImageReferenceBudgetEnforcesAggregateLimits(t *testing.T) {
	dataURI := "data:image/png;base64," + asyncImageOnePixelPNG
	pngBytes, err := base64.StdEncoding.DecodeString(asyncImageOnePixelPNG)
	require.NoError(t, err)

	tests := []struct {
		name   string
		budget AsyncImageReferenceBudget
	}{
		{name: "count", budget: AsyncImageReferenceBudget{MaxImages: 1}},
		{name: "bytes", budget: AsyncImageReferenceBudget{MaxImages: 2, MaxTotalBytes: int64(len(pngBytes)*2 - 1)}},
		{name: "pixels", budget: AsyncImageReferenceBudget{MaxImages: 2, MaxTotalPixels: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			downloader := AsyncImageReferenceDownloader{MaxBytes: 1 << 20, MaxPixels: 100, Budget: &tt.budget}
			_, err := downloader.Download(context.Background(), dataURI)
			require.NoError(t, err)
			_, err = downloader.Download(context.Background(), dataURI)
			require.Error(t, err)
		})
	}
}

func TestAsyncImageReferenceDownloaderUsesBoundObjectWithoutNetwork(t *testing.T) {
	validated, err := (AsyncImageReferenceDownloader{}).ValidateBytes(mustDecodeAsyncImagePNG(t), "image/png")
	require.NoError(t, err)
	called := false
	downloader := AsyncImageReferenceDownloader{
		BoundLoader: func(_ context.Context, rawURL string) (*AsyncImageReference, bool, error) {
			called = true
			require.Equal(t, "https://storage.invalid/input.png", rawURL)
			return validated, true, nil
		},
	}
	ref, err := downloader.Download(context.Background(), "https://storage.invalid/input.png")
	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, validated.SHA256, ref.SHA256)
}

func TestAsyncImageReferenceDownloadErrorCarriesCauseAndRetryAfter(t *testing.T) {
	err := &AsyncImageReferenceDownloadError{Phase: "read", StatusCode: 503, RetryAfter: 45 * time.Second, Err: io.ErrUnexpectedEOF}
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Contains(t, err.Error(), "HTTP status 503")
	require.Equal(t, 45*time.Second, err.RetryAfter)

	now := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	require.Equal(t, 30*time.Second, ParseAsyncImageRetryAfter("30", now))
	require.Equal(t, 2*time.Minute, ParseAsyncImageRetryAfter(now.Add(2*time.Minute).Format(http.TimeFormat), now))
	require.Zero(t, ParseAsyncImageRetryAfter("invalid", now))
	require.True(t, errors.Is(err, io.ErrUnexpectedEOF))
}

func TestAsyncImageReferenceCacheCopiesAndExpires(t *testing.T) {
	cache := NewAsyncImageReferenceCache(time.Minute, 10)
	ref := &AsyncImageReference{MIMEType: "image/png", Data: []byte{1, 2, 3}}
	cache.put("https://cdn.example/a", ref, time.Unix(100, 0))
	got := cache.get("https://cdn.example/a", time.Unix(101, 0))
	require.Equal(t, ref.Data, got.Data)
	got.Data[0] = 9
	require.Equal(t, byte(1), cache.get("https://cdn.example/a", time.Unix(101, 0)).Data[0])
	require.Nil(t, cache.get("https://cdn.example/a", time.Unix(161, 0)))
}

func TestAsyncImageFetchGateHonorsLimitAndCancellation(t *testing.T) {
	gate := NewAsyncImageFetchGate(1)
	release, err := gate.acquire(context.Background())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = gate.acquire(ctx)
	require.ErrorIs(t, err, context.Canceled)
	release()
	next, err := gate.acquire(context.Background())
	require.NoError(t, err)
	next()
}

func mustDecodeAsyncImagePNG(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(asyncImageOnePixelPNG)
	require.NoError(t, err)
	return data
}

func TestAsyncImageReferenceValidationRejectsGIFTrailingDataAndForgedMIME(t *testing.T) {
	downloader := AsyncImageReferenceDownloader{MaxBytes: 1 << 20, MaxPixels: 100}

	var gifData bytes.Buffer
	palette := color.Palette{color.Black, color.White}
	require.NoError(t, gif.Encode(&gifData, image.NewPaletted(image.Rect(0, 0, 1, 1), palette), nil))
	_, err := downloader.ValidateBytes(gifData.Bytes(), "image/gif")
	require.Error(t, err)

	pngData := testPNG(t)
	_, err = downloader.ValidateBytes(append(append([]byte(nil), pngData...), []byte("<script>alert(1)</script>")...), "image/png")
	require.Error(t, err)

	_, err = downloader.ValidateBytes(pngData, "image/jpeg")
	require.Error(t, err)
}

func TestAsyncImageReferenceDownloadedValidationTrimsOnlyVerifiedTrailingContainerData(t *testing.T) {
	pngData := testPNG(t)
	withTrailing := append(append([]byte(nil), pngData...), []byte("\n<!-- CDN metadata -->")...)
	downloader := AsyncImageReferenceDownloader{MaxBytes: 1 << 20, MaxPixels: 100}

	ref, err := downloader.validateDownloadedImage(withTrailing, "image/png")
	require.NoError(t, err)
	require.Equal(t, pngData, ref.Data)

	// The public validator remains strict for uploads and data URIs.
	_, err = downloader.ValidateBytes(withTrailing, "image/png")
	require.Error(t, err)
}

func TestAsyncImageReferenceValidationEnforcesByteAndPixelLimits(t *testing.T) {
	pngData := testPNG(t)
	_, err := (AsyncImageReferenceDownloader{MaxBytes: int64(len(pngData) - 1), MaxPixels: 100}).ValidateBytes(pngData, "image/png")
	require.Error(t, err)

	_, err = (AsyncImageReferenceDownloader{MaxBytes: 1 << 20, MaxPixels: 1}).ValidateBytes(pngData, "image/png")
	require.Error(t, err)
}

func TestAsyncImagePublicIPPolicy(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "::1", "fc00::1"}
	for _, raw := range blocked {
		require.False(t, isAsyncImagePublicIP(netip.MustParseAddr(raw)), raw)
	}
	require.True(t, isAsyncImagePublicIP(netip.MustParseAddr("1.1.1.1")))
	require.True(t, isAsyncImagePublicIP(netip.MustParseAddr("2606:4700:4700::1111")))
}

func TestBuildGeminiAsyncChatBodyWithDataURI(t *testing.T) {
	req := &AsyncImageNormalizedRequest{
		Model:       "gemini-image",
		ImageSize:   "2K",
		AspectRatio: "1:1",
		Parts: []AsyncImageInputPart{
			{Type: "image_url", URL: "data:image/png;base64," + asyncImageOnePixelPNG},
			{Type: "text", Text: "restyle"},
		},
	}
	body, err := BuildGeminiAsyncChatBody(context.Background(), req, AsyncImageReferenceDownloader{})
	require.NoError(t, err)
	require.JSONEq(t, `{
      "model":"gemini-image","stream":false,
      "messages":[{"role":"user","content":[
        {"type":"image_url","image_url":{"url":"data:image/png;base64,`+asyncImageOnePixelPNG+`"}},
        {"type":"text","text":"restyle"}
      ]}],
      "extra_body":{"google":{"image_config":{"image_size":"2K","aspect_ratio":"1:1"}}}
    }`, string(body))
}

func TestBuildGeminiAsyncChatBodyPassesHTTPSURLThrough(t *testing.T) {
	req := &AsyncImageNormalizedRequest{
		Model:     "gemini-image",
		ImageSize: "2K",
		Parts: []AsyncImageInputPart{
			{Type: "image_url", URL: "https://1.1.1.1/ref.png"},
			{Type: "text", Text: "restyle"},
		},
	}
	body, err := BuildGeminiAsyncChatBody(context.Background(), req, AsyncImageReferenceDownloader{
		Budget: &AsyncImageReferenceBudget{MaxImages: 2},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{
      "model":"gemini-image","stream":false,
      "messages":[{"role":"user","content":[
        {"type":"image_url","image_url":{"url":"https://1.1.1.1/ref.png"}},
        {"type":"text","text":"restyle"}
      ]}],
      "extra_body":{"google":{"image_config":{"image_size":"2K"}}}
    }`, string(body))
}

func TestBuildGeminiAsyncChatBodyPassthroughConsumesEachReferenceOnce(t *testing.T) {
	budget := &AsyncImageReferenceBudget{MaxImages: 2}
	req := &AsyncImageNormalizedRequest{
		Model: "gemini-image",
		Parts: []AsyncImageInputPart{
			{Type: "image_url", URL: "https://1.1.1.1/ref-1.png"},
			{Type: "image_url", URL: "https://8.8.8.8/ref-2.png"},
		},
	}

	_, err := BuildGeminiAsyncChatBody(context.Background(), req, AsyncImageReferenceDownloader{Budget: budget})
	require.NoError(t, err)
	require.Equal(t, 2, budget.images)
}

func TestBuildGeminiAsyncChatBodyLocalTransportDownloadsReference(t *testing.T) {
	png := mustDecodeAsyncImagePNG(t)
	req := &AsyncImageNormalizedRequest{
		Model: "gemini-image",
		Parts: []AsyncImageInputPart{{Type: "image_url", URL: "https://cdn.example/ref.png"}},
	}
	body, err := BuildGeminiAsyncChatBodyWithTransport(context.Background(), req, AsyncImageReferenceDownloader{
		BoundLoader: func(_ context.Context, rawURL string) (*AsyncImageReference, bool, error) {
			require.Equal(t, "https://cdn.example/ref.png", rawURL)
			return &AsyncImageReference{MIMEType: "image/png", Data: png, Width: 1, Height: 1}, true, nil
		},
	}, AsyncImageReferenceTransportLocal)
	require.NoError(t, err)
	require.Contains(t, string(body), "data:image/png;base64,")
	require.NotContains(t, string(body), "https://cdn.example/ref.png")
}

func TestAsyncImageTaskRequestHashIncludesDialectAndEndpoint(t *testing.T) {
	body := []byte(`{"model":"m"}`)
	a := AsyncImageTaskRequestHash(PlatformGemini, AsyncImageDialectBB, "/a", body)
	b := AsyncImageTaskRequestHash(PlatformGemini, AsyncImageDialectSC, "/a", body)
	c := AsyncImageTaskRequestHash(PlatformGemini, AsyncImageDialectBB, "/b", body)
	require.NotEqual(t, a, b)
	require.NotEqual(t, a, c)
	require.Equal(t, a, AsyncImageTaskRequestHash(PlatformGemini, AsyncImageDialectBB, "/a", body))
}
