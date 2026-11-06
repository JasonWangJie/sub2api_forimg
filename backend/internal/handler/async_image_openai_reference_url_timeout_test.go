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
	"strings"
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
	t                  *testing.T
	requests           []openAIReferenceURLTimeoutRequest
	convertedStatuses  []int
	firstConvertedBody string
	protocolErrors     []string
}

type openAIReferenceURLTimeoutRequest struct {
	accountID         int64
	path, contentType string
	body              []byte
	host              string
}

func (u *openAIReferenceURLTimeoutUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.t.Helper()
	body, err := io.ReadAll(req.Body)
	require.NoError(u.t, err)
	u.requests = append(u.requests, openAIReferenceURLTimeoutRequest{
		accountID: accountID, path: req.URL.Path, contentType: req.Header.Get("Content-Type"), body: body, host: req.URL.Host,
	})
	status, response := http.StatusOK, `{"data":[{"b64_json":"`+durableAsyncImageOnePixelPNG+`"}]}`
	protocolCode, protocolMessage := "", ""
	if req.URL.Scheme == "https" && req.URL.Host == "api.openai.com" && strings.HasPrefix(req.Header.Get("Content-Type"), "multipart/") {
		protocolCode, protocolMessage = validateOfficialOpenAIReferenceMultipart(body, req.Header.Get("Content-Type"))
	}
	if protocolCode != "" {
		u.protocolErrors = append(u.protocolErrors, protocolCode)
		status = http.StatusBadRequest
		errorBody, marshalErr := json.Marshal(map[string]any{"error": map[string]string{
			"type": "invalid_request_error", "code": protocolCode, "param": "image", "message": protocolMessage,
		}})
		require.NoError(u.t, marshalErr)
		response = string(errorBody)
	} else if len(u.requests) == 1 {
		status, response = http.StatusBadRequest, openAIReferenceURLTimeoutTestError
	} else if convertedIndex := len(u.requests) - 2; convertedIndex < len(u.convertedStatuses) {
		status = u.convertedStatuses[convertedIndex]
		switch status {
		case http.StatusBadRequest:
			response = openAIReferenceURLTimeoutTestError
		case http.StatusBadGateway:
			response = `{"error":{"type":"server_error","code":"execution_unknown","message":"generation outcome is unknown after context deadline exceeded; do not retry"}}`
		case http.StatusOK:
		default:
			response = `{"error":{"type":"server_error","code":"server_error","message":"test backend unavailable"}}`
		}
		if convertedIndex == 0 && u.firstConvertedBody != "" {
			response = u.firstConvertedBody
		}
	}
	return &http.Response{
		StatusCode: status, Body: io.NopCloser(bytes.NewBufferString(response)), Request: req,
		Header: http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {fmt.Sprintf("test-img-%d", len(u.requests))}},
	}, nil
}

