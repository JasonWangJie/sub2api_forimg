//go:build unit

package handler

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const openAIReferenceURLTimeoutTestError = `{"error":{"type":"invalid_request_error","code":"invalid_value","param":"url","message":"Unable to download content from the provided URL before the timeout. Check that the URL is publicly accessible and responds promptly, or upload the file and provide a file_id instead."}}`

type openAIReferenceURLTimeoutUpstream struct {
	service.HTTPUpstream
	t                 *testing.T
	requests          []openAIReferenceURLTimeoutRequest
	convertedStatuses []int
}

type openAIReferenceURLTimeoutRequest struct {
	accountID         int64
	path, contentType string
	body              []byte
}

func (u *openAIReferenceURLTimeoutUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.t.Helper()
	body, err := io.ReadAll(req.Body)
	require.NoError(u.t, err)
	u.requests = append(u.requests, openAIReferenceURLTimeoutRequest{accountID, req.URL.Path, req.Header.Get("Content-Type"), body})
	status, response := http.StatusOK, `{"data":[{"b64_json":"`+durableAsyncImageOnePixelPNG+`"}]}`
	if len(u.requests) == 1 {
		status, response = http.StatusBadRequest, openAIReferenceURLTimeoutTestError
	} else if convertedIndex := len(u.requests) - 2; convertedIndex < len(u.convertedStatuses) {
		status = u.convertedStatuses[convertedIndex]
		if status == http.StatusBadRequest {
			response = openAIReferenceURLTimeoutTestError
		} else if status != http.StatusOK {
			response = `{"error":{"type":"server_error","code":"server_error","message":"test backend unavailable"}}`
		}
	}
	return &http.Response{
		StatusCode: status, Body: io.NopCloser(bytes.NewBufferString(response)), Request: req,
		Header: http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {fmt.Sprintf("test-img-%d", len(u.requests))}},
	}, nil
}

type openAIReferenceURLTimeoutKeyRepo struct{ generatedWorkerKeyRepo }

func (r *openAIReferenceURLTimeoutKeyRepo) GetByID(ctx context.Context, id int64) (*service.APIKey, error) {
	return r.GetByIDIncludeDeleted(ctx, id)
}

type openAIReferenceURLTimeoutRepo struct{ *referenceLocalFallbackRepo }

func (r *openAIReferenceURLTimeoutRepo) RecordAsyncImageUpstreamSuccess(_ context.Context, p service.RecordAsyncImageUpstreamSuccessParams) (*service.AsyncImageTask, error) {
	if r.task.Version != p.ExpectedVersion || r.task.Status != service.AsyncImageTaskStatusInvoking {
		return nil, service.ErrAsyncImageInvalidTransition
	}
	r.task.Status, r.task.Version = service.AsyncImageTaskStatusUpstreamSucceeded, r.task.Version+1
	r.task.AccountID, r.task.BillingRequestID = &p.AccountID, &p.BillingRequestID
	r.task.BillingPayload, r.task.ImageCount = p.BillingPayload, p.ImageCount
	r.task.AccountAttempts, r.task.AttemptedAccountIDs = p.AccountAttempts, p.AttemptedAccountIDs
	r.staging = p.StagingObjects
	return r.GetAsyncImageTaskByTaskID(context.Background(), p.TaskID)
}

func (r *openAIReferenceURLTimeoutRepo) SaveAsyncImagePreparedRequest(_ context.Context, _ string, version int64, encrypted []byte) error {
	if r.task.Version != version || r.task.Status != service.AsyncImageTaskStatusInvoking {
		return service.ErrAsyncImageInvalidTransition
	}
	r.task.RequestPayload = append([]byte(nil), encrypted...)
	return nil
}

