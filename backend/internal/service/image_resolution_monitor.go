package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// ImageResolutionMonitor is shared by the forwarding services of one server.
// The two background workers only fetch existing generated results, never POST
// another generation request. Forwarding does not wait for remote image GETs.
type ImageResolutionMonitor struct {
	guard      ImageResolutionGuardObserver
	downloader AsyncImageReferenceDownloader
	jobs       chan imageResolutionProbe
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	runtime    interface {
		RuntimeConfig(context.Context) (AsyncImageRuntimeConfig, error)
	}
}

func (m *ImageResolutionMonitor) SetRuntimeConfigProvider(provider interface {
	RuntimeConfig(context.Context) (AsyncImageRuntimeConfig, error)
}) {
	m.runtime = provider
}

type imageResolutionProbe struct {
	observation ImageResolutionObservation
	sources     []imageResolutionSource
}
type imageResolutionSource struct {
	output ImageResolutionOutput
	url    string
}
type imageResolutionAdmittedKey struct{}

// Admit is used immediately before the durable actual-send budget is consumed.
// Do does not check again after this admission, so a locally rejected request
// never consumes a generation attempt or gets misclassified as a network error.
func (m *ImageResolutionMonitor) Admit(req *http.Request, account *Account) (*http.Request, error) {
	if m.enabled(req.Context()) && imageResolutionRequestIntent(req) {
		e := imageResolutionExpectationForHTTPRequest(req)
		if e.RequestedModel != "" && e.SizeTier != "" {
			if cooling, err := m.guard.IsCooling(req.Context(), account.ID, e.RequestedModel, e.SizeTier); err == nil && cooling {
				return req, imageResolutionCoolingError()
			}
		}
	}
	return req.WithContext(context.WithValue(req.Context(), imageResolutionAdmittedKey{}, true)), nil
}

func isImageResolutionCooling(err error) bool {
	var failure *UpstreamFailoverError
	return errors.As(err, &failure) && bytes.Contains(failure.ResponseBody, []byte(`"image_resolution_cooling"`))
}

func NewImageResolutionMonitor(guard ImageResolutionGuardObserver) *ImageResolutionMonitor {
	ctx, cancel := context.WithCancel(context.Background())
	m := &ImageResolutionMonitor{guard: guard, jobs: make(chan imageResolutionProbe, 128), ctx: ctx, cancel: cancel,
		downloader: AsyncImageReferenceDownloader{MaxBytes: 32 << 20, MaxPixels: 40_000_000, Timeout: 30 * time.Second}}
	for i := 0; i < 2; i++ {
		m.wg.Add(1)
		go m.worker()
	}
	return m
}

func (m *ImageResolutionMonitor) Close() {
	if m != nil {
		m.cancel()
		m.wg.Wait()
	}
}

func (m *ImageResolutionMonitor) enabled(ctx context.Context) bool {
	if m == nil || m.guard == nil {
		return false
	}
	if enabled, ok := m.guard.(interface{ Enabled(context.Context) bool }); ok {
		return enabled.Enabled(ctx)
	}
	return true
}

func (m *ImageResolutionMonitor) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case job := <-m.jobs:
			ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
			downloader := m.downloader
			if m.runtime != nil {
				if cfg, err := m.runtime.RuntimeConfig(ctx); err == nil {
					if cfg.DownloadMaxBytes > 0 {
						downloader.MaxBytes = cfg.DownloadMaxBytes
					}
					if cfg.DownloadMaxPixels > 0 {
						downloader.MaxPixels = cfg.DownloadMaxPixels
					}
					downloader.MaxRedirects = cfg.DownloadMaxRedirects
				}
			}
			for _, source := range job.sources {
				output := source.output
				if source.url != "" {
					if image, err := downloader.DownloadGenerated(ctx, source.url); err == nil {
						output = ImageResolutionOutput{Width: image.Width, Height: image.Height}
					}
				}
				job.observation.Outputs = append(job.observation.Outputs, output)
			}
			cancel()
			persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(m.ctx), 2*time.Second)
			if err := m.guard.Observe(persistCtx, job.observation); err != nil {
				slog.Warn("image_resolution.observe_failed", "account_id", job.observation.AccountID, "error", err)
			}
			persistCancel()
		}
	}
}