// This fake upstream enforces the observed official upload contract before it
// supplies a configured generation outcome. Invalid requests cannot pass merely
// because they are the second or third invocation.
func validateOfficialOpenAIReferenceMultipart(body []byte, contentType string) (string, string) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return "invalid_multipart", "missing multipart boundary"
	}
	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var imageFields []string
	for {
		part, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "invalid_multipart", "cannot read multipart part"
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return "invalid_multipart", "cannot read multipart file"
		}
		if part.FileName() == "" {
			continue
		}
		field := part.FormName()
		if field == "image" || field == "image[]" {
			imageFields = append(imageFields, field)
		} else if field != "mask" {
			return "invalid_multipart", "unexpected file field"
		}
		actualMIME := http.DetectContentType(data)
		if (actualMIME != "image/png" && actualMIME != "image/jpeg" && actualMIME != "image/webp") || part.Header.Get("Content-Type") != actualMIME {
			return "unsupported_file_mimetype", "Invalid file 'image': unsupported mimetype. Supported file formats are 'image/jpeg', 'image/png', and 'image/webp'."
		}
	}
	if len(imageFields) == 0 {
		return "invalid_multipart", "missing image file"
	}
	if len(imageFields) > 1 {
		for _, field := range imageFields {
			if field != "image[]" {
				return "duplicate_parameter", "Duplicate parameter: 'image'. Use the array syntax instead e.g. 'image[]=<value>'."
			}
		}
	}
	return "", ""
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
	const origin, alternate, third = int64(45), int64(46), int64(47)
	const refOne, refTwo = "https://8.8.8.8/reference.png?sig=mock", "https://8.8.8.8/reference.jpg?sig=mock"
	const ignoredRef = "https://8.8.8.8/ignored.png?sig=mock"
	png, jpeg := generatedWorkerImageFixtures(t)
	for _, tc := range []struct {
		name                string
		legacyImages        bool
		duplicateAliases    bool
		originDisabled      bool
		invalidReference    bool
		privateRedirect     bool
		downloadStatus      int
		exhaustedBudget     bool
		finalSend           bool
		workerRetry         bool
		thirdPartyOrigin    bool
		thirdPartyAlternate bool
		convertedStatuses   []int
		firstConvertedBody  string
		wantAccounts        []int64
		wantError           string
		wantStatus          string
	}{
		{name: "eligible original takes priority", wantAccounts: []int64{origin, origin}},
		{name: "legacy mixed images input converts both references", legacyImages: true, wantAccounts: []int64{origin, origin}},
		{name: "duplicate aliases and ignored URL preserve original inputs", duplicateAliases: true, wantAccounts: []int64{origin, origin}},
		{name: "unschedulable original uses alternate", originDisabled: true, wantAccounts: []int64{origin, alternate}},
		{name: "unschedulable original still allows only one alternate send", originDisabled: true, convertedStatuses: []int{http.StatusServiceUnavailable}, wantAccounts: []int64{origin, alternate}, wantStatus: service.AsyncImageTaskStatusFailed},
		{name: "invalid reference stops before sending", invalidReference: true, wantAccounts: []int64{origin}, wantError: "invalid_reference_image"},
		{name: "private reference redirect stops before sending", privateRedirect: true, wantAccounts: []int64{origin}, wantError: "invalid_reference_image"},
		{name: "transient local download failure switches once with original URLs", downloadStatus: http.StatusServiceUnavailable, wantAccounts: []int64{origin, alternate}},
		{name: "failed alternate URL send cannot retry or visit third account", downloadStatus: http.StatusServiceUnavailable, convertedStatuses: []int{http.StatusServiceUnavailable}, wantAccounts: []int64{origin, alternate}, wantStatus: service.AsyncImageTaskStatusFailed},
		{name: "exhausted reference budget stops before download", exhaustedBudget: true, wantAccounts: []int64{origin}, wantError: "upstream_failed"},
		{name: "sixteenth send cannot queue a seventeenth", finalSend: true, wantAccounts: []int64{origin}, wantError: "upstream_failed"},
		{name: "definite 400 on converted request switches once with multipart", convertedStatuses: []int{http.StatusBadRequest}, wantAccounts: []int64{origin, origin, alternate}},
		{name: "failed alternate multipart send cannot retry or visit third account", convertedStatuses: []int{http.StatusBadRequest, http.StatusServiceUnavailable}, wantAccounts: []int64{origin, origin, alternate}, wantStatus: service.AsyncImageTaskStatusFailed},
		{name: "unknown converted outcome cannot send to an alternate", convertedStatuses: []int{http.StatusBadGateway}, wantAccounts: []int64{origin, origin}, wantStatus: service.AsyncImageTaskStatusExecutionUnknown},
		{
			name: "structured 503 timeout on converted request cannot send to an alternate", convertedStatuses: []int{http.StatusServiceUnavailable},
			firstConvertedBody: `{"error":{"type":"server_error","code":"upstream_timeout","message":"upstream timed out"}}`,
			wantAccounts:       []int64{origin, origin}, wantStatus: service.AsyncImageTaskStatusExecutionUnknown,
		},
		{
			name: "moderation detail on converted 400 cannot send to an alternate", convertedStatuses: []int{http.StatusBadRequest},
			firstConvertedBody: `{"error":{"type":"invalid_request_error","code":"invalid_value","message":"request rejected","details":{"reason":"blocked by safety policy; do not retry"}}}`,
			wantAccounts:       []int64{origin, origin}, wantStatus: service.AsyncImageTaskStatusFailed,
		},
		{name: "original 503 switches without pool retry", convertedStatuses: []int{http.StatusServiceUnavailable}, wantAccounts: []int64{origin, origin, alternate}},
		{name: "original cannot recur in another worker attempt", workerRetry: true, convertedStatuses: []int{http.StatusServiceUnavailable}, wantAccounts: []int64{origin, origin, alternate}},
		{name: "third-party original keeps legacy multipart", thirdPartyOrigin: true, wantAccounts: []int64{origin, origin}},
		{name: "third-party rejection normalizes only the official alternate", thirdPartyOrigin: true, convertedStatuses: []int{http.StatusBadRequest}, wantAccounts: []int64{origin, origin, alternate}},
		{name: "official rejection sends one third-party alternate with legacy multipart", thirdPartyAlternate: true, convertedStatuses: []int{http.StatusBadRequest}, wantAccounts: []int64{origin, origin, alternate}},
		{name: "failed third-party alternate cannot retry or visit third account", thirdPartyAlternate: true, convertedStatuses: []int{http.StatusBadRequest, http.StatusServiceUnavailable}, wantAccounts: []int64{origin, origin, alternate}, wantStatus: service.AsyncImageTaskStatusFailed},
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
					if tc.downloadStatus != 0 {
						w.WriteHeader(tc.downloadStatus)
						return
					}
					if tc.privateRedirect {
						http.Redirect(w, req, "https://127.0.0.1/private.jpg", http.StatusFound)
						return
					}
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
			upstream := &openAIReferenceURLTimeoutUpstream{t: t, convertedStatuses: tc.convertedStatuses, firstConvertedBody: tc.firstConvertedBody}
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
				mockOpenAIReferenceURLTimeoutAccount(third, groupID, 3),
			}}
			if tc.thirdPartyOrigin {
				accountRepo.accounts[0].Credentials["base_url"] = "https://upstream.example/v1"
			}
			if tc.thirdPartyAlternate {
				accountRepo.accounts[1].Credentials["base_url"] = "https://upstream.example/v1"
			}
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
			assertOpenAIReferenceURLTimeoutURLs(t, upstream.requests[0], model, prompt, refOne, refTwo)
			require.NotContains(t, string(upstream.requests[0].body), ignoredRef)
			require.Zero(t, firstDownloads.Load())
			require.Zero(t, secondDownloads.Load())
			require.Equalf(t, !tc.exhaustedBudget && !tc.finalSend, first.requeue, "first response ended in %s with attempts %s", repo.task.Status, repo.task.AccountAttempts)
			if first.requeue {
				require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
				require.Equal(t, service.AsyncImageReferenceTransportLocal, *repo.task.ReferenceTransport)
				require.Equal(t, 1, repo.task.ReferenceRetryCount)
				require.Equal(t, 1, repo.task.RetryCount)
				// Reverse priorities after the initial timeout. Preference must come from persisted task history.
				accountRepo.accounts[0].Priority = 4
				accountRepo.accounts[0].Schedulable = !tc.originDisabled
				for i := 0; i < 4; i++ {
					next := worker.processAsyncImageTask(ctx, task.TaskID)
					if next.requeue && len(upstream.requests) >= 2 && tc.downloadStatus == 0 {
						// Official normalization belongs to the selected destination,
						// not to the snapshot another provider will consume later.
						assertOpenAIReferenceURLTimeoutLegacySnapshot(t, repo.task, cipher, model, png, jpeg)
					}
					if !next.requeue {
						break
					}
					// Prefer the original in ordinary scheduling on subsequent claims. It must still stay excluded.
					accountRepo.accounts[0].Priority = 1
				}
			}
			require.False(t, worker.processAsyncImageTask(ctx, task.TaskID).requeue, "a terminal task cannot send again")
			require.Zero(t, otherDownloads.Load())
			require.Empty(t, upstream.protocolErrors, "official sends must satisfy the upload contract before generation outcomes are simulated")
			var actualAccounts []int64
			for _, request := range upstream.requests {
				actualAccounts = append(actualAccounts, request.accountID)
			}
			require.Equal(t, tc.wantAccounts, actualAccounts)
			for index, request := range upstream.requests {
				if index > 0 {
					if tc.downloadStatus != 0 {
						assertOpenAIReferenceURLTimeoutURLs(t, request, model, prompt, refOne, refTwo)
					} else {
						assertOpenAIReferenceURLTimeoutMultipart(t, request, model, prompt, png, jpeg)
					}
				}
			}
			if first.requeue {
				require.Equal(t, int32(1), firstDownloads.Load())
				require.Equal(t, int32(1), secondDownloads.Load())
			}
			if tc.wantError != "" {
				require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
				require.NotNil(t, repo.task.ErrorCode)
				require.Equal(t, tc.wantError, *repo.task.ErrorCode)
				require.Zero(t, billingRepo.charges)
			} else if tc.wantStatus != "" {
				require.Equal(t, tc.wantStatus, repo.task.Status)
				require.Zero(t, billingRepo.charges)
			} else {
				require.Equal(t, service.AsyncImageTaskStatusSucceeded, repo.task.Status)
				require.Equal(t, service.AsyncImageBillingStatusNotBillable, repo.task.BillingStatus)
				require.Len(t, repo.results, 1)
			}
		})
	}
}

