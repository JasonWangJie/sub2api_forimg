package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type passthroughFlushTestWriter struct {
	gin.ResponseWriter
	recorder         *httptest.ResponseRecorder
	failAfterWrites  int
	successfulWrites int
	failedWrites     int
	flushBodyLengths []int
	flushEvents      chan int
}

func (w *passthroughFlushTestWriter) Write(data []byte) (int, error) {
	if w.failAfterWrites >= 0 && w.successfulWrites >= w.failAfterWrites {
		w.failedWrites++
		return 0, errors.New("client disconnected")
	}
	n, err := w.ResponseWriter.Write(data)
	if err == nil {
		w.successfulWrites++
	}
	return n, err
}

func (w *passthroughFlushTestWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func (w *passthroughFlushTestWriter) Flush() {
	w.ResponseWriter.Flush()
	w.flushBodyLengths = append(w.flushBodyLengths, w.recorder.Body.Len())
	if w.flushEvents != nil {
		select {
		case w.flushEvents <- len(w.flushBodyLengths):
		default:
		}
	}
}

type passthroughFlushTestErrorBody struct {
	payload []byte
	err     error
	sent    bool
}

func (r *passthroughFlushTestErrorBody) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, r.payload), nil
	}
	return 0, r.err
}

func (r *passthroughFlushTestErrorBody) Close() error { return nil }

func runPassthroughFlushTest(
	t *testing.T,
	body io.ReadCloser,
	failAfterWrites int,
	setups ...func(*gin.Context),
) (*openaiStreamingResultPassthrough, *httptest.ResponseRecorder, *passthroughFlushTestWriter, error) {
	t.Helper()
	return runPassthroughFlushTestWithConfig(
		t,
		body,
		failAfterWrites,
		config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		setups...,
	)
}

func runPassthroughFlushTestWithConfig(
	t *testing.T,
	body io.ReadCloser,
	failAfterWrites int,
	gatewayCfg config.GatewayConfig,
	setups ...func(*gin.Context),
) (*openaiStreamingResultPassthrough, *httptest.ResponseRecorder, *passthroughFlushTestWriter, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	writer := &passthroughFlushTestWriter{
		ResponseWriter:  c.Writer,
		recorder:        recorder,
		failAfterWrites: failAfterWrites,
		flushEvents:     make(chan int, 16),
	}
	c.Writer = writer
	for _, setup := range setups {
		setup(c)
	}

	if gatewayCfg.MaxLineSize <= 0 {
		gatewayCfg.MaxLineSize = defaultMaxLineSize
	}
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: gatewayCfg}}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
	}
	result, err := svc.handleStreamingResponsePassthrough(
		context.Background(),
		resp,
		c,
		&Account{ID: 1, Platform: PlatformOpenAI, Name: "flush-test"},
		time.Now(),
		"",
		"",
	)
	return result, recorder, writer, err
}

func TestOpenAIStreamingPassthroughFlushesAtCompleteEventBoundaries(t *testing.T) {
	firstEvent := "event: response.output_text.delta\n" +
		"id: event-1\n" +
		`data: {"type":"response.output_text.delta","delta":"hello"}` + "\n\n"
	heartbeat := ": keepalive\n\n"
	terminalEvent := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_flush","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}` + "\n\n"
	upstream := firstEvent + heartbeat + terminalEvent

	result, recorder, writer, err := runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(upstream)), -1)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "no-cache, no-transform", recorder.Header().Get("Cache-Control"))
	require.Equal(t, upstream, recorder.Body.String())
	require.Equal(t, []int{
		len(firstEvent),
		len(firstEvent) + len(heartbeat),
		len(upstream),
	}, writer.flushBodyLengths)
	require.Equal(t, 3, result.usage.InputTokens)
	require.Equal(t, 2, result.usage.OutputTokens)
}

func TestOpenAIStreamingPassthroughEarlyOptionDoesNotAddVisibleTextFlushes(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"type":"message"}}`,
		`data: {"type":"response.output_text.delta","delta":"first"}`,
		`data: {"type":"response.output_text.delta","delta":"second"}`,
		`data: [DONE]`,
	}, "\n\n") + "\n\n"

	_, defaultRecorder, defaultWriter, defaultErr := runPassthroughFlushTestWithConfig(
		t,
		io.NopCloser(strings.NewReader(upstream)),
		-1,
		config.GatewayConfig{},
	)
	_, enabledRecorder, enabledWriter, enabledErr := runPassthroughFlushTestWithConfig(
		t,
		io.NopCloser(strings.NewReader(upstream)),
		-1,
		config.GatewayConfig{OpenAIEarlyFlushCreated: true},
	)

	require.NoError(t, defaultErr)
	require.NoError(t, enabledErr)
	require.Equal(t, upstream, defaultRecorder.Body.String())
	require.Equal(t, defaultRecorder.Body.String(), enabledRecorder.Body.String())
	require.Equal(t, defaultWriter.flushBodyLengths, enabledWriter.flushBodyLengths)
}

