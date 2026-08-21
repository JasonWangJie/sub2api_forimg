package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestMapOpenAIImageDimensions(t *testing.T) {
	tests := []struct {
		resolution  string
		aspectRatio string
		wantSize    string
	}{
		{resolution: "1K", aspectRatio: "1:1", wantSize: "1024x1024"},
		{resolution: "1K", aspectRatio: "3:2", wantSize: "1536x1024"},
		{resolution: "1K", aspectRatio: "16:9", wantSize: "1536x1024"},
		{resolution: "1K", aspectRatio: "2:3", wantSize: "1024x1536"},
		{resolution: "1K", aspectRatio: "4:5", wantSize: "1024x1280"},
		{resolution: "1K", aspectRatio: "5:4", wantSize: "1280x1024"},
		{resolution: "1K", aspectRatio: "4:3", wantSize: "1360x1024"},
		{resolution: "1K", aspectRatio: "3:4", wantSize: "1024x1360"},
		{resolution: "1K", aspectRatio: "21:9", wantSize: "2384x1024"},
		{resolution: "1K", aspectRatio: "9:21", wantSize: "1024x2384"},
		{resolution: "1K", aspectRatio: "2:1", wantSize: "2048x1024"},
		{resolution: "1K", aspectRatio: "1:2", wantSize: "1024x2048"},
		{resolution: "1K", aspectRatio: "", wantSize: "1024x1024"},
		{resolution: "2K", aspectRatio: "16:9", wantSize: "2048x1152"},
		{resolution: "2K", aspectRatio: "9:16", wantSize: "1152x2048"},
		{resolution: "2K", aspectRatio: "4:3", wantSize: "2048x1536"},
		{resolution: "2K", aspectRatio: "21:9", wantSize: "2048x880"},
		{resolution: "2K", aspectRatio: "2:1", wantSize: "2048x1024"},
		{resolution: "4K", aspectRatio: "1:1", wantSize: "4096x4096"},
		{resolution: "4K", aspectRatio: "16:9", wantSize: "4096x2304"},
		{resolution: "4K", aspectRatio: "9:16", wantSize: "2304x4096"},
		{resolution: "4K", aspectRatio: "5:4", wantSize: "4096x3272"},
		{resolution: "4K", aspectRatio: "9:21", wantSize: "1752x4096"},
		{resolution: "4K", aspectRatio: "1:2", wantSize: "2048x4096"},
		{resolution: "auto", aspectRatio: "1:1", wantSize: "auto"},
		{resolution: "1K", aspectRatio: "auto", wantSize: "auto"},
		{resolution: "2K", aspectRatio: "auto", wantSize: "auto"},
	}
	for _, tt := range tests {
		t.Run(tt.resolution+"/"+tt.aspectRatio, func(t *testing.T) {
			got, err := MapOpenAIImageDimensions(tt.resolution, tt.aspectRatio)
			require.NoError(t, err)
			require.Equal(t, tt.wantSize, got)
		})
	}

	_, err := MapOpenAIImageDimensions("8K", "1:1")
	require.Error(t, err)
	_, err = MapOpenAIImageDimensions("1K", "7:3")
	require.Error(t, err)
}

func TestParseOpenAIImagesRequest_WideAspectRatioKeepsExplicitBillingTier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","resolution":"1K","aspect_ratio":"2:1"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	require.Equal(t, "2048x1024", parsed.Size)
	require.Equal(t, "1K", parsed.SizeTier)
	require.Equal(t, "1K", openAIImagesBillingInputSize(parsed))

	resolved := ResolveImageBillingSize(openAIImagesBillingInputSize(parsed), []string{parsed.Size})
	require.Equal(t, ImageBillingSize1K, resolved.BillingSize)
}

func TestParseOpenAIImagesRequest_ResolutionAspectRatio(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","resolution":"1K","aspect_ratio":"3:2","quality":"high"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	require.Equal(t, "1K", parsed.Resolution)
	require.Equal(t, "3:2", parsed.AspectRatio)
	require.Equal(t, "1536x1024", parsed.Size)
	require.Equal(t, "1K", parsed.SizeTier)
	require.True(t, parsed.NeedsSizeRewrite)
	require.Equal(t, "1K", openAIImagesBillingInputSize(parsed))

	rewritten, _, err := rewriteOpenAIImagesDimensions(body, "application/json", parsed)
	require.NoError(t, err)
	require.Equal(t, "1536x1024", gjson.GetBytes(rewritten, "size").String())
	require.False(t, gjson.GetBytes(rewritten, "resolution").Exists())
	require.False(t, gjson.GetBytes(rewritten, "aspect_ratio").Exists())
}

