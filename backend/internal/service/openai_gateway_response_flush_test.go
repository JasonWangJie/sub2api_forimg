package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAIResponseFlushRecorder struct {
	header          http.Header
	mu              sync.Mutex
	body            bytes.Buffer
	status          int
	writes          int
	failAfterWrites int
	flushSnapshots  []string
	flushEvents     chan int
	blockFlush      int
	flushBlocked    chan struct{}
	releaseFlush    <-chan struct{}
}

type openAIResponseBenchmarkWriter struct {
	header     http.Header
	status     int
	flushCount int
}

func (w *openAIResponseBenchmarkWriter) Header() http.Header { return w.header }

func (w *openAIResponseBenchmarkWriter) WriteHeader(statusCode int) {
	if w.status == 0 {
		w.status = statusCode
	}
}

func (w *openAIResponseBenchmarkWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return len(data), nil
}

func (w *openAIResponseBenchmarkWriter) Flush() { w.flushCount++ }

func newOpenAIResponseFlushRecorder() *openAIResponseFlushRecorder {
	return &openAIResponseFlushRecorder{
		header:          make(http.Header),
		failAfterWrites: -1,
		flushEvents:     make(chan int, 16),
	}
}

func (w *openAIResponseFlushRecorder) Header() http.Header {
	return w.header
}

func (w *openAIResponseFlushRecorder) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = statusCode
	}
}

func (w *openAIResponseFlushRecorder) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failAfterWrites >= 0 && w.writes >= w.failAfterWrites {
		return 0, errors.New("client disconnected")
	}
	w.writes++
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

func (w *openAIResponseFlushRecorder) Flush() {
	w.mu.Lock()
	w.flushSnapshots = append(w.flushSnapshots, w.body.String())
	count := len(w.flushSnapshots)
	w.mu.Unlock()
	w.flushEvents <- count
	if count == w.blockFlush {
		close(w.flushBlocked)
		<-w.releaseFlush
	}
}

func (w *openAIResponseFlushRecorder) snapshot() (string, []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String(), append([]string(nil), w.flushSnapshots...)
}

type stagedOpenAISSEReadCloser struct {
	segments   [][]byte
	gates      []<-chan struct{}
	waiting    []chan struct{}
	eofReached chan struct{}
	current    []byte
	index      int
}

func (r *stagedOpenAISSEReadCloser) Read(data []byte) (int, error) {
	if len(r.current) == 0 {
		if r.index >= len(r.segments) {
			if r.eofReached != nil {
				close(r.eofReached)
				r.eofReached = nil
			}
			return 0, io.EOF
		}
		index := r.index
		r.index++
		if index < len(r.waiting) && r.waiting[index] != nil {
			close(r.waiting[index])
		}
		if index < len(r.gates) && r.gates[index] != nil {
			<-r.gates[index]
		}
		r.current = r.segments[index]
	}
	n := copy(data, r.current)
	r.current = r.current[n:]
	return n, nil
}

func (r *stagedOpenAISSEReadCloser) Close() error { return nil }

type openAIResponseFlushReadError struct {
	payload []byte
	err     error
	sent    bool
}

func (r *openAIResponseFlushReadError) Read(data []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(data, r.payload), nil
	}
	if r.err != nil {
		return 0, r.err
	}
	return 0, io.ErrUnexpectedEOF
}

func (r *openAIResponseFlushReadError) Close() error { return nil }

func TestOpenAIResponseFlush_SlowEventsFlushOnceAtBoundaries(t *testing.T) {
	events := []string{
		`data: {"type":"response.output_text.delta","delta":"a"}`,
		`data: {"type":"response.output_text.delta","delta":"b"}`,
		`data: {"type":"response.output_text.delta","delta":"c"}`,
		`data: [DONE]`,
	}
	body := strings.Join(events, "\n\n") + "\n\n"
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), config.GatewayConfig{})

	require.NoError(t, err)
	require.NotNil(t, result)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, body, gotBody)
	require.Len(t, flushes, len(events))
	for _, flushed := range flushes {
		require.True(t, strings.HasSuffix(flushed, "\n\n"), "flush must occur after a complete SSE event")
	}
}