func TestOpenAIStreamingPassthroughKeepsPreamblePendingUntilFirstOutputBoundary(t *testing.T) {
	preamble := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_pending"}}` + "\n\n" +
		": waiting\n\n"
	firstOutput := `data: {"type":"response.output_text.delta","delta":"ready"}` + "\n\n"
	terminalEvent := `data: {"type":"response.completed","response":{"id":"resp_pending","usage":{"input_tokens":4,"output_tokens":1,"total_tokens":5}}}` + "\n\n"
	upstream := preamble + firstOutput + terminalEvent

	_, recorder, writer, err := runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(upstream)), -1)

	require.NoError(t, err)
	require.Equal(t, upstream, recorder.Body.String())
	require.Equal(t, []int{
		len(preamble) + len(firstOutput),
		len(upstream),
	}, writer.flushBodyLengths)
}

func TestOpenAIStreamingPassthroughEarlyFlushesCreatedBeforeSemanticOutput(t *testing.T) {
	created := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_early"}}` + "\n\n"
	inProgress := "event: response.in_progress\n" +
		`data: {"type":"response.in_progress","response":{"id":"resp_early"}}` + "\n\n"
	semantic := "event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","delta":"ready"}` + "\n\n"
	terminal := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_early","usage":{"input_tokens":4,"output_tokens":1,"total_tokens":5}}}` + "\n\n"

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
	writerReady := make(chan struct{})
	var streamWriter *passthroughFlushTestWriter
	resultCh := make(chan *openaiStreamingResultPassthrough, 1)
	errCh := make(chan error, 1)
	go func() {
		result, _, _, err := runPassthroughFlushTestWithConfig(
			t,
			reader,
			-1,
			config.GatewayConfig{OpenAIEarlyFlushCreated: true},
			func(c *gin.Context) {
				streamWriter = c.Writer.(*passthroughFlushTestWriter)
				close(writerReady)
			},
		)
		resultCh <- result
		errCh <- err
	}()

	<-writerReady
	select {
	case <-createdBoundaryWaiting:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for response.created data line")
	}
	select {
	case count := <-streamWriter.flushEvents:
		t.Fatalf("response.created flushed before its blank boundary: flush %d", count)
	default:
	}
	require.Empty(t, streamWriter.recorder.Body.String())

	close(allowCreatedBoundary)
	select {
	case <-streamWriter.flushEvents:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for response.created flush")
	}
	require.Equal(t, created, streamWriter.recorder.Body.String())
	require.Equal(t, []int{len(created)}, streamWriter.flushBodyLengths)

	select {
	case <-restWaiting:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for semantic events")
	}
	close(allowRest)
	require.NoError(t, <-errCh)
	result := <-resultCh
	require.NotNil(t, result)
	require.NotNil(t, result.firstTokenMs)
	require.Equal(t, created+inProgress+semantic+terminal, streamWriter.recorder.Body.String())
}

func TestOpenAIStreamingPassthroughEarlyCreatedDoesNotSetFirstToken(t *testing.T) {
	created := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_created_only"}}` + "\n\n"

	result, recorder, writer, err := runPassthroughFlushTestWithConfig(
		t,
		io.NopCloser(strings.NewReader(created)),
		-1,
		config.GatewayConfig{OpenAIEarlyFlushCreated: true},
	)

	require.ErrorContains(t, err, "missing terminal event")
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.NotNil(t, result)
	require.Nil(t, result.firstTokenMs)
	require.Equal(t, created, recorder.Body.String())
	require.Equal(t, []int{len(created)}, writer.flushBodyLengths)
}

