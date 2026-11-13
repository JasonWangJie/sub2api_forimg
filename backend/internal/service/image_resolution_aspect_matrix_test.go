//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Independent fixtures transcribed from the current Google official model
// tables, checked 2026-11-13:
// https://aistudio.google.com/docs/image-generation#aspect-ratios-and-image-size
// Do not derive this fixture from the production lookup maps: it must catch a
// wrong ratio, transposed dimension or accidental reuse of billing thresholds.
var resolutionOfficialGeminiFixture = []struct {
	ratio      string
	dimensions [3][2]int
}{
	{"1:1", [3][2]int{{1024, 1024}, {2048, 2048}, {4096, 4096}}},
	{"2:3", [3][2]int{{848, 1264}, {1696, 2528}, {3392, 5056}}},
	{"3:2", [3][2]int{{1264, 848}, {2528, 1696}, {5056, 3392}}},
	{"3:4", [3][2]int{{896, 1200}, {1792, 2400}, {3584, 4800}}},
	{"4:3", [3][2]int{{1200, 896}, {2400, 1792}, {4800, 3584}}},
	{"4:5", [3][2]int{{928, 1152}, {1856, 2304}, {3712, 4608}}},
	{"5:4", [3][2]int{{1152, 928}, {2304, 1856}, {4608, 3712}}},
	{"9:16", [3][2]int{{768, 1376}, {1536, 2752}, {3072, 5504}}},
	{"16:9", [3][2]int{{1376, 768}, {2752, 1536}, {5504, 3072}}},
	{"21:9", [3][2]int{{1584, 672}, {3168, 1344}, {6336, 2688}}},
}

func requireResolutionAreaOutcomes(t *testing.T, e ImageResolutionExpectation, width, height int) {
	t.Helper()
	observation := ImageResolutionObservation{Expectation: e, Outputs: []ImageResolutionOutput{{Width: width, Height: height}}}
	require.Equal(t, "pass", imageResolutionOutcome(observation, 75), "a correctly sized non-square image must not cool the channel")
	observation.Outputs = []ImageResolutionOutput{{Width: height, Height: width}}
	require.Equal(t, "pass", imageResolutionOutcome(observation, 75), "quality protection checks area, not orientation")
	observation.Outputs = []ImageResolutionOutput{{Width: width * 98 / 100, Height: height * 98 / 100}}
	require.Equal(t, "pass", imageResolutionOutcome(observation, 75), "small alignment/cropping differences remain within tolerance")
	observation.Outputs = []ImageResolutionOutput{{Width: width / 2, Height: height / 2}}
	require.Equal(t, "mismatch", imageResolutionOutcome(observation, 75), "half-sized edges mean one-quarter of the promised pixels")
}

func TestImageResolutionGeminiOfficialAspectMatrix(t *testing.T) {
	for _, model := range []string{"gemini-3-pro-image", "gemini-3-pro-image-preview", "gemini-3.1-flash-image", "gemini-3.1-flash-image-preview"} {
		for tierIndex, tier := range []string{"1K", "2K", "4K"} {
			for _, fixture := range resolutionOfficialGeminiFixture {
				t.Run(model+"/"+tier+"/"+fixture.ratio, func(t *testing.T) {
					body, err := json.Marshal(map[string]any{"generationConfig": map[string]any{"imageConfig": map[string]any{"imageSize": tier, "aspectRatio": fixture.ratio}}})
					require.NoError(t, err)
					e := ResolveImageResolutionExpectation(body, "application/json", "client-image-alias", model, tier)
					w, h := fixture.dimensions[tierIndex][0], fixture.dimensions[tierIndex][1]
					require.Equal(t, w, e.ExpectedWidth)
					require.Equal(t, h, e.ExpectedHeight)
					require.Equal(t, int64(w)*int64(h), e.ExpectedPixels)
					require.Equal(t, "client-image-alias", e.RequestedModel)
					require.Equal(t, model, e.UpstreamModel)
					require.Equal(t, tier, e.SizeTier)
					requireResolutionAreaOutcomes(t, e, w, h)
				})
			}
		}
	}
}

func TestImageResolutionGeminiFlashExtendedAspectMatrix(t *testing.T) {
	fixtures := []struct {
		ratio      string
		dimensions [3][2]int
	}{
		{"1:4", [3][2]int{{512, 2048}, {1024, 4096}, {2048, 8192}}},
		{"4:1", [3][2]int{{2048, 512}, {4096, 1024}, {8192, 2048}}},
		{"1:8", [3][2]int{{384, 3072}, {768, 6144}, {1536, 12288}}},
		{"8:1", [3][2]int{{3072, 384}, {6144, 768}, {12288, 1536}}},
	}
	for _, model := range []string{"gemini-3.1-flash-image", "gemini-3.1-flash-image-preview"} {
		for index, tier := range []string{"1K", "2K", "4K"} {
			for _, fixture := range fixtures {
				t.Run(model+"/"+tier+"/"+fixture.ratio, func(t *testing.T) {
					body, err := json.Marshal(map[string]any{"generation_config": map[string]any{"image_config": map[string]any{"image_size": tier, "aspect_ratio": fixture.ratio}}})
					require.NoError(t, err)
					e := ResolveImageResolutionExpectation(body, "application/json", "alias", model, tier)
					w, h := fixture.dimensions[index][0], fixture.dimensions[index][1]
					require.Equal(t, w, e.ExpectedWidth)
					require.Equal(t, h, e.ExpectedHeight)
					requireResolutionAreaOutcomes(t, e, w, h)
				})
			}
		}
	}
}