func TestOpenAIResponseFlush_DataQueuedButBlankDrainsFlushesOnce(t *testing.T) {
	first := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"first\"}\n\n"
	second := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"second\"}\n\n"
	terminal := "data: [DONE]\n\n"
	allowSecond := make(chan struct{})
	allowTerminal := make(chan struct{})
	terminalWaiting := make(chan struct{})
	reader := &stagedOpenAISSEReadCloser{
		segments: [][]byte{[]byte(first), []byte(second), []byte(terminal)},
		gates:    []<-chan struct{}{nil, allowSecond, allowTerminal},
		waiting:  []chan struct{}{nil, nil, terminalWaiting},
	}
	releaseFirstFlush := make(chan struct{})
	recorder := newOpenAIResponseFlushRecorder()
	recorder.blockFlush = 1
	recorder.flushBlocked = make(chan struct{})
	recorder.releaseFlush = releaseFirstFlush
	resultCh, errCh := runOpenAIResponseFlushTestAsync(recorder, reader, config.GatewayConfig{StreamDataIntervalTimeout: 30})

	waitOpenAIResponseFlushSignal(t, recorder.flushBlocked)
	close(allowSecond)
	waitOpenAIResponseFlushSignal(t, terminalWaiting)
	close(releaseFirstFlush)
	waitOpenAIResponseFlushCount(t, recorder, 2)
	close(allowTerminal)

	require.NoError(t, <-errCh)
	require.NotNil(t, <-resultCh)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, first+second+terminal, gotBody)
	require.Len(t, flushes, 3)
	require.Equal(t, first, flushes[0])
	require.Equal(t, first+second, flushes[1], "blank line that drains the queue must flush the complete event exactly once")
}

