package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIAsyncReferenceURLTimeoutClassification(t *testing.T) {
	body := fmt.Sprintf(`{"error":{"type":"invalid_request_error","code":"invalid_value","param":"url","message":%q}}`, openAIReferenceURLTimeoutMessage)
	ctx := WithAsyncImageAccountAttemptCapture(context.Background(), &AsyncImageAccountAttemptCapture{})
	ctx = WithAsyncImageReferenceLocalFallback(WithAsyncImageInputReferenceTask(ctx, true), true)
	parsed := &OpenAIImagesRequest{Endpoint: openAIImagesEditsEndpoint, InputImageURLs: []string{"https://source.example/ref.png"}}
	headers := http.Header{"X-Request-Id": {"req-url-timeout"}}
	failure := newAsyncImageOpenAIReferenceURLTimeout(ctx, parsed, http.StatusBadRequest, []byte(body), headers)
	require.True(t, failure.IsAsyncImageOpenAIReferenceURLTimeout())
	require.Equal(t, "invalid_value", failure.ProviderErrorCode)
	require.Equal(t, "req-url-timeout", failure.UpstreamRequestID)
	require.False(t, failure.RetryableOnSameAccount)
	require.False(t, failure.ShouldRetryNextAccount())
	require.False(t, failure.IsAsyncImageReferenceAccountFailover())

	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{"wrong status", http.StatusGatewayTimeout, body},
		{"wrong type", http.StatusBadRequest, strings.Replace(body, "invalid_request_error", "upstream_error", 1)},
		{"wrong code", http.StatusBadRequest, strings.Replace(body, "invalid_value", "unknown_parameter", 1)},
		{"wrong param", http.StatusBadRequest, strings.Replace(body, `"param":"url"`, `"param":"size"`, 1)},
		{"incomplete message", http.StatusBadRequest, strings.Replace(body, " or upload the file and provide a file_id instead.", "", 1)},
		{"malformed JSON", http.StatusBadRequest, strings.TrimSuffix(body, "}")},
		{"unknown details", http.StatusBadRequest, strings.TrimSuffix(body, "}") + `,"details":[{"execution_unknown":true}]}`},
		{"explicit stop", http.StatusBadRequest, strings.TrimSuffix(body, "}") + `,"details":["do not\u0020retry"]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Nil(t, newAsyncImageOpenAIReferenceURLTimeout(ctx, parsed, tt.status, []byte(tt.body), headers))
		})
	}
	for name, request := range map[string]*OpenAIImagesRequest{
		"text task": {Endpoint: openAIImagesGenerationsEndpoint},
		"multipart": {Endpoint: openAIImagesEditsEndpoint, Multipart: true, InputImageURLs: parsed.InputImageURLs},
		"inline":    {Endpoint: openAIImagesEditsEndpoint, InputImageURLs: []string{"data:image/png;base64,AA=="}},
	} {
		t.Run(name, func(t *testing.T) {
			require.Nil(t, newAsyncImageOpenAIReferenceURLTimeout(ctx, request, http.StatusBadRequest, []byte(body), headers))
		})
	}
}

func TestOpenAIImagesForwardAsyncReferenceURLTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const requestBody = `{"model":"gpt-image-2","prompt":"edit a photo","image_urls":["https://source.example/ref.png"]}`
	upstreamBody := fmt.Sprintf(`{"error":{"type":"invalid_request_error","code":"invalid_value","param":"url","message":%q}}`, openAIReferenceURLTimeoutMessage)
	for _, tt := range []struct {
		name       string
		async      bool
		hybrid     bool
		inputTask  bool
		passthru   bool
		wantMarker bool
	}{
		{name: "durable hybrid", async: true, hybrid: true, inputTask: true, wantMarker: true},
		{name: "synchronous", hybrid: true, inputTask: true},
		{name: "no fallback budget", async: true, inputTask: true},
		{name: "no image task flag", async: true, hybrid: true},
		{name: "admin passthrough", async: true, hybrid: true, inputTask: true, passthru: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := WithAsyncImageInputReferenceTask(context.Background(), tt.inputTask)
			ctx = WithAsyncImageReferenceLocalFallback(ctx, tt.hybrid)
			if tt.async {
				ctx = WithAsyncImageAccountAttemptCapture(ctx, &AsyncImageAccountAttemptCapture{})
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, openAIImagesEditsEndpoint, bytes.NewBufferString(requestBody)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			if tt.passthru {
				status, message := http.StatusConflict, "custom rejection"
				BindErrorPassthroughService(c, &ErrorPassthroughService{localCache: []*cachedPassthroughRule{{
					ErrorPassthroughRule: &model.ErrorPassthroughRule{
						ID: 1, Enabled: true, Platforms: []string{PlatformOpenAI}, MatchMode: model.MatchModeAny,
						ErrorCodes: []int{400}, ResponseCode: &status, CustomMessage: &message,
					}, lowerPlatforms: []string{PlatformOpenAI}, errorCodeSet: map[int]struct{}{400: {}},
				}}})
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"X-Request-Id": {"req-url-timeout"}},
				Body:       io.NopCloser(strings.NewReader(upstreamBody)),
			}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 45, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
				"api_key": "test", "base_url": "https://images.example/v1", "pool_mode": true, "pool_mode_retry_status_codes": []int{400},
			}}
			parsed, err := svc.ParseOpenAIImagesRequest(c, []byte(requestBody))
			require.NoError(t, err)
			_, err = svc.ForwardImages(ctx, c, account, []byte(requestBody), parsed, "")
			require.Len(t, upstream.requests, 1)
			var failure *UpstreamFailoverError
			if tt.wantMarker {
				require.ErrorAs(t, err, &failure)
				require.True(t, failure.IsAsyncImageOpenAIReferenceURLTimeout())
				require.Empty(t, recorder.Body.Bytes())
				return
			}
			var upstreamErr *OpenAIImagesUpstreamError
			require.ErrorAs(t, err, &upstreamErr)
			require.NotErrorAs(t, err, &failure)
			if tt.passthru {
				require.Equal(t, http.StatusConflict, recorder.Code)
				require.Contains(t, recorder.Body.String(), "custom rejection")
			} else {
				require.Equal(t, http.StatusBadRequest, recorder.Code)
			}
		})
	}
}

func TestNormalizeOpenAIImageEditReferencesForLocalFallback(t *testing.T) {
	const body = `{"prompt":"edit","model":"gpt-image-2","n":1,"quality":"high","image_urls":["https://src.example/first.png"],"images":["https://src.example/second.png",{"image_url":"https://src.example/first.png"},{"url":"https://src.example/ignored.png"}],"image":"https://src.example/ignored.png","mask":{"image_url":"https://src.example/mask.png"}}`
	prepared, err := NormalizeOpenAIImageEditReferencesForLocalFallback([]byte(body))
	require.NoError(t, err)
	require.JSONEq(t, `[{"image_url":"https://src.example/first.png"},{"image_url":"https://src.example/second.png"}]`, gjson.GetBytes(prepared, "images").Raw)
	require.False(t, gjson.GetBytes(prepared, "image_urls").Exists())
	require.False(t, gjson.GetBytes(prepared, "image").Exists())
	require.Equal(t, "https://src.example/mask.png", gjson.GetBytes(prepared, "mask.image_url").String())
	require.Equal(t, "edit", gjson.GetBytes(prepared, "prompt").String())
	require.Equal(t, "high", gjson.GetBytes(prepared, "quality").String())
	require.EqualValues(t, 1, gjson.GetBytes(prepared, "n").Int())

	ignoredMask := strings.Replace(body, `"mask":{"image_url":`, `"mask":{"url":`, 1)
	prepared, err = NormalizeOpenAIImageEditReferencesForLocalFallback([]byte(ignoredMask))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(prepared, "mask").Exists())
}
