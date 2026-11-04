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

func TestOpenAIImagesGPTImage2FourKPresets(t *testing.T) {
	for _, tt := range []struct {
		aspect string
		want   string
	}{
		{"1:1", "2880x2880"},
		{"3:2", "3504x2336"},
		{"2:3", "2336x3504"},
		{"16:9", "3840x2160"},
		{"9:16", "2160x3840"},
		{"5:4", "3200x2560"},
		{"4:5", "2560x3200"},
		{"4:3", "3264x2448"},
		{"3:4", "2448x3264"},
		{"21:9", "3808x1632"},
		{"9:21", "1632x3808"},
		{"2:1", "3840x1920"},
		{"1:2", "1920x3840"},
		{"", "2880x2880"},
	} {
		t.Run("aspect="+tt.aspect, func(t *testing.T) {
			req := &OpenAIImagesRequest{Model: "gpt-image-2", Resolution: "4K", AspectRatio: tt.aspect}
			require.NoError(t, normalizeOpenAIImagesDimensions(req))
			require.NoError(t, req.ValidateAsyncDimensions())
			require.Equal(t, tt.want, req.Size)
			require.Equal(t, ImageBillingSize4K, req.SizeTier)
			require.Equal(t, ImageBillingSize4K, OpenAIImagesBillingInputSize(req))
		})
	}
}

func TestParseOpenAIImagesRequest_ModelScopedFourKAliases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	for _, tt := range []struct {
		model string
		want  string
	}{
		{"gpt-image-2", "3504x2336"},
		{"gpt-image-2-2026-04-21", "3504x2336"},
		{"gpt-image-2.5-flare", "3504x2336"},
		{"gpt-image-2.5-flare-2026-09-08", "3504x2336"},
		{"gpt-image-2.5-sunburst", "3504x2336"},
		{"gpt-image-2.5-sunburst-2026-09-08", "3504x2336"},
		{"", "3504x2336"}, // Omitted model defaults to gpt-image-2.
		{"gpt-image-1.5", "4096x2304"},
		{"gpt-image-2-custom", "4096x2304"},
	} {
		t.Run("model="+tt.model, func(t *testing.T) {
			fields := `"prompt":"draw","size":"4K","aspect_ratio":"3:2"`
			if tt.model != "" {
				fields = `"model":"` + tt.model + `",` + fields
			}
			body := []byte(`{` + fields + `}`)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			require.NoError(t, parsed.ValidateAsyncDimensions())
			require.Equal(t, "3:2", parsed.AspectRatio)
			rewritten, _, err := rewriteOpenAIImagesDimensions(body, "application/json", parsed)
			require.NoError(t, err)
			require.Equal(t, tt.want, gjson.GetBytes(rewritten, "size").String())
			require.False(t, gjson.GetBytes(rewritten, "aspect_ratio").Exists())
		})
	}
}

func TestOpenAIImagesRequest_ValidateAsyncDimensions(t *testing.T) {
	for _, tt := range []struct {
		size       string
		wantReason string
	}{
		{size: ""},
		{size: "auto"},
		{size: "3504x2336"},
		{size: "2160x3840"},
		{size: "2880x2880"},
		{size: "1024x640"},
		{size: "3072x1024"},
		{size: "4096x2304", wantReason: "longest edge must be at most 3840 pixels"},
		{size: "3504x2335", wantReason: "both edges must be multiples of 16 pixels"},
		{size: "3840x1024", wantReason: "short edge must be at most 3:1"},
		{size: "3840x3840", wantReason: "total pixel count"},
		{size: "1024x624", wantReason: "total pixel count"},
		{size: "not-a-size", wantReason: "size must be WIDTHxHEIGHT or auto"},
	} {
		t.Run(tt.size, func(t *testing.T) {
			req := &OpenAIImagesRequest{Model: "gpt-image-2.5-sunburst", Size: tt.size}
			err := req.ValidateAsyncDimensions()
			if tt.wantReason == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, "unsupported_image_dimensions")
			require.ErrorContains(t, err, tt.wantReason)
			require.ErrorContains(t, err, "3504x2336")
		})
	}
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