func TestOpenAIResponseFlush_BurstDoesNotIncreaseFlushes(t *testing.T) {
	first := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"first\"}\n\n"
	burst := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"second"}`,
		`data: {"type":"response.output_text.delta","delta":"third"}`,
		`data: [DONE]`,
	}, "\n\n") + "\n\n"
	allowBurst := make(chan struct{})
	eofReached := make(chan struct{})
	reader := &stagedOpenAISSEReadCloser{
		segments:   [][]byte{[]byte(first), []byte(burst)},
		gates:      []<-chan struct{}{nil, allowBurst},
		eofReached: eofReached,
	}
	releaseFirstFlush := make(chan struct{})
	recorder := newOpenAIResponseFlushRecorder()
	recorder.blockFlush = 1
	recorder.flushBlocked = make(chan struct{})
	recorder.releaseFlush = releaseFirstFlush
	resultCh, errCh := runOpenAIResponseFlushTestAsync(recorder, reader, config.GatewayConfig{StreamDataIntervalTimeout: 30})

	waitOpenAIResponseFlushSignal(t, recorder.flushBlocked)
	close(allowBurst)
	waitOpenAIResponseFlushSignal(t, eofReached)
	close(releaseFirstFlush)

	require.NoError(t, <-errCh)
	require.NotNil(t, <-resultCh)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, first+burst, gotBody)
	require.Len(t, flushes, 2, "queued burst must remain batched until its drained event boundary")
	require.Equal(t, first, flushes[0])
	require.Equal(t, first+burst, flushes[1])
}

func TestOpenAIResponseFlush_CommentAndEOFOnlyFlushCompleteResidual(t *testing.T) {
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"a\"}\n\n" +
		": upstream-comment\n\n" +
		"data: [DONE]\n"
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), config.GatewayConfig{})

	require.NoError(t, err)
	require.NotNil(t, result)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, body, gotBody)
	require.Len(t, flushes, 3)
	require.True(t, strings.HasSuffix(flushes[0], "\n\n"))
	require.True(t, strings.HasSuffix(flushes[1], "\n\n"))
	require.True(t, strings.HasSuffix(flushes[2], "data: [DONE]\n"), "EOF must flush only the remaining bytes")
}

func TestOpenAIResponseFlush_TerminalReadErrorFlushesResidual(t *testing.T) {
	body := "data: [DONE]\n"
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(recorder, &openAIResponseFlushReadError{payload: []byte(body)}, config.GatewayConfig{})

	require.NoError(t, err)
	require.NotNil(t, result)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, body, gotBody)
	require.Equal(t, []string{body}, flushes)
}

func TestOpenAIResponseFlush_OutputWithoutTerminalFlushesResidualWithoutFailover(t *testing.T) {
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n"
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), config.GatewayConfig{})

	require.ErrorContains(t, err, "missing terminal event")
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.NotNil(t, result)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, body, gotBody)
	require.Equal(t, []string{body}, flushes)
}

func TestOpenAIResponseFlush_PreambleWithoutTerminalRemainsBufferedForFailover(t *testing.T) {
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n"
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), config.GatewayConfig{})

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.NotNil(t, result)
	gotBody, flushes := recorder.snapshot()
	require.Empty(t, gotBody)
	require.Empty(t, flushes)
}

func TestOpenAIResponseFlush_EarlyCreatedFlushesBeforeSemanticOutputInOrder(t *testing.T) {
	created := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_early"}}` + "\n\n"
	inProgress := "event: response.in_progress\n" +
		`data: {"type":"response.in_progress","response":{"id":"resp_early"}}` + "\n\n"
	semantic := "event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","delta":"ready"}` + "\n\n"
	terminal := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_early","usage":{"input_tokens":4,"output_tokens":1,"total_tokens":5},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ready"}]}]}}` + "\n\n"
	allowCreatedBoundary := make(chan struct{})
	allowRest := make(chan struct{})
	createdBoundaryWaiting := make(chan struct{})
	restWaiting := make(chan struct{})
	reader := &stagedOpenAISSEReadCloser{
		segments: [][]byte{
			[]byte(strings.TrimSuffix(created, "\n")),
			[]byte("\n"),
			[]byte(inProgress + semantic + terminal),
		},
		gates:   []<-chan struct{}{nil, allowCreatedBoundary, allowRest},
		waiting: []chan struct{}{nil, createdBoundaryWaiting, restWaiting},
	}
	recorder := newOpenAIResponseFlushRecorder()
	resultCh, errCh := runOpenAIResponseFlushTestAsync(recorder, reader, config.GatewayConfig{
		OpenAIEarlyFlushCreated:         true,
		OpenAIFirstOutputTimeoutSeconds: 30,
	})

	waitOpenAIResponseFlushSignal(t, createdBoundaryWaiting)
	select {
	case count := <-recorder.flushEvents:
		t.Fatalf("response.created flushed before its blank boundary: flush %d", count)
	default:
	}
	gotBody, flushes := recorder.snapshot()
	require.Empty(t, gotBody)
	require.Empty(t, flushes)

	close(allowCreatedBoundary)
	waitOpenAIResponseFlushCount(t, recorder, 1)
	gotBody, flushes = recorder.snapshot()
	require.Equal(t, created, gotBody)
	require.Equal(t, []string{created}, flushes)

	waitOpenAIResponseFlushSignal(t, restWaiting)
	close(allowRest)
	require.NoError(t, <-errCh)
	result := <-resultCh
	require.NotNil(t, result)
	require.NotNil(t, result.firstTokenMs)
	gotBody, flushes = recorder.snapshot()
	require.Equal(t, created+inProgress+semantic+terminal, gotBody)
	require.GreaterOrEqual(t, len(flushes), 2)
	for _, snapshot := range flushes {
		require.True(t, strings.HasSuffix(snapshot, "\n\n"), "flush must preserve a complete SSE event boundary")
	}
}

func TestOpenAIResponseFlush_EarlyCreatedDoesNotSetFirstToken(t *testing.T) {
	created := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_created_only"}}` + "\n\n"
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(
		recorder,
		io.NopCloser(strings.NewReader(created)),
		config.GatewayConfig{OpenAIEarlyFlushCreated: true},
	)

	require.ErrorContains(t, err, "missing terminal event")
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.NotNil(t, result)
	require.Nil(t, result.firstTokenMs)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, created, gotBody)
	require.Equal(t, []string{created}, flushes)
}

