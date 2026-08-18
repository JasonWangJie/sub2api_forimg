package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const durableAsyncImageOnePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestAsyncImagePublicStatus(t *testing.T) {
	cfg := service.AsyncImageRuntimeConfig{StorageRetryAttempts: 3, BillingRetryAttempts: 4}
	tests := []struct {
		name          string
		status        string
		billingStatus string
		retries       int
		want          string
	}{
		{"queued", service.AsyncImageTaskStatusQueued, service.AsyncImageBillingStatusPending, 0, "queued"},
		{"invoking", service.AsyncImageTaskStatusInvoking, service.AsyncImageBillingStatusPending, 0, "processing"},
		{"upstream_succeeded", service.AsyncImageTaskStatusUpstreamSucceeded, service.AsyncImageBillingStatusPrepared, 0, "processing"},
		{"storage_retrying", service.AsyncImageTaskStatusStorageFailed, service.AsyncImageBillingStatusPrepared, 2, "processing"},
		{"storage_exhausted", service.AsyncImageTaskStatusStorageFailed, service.AsyncImageBillingStatusPrepared, 3, "failed"},
		{"billing_retrying", service.AsyncImageTaskStatusBillingFailed, service.AsyncImageBillingStatusFailed, 3, "processing"},
		{"billing_exhausted", service.AsyncImageTaskStatusBillingFailed, service.AsyncImageBillingStatusFailed, 4, "failed"},
		{"execution_unknown", service.AsyncImageTaskStatusExecutionUnknown, service.AsyncImageBillingStatusPending, 0, "failed"},
		{"succeeded_and_billed", service.AsyncImageTaskStatusSucceeded, service.AsyncImageBillingStatusSucceeded, 0, "succeeded"},
		{"succeeded_not_billable", service.AsyncImageTaskStatusSucceeded, service.AsyncImageBillingStatusNotBillable, 0, "succeeded"},
		{"succeeded_but_billing_prepared", service.AsyncImageTaskStatusSucceeded, service.AsyncImageBillingStatusPrepared, 0, "processing"},
		{"succeeded_but_billing_failed", service.AsyncImageTaskStatusSucceeded, service.AsyncImageBillingStatusFailed, 0, "processing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, asyncImagePublicStatus(&service.AsyncImageTask{
				Status: test.status, BillingStatus: test.billingStatus, RetryCount: test.retries,
				StorageRetryCount: test.retries, BillingRetryCount: test.retries,
			}, cfg))
		})
	}
}

func TestAsyncImagePublicQueriesDoNotReleaseResultsBeforeBillingSucceeds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &DurableAsyncImageHandler{}
	details := &service.AsyncImageTaskDetails{
		Task: &service.AsyncImageTask{
			TaskID: "asyncimg_unbilled", Status: service.AsyncImageTaskStatusSucceeded,
			BillingStatus: service.AsyncImageBillingStatusPrepared, Progress: 95,
		},
		Results: []service.AsyncImageResult{{TaskID: "asyncimg_unbilled", ImageIndex: 0, ObjectKey: "must-not-leak.png"}},
	}
	cfg := service.AsyncImageRuntimeConfig{}

	bbRecorder := httptest.NewRecorder()
	bbContext, _ := gin.CreateTestContext(bbRecorder)
	h.writeBBQuery(bbContext, details, cfg)
	require.Equal(t, http.StatusOK, bbRecorder.Code)
	require.JSONEq(t, `{"status":"processing","task_id":"asyncimg_unbilled"}`, bbRecorder.Body.String())

	scRecorder := httptest.NewRecorder()
	scContext, _ := gin.CreateTestContext(scRecorder)
	h.writeSCQuery(scContext, details, cfg)
	require.Equal(t, http.StatusOK, scRecorder.Code)
	require.NotContains(t, scRecorder.Body.String(), "must-not-leak")
	require.JSONEq(t, `{"status":"processing","task_id":"asyncimg_unbilled"}`, scRecorder.Body.String())
}

func TestAsyncImageAbsoluteURL(t *testing.T) {
	require.Equal(t, "https://api.example.com/v1/tasks_sc/task_1", asyncImageAbsoluteURL("https://api.example.com/", "/v1/tasks_sc/task_1"))
	require.Equal(t, "/v1/tasks_sc/task_1", asyncImageAbsoluteURL("", "/v1/tasks_sc/task_1"))
}

