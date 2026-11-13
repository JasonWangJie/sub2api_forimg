//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type resolutionMonitorGuardStub struct {
	mu           sync.Mutex
	on           bool
	cooling      bool
	observations []ImageResolutionObservation
	keys         []string
	notified     chan struct{}
}

func (g *resolutionMonitorGuardStub) Enabled(context.Context) bool { return g.on }
func (g *resolutionMonitorGuardStub) IsCooling(_ context.Context, id int64, model, tier string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.keys = append(g.keys, model+":"+tier)
	return g.cooling, nil
}
func (g *resolutionMonitorGuardStub) Observe(ctx context.Context, o ImageResolutionObservation) error {
	g.mu.Lock()
	g.observations = append(g.observations, o)
	g.mu.Unlock()
	if g.notified != nil {
		select {
		case g.notified <- struct{}{}:
		default:
		}
	}
	return ctx.Err()
}

type resolutionMonitorUpstreamStub struct {
	response    string
	contentType string
	calls       int
}

func (u *resolutionMonitorUpstreamStub) Do(*http.Request, string, int64, int) (*http.Response, error) {
	u.calls++
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{u.contentType}}, Body: io.NopCloser(strings.NewReader(u.response))}, nil
}
func (u *resolutionMonitorUpstreamStub) DoWithTLS(r *http.Request, p string, id int64, c int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, p, id, c)
}

func resolutionMonitorPNG(t *testing.T, w, h int) string {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, w, h))))
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
func resolutionMonitorRequest(t *testing.T, ctx context.Context, body string) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://upstream.example/v1/images/generations", strings.NewReader(body))
	require.NoError(t, err)
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestImageResolutionExpectationUsesActualRequestAndModelPresets(t *testing.T) {
	for _, test := range []struct {
		name, body, model string
		pixels            int64
		tier              string
	}{
		{"gpt4k_square", `{"model":"gpt-image-2","resolution":"4K","aspect_ratio":"1:1"}`, "", 2880 * 2880, "4K"},
		{"gpt4k_3_2", `{"model":"gpt-image-2","resolution":"4K","aspect_ratio":"3:2"}`, "", 3504 * 2336, "4K"},
		{"explicit_pixels", `{"model":"alias","size":"2048x2048"}`, "", 2048 * 2048, "2K"},
		{"auto_not_billing_default", `{"model":"gpt-image-2","size":"auto","resolution":"4K"}`, "", 0, ""},
		{"gemini_2k_wide", `{"generationConfig":{"imageConfig":{"imageSize":"2K","aspectRatio":"16:9"}}}`, "gemini-3-pro-image-preview", 2752 * 1536, "2K"},
		{"gemini_4k_auto", `{"generationConfig":{"imageConfig":{"imageSize":"4K","aspectRatio":"auto"}}}`, "gemini-3.1-flash-image-preview", 4096 * 4096, "4K"},
		{"gemini_flash_4k_extreme_ratio", `{"generationConfig":{"imageConfig":{"imageSize":"4K","aspectRatio":"1:8"}}}`, "gemini-3.1-flash-image-preview", 1536 * 12288, "4K"},
		{"gemini_pro_unknown_ratio", `{"generationConfig":{"imageConfig":{"imageSize":"4K","aspectRatio":"1:8"}}}`, "gemini-3-pro-image-preview", 0, "4K"},
		{"unknown_gemini_model", `{"generationConfig":{"imageConfig":{"imageSize":"4K","aspectRatio":"1:1"}}}`, "gemini-3-unknown-image", 0, "4K"},
		{"response_tool", `{"model":"gpt-5","tools":[{"type":"image_generation","size":"1536x1024"}]}`, "", 1536 * 1024, "2K"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := ResolveImageResolutionExpectation([]byte(test.body), "application/json", "", test.model, "")
			require.Equal(t, test.pixels, got.ExpectedPixels)
			require.Equal(t, test.tier, got.SizeTier)
		})
	}
}