func TestOpenAIResponseFlush_EarlyCreatedWithoutBoundaryStaysPrivate(t *testing.T) {
	incompleteCreated := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_incomplete_created"}}`
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(
		recorder,
		io.NopCloser(strings.NewReader(incompleteCreated)),
		config.GatewayConfig{
			OpenAIEarlyFlushCreated:         true,
			OpenAIFirstOutputTimeoutSeconds: 30,
		},
	)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.NotNil(t, result)
	require.Nil(t, result.firstTokenMs)
	gotBody, flushes := recorder.snapshot()
	require.Empty(t, gotBody)
	require.Empty(t, flushes)
}

func TestOpenAIResponseFlush_FailedAfterEarlyCreatedDoesNotFailOver(t *testing.T) {
	created := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_early_failed"}}` + "\n\n"
	failed := "event: response.failed\n" +
		`data: {"type":"response.failed","response":{"id":"resp_early_failed","status":"failed","error":{"code":"server_error","message":"upstream failed"}}}` + "\n\n"
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(
		recorder,
		io.NopCloser(strings.NewReader(created+failed)),
		config.GatewayConfig{
			OpenAIEarlyFlushCreated:         true,
			OpenAIFirstOutputTimeoutSeconds: 30,
		},
	)

	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.NotNil(t, result)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, created+failed, gotBody)
	require.Equal(t, []string{created, created + failed}, flushes)
}

func TestOpenAIResponseFlush_GroupScopedFirstVisibleTextFlush(t *testing.T) {
	groupID := int64(19)
	created := `data: {"type":"response.created","response":{"id":"resp_visible"}}` + "\n\n"
	outputItem := `data: {"type":"response.output_item.added","item":{"type":"message"}}` + "\n\n"
	blankText := `data: {"type":"response.output_text.delta","delta":"   "}` + "\n\n"
	firstText := `data: {"type":"response.output_text.delta","delta":"first"}` + "\n\n"
	secondText := `data: {"type":"response.output_text.delta","delta":"second"}` + "\n\n"
	terminal := "data: [DONE]\n\n"
	allowBurst := make(chan struct{})
	eofReached := make(chan struct{})
	reader := &stagedOpenAISSEReadCloser{
		segments:   [][]byte{[]byte(created), []byte(outputItem + blankText + firstText + secondText + terminal)},
		gates:      []<-chan struct{}{nil, allowBurst},
		eofReached: eofReached,
	}
	releaseCreatedFlush := make(chan struct{})
	recorder := newOpenAIResponseFlushRecorder()
	recorder.blockFlush = 1
	recorder.flushBlocked = make(chan struct{})
	recorder.releaseFlush = releaseCreatedFlush
	cfg := config.GatewayConfig{
		OpenAIEarlyFlushCreatedGroupIDs: []int64{groupID},
		StreamDataIntervalTimeout:       30,
	}
	resultCh, errCh := runOpenAIResponseFlushTestForGroupAsync(recorder, reader, cfg, &groupID)

	waitOpenAIResponseFlushSignal(t, recorder.flushBlocked)
	close(allowBurst)
	waitOpenAIResponseFlushSignal(t, eofReached)
	close(releaseCreatedFlush)

	require.NoError(t, <-errCh)
	result := <-resultCh
	require.NotNil(t, result)
	require.NotNil(t, result.firstTokenMs)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, created+outputItem+blankText+firstText+secondText+terminal, gotBody)
	require.Equal(t, []string{
		created,
		created + outputItem,
		created + outputItem + blankText + firstText,
		created + outputItem + blankText + firstText + secondText + terminal,
	}, flushes)
	for _, snapshot := range flushes {
		require.True(t, strings.HasSuffix(snapshot, "\n\n"), "flush must occur at a complete SSE event boundary")
	}
}