func TestOpenAIStreamingPassthroughEarlyCreatedWithoutBoundaryStaysPrivate(t *testing.T) {
	incompleteCreated := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_incomplete_created"}}`

	result, recorder, writer, err := runPassthroughFlushTestWithConfig(
		t,
		io.NopCloser(strings.NewReader(incompleteCreated)),
		-1,
		config.GatewayConfig{OpenAIEarlyFlushCreated: true},
	)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.NotNil(t, result)
	require.Nil(t, result.firstTokenMs)
	require.Empty(t, recorder.Body.String())
	require.Empty(t, writer.flushBodyLengths)
}

func TestOpenAIStreamingPassthroughFailedAfterEarlyCreatedDoesNotFailOver(t *testing.T) {
	created := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_early_failed"}}` + "\n\n"
	failed := "event: response.failed\n" +
		`data: {"type":"response.failed","response":{"id":"resp_early_failed","status":"failed","error":{"code":"server_error","message":"upstream failed"}}}` + "\n\n"

	_, recorder, writer, err := runPassthroughFlushTestWithConfig(
		t,
		io.NopCloser(strings.NewReader(created+failed)),
		-1,
		config.GatewayConfig{OpenAIEarlyFlushCreated: true},
	)

	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Equal(t, created+failed, recorder.Body.String())
	require.Equal(t, []int{len(created), len(created) + len(failed)}, writer.flushBodyLengths)
}

func TestOpenAIEarlyFlushCreatedSelection(t *testing.T) {
	groupID := int64(9)
	otherGroupID := int64(10)
	newContext := func(id *int64) *gin.Context {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("api_key", &APIKey{GroupID: id})
		return c
	}

	groupScoped := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		OpenAIEarlyFlushCreatedGroupIDs: []int64{7, groupID},
	}}}
	require.True(t, groupScoped.openAIEarlyFlushCreated(newContext(&groupID)))
	require.False(t, groupScoped.openAIEarlyFlushCreated(newContext(&otherGroupID)))
	require.False(t, groupScoped.openAIEarlyFlushCreated(newContext(nil)))

	global := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		OpenAIEarlyFlushCreated: true,
	}}}
	require.True(t, global.openAIEarlyFlushCreated(newContext(&otherGroupID)))

	semanticOutput, downstreamOutput := openAIStreamOutputDecisions(
		`{"type":"response.created","response":{"id":"resp_decision"}}`,
		"response.created",
		false,
		true,
	)
	require.False(t, semanticOutput)
	require.True(t, downstreamOutput)
}

func TestOpenAIStreamingPassthroughFlushesTerminalEventAtEOFWithoutBlankLine(t *testing.T) {
	upstream := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_eof","usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`
	wantBody := upstream + "\n"

	result, recorder, writer, err := runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(upstream)), -1)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, wantBody, recorder.Body.String())
	require.Equal(t, []int{len(wantBody)}, writer.flushBodyLengths)
	require.Equal(t, 5, result.usage.InputTokens)
	require.Equal(t, 2, result.usage.OutputTokens)
}

func TestOpenAIStreamingPassthroughFailedBeforeOutputCanStillFailOverWithoutFlush(t *testing.T) {
	upstream := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_failover"}}` + "\n\n" +
		"event: response.failed\n" +
		`data: {"type":"response.failed","error":{"code":"server_error","message":"upstream processing failed"}}` + "\n\n"

	_, recorder, writer, err := runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(upstream)), -1)

	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Empty(t, recorder.Body.String())
	require.Empty(t, writer.flushBodyLengths)
}

func TestOpenAIStreamingPassthroughNonRetryableFailedBeforeOutputFlushesAtBoundary(t *testing.T) {
	upstream := "event: response.failed\n" +
		`data: {"type":"response.failed","error":{"code":"content_policy","message":"request blocked by policy"},"usage":{"input_tokens":6,"output_tokens":0,"total_tokens":6}}` + "\n\n"

	result, recorder, writer, err := runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(upstream)), -1)

	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.NotNil(t, result)
	require.Equal(t, upstream, recorder.Body.String())
	require.Equal(t, []int{len(upstream)}, writer.flushBodyLengths)
	require.Equal(t, 6, result.usage.InputTokens)
	require.Zero(t, result.usage.OutputTokens)
}