func assertOpenAIReferenceURLTimeoutLegacySnapshot(t *testing.T, task *service.AsyncImageTask, cipher resultFetchTestCipher, model string, png, jpeg []byte) {
	t.Helper()
	plain, err := cipher.Decrypt(string(task.RequestPayload))
	require.NoError(t, err)
	var payload durableAsyncImagePayload
	require.NoError(t, json.Unmarshal([]byte(plain), &payload))
	require.NotNil(t, payload.Prepared)
	require.Equal(t, model, payload.Prepared.Model)
	_, params, err := mime.ParseMediaType(payload.Prepared.ContentType)
	require.NoError(t, err)
	form, err := multipart.NewReader(bytes.NewReader(payload.Prepared.Body), params["boundary"]).ReadForm(1 << 20)
	require.NoError(t, err)
	defer form.RemoveAll()
	require.Len(t, form.File, 1)
	require.Len(t, form.File["image"], 2)
	for i, want := range [][]byte{png, jpeg} {
		require.Equal(t, "application/octet-stream", form.File["image"][i].Header.Get("Content-Type"))
		file, err := form.File["image"][i].Open()
		require.NoError(t, err)
		got, err := io.ReadAll(file)
		require.NoError(t, err)
		require.NoError(t, file.Close())
		require.Equal(t, want, got)
	}
}