func TestOpenAIResponseFlush_UnmatchedGroupKeepsVisibleTextBatched(t *testing.T) {
	enabledGroupID := int64(19)
	requestGroupID := int64(20)
	first := `data: {"type":"response.output_item.added","item":{"type":"message"}}` + "\n\n"
	firstText := `data: {"type":"response.output_text.delta","delta":"first"}` + "\n\n"
	secondText := `data: {"type":"response.output_text.delta","delta":"second"}` + "\n\n"
	terminal := "data: [DONE]\n\n"
	allowBurst := make(chan struct{})
	eofReached := make(chan struct{})
	reader := &stagedOpenAISSEReadCloser{
		segments:   [][]byte{[]byte(first), []byte(firstText + secondText + terminal)},
		gates:      []<-chan struct{}{nil, allowBurst},
		eofReached: eofReached,
	}
	releaseFirstFlush := make(chan struct{})
	recorder := newOpenAIResponseFlushRecorder()
	recorder.blockFlush = 1
	recorder.flushBlocked = make(chan struct{})
	recorder.releaseFlush = releaseFirstFlush
	cfg := config.GatewayConfig{
		OpenAIEarlyFlushCreatedGroupIDs: []int64{enabledGroupID},
		StreamDataIntervalTimeout:       30,
	}
	resultCh, errCh := runOpenAIResponseFlushTestForGroupAsync(recorder, reader, cfg, &requestGroupID)

	waitOpenAIResponseFlushSignal(t, recorder.flushBlocked)
	close(allowBurst)
	waitOpenAIResponseFlushSignal(t, eofReached)
	close(releaseFirstFlush)

	require.NoError(t, <-errCh)
	require.NotNil(t, <-resultCh)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, first+firstText+secondText+terminal, gotBody)
	require.Equal(t, []string{first, first + firstText + secondText + terminal}, flushes)
}

func TestOpenAIResponseFlush_FirstVisibleTextWaitsForBlankBoundary(t *testing.T) {
	first := `data: {"type":"response.output_item.added","item":{"type":"message"}}` + "\n\n"
	visible := `data: {"type":"response.refusal.delta","delta":"visible"}` + "\n\n"
	terminal := "data: [DONE]\n\n"
	allowVisible := make(chan struct{})
	allowBoundary := make(chan struct{})
	allowTerminal := make(chan struct{})
	boundaryWaiting := make(chan struct{})
	terminalWaiting := make(chan struct{})
	reader := &stagedOpenAISSEReadCloser{
		segments: [][]byte{
			[]byte(first),
			[]byte(strings.TrimSuffix(visible, "\n")),
			[]byte("\n"),
			[]byte(terminal),
		},
		gates:   []<-chan struct{}{nil, allowVisible, allowBoundary, allowTerminal},
		waiting: []chan struct{}{nil, nil, boundaryWaiting, terminalWaiting},
	}
	recorder := newOpenAIResponseFlushRecorder()
	resultCh, errCh := runOpenAIResponseFlushTestAsync(recorder, reader, config.GatewayConfig{
		OpenAIEarlyFlushCreated:   true,
		StreamDataIntervalTimeout: 30,
	})

	waitOpenAIResponseFlushCount(t, recorder, 1)
	close(allowVisible)
	waitOpenAIResponseFlushSignal(t, boundaryWaiting)
	_, flushes := recorder.snapshot()
	require.Equal(t, []string{first}, flushes, "visible text must not flush before its terminating blank line")

	close(allowBoundary)
	waitOpenAIResponseFlushSignal(t, terminalWaiting)
	waitOpenAIResponseFlushCount(t, recorder, 2)
	_, flushes = recorder.snapshot()
	require.Equal(t, first+visible, flushes[1])

	close(allowTerminal)
	require.NoError(t, <-errCh)
	require.NotNil(t, <-resultCh)
}

