package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageInputCapabilitiesPreparedJSON(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(asyncImageWorkerOnePixelPNG)
	require.NoError(t, err)
	inline := "data:image/png;base64," + asyncImageWorkerOnePixelPNG
	for _, body := range []string{
		fmt.Sprintf(`{"image_urls":[%q,%q],"prompt":"https://example.org/not-an-input.png"}`, inline, inline),
		fmt.Sprintf(`{"images":[{"image_url":%q},{"image_url":{"url":%q}}]}`, inline, inline),
		fmt.Sprintf(`{"image":%q,"mask":%q}`, inline, inline),
		fmt.Sprintf(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":%q}},{"type":"image_url","image_url":{"url":%q}},{"type":"text","text":"data:image/png;base64,fake"}]}]}`, inline, inline),
		fmt.Sprintf(`{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png","data":%q}},{"inline_data":{"mime_type":"image/png","data":%q}},{"inlineData":{"mimeType":"audio/mp3","data":"AAAA"}}]}]}`, asyncImageWorkerOnePixelPNG, asyncImageWorkerOnePixelPNG),
	} {
		t.Run(body[:min(28, len(body))], func(t *testing.T) {
			input := asyncImageInputCapabilities([]byte(body), "application/json")
			require.Equal(t, service.ImageInputCapabilities{
				Transport: "local", Count: 2, MaxBytes: int64(len(data)), TotalBytes: 2 * int64(len(data)), MaxPixels: 1, TotalPixels: 2,
			}, input)
		})
	}
}

func TestAsyncImageInputCapabilitiesRemoteAndMixed(t *testing.T) {
	remote := []byte(`{"images":["https://127.0.0.1:1/private",{"url":"https://example.org/image"}]}`)
	input := asyncImageInputCapabilities(remote, "application/json")
	require.Equal(t, service.ImageInputCapabilities{Transport: "passthrough", Count: 2}, input,
		"remote images must remain unmeasured; inspection must never make a request")
	body := []byte(fmt.Sprintf(`{"image_urls":["https://example.org/image",%q]}`, "data:image/png;base64,"+asyncImageWorkerOnePixelPNG))
	input = asyncImageInputCapabilities(body, "application/json")
	require.Equal(t, "passthrough", input.Transport, "remaining URL still requires passthrough even after a local input")
	require.Equal(t, 2, input.Count)
	require.Equal(t, int64(1), input.TotalPixels)
	require.Positive(t, input.TotalBytes)
	require.Equal(t, service.ImageInputCapabilities{}, asyncImageInputCapabilities([]byte(`{"prompt":"data:image/png;base64,AAAA"}`), "application/json"))
}

func TestAsyncImageInputCapabilitiesMultipartAndAdmission(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(asyncImageWorkerOnePixelPNG)
	require.NoError(t, err)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("prompt", "data:image/png;base64,not-an-image"))
	for _, name := range []string{" image[] ", " mask "} {
		part, err := writer.CreateFormFile(name, "reference.png")
		require.NoError(t, err)
		_, err = part.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	input := asyncImageInputCapabilities(body.Bytes(), writer.FormDataContentType())
	require.Equal(t, service.ImageInputCapabilities{
		Transport: "local", Count: 2, MaxBytes: int64(len(data)), TotalBytes: 2 * int64(len(data)), MaxPixels: 1, TotalPixels: 2,
	}, input)
	ctx := service.WithImageInputCapabilities(context.Background(), input)
	for _, limits := range []map[string]any{
		{"max_images": 1}, {"max_image_bytes": len(data) - 1}, {"max_total_bytes": 2*len(data) - 1},
		{"max_total_pixels": 1}, {"transports": []string{"passthrough"}},
	} {
		require.False(t, service.ImageAccountSupportsInput(ctx, &service.Account{ID: 1, Extra: map[string]any{"image_input_capabilities": limits}}))
	}
	require.True(t, service.ImageAccountSupportsInput(ctx, &service.Account{ID: 2, Extra: map[string]any{
		"image_input_capabilities": map[string]any{"transports": []string{"local"}, "max_images": 2, "max_total_pixels": 2},
	}}))
}
