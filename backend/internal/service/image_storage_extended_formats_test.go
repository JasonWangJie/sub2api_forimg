package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func legacyGeneratedGIF(t *testing.T) []byte {
	t.Helper()
	palette := color.Palette{color.Black, color.White}
	first := image.NewPaletted(image.Rect(0, 0, 3, 2), palette)
	second := image.NewPaletted(image.Rect(0, 0, 3, 2), palette)
	second.SetColorIndex(1, 1, 1)
	var encoded bytes.Buffer
	require.NoError(t, gif.EncodeAll(&encoded, &gif.GIF{
		Image: []*image.Paletted{first, second}, Delay: []int{20, 30}, LoopCount: 0,
	}))
	return encoded.Bytes()
}

func TestImageResultUploaderExtendedFormats(t *testing.T) {
	for _, fixture := range []struct {
		name, mime, extension string
		data                  []byte
	}{
		{"animated GIF", "image/gif", ".gif", legacyGeneratedGIF(t)},
		{"SVG", "image/svg+xml", ".svg", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="3" height="2"><rect width="3" height="2" fill="red"/></svg>`)},
	} {
		for _, source := range []string{"b64_json", "data URI", "URL"} {
			t.Run(fixture.name+"/"+source, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "image/png") // Unreliable upstream declaration.
					_, _ = w.Write(fixture.data)
				}))
				defer server.Close()
				item := map[string]string{"revised_prompt": "retained"}
				switch source {
				case "b64_json":
					item["b64_json"] = base64.StdEncoding.EncodeToString(fixture.data)
				case "data URI":
					item["url"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(fixture.data)
				case "URL":
					item["url"] = server.URL + "/image"
				}
				payload, err := json.Marshal(map[string]any{"data": []any{item}})
				require.NoError(t, err)
				storage := &fakeImageStorage{}
				uploader := NewImageResultUploader(storage, "images/", 1<<20, server.Client())
				result, err := uploader.Rewrite(context.Background(), "extended", payload)
				require.NoError(t, err)
				require.Len(t, storage.saved, 1)
				require.Equal(t, fixture.data, storage.saved[0].data, "all animation frames and SVG bytes must be preserved")
				require.Equal(t, fixture.mime, storage.saved[0].contentType)
				require.Equal(t, "images/extended-0"+fixture.extension, storage.saved[0].key)
				require.NotContains(t, string(result), "b64_json")
				require.Contains(t, string(result), "retained")
				if fixture.mime == "image/gif" {
					animation, err := gif.DecodeAll(bytes.NewReader(storage.saved[0].data))
					require.NoError(t, err)
					require.Len(t, animation.Image, 2)
					require.Equal(t, []int{20, 30}, animation.Delay)
				}
			})
		}
	}
}

func TestImageResultUploaderRejectsInvalidExtendedFormatsBeforeSave(t *testing.T) {
	gifBytes := legacyGeneratedGIF(t)
	for _, test := range []struct {
		name  string
		data  []byte
		limit int64
	}{
		{"active SVG", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="3" height="2"><script>alert(1)</script></svg>`), 1 << 20},
		{"external SVG", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="3" height="2"><image href="https://example.org/tracker"/></svg>`), 1 << 20},
		{"truncated GIF", gifBytes[:len(gifBytes)-1], 1 << 20},
		{"GIF trailing HTML", append(append([]byte(nil), gifBytes...), []byte("<script>alert(1)</script>")...), 1 << 20},
		{"oversized GIF Base64", gifBytes, int64(len(gifBytes) - 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			storage := &fakeImageStorage{}
			uploader := NewImageResultUploader(storage, "images/", test.limit, nil)
			payload, err := json.Marshal(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(test.data)}}})
			require.NoError(t, err)
			_, err = uploader.Rewrite(context.Background(), "invalid", payload)
			require.Error(t, err)
			require.Empty(t, storage.saved)
		})
	}
}

func TestImageLibraryFromTaskPreservesExtendedFormats(t *testing.T) {
	for _, fixture := range []struct {
		mime string
		data []byte
	}{
		{"image/gif", legacyGeneratedGIF(t)},
		{"image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 3 2"><path d="M0 0L3 2"/></svg>`)},
	} {
		t.Run(fixture.mime, func(t *testing.T) {
			storage := &imageLibraryFlowStorage{data: fixture.data}
			ref := ObjectRef{Provider: "custom_s3", Bucket: "images", ObjectKey: "results/output" + extensionForContentType(fixture.mime), ContentType: fixture.mime}
			repo := &imageLibraryFlowRepo{
				prepareFn: func(int64, string, int) (*ImageLibraryItem, *ObjectRef, bool, error) {
					return nil, &ref, false, nil
				},
				createTaskFn: func(_ int64, _ string, _ int, validated *ObjectRef) (*ImageLibraryItem, bool, error) {
					require.Equal(t, fixture.mime, validated.ContentType)
					require.Equal(t, int64(len(fixture.data)), validated.SizeBytes)
					require.Equal(t, 3, validated.Width)
					require.Equal(t, 2, validated.Height)
					require.NotEmpty(t, validated.ChecksumSHA256)
					return &ImageLibraryItem{AssetID: "img_extended"}, false, nil
				},
				quarantineFn: func(int64, string, int, ObjectRef, string) error {
					t.Fatal("valid generated output must not be quarantined")
					return nil
				},
			}
			item, reused, err := imageLibraryFlowService(repo, storage).FromTask(context.Background(), 42, "asyncimg_task", 0, "")
			require.NoError(t, err)
			require.False(t, reused)
			require.Equal(t, "img_extended", item.AssetID)
			require.Equal(t, 1, storage.readCalls)
			require.Zero(t, storage.saveCalls, "archiving reuses the original object and keeps all bytes")
		})
	}
}