func TestAsyncImagePromptPreviewRedactsAndCanBeDisabled(t *testing.T) {
	cfg := service.AsyncImageRuntimeConfig{PromptPreviewEnabled: true, PromptPreviewMaxChars: 80}
	preview := asyncImagePromptPreview("draw this api_key=sk-secret-value with clean lines", cfg)
	require.NotContains(t, preview, "sk-secret-value")
	require.NotEmpty(t, preview)
	require.Empty(t, asyncImagePromptPreview("private prompt", service.AsyncImageRuntimeConfig{}))
}

func TestExtractOpenAIAsyncImageOutputsB64(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"data": []any{map[string]any{"b64_json": durableAsyncImageOnePixelPNG}},
	})
	require.NoError(t, err)

	outputs, err := extractOpenAIAsyncImageOutputs(context.Background(), body, service.AsyncImageRuntimeConfig{DownloadMaxBytes: 1 << 20})
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Equal(t, "image/png", outputs[0].ContentType)
	require.Equal(t, 1, outputs[0].Width)
	require.Equal(t, 1, outputs[0].Height)
	require.NotEmpty(t, outputs[0].Checksum)
}

func TestWriteBBQueryFailedIncludesTaskIDAndFailReason(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &DurableAsyncImageHandler{}
	message := "upstream image generation failed"
	code := "upstream_failed"
	details := &service.AsyncImageTaskDetails{
		Task: &service.AsyncImageTask{
			TaskID:       "asyncimg_failed",
			Protocol:     service.AsyncImageProtocolSC,
			Platform:     service.PlatformGemini,
			Status:       service.AsyncImageTaskStatusFailed,
			ErrorCode:    &code,
			ErrorMessage: &message,
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	h.writeBBQuery(ctx, details, service.AsyncImageRuntimeConfig{})
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"status":"failed","task_id":"asyncimg_failed","fail_reason":"upstream image generation failed"}`, recorder.Body.String())
}

func TestWriteAsyncImageSubmitResponsesAlignGeminiSCWithOpenAI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &DurableAsyncImageHandler{}
	cfg := service.AsyncImageRuntimeConfig{PublicBaseURL: "https://api.example.com"}

	openaiRecorder := httptest.NewRecorder()
	openaiContext, _ := gin.CreateTestContext(openaiRecorder)
	h.writeSubmitResponse(openaiContext, service.AsyncImageProtocolBB, &service.AsyncImageTask{
		TaskID: "asyncimg_oa", Platform: service.PlatformOpenAI,
	}, cfg)
	require.Equal(t, http.StatusAccepted, openaiRecorder.Code)
	require.JSONEq(t, `{"task_id":"asyncimg_oa","query_url":"https://api.example.com/v1/images/tasks_async/asyncimg_oa"}`, openaiRecorder.Body.String())

	scRecorder := httptest.NewRecorder()
	scContext, _ := gin.CreateTestContext(scRecorder)
	h.writeSubmitResponse(scContext, service.AsyncImageProtocolSC, &service.AsyncImageTask{
		TaskID: "asyncimg_sc", Platform: service.PlatformGemini,
	}, cfg)
	require.Equal(t, http.StatusAccepted, scRecorder.Code)
	require.JSONEq(t, `{"task_id":"asyncimg_sc","query_url":"https://api.example.com/v1/images/tasks_async/asyncimg_sc"}`, scRecorder.Body.String())
}

func TestAsyncImageFailureMessageExecutionTimeout(t *testing.T) {
	code := "execution_timeout"
	message := "image generation timed out after 20m0s"
	require.Equal(t, message, asyncImageFailureMessage(&service.AsyncImageTask{
		Status:       service.AsyncImageTaskStatusFailed,
		ErrorCode:    &code,
		ErrorMessage: &message,
	}))
	require.Equal(t, "image generation timed out", asyncImageFailureMessage(&service.AsyncImageTask{
		Status:    service.AsyncImageTaskStatusFailed,
		ErrorCode: &code,
	}))
}

func TestAsyncImageFailureMessageLocalCapacityExhausted(t *testing.T) {
	code := "local_capacity_exhausted"
	require.Equal(t, "image generation could not be scheduled because no account capacity became available", asyncImageFailureMessage(&service.AsyncImageTask{
		Status:    service.AsyncImageTaskStatusFailed,
		ErrorCode: &code,
	}))
}
