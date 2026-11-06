package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/jpeg"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type officialImageMultipartFile struct {
	field, filename, mime string
	data                  []byte
}

func officialImageMultipartFixtures(t *testing.T) []officialImageMultipartFile {
	t.Helper()
	var jpg bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 2, 3)), nil))
	require.Greater(t, jpg.Len(), 512, "exercise file bytes beyond the MIME sniff prefix")
	webp, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	require.NoError(t, err)
	return []officialImageMultipartFile{
		{"image", `source-"one".bin`, "image/png", mustDecodeAsyncImagePNG(t)},
		{"image", "source-two.png", "image/jpeg", jpg.Bytes()},
		{"image", "source-three.jpg", "image/webp", webp},
	}
}

func officialImageMultipartBody(t *testing.T, files []officialImageMultipartFile) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, field := range [][2]string{{"model", "gpt-image-2.5-flare"}, {"prompt", "preserve all reference details"}, {"size", "3840x2160"}, {"quality", "high"}, {"n", "1"}, {"metadata", "first"}, {"metadata", "second"}} {
		require.NoError(t, w.WriteField(field[0], field[1]))
	}
	for _, file := range files {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": file.field, "filename": file.filename}))
		header.Set("Content-Type", "application/octet-stream")
		header.Set("X-Reference-Metadata", "preserve-me")
		part, err := w.CreatePart(header)
		require.NoError(t, err)
		_, err = part.Write(file.data)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return body.Bytes(), w.FormDataContentType()
}

func assertOfficialImageMultipart(t *testing.T, body []byte, contentType string, files []officialImageMultipartFile) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	count := 0
	for _, file := range files {
		if file.field == "image" || file.field == "image[]" {
			count++
		}
	}
	var fields [][2]string
	index := 0
	for {
		part, err := r.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		data, err := io.ReadAll(part)
		require.NoError(t, err)
		if part.FileName() == "" {
			fields = append(fields, [2]string{part.FormName(), string(data)})
			continue
		}
		require.Less(t, index, len(files))
		want := files[index]
		field := want.field
		if count > 1 && (field == "image" || field == "image[]") {
			field = "image[]"
		}
		require.Equal(t, field, part.FormName(), "multiple images must use the array field")
		require.Equal(t, want.filename, part.FileName())
		require.Equal(t, want.mime, part.Header.Get("Content-Type"), "MIME must describe the bytes, not the filename")
		require.Equal(t, "preserve-me", part.Header.Get("X-Reference-Metadata"))
		require.Equal(t, want.data, data)
		index++
	}
	require.Equal(t, len(files), index)
	require.Equal(t, [][2]string{{"model", "gpt-image-2.5-flare"}, {"prompt", "preserve all reference details"}, {"size", "3840x2160"}, {"quality", "high"}, {"n", "1"}, {"metadata", "first"}, {"metadata", "second"}}, fields)
}

func buildOfficialImageMultipartRequest(t *testing.T, base, endpoint string, body []byte, contentType string) (*http.Request, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	account := &Account{ID: 45, Name: "arbitrary account name", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": base, "api_key": "local-test"}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc := &OpenAIGatewayService{cfg: cfg}
	return svc.buildOpenAIImagesRequest(context.Background(), c, account, body, contentType, "local-test", endpoint)
}

func TestOpenAIImagesOfficialMultipartProtocol(t *testing.T) {
	fixtures := officialImageMultipartFixtures(t)
	mask := fixtures[0]
	mask.field, mask.filename = "mask", "mask.png"
	array := append([]officialImageMultipartFile{}, fixtures[:2]...)
	array[0].field, array[1].field = "image[]", "image[]"
	unknown := officialImageMultipartFile{"image", "not-a-png.png", "application/octet-stream", []byte("not an image")}
	attachment := fixtures[0]
	attachment.field, attachment.mime = "attachment", "application/octet-stream"
	tests := []struct {
		name, base string
		files      []officialImageMultipartFile
	}{
		{"default single png", "", fixtures[:1]},
		{"single jpeg", "https://api.openai.com", fixtures[1:2]},
		{"single webp", "https://api.openai.com/v1", fixtures[2:]},
		{"two images", "https://api.openai.com/v1/", fixtures[:2]},
		{"three images with mask", "https://api.openai.com:443", append(append([]officialImageMultipartFile{}, fixtures...), mask)},
		{"single image with mask", "https://API.OPENAI.COM", []officialImageMultipartFile{fixtures[0], mask}},
		{"already array fields", "https://api.openai.com", array},
		{"unknown image bytes are not relabeled", "https://api.openai.com", []officialImageMultipartFile{unknown}},
		{"other file fields keep headers", "https://api.openai.com", []officialImageMultipartFile{fixtures[0], attachment}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, ct := officialImageMultipartBody(t, tc.files)
			original := append([]byte(nil), body...)
			req, err := buildOfficialImageMultipartRequest(t, tc.base, openAIImagesEditsEndpoint, body, ct)
			require.NoError(t, err)
			out, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			assertOfficialImageMultipart(t, out, req.Header.Get("Content-Type"), tc.files)
			require.Equal(t, int64(len(out)), req.ContentLength)
			replayed, err := req.GetBody()
			require.NoError(t, err)
			replayBody, err := io.ReadAll(replayed)
			require.NoError(t, err)
			require.NoError(t, replayed.Close())
			require.Equal(t, out, replayBody)
			require.Equal(t, original, body, "do not mutate the worker snapshot used by another account")
		})
	}
}

