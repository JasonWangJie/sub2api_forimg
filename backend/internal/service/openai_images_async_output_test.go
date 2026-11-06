package service

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func parseAsyncOutputTestRequest(body []byte, contentType string) (*OpenAIImagesRequest, error) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	return (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body)
}

func TestAsyncOpenAIOutputOptionsValidation(t *testing.T) {
	for _, test := range []struct {
		name, fields, format, wantErr string
		count, duration               int
	}{
		{"default png", ``, "png", "", 0, 0},
		{"png", `,"output_format":"png"`, "png", "", 0, 0},
		{"webp", `,"output_format":"webp"`, "webp", "", 0, 0},
		{"jpeg", `,"output_format":"jpeg"`, "jpeg", "", 0, 0},
		{"svg", `,"output_format":"svg"`, "svg", "", 0, 0},
		{"gif defaults", `,"output_format":"gif"`, "gif", "", 9, 200},
		{"gif lower bounds", `,"output_format":"gif","frame_count":2,"frame_duration_ms":20`, "gif", "", 2, 20},
		{"gif upper bounds", `,"output_format":"gif","frame_count":16,"frame_duration_ms":2000`, "gif", "", 16, 2000},
		{"gif partial defaults", `,"output_format":"gif","frame_count":15`, "gif", "", 15, 200},
		{"format number", `,"output_format":123`, "", "output_format", 0, 0},
		{"format null", `,"output_format":null`, "", "output_format", 0, 0},
		{"unknown format", `,"output_format":"bmp"`, "", "output_format", 0, 0},
		{"count low", `,"output_format":"gif","frame_count":1`, "", "frame_count", 0, 0},
		{"count high", `,"output_format":"gif","frame_count":17`, "", "frame_count", 0, 0},
		{"count zero", `,"output_format":"gif","frame_count":0`, "", "frame_count", 0, 0},
		{"count fractional", `,"output_format":"gif","frame_count":9.5`, "", "integer", 0, 0},
		{"count decimal", `,"output_format":"gif","frame_count":9.0`, "", "integer", 0, 0},
		{"count exponent", `,"output_format":"gif","frame_count":9e0`, "", "integer", 0, 0},
		{"count string", `,"output_format":"gif","frame_count":"9"`, "", "integer", 0, 0},
		{"count null", `,"output_format":"gif","frame_count":null`, "", "integer", 0, 0},
		{"count overflow", `,"output_format":"gif","frame_count":999999999999999999999999`, "", "integer", 0, 0},
		{"duration low", `,"output_format":"gif","frame_duration_ms":19`, "", "frame_duration_ms", 0, 0},
		{"duration high", `,"output_format":"gif","frame_duration_ms":2001`, "", "frame_duration_ms", 0, 0},
		{"duration boolean", `,"output_format":"gif","frame_duration_ms":true`, "", "integer", 0, 0},
		{"non gif count", `,"output_format":"svg","frame_count":9`, "", "require output_format=gif", 0, 0},
		{"omitted format duration", `,"frame_duration_ms":200`, "", "require output_format=gif", 0, 0},
	} {
		for _, model := range []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
			t.Run(model+"/"+test.name, func(t *testing.T) {
				original := []byte(`{"model":"` + model + `","prompt":"draw a cat"` + test.fields + `}`)
				parsed, err := parseAsyncOutputTestRequest(original, "application/json")
				var normalized []byte
				if err == nil {
					normalized, _, err = parsed.NormalizeAsyncOutputOptions()
				}
				if test.wantErr != "" {
					require.ErrorContains(t, err, test.wantErr)
					return
				}
				require.NoError(t, err)
				require.Equal(t, test.format, gjson.GetBytes(normalized, "output_format").String())
				if test.format == "gif" {
					require.EqualValues(t, test.count, gjson.GetBytes(normalized, "frame_count").Int())
					require.EqualValues(t, test.duration, gjson.GetBytes(normalized, "frame_duration_ms").Int())
				} else {
					require.False(t, gjson.GetBytes(normalized, "frame_count").Exists())
					require.False(t, gjson.GetBytes(normalized, "frame_duration_ms").Exists())
				}
			})
		}
	}
	for _, fields := range []string{``, `,"output_format":"jpeg"`, `,"output_format":"vendor-format"`} {
		body := []byte(`{"model":"gpt-image-2.5-flare","prompt":"cat"` + fields + `}`)
		parsed, err := parseAsyncOutputTestRequest(body, "application/json")
		require.NoError(t, err)
		normalized, _, err := parsed.NormalizeAsyncOutputOptions()
		require.NoError(t, err)
		require.Equal(t, body, normalized, "older static models retain the exact request")
	}
	for _, fields := range []string{`,"output_format":"gif"`, `,"output_format":"svg"`, `,"frame_count":9`} {
		parsed, err := parseAsyncOutputTestRequest([]byte(`{"model":"gpt-image-2.5-flare","prompt":"cat"`+fields+`}`), "application/json")
		require.NoError(t, err)
		_, _, err = parsed.NormalizeAsyncOutputOptions()
		require.ErrorContains(t, err, "require gpt-image-2.6")
	}
}