func TestOpenAIStreamingPassthroughFailedAfterOutputFlushesAtBoundaryAndKeepsUsage(t *testing.T) {
	firstOutput := `data: {"type":"response.output_text.delta","delta":"partial"}` + "\n\n"
	failedEvent := "event: response.failed\n" +
		`data: {"type":"response.failed","error":{"code":"server_error","message":"upstream processing failed"},"usage":{"input_tokens":7,"output_tokens":2,"total_tokens":9}}` + "\n\n"
	upstream := firstOutput + failedEvent

	result, recorder, writer, err := runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(upstream)), -1)

	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.NotNil(t, result)
	require.Equal(t, upstream, recorder.Body.String())
	require.Equal(t, []int{len(firstOutput), len(upstream)}, writer.flushBodyLengths)
	require.Equal(t, 7, result.usage.InputTokens)
	require.Equal(t, 2, result.usage.OutputTokens)
}

func TestOpenAIStreamingPassthroughClientDisconnectStillDrainsTerminalUsage(t *testing.T) {
	firstOutput := `data: {"type":"response.output_text.delta","delta":"partial"}` + "\n\n"
	terminalEvent := `data: {"type":"response.completed","response":{"id":"resp_drain","usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15}}}` + "\n\n"

	result, recorder, writer, err := runPassthroughFlushTest(
		t,
		io.NopCloser(strings.NewReader(firstOutput+terminalEvent)),
		2,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, firstOutput, recorder.Body.String())
	require.Equal(t, []int{len(firstOutput)}, writer.flushBodyLengths)
	require.Equal(t, 1, writer.failedWrites)
	require.Equal(t, 11, result.usage.InputTokens)
	require.Equal(t, 4, result.usage.OutputTokens)
}

func TestOpenAIStreamingPassthroughScannerErrorFlushesWrittenResidual(t *testing.T) {
	upstream := []byte(`data: {"type":"response.output_text.delta","delta":"partial"}`)
	readErr := errors.New("upstream read failed")

	_, recorder, writer, err := runPassthroughFlushTest(t, &passthroughFlushTestErrorBody{
		payload: upstream,
		err:     readErr,
	}, -1)

	require.ErrorIs(t, err, readErr)
	wantBody := string(upstream) + "\n"
	require.Equal(t, wantBody, recorder.Body.String())
	require.Equal(t, []int{len(wantBody)}, writer.flushBodyLengths)
}

func TestOpenAIStreamingPassthroughNamespaceRestoreErrorFlushesWrittenResidualOnce(t *testing.T) {
	writtenPrefix := `data: {"type":"response.output_text.delta","delta":"prefix"}` + "\n"
	overflowData := `data: {"type":"response.output_text.delta","delta":"not-written","overflow":1e1000}`

	_, recorder, writer, err := runPassthroughFlushTest(
		t,
		io.NopCloser(strings.NewReader(writtenPrefix+overflowData)),
		-1,
		func(c *gin.Context) {
			setOpenAIResponsesNamespaceNames(c, map[string]apicompat.ResponsesNamespaceName{
				"collaboration__spawn_agent": {Namespace: "collaboration", Name: "spawn_agent"},
			})
		},
	)

	require.ErrorContains(t, err, "restore OpenAI passthrough namespace response")
	require.Equal(t, writtenPrefix, recorder.Body.String())
	require.Equal(t, []int{len(writtenPrefix)}, writer.flushBodyLengths)
}

func TestOpenAIStreamingPassthroughBlankWriteFailureDoesNotFlushAndStillDrainsUsage(t *testing.T) {
	writtenDataLine := `data: {"type":"response.output_text.delta","delta":"partial"}` + "\n"
	terminalEvent := `data: {"type":"response.completed","response":{"id":"resp_blank_failure","usage":{"input_tokens":13,"output_tokens":5,"total_tokens":18}}}` + "\n\n"

	result, recorder, writer, err := runPassthroughFlushTest(
		t,
		io.NopCloser(strings.NewReader(writtenDataLine+"\n"+terminalEvent)),
		1,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, writtenDataLine, recorder.Body.String())
	require.Empty(t, writer.flushBodyLengths)
	require.Equal(t, 1, writer.successfulWrites)
	require.Equal(t, 1, writer.failedWrites)
	require.Equal(t, 13, result.usage.InputTokens)
	require.Equal(t, 5, result.usage.OutputTokens)
}