func TestOpenAIResponseFlush_NewAPIFirstDataHTTPSmoke(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(41)
	created := `data: {"type":"response.created","response":{"id":"resp_newapi_smoke"}}` + "\n\n"
	semantic := `data: {"type":"response.output_item.added","item":{"type":"message"}}` + "\n\n"
	textDelta := `data: {"type":"response.output_text.delta","delta":"ready"}` + "\n\n"
	terminal := "data: [DONE]\n\n"
	upstreamReader, upstreamWriter := io.Pipe()
	releaseSemantic := make(chan struct{})
	upstreamDone := make(chan error, 1)
	go func() {
		if _, err := io.WriteString(upstreamWriter, created); err != nil {
			upstreamDone <- err
			return
		}
		<-releaseSemantic
		_, err := io.WriteString(upstreamWriter, semantic+textDelta+terminal)
		if closeErr := upstreamWriter.Close(); err == nil {
			err = closeErr
		}
		upstreamDone <- err
	}()

	svc := &OpenAIGatewayService{
		cfg: &config.Config{Gateway: config.GatewayConfig{
			OpenAIEarlyFlushCreatedGroupIDs: []int64{groupID},
		}},
		toolCorrector: NewCodexToolCorrector(),
	}
	router := gin.New()
	router.POST("/v1/responses", func(c *gin.Context) {
		c.Set("api_key", &APIKey{GroupID: &groupID})
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       upstreamReader,
		}
		_, _ = svc.handleStreamingResponse(
			c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI},
			time.Now(), "gpt-5", "gpt-5",
		)
	})
	server := httptest.NewServer(router)
	defer server.Close()

	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/responses", strings.NewReader(`{"stream":true}`))
	require.NoError(t, err)
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	require.Equal(t, "no-cache, no-transform", response.Header.Get("Cache-Control"))

	// newapi records FRT at the first non-empty data line. Semantic output is
	// still gated here, so the first observable data must be genuine created.
	scanner := bufio.NewScanner(response.Body)
	require.True(t, scanner.Scan())
	firstData := scanner.Text()
	require.Equal(t, strings.TrimSuffix(created, "\n\n"), firstData)
	close(releaseSemantic)

	var remainingLines []string
	for scanner.Scan() {
		remainingLines = append(remainingLines, scanner.Text())
	}
	require.NoError(t, scanner.Err())
	require.NoError(t, <-upstreamDone)
	fullStream := firstData + "\n" + strings.Join(remainingLines, "\n")
	require.Equal(t, strings.TrimSuffix(created+semantic+textDelta+terminal, "\n"), fullStream)
}

func TestOpenAIResponseFlush_CanceledAfterOutputFlushesResidualWithoutErrorEvent(t *testing.T) {
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n"
	recorder := newOpenAIResponseFlushRecorder()

	result, err := runOpenAIResponseFlushTest(recorder, &openAIResponseFlushReadError{payload: []byte(body), err: context.Canceled}, config.GatewayConfig{})

	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, result)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, body, gotBody)
	require.Equal(t, []string{body}, flushes)
	require.NotContains(t, gotBody, "stream_read_error")
}

func TestOpenAIResponseFlush_KeepaliveFlushesImmediately(t *testing.T) {
	recorder := newOpenAIResponseFlushRecorder()
	reader, writer := io.Pipe()
	resultCh, errCh := runOpenAIResponseFlushTestAsync(recorder, reader, config.GatewayConfig{StreamKeepaliveInterval: 1})

	waitOpenAIResponseFlushCount(t, recorder, 1)
	_, flushes := recorder.snapshot()
	require.Equal(t, ":\n\n", flushes[0])
	_, err := writer.Write([]byte("data: [DONE]\n\n"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	require.NoError(t, <-errCh)
	require.NotNil(t, <-resultCh)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, ":\n\ndata: [DONE]\n\n", gotBody)
	require.Len(t, flushes, 2)
}

func TestOpenAIResponseFlush_KeepaliveDoesNotSplitOpenEvent(t *testing.T) {
	const dataLine = `data: {"type":"response.output_text.delta","delta":"a"}`
	// Filling the 16-slot scan queue proves the main loop processed data before the reader reaches the gated blank.
	dataLines := make([]string, 17)
	for i := range dataLines {
		dataLines[i] = dataLine
	}
	partialEvent := strings.Join(dataLines, "\n") + "\n"
	completeEvent := partialEvent + "\n"
	terminal := "data: [DONE]\n\n"
	allowBlank := make(chan struct{})
	allowTerminal := make(chan struct{})
	blankWaiting := make(chan struct{})
	terminalWaiting := make(chan struct{})
	reader := &stagedOpenAISSEReadCloser{
		segments: [][]byte{[]byte(partialEvent), []byte("\n"), []byte(terminal)},
		gates:    []<-chan struct{}{nil, allowBlank, allowTerminal},
		waiting:  []chan struct{}{nil, blankWaiting, terminalWaiting},
	}
	recorder := newOpenAIResponseFlushRecorder()
	resultCh, errCh := runOpenAIResponseFlushTestAsync(recorder, reader, config.GatewayConfig{StreamKeepaliveInterval: 1})

	waitOpenAIResponseFlushSignal(t, blankWaiting)
	timer := time.NewTimer(1250 * time.Millisecond)
	select {
	case count := <-recorder.flushEvents:
		timer.Stop()
		t.Fatalf("keepalive flushed open event before its blank boundary: flush %d", count)
	case <-timer.C:
	}

	close(allowBlank)
	waitOpenAIResponseFlushSignal(t, terminalWaiting)
	waitOpenAIResponseFlushCount(t, recorder, 1)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, completeEvent, gotBody)
	require.Equal(t, []string{completeEvent}, flushes)

	close(allowTerminal)
	require.NoError(t, <-errCh)
	require.NotNil(t, <-resultCh)
	gotBody, flushes = recorder.snapshot()
	require.Equal(t, completeEvent+terminal, gotBody)
	require.Len(t, flushes, 2)
	require.Equal(t, completeEvent+terminal, flushes[1])
}