// Do applies the same quality gate at the actual send boundary, including
// retries and sticky-account paths that bypass candidate sorting.
func (m *ImageResolutionMonitor) Do(upstream HTTPUpstream, req *http.Request, proxy string, account *Account) (*http.Response, error) {
	if !m.enabled(req.Context()) || !imageResolutionRequestIntent(req) {
		return upstream.Do(req, proxy, account.ID, account.Concurrency)
	}
	expectation := imageResolutionExpectationForHTTPRequest(req)
	if admitted, _ := req.Context().Value(imageResolutionAdmittedKey{}).(bool); !admitted && expectation.RequestedModel != "" && expectation.SizeTier != "" {
		cooling, err := m.guard.IsCooling(req.Context(), account.ID, expectation.RequestedModel, expectation.SizeTier)
		if err == nil && cooling {
			return nil, imageResolutionCoolingError()
		}
	}
	started := time.Now().UTC()
	expectation.AttemptStartedAt = started
	resp, err := upstream.Do(req, proxy, account.ID, account.Concurrency)
	if err != nil || resp == nil || resp.StatusCode >= 400 {
		return resp, err
	}
	setImageResolutionCapture(req.Context(), expectation)
	// The durable worker measures the validated, persisted bytes and reports
	// once with the task ID, including result-download recovery after a restart.
	if AsyncImageAccountAttemptCaptureFromContext(req.Context()) != nil || IsGeminiAsyncImageGeneration(req.Context()) {
		return resp, nil
	}
	if expectation.ExpectedPixels <= 0 || resp.Body == nil {
		slog.Debug("image_resolution.skipped", "reason", "no_verifiable_target", "account_id", account.ID, "model", expectation.RequestedModel, "tier", expectation.SizeTier)
		return resp, nil
	}
	observation := ImageResolutionObservation{ID: uuid.NewString(), AccountID: account.ID, AccountName: account.Name, Platform: account.Platform,
		RequestID: resp.Header.Get("x-request-id"), Expectation: expectation, StartedAt: started}
	if observation.RequestID == "" {
		observation.RequestID, _ = req.Context().Value(ctxkey.ClientRequestID).(string)
	}
	if observation.RequestID == "" {
		observation.RequestID, _ = req.Context().Value(ctxkey.RequestID).(string)
	}
	if observation.RequestID == "" {
		observation.RequestID = observation.ID
	}
	resp.Body = &imageResolutionResponseBody{ReadCloser: resp.Body, monitor: m, observation: observation,
		stream: strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream"), outputs: make(map[string]imageResolutionSource)}
	return resp, nil
}

// BeginWebSocketTurn is called for each request, not for the lifetime of a
// reusable websocket. Neither image data nor model/size state crosses turns.
func (m *ImageResolutionMonitor) beginWebSocketTurn(ctx context.Context, account *Account, payload []byte, requestedModel, upstreamModel, tier string) (*imageResolutionResponseBody, error) {
	if !m.enabled(ctx) {
		return nil, nil
	}
	e := ResolveImageResolutionExpectation(payload, "application/json", requestedModel, upstreamModel, tier)
	e.AttemptStartedAt = time.Now().UTC()
	if e.RequestedModel != "" && e.SizeTier != "" {
		if cooling, err := m.guard.IsCooling(ctx, account.ID, e.RequestedModel, e.SizeTier); err == nil && cooling {
			return nil, imageResolutionCoolingError()
		}
	}
	if e.ExpectedPixels <= 0 {
		slog.Debug("image_resolution.skipped", "reason", "no_verifiable_target", "account_id", account.ID, "model", e.RequestedModel, "tier", e.SizeTier)
		return nil, nil
	}
	return &imageResolutionResponseBody{monitor: m, observation: ImageResolutionObservation{ID: uuid.NewString(), AccountID: account.ID, AccountName: account.Name, Platform: account.Platform, Expectation: e, StartedAt: time.Now().UTC()}, outputs: make(map[string]imageResolutionSource)}, nil
}

func (r *imageResolutionResponseBody) websocketMessage(payload []byte) {
	if r != nil {
		r.parseJSON(payload)
	}
}
func (r *imageResolutionResponseBody) finishWebSocketTurn(requestID string) {
	if r != nil {
		r.complete = true
		r.observation.RequestID = requestID
		if r.observation.RequestID == "" {
			r.observation.RequestID = r.observation.ID
		}
		_ = r.Close()
	}
}

func imageResolutionCoolingError() error {
	return &UpstreamFailoverError{StatusCode: 503, Scope: GatewayFailureScopeRequest, RequestScopedTransient: true,
		NextAccountAction: NextAccountRetry, ResponseBody: []byte(`{"error":{"code":"image_resolution_cooling","message":"Image channel resolution is cooling down"}}`)}
}

type imageResolutionResponseBody struct {
	io.ReadCloser
	monitor      *ImageResolutionMonitor
	observation  ImageResolutionObservation
	stream       bool
	buffer       []byte
	outputs      map[string]imageResolutionSource
	order        []string
	complete     bool
	overflow     bool
	failed       bool
	once         sync.Once
	sse          openAISSEDataAccumulator
	eventBytes   int
	payloadLimit int
}

const imageResolutionPayloadLimit = 64 << 20

func (r *imageResolutionResponseBody) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 && !r.overflow {
		if len(r.buffer)+n > r.limit() {
			r.overflow = true
			r.buffer = nil
		} else {
			r.buffer = append(r.buffer, p[:n]...)
			if r.stream {
				for {
					i := bytes.IndexByte(r.buffer, '\n')
					if i < 0 {
						break
					}
					r.parseSSELine(r.buffer[:i])
					if r.overflow {
						break
					}
					r.buffer = r.buffer[i+1:]
				}
			}
		}
	}
	if err == io.EOF {
		r.complete = true
		if r.stream {
			r.parseSSELine(r.buffer)
			r.sse.Flush(r.parseJSON)
		} else {
			r.parseJSON(r.buffer)
		}
		r.buffer = nil
	}
	return n, err
}