func TestImageResolutionMonitorPreservesDeliveryAndMeasuresBytes(t *testing.T) {
	g := &resolutionMonitorGuardStub{on: true}
	m := NewImageResolutionMonitor(g)
	defer m.Close()
	encoded := resolutionMonitorPNG(t, 64, 32)
	body := `{"data":[{"size":"4096x4096","b64_json":"` + encoded + `"},{"size":"4096x4096","b64_json":"invalid"}]}`
	u := &resolutionMonitorUpstreamStub{response: body, contentType: "application/json"}
	r := resolutionMonitorRequest(t, context.Background(), `{"model":"gpt-image-2","size":"2048x2048"}`)
	resp, err := m.Do(u, r, "", &Account{ID: 4, Name: "channel", Platform: PlatformOpenAI})
	require.NoError(t, err)
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, body, string(got))
	require.NoError(t, resp.Body.Close())
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 1, u.calls)
	require.Len(t, g.observations, 1)
	o := g.observations[0]
	require.Equal(t, int64(2048*2048), o.Expectation.ExpectedPixels)
	require.Equal(t, []ImageResolutionOutput{{Width: 64, Height: 32}, {}}, o.Outputs)
}

func TestImageResolutionMonitorFinalSSEWithoutEOFAndMultiline(t *testing.T) {
	g := &resolutionMonitorGuardStub{on: true}
	m := NewImageResolutionMonitor(g)
	defer m.Close()
	b64 := resolutionMonitorPNG(t, 32, 16)
	r := &imageResolutionResponseBody{monitor: m, stream: true, outputs: map[string]imageResolutionSource{}, observation: ImageResolutionObservation{ID: "stream", AccountID: 4}}
	// A valid multi-line data event, followed by a repeated completed result.
	lines := []string{`data: {"type":"response.output_item.done",`, `data: "item":{"id":"img1","type":"image_generation_call","status":"completed","result":"` + b64 + `"}}`, "",
		`data: {"type":"response.completed","response":{"output":[{"id":"img1","type":"image_generation_call","status":"completed","result":"` + b64 + `"}]}}`}
	for _, line := range lines {
		r.parseSSELine([]byte(line))
	}
	// No io.EOF and no final blank line: forwarding stops on the done event.
	require.NoError(t, r.Close())
	require.Len(t, g.observations, 1)
	require.Equal(t, []ImageResolutionOutput{{Width: 32, Height: 16}}, g.observations[0].Outputs)
}

func TestImageResolutionMonitorIgnoresPartialAndGeminiThoughtImages(t *testing.T) {
	g := &resolutionMonitorGuardStub{on: true}
	m := NewImageResolutionMonitor(g)
	defer m.Close()
	b64 := resolutionMonitorPNG(t, 8, 8)
	r := &imageResolutionResponseBody{monitor: m, outputs: map[string]imageResolutionSource{}, observation: ImageResolutionObservation{ID: "partial", AccountID: 4}}
	r.parseJSON([]byte(`{"type":"response.image_generation_call.partial_image","partial_image_b64":"` + b64 + `"}`))
	r.parseJSON([]byte(`{"candidates":[{"content":{"parts":[{"thought":true,"inlineData":{"data":"` + b64 + `","mimeType":"image/png"}}]},"finishReason":"STOP"}]}`))
	require.NoError(t, r.Close())
	require.Empty(t, g.observations)
}

func TestImageResolutionMonitorWebSocketTurnIsolationAndAdmission(t *testing.T) {
	g := &resolutionMonitorGuardStub{on: true}
	m := NewImageResolutionMonitor(g)
	defer m.Close()
	account := &Account{ID: 4}
	payload := []byte(`{"model":"mapped","tools":[{"type":"image_generation","size":"2048x2048"}]}`)
	first, err := m.beginWebSocketTurn(context.Background(), account, payload, "client-alias", "mapped", "")
	require.NoError(t, err)
	first.websocketMessage([]byte(`{"type":"response.completed","response":{"output":[{"id":"img1","type":"image_generation_call","result":"` + resolutionMonitorPNG(t, 16, 16) + `"}]}}`))
	first.finishWebSocketTurn("resp1")
	second, err := m.beginWebSocketTurn(context.Background(), account, payload, "other-alias", "mapped", "")
	require.NoError(t, err)
	second.finishWebSocketTurn("resp2")
	require.Len(t, g.observations, 1)
	require.Equal(t, "client-alias", g.observations[0].Expectation.RequestedModel)
	g.cooling = true
	_, err = m.beginWebSocketTurn(context.Background(), account, payload, "client-alias", "mapped", "")
	require.True(t, isImageResolutionCooling(err))
}