func TestParseOpenAIImagesRequest_SizeAsAspectRatio(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","resolution":"2K","size":"9:16"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	require.Equal(t, "2K", parsed.Resolution)
	require.Equal(t, "9:16", parsed.AspectRatio)
	require.Equal(t, "1152x2048", parsed.Size)
	require.Equal(t, "2K", parsed.SizeTier)
	require.True(t, parsed.NeedsSizeRewrite)

	rewritten, _, err := rewriteOpenAIImagesDimensions(body, "application/json", parsed)
	require.NoError(t, err)
	require.Equal(t, "1152x2048", gjson.GetBytes(rewritten, "size").String())
	require.False(t, gjson.GetBytes(rewritten, "resolution").Exists())
	require.False(t, gjson.GetBytes(rewritten, "aspect_ratio").Exists())
}

func TestParseOpenAIImagesRequest_AspectRatioWinsOverSizeRatio(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","resolution":"1K","aspect_ratio":"1:1","size":"9:16"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	require.Equal(t, "1:1", parsed.AspectRatio)
	require.Equal(t, "1024x1024", parsed.Size)
}

func TestParseOpenAIImagesRequest_AspectRatioAutoMapsToSizeAuto(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","resolution":"2K","aspect_ratio":"auto"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	require.Equal(t, "2K", parsed.Resolution)
	require.Equal(t, "auto", parsed.AspectRatio)
	require.Equal(t, "auto", parsed.Size)
	require.Equal(t, "2K", parsed.SizeTier)
	require.True(t, parsed.NeedsSizeRewrite)

	rewritten, _, err := rewriteOpenAIImagesDimensions(body, "application/json", parsed)
	require.NoError(t, err)
	require.Equal(t, "auto", gjson.GetBytes(rewritten, "size").String())
	require.False(t, gjson.GetBytes(rewritten, "resolution").Exists())
	require.False(t, gjson.GetBytes(rewritten, "aspect_ratio").Exists())
}

func TestParseOpenAIImagesRequest_LegacySizeStillWorks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","size":"1024x1024"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	require.Equal(t, "1024x1024", parsed.Size)
	require.Equal(t, "1K", parsed.SizeTier)
	require.False(t, parsed.NeedsSizeRewrite)
}

func TestParseOpenAIImagesRequest_ConcreteSizeIsCanonicalAndControlsBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	for _, rawSize := range []string{"1080X1350", "1080*1350", "1080\\u00d71350"} {
		t.Run(rawSize, func(t *testing.T) {
			body := []byte(`{"model":"gpt-image-2","prompt":"edit this image","resolution":"2K","size":"` + rawSize + `"}`)
			req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = req

			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			require.Equal(t, "1080x1350", parsed.Size)
			require.Equal(t, "2K", parsed.SizeTier)
			require.Equal(t, "1080x1350", openAIImagesBillingInputSize(parsed))
			require.True(t, parsed.NeedsSizeRewrite)

			rewritten, _, err := rewriteOpenAIImagesDimensions(body, "application/json", parsed)
			require.NoError(t, err)
			require.Equal(t, "1080x1350", gjson.GetBytes(rewritten, "size").String())
		})
	}

	matching := &OpenAIImagesRequest{Size: "4096x4096", Resolution: "4K"}
	require.NoError(t, normalizeOpenAIImagesDimensions(matching))
	require.Equal(t, "4K", matching.SizeTier)
	require.Equal(t, "4096x4096", openAIImagesBillingInputSize(matching))

	conflicting := &OpenAIImagesRequest{Size: "4096x4096", Resolution: "1K"}
	err := normalizeOpenAIImagesDimensions(conflicting)
	require.ErrorContains(t, err, "conflicts with resolution")
}

func TestImageWorkbenchCapabilitiesOpenAIExposesResolutionAspect(t *testing.T) {
	key := imageWorkbenchTestKey(PlatformOpenAI)
	key.Group.AllowAsyncImageGeneration = true
	svc := NewImageWorkbenchService(
		imageWorkbenchAPIKeyReaderStub{key: key},
		imageWorkbenchModelCatalogStub{models: []string{"gpt-image-2"}},
	)

	got, err := svc.GetCapabilities(context.Background(), 7, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"1K", "2K", "4K"}, got.ImageSizes)
	require.Equal(t, []string{
		"auto", "1:1", "2:3", "3:2", "4:5", "5:4", "4:3", "3:4",
		"16:9", "9:16", "21:9", "9:21", "2:1", "1:2",
	}, got.AspectRatios)
}