func TestImageResolutionGeminiAutoDoesNotAssumeSquareActualOutput(t *testing.T) {
	for _, model := range []string{"gemini-3-pro-image-preview", "gemini-3.1-flash-image-preview"} {
		for index, tier := range []string{"1K", "2K", "4K"} {
			t.Run(model+"/"+tier, func(t *testing.T) {
				body, err := json.Marshal(map[string]any{"generationConfig": map[string]any{"imageConfig": map[string]any{"imageSize": tier}}})
				require.NoError(t, err)
				e := ResolveImageResolutionExpectation(body, "application/json", "alias", model, tier)
				minimum := int64(1 << 62)
				for _, fixture := range resolutionOfficialGeminiFixture {
					w, h := fixture.dimensions[index][0], fixture.dimensions[index][1]
					area := int64(w) * int64(h)
					if area < minimum {
						minimum = area
					}
					require.Equal(t, "pass", imageResolutionOutcome(ImageResolutionObservation{Expectation: e, Outputs: []ImageResolutionOutput{{Width: w, Height: h}}}, 75))
				}
				require.Equal(t, minimum, e.ExpectedPixels, "auto target is the smallest official area, never a guessed output aspect ratio")
			})
		}
	}
}

func TestImageResolutionOpenAIForwardedFourKAspectMatrix(t *testing.T) {
	fixtures := []struct {
		ratio         string
		width, height int
	}{
		{"1:1", 2880, 2880}, {"3:2", 3504, 2336}, {"2:3", 2336, 3504},
		{"16:9", 3840, 2160}, {"9:16", 2160, 3840}, {"5:4", 3200, 2560}, {"4:5", 2560, 3200},
		{"4:3", 3264, 2448}, {"3:4", 2448, 3264}, {"21:9", 3808, 1632}, {"9:21", 1632, 3808},
		{"2:1", 3840, 1920}, {"1:2", 1920, 3840},
	}
	for _, model := range []string{"gpt-image-2", "gpt-image-2-2026-04-21", "gpt-image-2.5-flare", "gpt-image-2.5-flare-2026-09-08", "gpt-image-2.5-sunburst", "gpt-image-2.5-sunburst-2026-09-08"} {
		for _, fixture := range fixtures {
			for _, format := range []string{"json", "multipart"} {
				t.Run(model+"/"+fixture.ratio+"/"+format, func(t *testing.T) {
					fields := map[string]string{"model": model, "prompt": "render a lighthouse", "resolution": "4K", "aspect_ratio": fixture.ratio}
					contentType := "application/json"
					body, err := json.Marshal(fields)
					require.NoError(t, err)
					if format == "multipart" {
						var buffer bytes.Buffer
						writer := multipart.NewWriter(&buffer)
						for key, value := range fields {
							require.NoError(t, writer.WriteField(key, value))
						}
						require.NoError(t, writer.Close())
						body = buffer.Bytes()
						contentType = writer.FormDataContentType()
					}
					parsed := &OpenAIImagesRequest{Model: model, Resolution: "4K", AspectRatio: fixture.ratio}
					require.NoError(t, normalizeOpenAIImagesDimensions(parsed))
					require.NoError(t, parsed.ValidateAsyncDimensions())
					// Account mapping changes the model, while the normalized concrete
					// pixels already selected for this request remain authoritative.
					forwarded, forwardedType, err := rewriteOpenAIImagesModel(body, contentType, "gpt-image-2")
					require.NoError(t, err)
					forwarded, forwardedType, err = rewriteOpenAIImagesDimensions(forwarded, forwardedType, parsed)
					require.NoError(t, err)
					ctx, err := WithImageAccountPoolRoute(context.Background(), model, "4K")
					require.NoError(t, err)
					request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://upstream.example/v1/images/generations", bytes.NewReader(forwarded))
					require.NoError(t, err)
					request.Header.Set("Content-Type", forwardedType)
					e := imageResolutionExpectationForHTTPRequest(request)
					require.Equal(t, fixture.width, e.ExpectedWidth)
					require.Equal(t, fixture.height, e.ExpectedHeight)
					require.Equal(t, model, e.RequestedModel)
					require.Equal(t, "gpt-image-2", e.UpstreamModel)
					require.Equal(t, "4K", e.SizeTier)
					requireResolutionAreaOutcomes(t, e, fixture.width, fixture.height)
				})
			}
		}
	}
}

func TestImageResolutionOpenAIExplicitForwardedSizeWinsOverMappedModelPreset(t *testing.T) {
	// The guard measures the concrete protocol sent to the channel. It must
	// not recompute a different target from an account-mapped model name.
	e := ResolveImageResolutionExpectation([]byte(`{"model":"gpt-image-2","size":"3504x2336"}`), "application/json", "legacy-client", "", "4K")
	require.Equal(t, int64(3504*2336), e.ExpectedPixels)
	require.Equal(t, "pass", imageResolutionOutcome(ImageResolutionObservation{Expectation: e, Outputs: []ImageResolutionOutput{{Width: 3504, Height: 2336}}}, 75))
	require.Equal(t, "mismatch", imageResolutionOutcome(ImageResolutionObservation{Expectation: e, Outputs: []ImageResolutionOutput{{Width: 1752, Height: 1168}}}, 75))
	e = ResolveImageResolutionExpectation([]byte(`{"model":"gpt-image-2","size":"3840x2160"}`), "application/json", "legacy-client", "", "4K")
	require.Equal(t, "pass", imageResolutionOutcome(ImageResolutionObservation{Expectation: e, Outputs: []ImageResolutionOutput{{Width: 3840, Height: 2160}}}, 75), "the normalized 16:9 four-K target is accepted")
	require.Equal(t, "mismatch", imageResolutionOutcome(ImageResolutionObservation{Expectation: e, Outputs: []ImageResolutionOutput{{Width: 1920, Height: 1080}}}, 75), "half-sized edges mean one-quarter of the requested pixels")
}