func TestImageResolutionRequestPreservesClientAliasAcrossMappings(t *testing.T) {
	original := []byte(`{"model":"client-alias","tools":[{"type":"image_generation","size":"2048x2048"}]}`)
	ctx := WithImageResolutionRequest(context.Background(), original, "application/json", "client-alias")
	forward := `{"model":"channel-mapped","tools":[{"type":"image_generation","size":"2048x2048"}]}`
	ctx = WithImageResolutionRequest(ctx, []byte(forward), "application/json", "channel-mapped")
	expectation := imageResolutionExpectationForHTTPRequest(resolutionMonitorRequest(t, ctx, forward))
	require.Equal(t, "client-alias", expectation.RequestedModel)
	require.Equal(t, "channel-mapped", expectation.UpstreamModel)
}

func TestImageResolutionMonitorUnsafeResultURLRecordsUnknown(t *testing.T) {
	g := &resolutionMonitorGuardStub{on: true, notified: make(chan struct{}, 1)}
	m := NewImageResolutionMonitor(g)
	defer m.Close()
	u := &resolutionMonitorUpstreamStub{contentType: "application/json", response: `{"data":[{"url":"http://127.0.0.1:1/private"}]}`}
	resp, err := m.Do(u, resolutionMonitorRequest(t, context.Background(), `{"model":"gpt-image-2","size":"2048x2048"}`), "", &Account{ID: 4})
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	select {
	case <-g.notified:
	case <-time.After(3 * time.Second):
		t.Fatal("missing unknown observation")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	require.Len(t, g.observations, 1)
	require.Equal(t, []ImageResolutionOutput{{}}, g.observations[0].Outputs)
}

func TestImageResolutionExpectationSnapshotRoundTrip(t *testing.T) {
	expectation := ResolveImageResolutionExpectation([]byte(`{"model":"gpt-image-2","size":"2880x2880"}`), "application/json", "requested-model", "", "4K")
	result := &OpenAIForwardResult{ImageResolutionExpectation: &expectation}
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	var recovered OpenAIForwardResult
	require.NoError(t, json.Unmarshal(encoded, &recovered))
	require.Equal(t, &expectation, recovered.ImageResolutionExpectation)
}

func TestImageResolutionMonitorBoundsMultilineEventsAndResultURLs(t *testing.T) {
	r := &imageResolutionResponseBody{payloadLimit: 32, outputs: map[string]imageResolutionSource{}}
	for i := 0; i < 20; i++ {
		r.parseSSELine([]byte("data: {\"x\":1}"))
	}
	require.True(t, r.overflow)
	require.Empty(t, r.sse.lines)
	require.Zero(t, len(r.buffer))
	r = &imageResolutionResponseBody{outputs: map[string]imageResolutionSource{}}
	r.add("long-url", "", "https://generated.example/"+strings.Repeat("a", 64<<10))
	require.Equal(t, imageResolutionSource{}, r.outputs["long-url"], "unbounded URL must become unknown and never enter the background queue")
	r.add("data-uri", "", "data:image/png;base64,"+resolutionMonitorPNG(t, 17, 19))
	require.Empty(t, r.outputs["data-uri"].url)
	require.Equal(t, ImageResolutionOutput{Width: 17, Height: 19}, r.outputs["data-uri"].output)
	r.add(strings.Repeat("untrusted-item-id", 100), "", "https://generated.example/image.png")
	require.LessOrEqual(t, len(r.order[len(r.order)-1]), 256)
}