func (r *imageResolutionResponseBody) Close() error {
	var err error
	if r.ReadCloser != nil {
		err = r.ReadCloser.Close()
	}
	r.once.Do(func() {
		if r.stream {
			r.parseSSELine(r.buffer)
			r.sse.Flush(r.parseJSON)
			r.buffer = nil
		}
		if !r.complete || r.failed || r.overflow || len(r.outputs) == 0 {
			return
		}
		job := imageResolutionProbe{observation: r.observation}
		remote := false
		for _, key := range r.order {
			s := r.outputs[key]
			job.sources = append(job.sources, s)
			remote = remote || s.url != ""
		}
		if remote {
			select {
			case r.monitor.jobs <- job:
			case <-r.monitor.ctx.Done():
			default:
				slog.Warn("image_resolution.probe_queue_full", "account_id", r.observation.AccountID)
			}
			return
		}
		for _, source := range job.sources {
			job.observation.Outputs = append(job.observation.Outputs, source.output)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := r.monitor.guard.Observe(ctx, job.observation); err != nil {
			slog.Warn("image_resolution.observe_failed", "account_id", r.observation.AccountID, "error", err)
		}
	})
	return err
}

func (r *imageResolutionResponseBody) parseSSELine(line []byte) {
	if r.overflow {
		return
	}
	if _, ok := extractOpenAISSEDataLine(string(line)); ok {
		r.eventBytes += len(line) + 1
		if r.eventBytes > r.limit() {
			r.overflow = true
			r.buffer = nil
			r.sse.lines = nil
			r.outputs = nil
			r.order = nil
			return
		}
	}
	r.sse.AddLine(string(line), r.parseJSON)
	if len(bytes.TrimSpace(line)) == 0 {
		r.eventBytes = 0
	}
}

func (r *imageResolutionResponseBody) limit() int {
	if r.payloadLimit > 0 {
		return r.payloadLimit
	}
	return imageResolutionPayloadLimit
}

func imageResolutionRequestIntent(req *http.Request) bool {
	ctx := req.Context()
	if ctx.Value(imageResolutionRequestKey{}) != nil || OpenAIImageGenerationIntentFromContext(ctx) || GeminiImageGenerationIntentFromContext(ctx) || AsyncImageAccountAttemptCaptureFromContext(ctx) != nil || IsGeminiAsyncImageGeneration(ctx) {
		return true
	}
	if _, ok := ImageAccountPoolRouteFromContext(ctx); ok {
		return true
	}
	return strings.Contains(req.URL.Path, "/images/") || knownGeminiResolutionModel(imageResolutionModelFromURL(req))
}

func (r *imageResolutionResponseBody) add(key, encoded, url string) {
	if len(r.outputs) >= 16 {
		return
	}
	if key == "" {
		key = hashOpenAIImageOutputResult(encoded + url)
	}
	if len(key) > 256 {
		key = hashOpenAIImageOutputResult(key)
	}
	if strings.HasPrefix(url, "data:") {
		encoded = url
		url = ""
	} else if len(url) > 64<<10 {
		url = ""
	}
	if _, ok := r.outputs[key]; !ok {
		r.order = append(r.order, strings.Clone(key))
	}
	source := imageResolutionSource{url: strings.Clone(url)}
	if encoded != "" {
		source.url = ""
		source.output = measureImageResolutionBase64(encoded)
	}
	r.outputs[strings.Clone(key)] = source
}

func (r *imageResolutionResponseBody) parseJSON(body []byte) {
	if !gjson.ValidBytes(body) {
		return
	}
	root := gjson.ParseBytes(body)
	if strings.Contains(root.Get("type").String(), "partial_image") {
		return
	}
	if kind := root.Get("type").String(); kind == "response.failed" || kind == "response.incomplete" || kind == "error" {
		r.failed = true
		return
	}
	if status := root.Get("status").String(); status == "failed" || status == "incomplete" {
		r.failed = true
		return
	}
	switch root.Get("type").String() {
	case "response.completed", "response.done", "image_generation.completed":
		r.complete = true
	}
	root.Get("data").ForEach(func(index, item gjson.Result) bool {
		if b, u := item.Get("b64_json").String(), item.Get("url").String(); b != "" || u != "" {
			r.add("data:"+index.String(), b, u)
		}
		return true
	})
	addItem := func(item gjson.Result) {
		kind := item.Get("type").String()
		if kind != "image_generation_call" && kind != "image_generation.completed" {
			return
		}
		if status := item.Get("status").String(); status != "" && status != "completed" {
			return
		}
		b := item.Get("result").String()
		if b == "" {
			b = item.Get("b64_json").String()
		}
		u := item.Get("url").String()
		if strings.HasPrefix(b, "https://") || strings.HasPrefix(b, "http://") {
			u = b
			b = ""
		}
		if b != "" || u != "" {
			r.add(item.Get("id").String(), b, u)
		}
	}
	for _, path := range []string{"output", "response.output"} {
		root.Get(path).ForEach(func(_, item gjson.Result) bool { addItem(item); return true })
	}
	switch root.Get("type").String() {
	case "response.output_item.done":
		addItem(root.Get("item"))
	case "image_generation.completed":
		if root.Get("item").Exists() {
			addItem(root.Get("item"))
		} else if root.Get("output").Exists() {
			addItem(root.Get("output"))
		} else {
			addItem(root)
		}
	}
	if root.Get("response.candidates").Exists() {
		root = root.Get("response")
	}
	root.Get("candidates").ForEach(func(ci, candidate gjson.Result) bool {
		if candidate.Get("finishReason").String() == "STOP" {
			r.complete = true
		}
		candidate.Get("content.parts").ForEach(func(pi, part gjson.Result) bool {
			if part.Get("thought").Bool() {
				return true
			}
			inline := part.Get("inlineData")
			if !inline.Exists() {
				inline = part.Get("inline_data")
			}
			if data := inline.Get("data").String(); data != "" {
				r.add("gemini:"+ci.String()+":"+pi.String(), data, "")
			}
			file := part.Get("fileData")
			if !file.Exists() {
				file = part.Get("file_data")
			}
			url := file.Get("fileUri").String()
			if url == "" {
				url = file.Get("file_uri").String()
			}
			if url != "" {
				r.add("gemini:"+ci.String()+":"+pi.String(), "", url)
			}
			return true
		})
		return true
	})
}

func measureImageResolutionBase64(encoded string) ImageResolutionOutput {
	encoded = strings.TrimSpace(encoded)
	if strings.HasPrefix(encoded, "data:") {
		if index := strings.IndexByte(encoded, ','); index >= 0 {
			encoded = encoded[index+1:]
		}
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(32<<20) {
		return ImageResolutionOutput{}
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		return ImageResolutionOutput{}
	}
	if image, err := ValidateGeneratedImageBytes(data, 32<<20, 40_000_000); err == nil {
		return ImageResolutionOutput{Width: image.Width, Height: image.Height}
	}
	return ImageResolutionOutput{}
}

func imageResolutionExpectationForHTTPRequest(req *http.Request) ImageResolutionExpectation {
	if req.GetBody == nil {
		return ImageResolutionExpectation{}
	}
	reader, err := req.GetBody()
	if err != nil {
		return ImageResolutionExpectation{}
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, 128<<20))
	if err != nil {
		return ImageResolutionExpectation{}
	}
	model := ""
	tier := ""
	if route, ok := ImageAccountPoolRouteFromContext(req.Context()); ok {
		model, tier = route.Model, route.SizeTier
	}
	if original, ok := req.Context().Value(imageResolutionRequestKey{}).(ImageResolutionExpectation); ok {
		if model == "" {
			model = original.RequestedModel
		}
		if tier == "" {
			tier = original.SizeTier
		}
	}
	return ResolveImageResolutionExpectation(body, req.Header.Get("Content-Type"), model, imageResolutionModelFromURL(req), tier)
}

// ResolveImageResolutionExpectation accepts native Gemini, Images JSON or
// multipart, and Responses image tools. It never invents a target from billing
// defaults: size=auto and unsupported models without an explicit WxH skip.
func ResolveImageResolutionExpectation(body []byte, contentType, requestedModel, upstreamModel, requestedTier string) ImageResolutionExpectation {
	e := ImageResolutionExpectation{RequestedModel: requestedModel, UpstreamModel: upstreamModel, SizeTier: requestedTier, RuleVersion: 1}
	media, params, _ := mime.ParseMediaType(contentType)
	if media == "multipart/form-data" {
		values := map[string]string{}
		r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			part, err := r.NextPart()
			if err != nil {
				break
			}
			if part.FileName() == "" {
				v, _ := io.ReadAll(io.LimitReader(part, 2048))
				values[part.FormName()] = string(v)
			}
			part.Close()
		}
		body, _ = json.Marshal(values)
	}
	root := gjson.ParseBytes(body)
	if root.Get("type").String() == "response.create" && root.Get("response").IsObject() {
		root = root.Get("response")
	}
	if e.UpstreamModel == "" {
		e.UpstreamModel = root.Get("model").String()
	}
	if e.RequestedModel == "" {
		e.RequestedModel = e.UpstreamModel
	}
	if root.Get("request").IsObject() {
		root = root.Get("request")
	}
	size := root.Get("size").String()
	resolution := root.Get("resolution").String()
	ratio := root.Get("aspect_ratio").String()
	for _, tool := range root.Get("tools").Array() {
		if tool.Get("type").String() == "image_generation" {
			size = tool.Get("size").String()
			if model := tool.Get("model").String(); model != "" {
				e.UpstreamModel = model
			}
			break
		}
	}
	geminiSize := ""
	for _, path := range []string{"generationConfig.imageConfig", "generation_config.image_config", "extra_body.google.image_config"} {
		cfg := root.Get(path)
		if cfg.Exists() {
			geminiSize = cfg.Get("imageSize").String()
			if geminiSize == "" {
				geminiSize = cfg.Get("image_size").String()
			}
			if ratio == "" {
				ratio = cfg.Get("aspectRatio").String()
				if ratio == "" {
					ratio = cfg.Get("aspect_ratio").String()
				}
			}
			break
		}
	}
	if geminiSize != "" {
		e.SizeTier = strings.ToUpper(strings.TrimSpace(geminiSize))
		if !knownGeminiResolutionModel(e.UpstreamModel) {
			return cloneImageResolutionExpectation(e)
		}
		w, h, ok := geminiResolutionTarget(e.SizeTier, ratio, e.UpstreamModel)
		if ok {
			e.ExpectedWidth = w
			e.ExpectedHeight = h
			e.ExpectedPixels = int64(w) * int64(h)
		}
		return cloneImageResolutionExpectation(e)
	}
	if strings.EqualFold(size, "auto") {
		return cloneImageResolutionExpectation(e)
	}
	if w, h, ok := parseImageBillingDimensions(size); ok {
		e.ExpectedWidth = w
		e.ExpectedHeight = h
		e.ExpectedPixels = int64(w) * int64(h)
		if e.SizeTier == "" {
			e.SizeTier = NormalizeImageBillingTierOrDefault(size)
		}
		return cloneImageResolutionExpectation(e)
	}
	if resolution == "" {
		resolution = normalizeOpenAIImageResolution(size)
	}
	if resolution != "" && resolution != "auto" {
		if mapped, err := mapOpenAIImageDimensions(e.UpstreamModel, resolution, ratio); err == nil {
			if w, h, ok := parseImageBillingDimensions(mapped); ok {
				e.ExpectedWidth = w
				e.ExpectedHeight = h
				e.ExpectedPixels = int64(w) * int64(h)
				e.SizeTier = resolution
			}
		}
	}
	return cloneImageResolutionExpectation(e)
}

func knownGeminiResolutionModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndexByte(model, '/'); i >= 0 {
		model = model[i+1:]
	}
	switch model {
	case "gemini-3-pro-image", "gemini-3-pro-image-preview", "gemini-3.1-flash-image", "gemini-3.1-flash-image-preview":
		return true
	}
	return false
}

// Explicit per-tier values from Google's image-generation size table (2026-11-13).
// Do not reuse Gemini's billing short-edge classifier for quality verification.
var geminiResolutionTargets = map[string]map[string][2]int{
	"1K": {"1:1": {1024, 1024}, "2:3": {848, 1264}, "3:2": {1264, 848}, "3:4": {896, 1200}, "4:3": {1200, 896}, "4:5": {928, 1152}, "5:4": {1152, 928}, "9:16": {768, 1376}, "16:9": {1376, 768}, "21:9": {1584, 672}},
	"2K": {"1:1": {2048, 2048}, "2:3": {1696, 2528}, "3:2": {2528, 1696}, "3:4": {1792, 2400}, "4:3": {2400, 1792}, "4:5": {1856, 2304}, "5:4": {2304, 1856}, "9:16": {1536, 2752}, "16:9": {2752, 1536}, "21:9": {3168, 1344}},
	"4K": {"1:1": {4096, 4096}, "2:3": {3392, 5056}, "3:2": {5056, 3392}, "3:4": {3584, 4800}, "4:3": {4800, 3584}, "4:5": {3712, 4608}, "5:4": {4608, 3712}, "9:16": {3072, 5504}, "16:9": {5504, 3072}, "21:9": {6336, 2688}},
}