func TestAsyncImageOpenAIReferenceURLTimeoutFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const model, prompt = "gpt-image-2.5-flare", "Render a moonlit observatory using the silhouette and colors of the two references."
	const origin, alternate = int64(45), int64(46)
	const refOne, refTwo = "https://8.8.8.8/reference.png?sig=mock", "https://8.8.8.8/reference.jpg?sig=mock"
	const ignoredRef = "https://8.8.8.8/ignored.png?sig=mock"
	png, jpeg := generatedWorkerImageFixtures(t)
	for _, tc := range []struct {
		name              string
		legacyImages      bool
		duplicateAliases  bool
		originDisabled    bool
		invalidReference  bool
		exhaustedBudget   bool
		finalSend         bool
		workerRetry       bool
		convertedStatuses []int
		wantAccounts      []int64
		wantError         string
	}{
		{name: "eligible original takes priority", wantAccounts: []int64{origin, origin}},
		{name: "legacy mixed images input converts both references", legacyImages: true, wantAccounts: []int64{origin, origin}},
		{name: "duplicate aliases and ignored URL preserve original inputs", duplicateAliases: true, wantAccounts: []int64{origin, origin}},
		{name: "unschedulable original uses alternate", originDisabled: true, wantAccounts: []int64{origin, alternate}},
		{name: "invalid reference stops before sending", invalidReference: true, wantAccounts: []int64{origin}, wantError: "invalid_reference_image"},
		{name: "exhausted reference budget stops before download", exhaustedBudget: true, wantAccounts: []int64{origin}, wantError: "upstream_failed"},
		{name: "sixteenth send cannot queue a seventeenth", finalSend: true, wantAccounts: []int64{origin}, wantError: "upstream_failed"},
		{name: "same error on converted request does not requeue", convertedStatuses: []int{http.StatusBadRequest}, wantAccounts: []int64{origin, origin}, wantError: "upstream_failed"},
		{name: "original 503 switches without pool retry", convertedStatuses: []int{http.StatusServiceUnavailable}, wantAccounts: []int64{origin, origin, alternate}},
		{name: "original cannot recur in another worker attempt", workerRetry: true, convertedStatuses: []int{http.StatusServiceUnavailable}, wantAccounts: []int64{origin, origin, alternate}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var firstDownloads, secondDownloads, otherDownloads atomic.Int32
			mockOpenAIReferenceURLTimeoutTLS(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/reference.png":
					firstDownloads.Add(1)
					w.Header().Set("Content-Type", "image/png")
					_, _ = w.Write(png)
				case "/reference.jpg":
					secondDownloads.Add(1)
					w.Header().Set("Content-Type", "image/jpeg")
					if tc.invalidReference {
						_, _ = w.Write([]byte("invalid JPEG bytes"))
					} else {
						_, _ = w.Write(jpeg)
					}
				default:
					otherDownloads.Add(1)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			upstream := &openAIReferenceURLTimeoutUpstream{t: t, convertedStatuses: tc.convertedStatuses}
			mode, size, ratio := service.AsyncImageReferenceTransportPassthroughFallbackLocal, "4K", "16:9"
			groupID, price := int64(3), 0.04
			group := &service.Group{
				ID: groupID, Hydrated: true, Status: service.StatusActive, Platform: service.PlatformOpenAI,
				AllowImageGeneration: true, AllowAsyncImageGeneration: true, RateMultiplier: 1,
				ImagePrice1K: &price, ImagePrice2K: &price, ImagePrice4K: &price,
			}
			key := &service.APIKey{ID: 7, UserID: 9, Status: service.StatusActive, GroupID: &groupID, Group: group,
				User: &service.User{ID: 9, Status: service.StatusActive, Concurrency: 1, Balance: 100}}
			fields := map[string]any{
				"model": model, "prompt": prompt, "resolution": size, "aspect_ratio": ratio,
				"quality": "high", "n": 1,
			}
			if tc.legacyImages {
				fields["images"] = []any{refOne, map[string]string{"image_url": refTwo}}
			} else if tc.duplicateAliases {
				fields["image_urls"] = []string{refOne}
				fields["images"] = []any{refOne, map[string]string{"image_url": refTwo}, map[string]string{"url": ignoredRef}}
			} else {
				fields["image_urls"] = []string{refOne, refTwo}
			}
			body, err := json.Marshal(fields)
			require.NoError(t, err)
			payload, err := json.Marshal(durableAsyncImagePayload{SourcePath: "/v1/images/generations_oa", ContentType: "application/json", Body: body})
			require.NoError(t, err)
			cipher := resultFetchTestCipher{}
			encrypted, err := cipher.Encrypt(string(payload))
			require.NoError(t, err)
			task := &service.AsyncImageTask{
				TaskID: "asyncimg_test_openai_url_timeout", Version: 1, UserID: key.UserID, APIKeyID: key.ID, GroupID: groupID,
				Platform: service.PlatformOpenAI, Model: model, Protocol: service.AsyncImageProtocolBB, RequestType: service.AsyncImageRequestTypeImageToImage,
				Status: service.AsyncImageTaskStatusQueued, BillingStatus: service.AsyncImageBillingStatusPending, SubmittedAt: time.Now().UTC(),
				RequestedImageSize: &size, AspectRatio: &ratio, ReferenceTransport: &mode, RequestHash: "simulated-input", RequestPayload: []byte(encrypted),
			}
			if tc.finalSend {
				var attempts []service.AsyncImageAccountAttempt
				for i := 0; i < 15; i++ {
					id := fmt.Sprintf("prior-send-%d", i)
					attempts = append(attempts, service.AsyncImageAccountAttempt{
						AccountID: origin, Status: service.AsyncImageAccountAttemptInvoked,
						EventID: id, AttemptID: id, AttemptedAt: task.SubmittedAt,
					})
				}
				task.AccountAttempts, task.AttemptedAccountIDs = service.MergeAsyncImageAccountAttempts(nil, attempts)
				require.Equal(t, 1, asyncImageRemainingUpstreamAttempts(task))
			}
			base := &generatedWorkerResultRepo{task: task}
			repo := &openAIReferenceURLTimeoutRepo{&referenceLocalFallbackRepo{base}}
			root := t.TempDir()
			const localURL = "https://images.example.test"
			local, err := service.NewLocalImageStorageWithURLOptions(root, localURL, "", "", nil)
			require.NoError(t, err)
			storage := service.NewImageStorageSettingService(nil, nil, nil,
				func(context.Context, *config.ImageStorageConfig) (service.ImageStorage, error) { return local, nil },
				config.ImageStorageConfig{
					Enabled: true, Backend: config.ImageStorageBackendLocal, Provider: config.ImageStorageProviderLocal,
					Prefix: "images/", Local: config.ImageStorageLocalConfig{DataDir: root, LocalURL: localURL},
				}, config.AsyncImageConfig{
					StorageRetryAttempts: 3, BillingRetryAttempts: 3, RetryBackoffSeconds: 1,
					OpenAIReferenceTransportMode: mode, ReferenceFetchMaxRetries: 2, UpstreamTransientMaxRetries: 3, TotalMaxRetries: 16,
					DownloadTimeoutSeconds: 3, MaxReferenceImages: 2,
				})
			worker := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo), storage: storage, encryptor: cipher}
			groupRepo := &generatedWorkerGroupRepo{group: group}
			worker.apiKeys = service.NewAPIKeyService(&openAIReferenceURLTimeoutKeyRepo{generatedWorkerKeyRepo{key: key}}, nil, groupRepo, nil, nil, nil, nil)
			accountRepo := openAIImagesFailoverAccountRepo{accounts: []service.Account{
				mockOpenAIReferenceURLTimeoutAccount(origin, groupID, 1), mockOpenAIReferenceURLTimeoutAccount(alternate, groupID, 2),
			}}
			worker.accounts = service.NewAccountService(accountRepo, groupRepo)
			cfg := &config.Config{RunMode: config.RunModeSimple}
			billingRepo := &generatedWorkerBillingRepo{}
			billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billingCache.Stop)
			gateway := service.NewOpenAIGatewayService(accountRepo, &generatedWorkerUsageRepo{}, billingRepo, nil, nil, nil, nil, cfg,
				nil, nil, service.NewBillingService(cfg, nil), nil, billingCache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			worker.openAI = NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache,
				worker.apiKeys, nil, nil, nil, nil, cfg)
			worker.openAI.maxAccountSwitches = 3
			if tc.workerRetry {
				worker.openAI.maxAccountSwitches = 0 // Let the worker own retry after the converted 503.
			}

			ctx := context.Background()
			if tc.exhaustedBudget {
				runtime, err := worker.storage.RuntimeConfig(ctx)
				require.NoError(t, err)
				task.ReferenceRetryCount = runtime.ReferenceFetchMaxRetries
			}
			first := worker.processAsyncImageTask(ctx, task.TaskID)
			require.Len(t, upstream.requests, 1, "the first invocation must send the URL request only once")
			initial := upstream.requests[0]
			require.Equal(t, "application/json", initial.contentType)
			require.Equal(t, "/v1/images/edits", initial.path)
			require.Equal(t, model, gjson.GetBytes(initial.body, "model").String())
			require.Equal(t, "3840x2160", gjson.GetBytes(initial.body, "size").String())
			require.Contains(t, string(initial.body), refOne)
			require.Contains(t, string(initial.body), refTwo)
			require.NotContains(t, string(initial.body), ignoredRef)
			require.Zero(t, firstDownloads.Load())
			require.Zero(t, secondDownloads.Load())
			require.Equalf(t, !tc.exhaustedBudget && !tc.finalSend, first.requeue, "first response ended in %s with attempts %s", repo.task.Status, repo.task.AccountAttempts)
			if first.requeue {
				require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
				require.Equal(t, service.AsyncImageReferenceTransportLocal, *repo.task.ReferenceTransport)
				require.Equal(t, 1, repo.task.ReferenceRetryCount)
				require.Equal(t, 1, repo.task.RetryCount)
				// Reverse priorities after the initial timeout. Preference must come from persisted task history.
				accountRepo.accounts[0].Priority = 3
				accountRepo.accounts[0].Schedulable = !tc.originDisabled
				require.Equal(t, tc.workerRetry, worker.processAsyncImageTask(ctx, task.TaskID).requeue)
				if tc.workerRetry {
					require.Len(t, upstream.requests, 2, "pool retry cannot resend the converted request to the original account")
					require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
					accountRepo.accounts[0].Priority = 1
					require.False(t, worker.processAsyncImageTask(ctx, task.TaskID).requeue)
				}
				require.Equal(t, int32(1), firstDownloads.Load())
				require.Equal(t, int32(1), secondDownloads.Load())
			}
			require.False(t, worker.processAsyncImageTask(ctx, task.TaskID).requeue, "a terminal task cannot send again")
			require.Zero(t, otherDownloads.Load())
			require.Len(t, upstream.requests, len(tc.wantAccounts))
			for index, request := range upstream.requests {
				require.Equal(t, tc.wantAccounts[index], request.accountID)
				if index > 0 {
					assertOpenAIReferenceURLTimeoutMultipart(t, request, model, prompt, png, jpeg)
				}
			}
			if tc.wantError != "" {
				require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
				require.NotNil(t, repo.task.ErrorCode)
				require.Equal(t, tc.wantError, *repo.task.ErrorCode)
				require.Zero(t, billingRepo.charges)
			} else {
				require.Equal(t, service.AsyncImageTaskStatusSucceeded, repo.task.Status)
				require.Equal(t, service.AsyncImageBillingStatusNotBillable, repo.task.BillingStatus)
				require.Len(t, repo.results, 1)
			}
		})
	}
}

