package service

import (
	"bytes"
	"mime/multipart"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAsyncOpenAISVGOptionsValidation(t *testing.T) {
	for _, test := range []struct{ name, raw, wantErr string }{
		{"optional null", `null`, ""},
		{"optional empty object", `{}`, ""},
		{"author only", `{"model":"test-svg-author"}`, ""},
		{"reasoning only", `{"reasoning":{"effort":"low"}}`, ""},
		{"reasoning null", `{"model":"test-svg-author","reasoning":null}`, ""},
		{"effort null", `{"model":"test-svg-author","reasoning":{"effort":null}}`, ""},
		{"object number", `4`, "svg must be an object"},
		{"object string", `"svg"`, "svg must be an object"},
		{"object array", `[]`, "svg must be an object"},
		{"model null", `{"model":null}`, "svg.model"},
		{"model number", `{"model":4}`, "svg.model"},
		{"model empty", `{"model":" "}`, "svg.model"},
		{"reasoning array", `{"reasoning":[]}`, "svg.reasoning"},
		{"reasoning number", `{"reasoning":4}`, "svg.reasoning"},
		{"effort number", `{"reasoning":{"effort":4}}`, "svg.reasoning.effort"},
		{"effort unknown", `{"reasoning":{"effort":"fast"}}`, "svg.reasoning.effort"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-image-2.6-flare","prompt":"vector cat","output_format":"svg","svg":` + test.raw + `}`)
			parsed, err := parseAsyncOutputTestRequest(body, "application/json")
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			normalized, _, err := parsed.NormalizeAsyncOutputOptions()
			require.NoError(t, err)
			require.JSONEq(t, test.raw, gjson.GetBytes(normalized, "svg").Raw)
			responses, err := buildOpenAIImagesResponsesRequest(parsed, parsed.Model)
			require.NoError(t, err)
			if parsed.SVG == nil || parsed.SVG.Model == "" {
				require.False(t, gjson.GetBytes(responses, "tools.0.svg.model").Exists(), "author must never default to the image model")
			}
		})
	}
	for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
		_, err := ParseOpenAIImagesSVGOptions([]byte(`{"reasoning":{"effort":"` + effort + `"}}`))
		require.NoError(t, err)
	}
}

func TestAsyncOpenAISVGOptionsMultipartValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		fields  [][2]string
		wantErr string
	}{
		{"valid", [][2]string{{"svg[model]", "test-svg-author"}, {"svg[reasoning][effort]", "low"}}, ""},
		{"no author default", [][2]string{{"svg[reasoning][effort]", "low"}}, ""},
		{"empty model", [][2]string{{"svg[model]", ""}}, "svg.model"},
		{"bad effort", [][2]string{{"svg[reasoning][effort]", "fast"}}, "svg.reasoning.effort"},
		{"repeated model", [][2]string{{"svg[model]", "author-a"}, {"svg[model]", "author-b"}}, "must not be repeated"},
		{"repeated effort", [][2]string{{"svg[reasoning][effort]", "low"}, {"svg[reasoning][effort]", "high"}}, "must not be repeated"},
		{"JSON form scalar", [][2]string{{"svg", `{"model":"author"}`}}, "must use svg[model]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			for _, field := range append([][2]string{{"model", "gpt-image-2.6-sunburst"}, {"prompt", "vector cat"}, {"output_format", "svg"}}, test.fields...) {
				require.NoError(t, writer.WriteField(field[0], field[1]))
			}
			require.NoError(t, writer.Close())
			parsed, err := parseAsyncOutputTestRequest(body.Bytes(), writer.FormDataContentType())
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			out, contentType, err := parsed.NormalizeAsyncOutputOptions()
			require.NoError(t, err)
			reparsed, err := parseAsyncOutputTestRequest(out, contentType)
			require.NoError(t, err)
			require.Equal(t, parsed.SVG, reparsed.SVG)
		})
	}
}