var geminiFlashResolutionExtras = map[string]map[string][2]int{
	"1K": {"1:4": {512, 2048}, "4:1": {2048, 512}, "1:8": {384, 3072}, "8:1": {3072, 384}},
	"2K": {"1:4": {1024, 4096}, "4:1": {4096, 1024}, "1:8": {768, 6144}, "8:1": {6144, 768}},
	"4K": {"1:4": {2048, 8192}, "4:1": {8192, 2048}, "1:8": {1536, 12288}, "8:1": {12288, 1536}},
}

func geminiResolutionTarget(tier, ratio, model string) (int, int, bool) {
	targets, ok := geminiResolutionTargets[tier]
	if !ok {
		return 0, 0, false
	}
	if ratio == "" || strings.EqualFold(ratio, "auto") {
		var minimum [2]int
		for _, target := range targets {
			if minimum[0] == 0 || int64(target[0])*int64(target[1]) < int64(minimum[0])*int64(minimum[1]) {
				minimum = target
			}
		}
		return minimum[0], minimum[1], true
	}
	if strings.Contains(strings.ToLower(model), "gemini-3.1-flash-image") {
		if extra, exists := geminiFlashResolutionExtras[tier][ratio]; exists {
			return extra[0], extra[1], true
		}
	}
	target, ok := targets[ratio]
	return target[0], target[1], ok
}

func imageResolutionModelFromURL(req *http.Request) string {
	path := req.URL.Path
	if i := strings.Index(path, "/models/"); i >= 0 {
		model := strings.SplitN(path[i+8:], ":", 2)[0]
		return model
	}
	return ""
}

func cloneImageResolutionExpectation(e ImageResolutionExpectation) ImageResolutionExpectation {
	e.RequestedModel = strings.Clone(e.RequestedModel)
	e.UpstreamModel = strings.Clone(e.UpstreamModel)
	e.SizeTier = strings.Clone(e.SizeTier)
	return e
}