func mockOpenAIReferenceURLTimeoutAccount(id, groupID int64, priority int) service.Account {
	return service.Account{
		ID: id, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Priority: priority, Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "mock-token", "base_url": "https://upstream.example/v1",
			"pool_mode": true, "pool_mode_retry_count": 3, "pool_mode_retry_status_codes": []any{400, 503},
		},
		AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
	}
}

func mockOpenAIReferenceURLTimeoutTLS(t *testing.T, handler http.Handler) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	original := http.DefaultTransport
	tlsConfig := server.Client().Transport.(*http.Transport).TLSClientConfig
	http.DefaultTransport = &http.Transport{DialTLSContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		if address != "8.8.8.8:443" {
			return nil, fmt.Errorf("unexpected reference host: %s", address)
		}
		return (&tls.Dialer{Config: tlsConfig}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}}
	t.Cleanup(func() {
		http.DefaultTransport = original
		server.Close()
	})
}

func assertOpenAIReferenceURLTimeoutMultipart(t *testing.T, req openAIReferenceURLTimeoutRequest, model, prompt string, png, jpeg []byte) {
	t.Helper()
	require.Equal(t, "/v1/images/edits", req.path)
	mediaType, params, err := mime.ParseMediaType(req.contentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	form, err := multipart.NewReader(bytes.NewReader(req.body), params["boundary"]).ReadForm(1 << 20)
	require.NoError(t, err)
	defer form.RemoveAll()
	require.Equal(t, map[string][]string{
		"model": {model}, "prompt": {prompt}, "size": {"3840x2160"}, "quality": {"high"}, "n": {"1"},
	}, form.Value)
	require.Len(t, form.File, 1)
	require.Len(t, form.File["image"], 2)
	for i, want := range [][]byte{png, jpeg} {
		file, err := form.File["image"][i].Open()
		require.NoError(t, err)
		data, err := io.ReadAll(file)
		require.NoError(t, err)
		require.NoError(t, file.Close())
		require.Equal(t, want, data)
	}
	require.NotContains(t, string(req.body), "https://8.8.8.8")
}