func TestOpenAIResponseFlush_FailedAndErrorEventsFlushAtBoundaries(t *testing.T) {
	t.Run("failed at EOF", func(t *testing.T) {
		body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"a\"}\n\n" +
			"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"safety_error\",\"message\":\"blocked\"},\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n"
		recorder := newOpenAIResponseFlushRecorder()

		result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), config.GatewayConfig{})

		require.Error(t, err)
		require.NotNil(t, result)
		require.Equal(t, 3, result.usage.InputTokens)
		gotBody, flushes := recorder.snapshot()
		expectedBody := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"a\"}\n\n" +
			"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"safety_error\",\"message\":\"blocked\"}}}\n"
		require.Equal(t, expectedBody, gotBody)
		require.Len(t, flushes, 2)
		require.Contains(t, flushes[1], "response.failed")
	})

	t.Run("retryable error event buffered until terminal", func(t *testing.T) {
		// 可重试类 error 帧不算客户端输出：保持在 attempt 缓冲中不单独 flush，
		// 为随后可能到达的 response.failed 保留 pre-output failover 能力，
		// 与终止帧一起出站。
		body := "data: {\"type\":\"error\",\"error\":{\"message\":\"failed\"}}\n\n" +
			"data: [DONE]\n\n"
		recorder := newOpenAIResponseFlushRecorder()

		result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), config.GatewayConfig{})

		require.NoError(t, err)
		require.NotNil(t, result)
		gotBody, flushes := recorder.snapshot()
		require.Equal(t, body, gotBody)
		require.Len(t, flushes, 1)
	})

	t.Run("non-retryable error event flushes at boundary", func(t *testing.T) {
		body := "data: {\"type\":\"error\",\"error\":{\"code\":\"invalid_request\",\"message\":\"bad request\"}}\n\n" +
			"data: [DONE]\n\n"
		recorder := newOpenAIResponseFlushRecorder()

		result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), config.GatewayConfig{})

		require.NoError(t, err)
		require.NotNil(t, result)
		gotBody, flushes := recorder.snapshot()
		require.Equal(t, body, gotBody)
		require.Len(t, flushes, 2)
	})
}

func TestOpenAIResponseFlush_ReusedTypeKeepsSSEBytesAndTerminalSemantics(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		flushCount int
	}{
		{
			name:       "whitespace around done",
			body:       "data: \t[DONE]  \n\n",
			flushCount: 1,
		},
		{
			name:       "invalid JSON before done",
			body:       "data: {\"type\":\n\ndata: [DONE]\n\n",
			flushCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := newOpenAIResponseFlushRecorder()

			result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(tt.body)), config.GatewayConfig{})

			require.NoError(t, err)
			require.NotNil(t, result)
			gotBody, flushes := recorder.snapshot()
			require.Equal(t, tt.body, gotBody)
			require.Len(t, flushes, tt.flushCount)
		})
	}
}