func assertOpenAIReferenceURLTimeoutURLs(t *testing.T, req openAIReferenceURLTimeoutRequest, model, prompt, refOne, refTwo string) {
	t.Helper()
	require.Equal(t, "application/json", req.contentType)
	require.Equal(t, "/v1/images/edits", req.path)
	require.Equal(t, model, gjson.GetBytes(req.body, "model").String())
	require.Equal(t, prompt, gjson.GetBytes(req.body, "prompt").String())
	require.Equal(t, "3840x2160", gjson.GetBytes(req.body, "size").String())
	require.Equal(t, "high", gjson.GetBytes(req.body, "quality").String())
	require.Equal(t, int64(1), gjson.GetBytes(req.body, "n").Int())
	require.Contains(t, string(req.body), refOne)
	require.Contains(t, string(req.body), refTwo)
}

func mockOpenAIReferenceURLTimeoutAccount(id, groupID int64, priority int) service.Account {
	return service.Account{
		ID: id, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Priority: priority, Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "mock-token", "base_url": "https://api.openai.com/v1",
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
	field := "image"
	if req.host == "api.openai.com" {
		field = "image[]"
	}
	require.Len(t, form.File[field], 2)
	for i, want := range [][]byte{png, jpeg} {
		wantMIME := "application/octet-stream"
		if req.host == "api.openai.com" {
			wantMIME = http.DetectContentType(want)
		}
		require.Equal(t, wantMIME, form.File[field][i].Header.Get("Content-Type"))
		file, err := form.File[field][i].Open()
		require.NoError(t, err)
		data, err := io.ReadAll(file)
		require.NoError(t, err)
		require.NoError(t, file.Close())
		require.Equal(t, want, data)
	}
	require.NotContains(t, string(req.body), "https://8.8.8.8")
}