func TestAsyncOpenAIOutputOptionsForwarding(t *testing.T) {
	for _, multipartInput := range []bool{false, true} {
		for _, format := range []string{"gif", "svg"} {
			t.Run(format+map[bool]string{false: "/json", true: "/multipart"}[multipartInput], func(t *testing.T) {
				body := []byte(`{"model":"gpt-image-2.6-flare","prompt":"animate the cat","output_format":"` + format + `"}`)
				if format == "svg" {
					body = []byte(`{"model":"gpt-image-2.6-flare","prompt":"a vector cat","output_format":"svg","svg":{"model":"test-svg-author","reasoning":{"effort":"high"}}}`)
				}
				contentType := "application/json"
				png := mustDecodeAsyncImagePNG(t)
				if multipartInput {
					var buffer bytes.Buffer
					writer := multipart.NewWriter(&buffer)
					for key, value := range map[string]string{"model": "gpt-image-2.6-flare", "prompt": "animate the cat", "output_format": format} {
						require.NoError(t, writer.WriteField(key, value))
					}
					if format == "svg" {
						require.NoError(t, writer.WriteField("svg[model]", "test-svg-author"))
						require.NoError(t, writer.WriteField("svg[reasoning][effort]", "high"))
					}
					file, err := writer.CreateFormFile("image", "source.png")
					require.NoError(t, err)
					_, err = file.Write(png)
					require.NoError(t, err)
					require.NoError(t, writer.Close())
					body, contentType = buffer.Bytes(), writer.FormDataContentType()
				}
				parsed, err := parseAsyncOutputTestRequest(body, contentType)
				require.NoError(t, err)
				body, contentType, err = parsed.NormalizeAsyncOutputOptions()
				require.NoError(t, err)
				parsed, err = parseAsyncOutputTestRequest(body, contentType)
				require.NoError(t, err)
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"Zml4dHVyZQ=="}]}`))}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, parsed.Endpoint, bytes.NewReader(body))
				c.Request.Header.Set("Content-Type", contentType)
				account := &Account{ID: 45, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "local-test", "base_url": "https://api.openai.com"}}
				_, err = svc.ForwardImages(context.Background(), c, account, body, parsed, "")
				require.NoError(t, err)
				actual, err := parseAsyncOutputTestRequest(upstream.lastBody, upstream.lastReq.Header.Get("Content-Type"))
				require.NoError(t, err)
				require.Equal(t, format, actual.OutputFormat)
				if multipartInput {
					require.Len(t, actual.Uploads, 1)
					require.Equal(t, png, actual.Uploads[0].Data)
					require.Equal(t, "image/png", actual.Uploads[0].ContentType)
				}
				responses, err := buildOpenAIImagesResponsesRequest(actual, actual.Model)
				require.NoError(t, err)
				require.Equal(t, format, gjson.GetBytes(responses, "tools.0.output_format").String())
				if format == "gif" {
					require.Equal(t, 9, *actual.FrameCount)
					require.Equal(t, 200, *actual.FrameDurationMS)
					require.EqualValues(t, 9, gjson.GetBytes(responses, "tools.0.frame_count").Int())
					require.EqualValues(t, 200, gjson.GetBytes(responses, "tools.0.frame_duration_ms").Int())
				} else {
					require.Nil(t, actual.FrameCount)
					require.Nil(t, actual.FrameDurationMS)
					require.False(t, gjson.GetBytes(responses, "tools.0.frame_count").Exists())
					require.NotNil(t, actual.SVG)
					require.Equal(t, "test-svg-author", actual.SVG.Model)
					require.Equal(t, "high", actual.SVG.Reasoning.Effort)
					require.Equal(t, "test-svg-author", gjson.GetBytes(responses, "tools.0.svg.model").String())
					require.Equal(t, "high", gjson.GetBytes(responses, "tools.0.svg.reasoning.effort").String())
				}
			})
		}
	}
	require.Equal(t, "image/gif", openAIImageOutputMIMEType("gif"))
	require.Equal(t, "image/svg+xml", openAIImageOutputMIMEType("svg"))
}

func TestAsyncOpenAIOutputOptionsMultipartValidation(t *testing.T) {
	for _, test := range []struct{ field, value, wantErr string }{
		{"frame_count", "9.0", "integer"},
		{"frame_count", "9e0", "integer"},
		{"frame_count", "", "integer"},
		{"frame_count", "99999999999999999999999", "integer"},
		{"frame_count", "0", "between 2 and 16"},
		{"frame_count", "-2", "between 2 and 16"},
		{"frame_duration_ms", "199.5", "integer"},
		{"frame_duration_ms", "0", "between 20 and 2000"},
		{"frame_duration_ms", "2001", "between 20 and 2000"},
	} {
		t.Run(test.field+"="+test.value, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			require.NoError(t, writer.WriteField("model", "gpt-image-2.6-flare"))
			require.NoError(t, writer.WriteField("prompt", "animate a cat"))
			require.NoError(t, writer.WriteField("output_format", "gif"))
			require.NoError(t, writer.WriteField(test.field, test.value))
			require.NoError(t, writer.Close())
			parsed, err := parseAsyncOutputTestRequest(body.Bytes(), writer.FormDataContentType())
			if err == nil {
				_, _, err = parsed.NormalizeAsyncOutputOptions()
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}