func TestOpenAIImagesOfficialMultipartScope(t *testing.T) {
	files := officialImageMultipartFixtures(t)[:2]
	body, ct := officialImageMultipartBody(t, files)
	for _, base := range []string{"https://images.example.test", "https://api.openai.com.example.test", "https://sub.api.openai.com", "https://api.openai.com:8443", "http://api.openai.com", "https://example.test/api.openai.com", "https://user@api.openai.com", "https://api.openai.com@third.example", "https://api.openai.com/custom", "https://api.openai.com."} {
		t.Run(base, func(t *testing.T) {
			req, err := buildOfficialImageMultipartRequest(t, base, openAIImagesEditsEndpoint, body, ct)
			require.NoError(t, err)
			out, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Equal(t, body, out)
			require.Equal(t, ct, req.Header.Get("Content-Type"))
		})
	}
	for _, endpoint := range []string{openAIImagesGenerationsEndpoint, "/v1/responses"} {
		req, err := buildOfficialImageMultipartRequest(t, "https://api.openai.com", endpoint, body, ct)
		require.NoError(t, err)
		out, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.Equal(t, body, out)
		require.Equal(t, ct, req.Header.Get("Content-Type"))
	}
	jsonBody := []byte(`{"model":"gpt-image-2","images":[{"image_url":"https://images.example.test/ref.png"}]}`)
	req, err := buildOfficialImageMultipartRequest(t, "https://api.openai.com", openAIImagesEditsEndpoint, jsonBody, "application/json")
	require.NoError(t, err)
	out, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, jsonBody, out)
}

func TestOpenAIImagesOfficialMultipartPreservesValidUpload(t *testing.T) {
	file := officialImageMultipartFixtures(t)[0]
	file.field = "image[]"
	files := []officialImageMultipartFile{file, file}
	body, ct := officialImageMultipartBody(t, files)
	body = bytes.ReplaceAll(body, []byte("application/octet-stream"), []byte("image/png"))
	req, err := buildOfficialImageMultipartRequest(t, "https://api.openai.com", openAIImagesEditsEndpoint, body, ct)
	require.NoError(t, err)
	out, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assertOfficialImageMultipart(t, out, req.Header.Get("Content-Type"), files)
}

func TestOpenAIImagesOfficialMultipartForwardPreservesModelAndSize(t *testing.T) {
	files := officialImageMultipartFixtures(t)[:2]
	body, ct := officialImageMultipartBody(t, files)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, openAIImagesEditsEndpoint, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", ct)
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"Zml4dHVyZQ=="}]}`))}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	parsed.NeedsSizeRewrite = true
	account := &Account{ID: 45, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "local-test", "base_url": "https://api.openai.com/v1", credKeyHeaderOverrideEnabled: true, credKeyHeaderOverrides: map[string]any{"User-Agent": "custom-image-agent"}}}
	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.Equal(t, 1, result.ImageCount)
	assertOfficialImageMultipart(t, upstream.lastBody, upstream.lastReq.Header.Get("Content-Type"), files)
	require.Equal(t, "https://api.openai.com/v1/images/edits", upstream.lastReq.URL.String())
	require.Equal(t, "custom-image-agent", upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "Bearer local-test", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "3840x2160", parsed.Size)
}