func TestOpenAIResponseFlush_ClientDisconnectStillDrainsUsage(t *testing.T) {
	first := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"a\"}\n\n"
	terminal := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":7,\"output_tokens\":5,\"input_tokens_details\":{\"cached_tokens\":2}}}}\n\n"
	recorder := newOpenAIResponseFlushRecorder()
	recorder.failAfterWrites = 1

	result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(first+terminal)), config.GatewayConfig{})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 7, result.usage.InputTokens)
	require.Equal(t, 5, result.usage.OutputTokens)
	require.Equal(t, 2, result.usage.CacheReadInputTokens)
	gotBody, flushes := recorder.snapshot()
	require.Equal(t, first, gotBody)
	require.Len(t, flushes, 1)
}

func runOpenAIResponseFlushTest(recorder *openAIResponseFlushRecorder, body io.ReadCloser, gatewayCfg config.GatewayConfig) (*openaiStreamingResult, error) {
	return runOpenAIResponseFlushTestForGroup(recorder, body, gatewayCfg, nil)
}

func runOpenAIResponseFlushTestForGroup(recorder *openAIResponseFlushRecorder, body io.ReadCloser, gatewayCfg config.GatewayConfig, groupID *int64) (*openaiStreamingResult, error) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if groupID != nil {
		c.Set("api_key", &APIKey{GroupID: groupID})
	}
	svc := &OpenAIGatewayService{
		cfg:           &config.Config{Gateway: gatewayCfg},
		toolCorrector: NewCodexToolCorrector(),
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
	}
	return svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "gpt-5", "gpt-5")
}

func runOpenAIResponseFlushTestAsync(recorder *openAIResponseFlushRecorder, body io.ReadCloser, gatewayCfg config.GatewayConfig) (<-chan *openaiStreamingResult, <-chan error) {
	return runOpenAIResponseFlushTestForGroupAsync(recorder, body, gatewayCfg, nil)
}

func runOpenAIResponseFlushTestForGroupAsync(recorder *openAIResponseFlushRecorder, body io.ReadCloser, gatewayCfg config.GatewayConfig, groupID *int64) (<-chan *openaiStreamingResult, <-chan error) {
	resultCh := make(chan *openaiStreamingResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := runOpenAIResponseFlushTestForGroup(recorder, body, gatewayCfg, groupID)
		resultCh <- result
		errCh <- err
	}()
	return resultCh, errCh
}

func BenchmarkOpenAIResponseStreaming(b *testing.B) {
	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_bench"}}`,
		`data: {"type":"response.in_progress","response":{"id":"resp_bench"}}`,
		`data: {"type":"response.output_item.added","item":{"type":"message"}}`,
		`data: {"type":"response.output_text.delta","delta":"benchmark text"}`,
		`data: {"type":"response.output_text.delta","delta":" continued"}`,
		`data: [DONE]`,
	}, "\n\n") + "\n\n"

	for _, tt := range []struct {
		name string
		cfg  config.GatewayConfig
	}{
		{name: "default"},
		{name: "early_created_and_first_text", cfg: config.GatewayConfig{OpenAIEarlyFlushCreated: true}},
	} {
		b.Run(tt.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				writer := &openAIResponseBenchmarkWriter{header: make(http.Header)}
				gin.SetMode(gin.TestMode)
				c, _ := gin.CreateTestContext(writer)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				svc := &OpenAIGatewayService{
					cfg:           &config.Config{Gateway: tt.cfg},
					toolCorrector: NewCodexToolCorrector(),
				}
				resp := &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(body)),
				}
				result, err := svc.handleStreamingResponse(
					context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI},
					time.Now(), "gpt-5", "gpt-5",
				)
				if err != nil || result == nil {
					b.Fatalf("stream failed: result=%v err=%v", result, err)
				}
			}
		})
	}
}

func waitOpenAIResponseFlushCount(t *testing.T, recorder *openAIResponseFlushRecorder, want int) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case count := <-recorder.flushEvents:
			if count >= want {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for flush %d", want)
		}
	}
}

func waitOpenAIResponseFlushSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for stream signal")
	}
}
