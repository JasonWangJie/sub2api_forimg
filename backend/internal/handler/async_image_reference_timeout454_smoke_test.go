//go:build unit

package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Only the socket destination changes. The actual gateway builds the request,
// and http.Client serializes it over loopback to the test HTTP server.
type referenceTimeout454HTTPUpstream struct {
	service.HTTPUpstream
	client *http.Client
	target *url.URL
}

func (u *referenceTimeout454HTTPUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	if req.URL.Host != "api.openai.com" {
		return nil, fmt.Errorf("unexpected smoke upstream host %q", req.URL.Host)
	}
	local := req.Clone(req.Context())
	endpoint := *req.URL
	endpoint.Scheme, endpoint.Host = u.target.Scheme, u.target.Host
	local.URL, local.Host = &endpoint, u.target.Host
	local.Header.Set("X-Smoke-Account-ID", strconv.FormatInt(accountID, 10))
	return u.client.Do(local)
}

type referenceTimeout454SmokeRepo struct {
	*openAIReferenceURLTimeoutRepo
	snapshotWrites int
	snapshot       []byte
}

func (r *referenceTimeout454SmokeRepo) SaveAsyncImagePreparedRequest(ctx context.Context, taskID string, version int64, encrypted []byte) error {
	if err := r.openAIReferenceURLTimeoutRepo.SaveAsyncImagePreparedRequest(ctx, taskID, version, encrypted); err != nil {
		return err
	}
	r.snapshotWrites++
	r.snapshot = append([]byte(nil), encrypted...)
	return nil
}

func TestAsyncImageReferenceTimeout454HTTPSmoke(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const model = "gpt-image-2.5-flare"
	const prompt = "Apply the reference colors to a moonlit observatory."
	const refOne, refTwo = "https://8.8.8.8/reference.png?sig=synthetic", "https://8.8.8.8/reference.jpg?sig=synthetic"
	for _, scenario := range []struct {
		name, mode       string
		accounts         int
		alwaysReject     bool
		totalExhausted   bool
		referenceBlocked bool
		wantAccounts     []int64
		wantStatus       string
	}{
		{"hybrid restart downloads files and switches", service.AsyncImageReferenceTransportPassthroughFallbackLocal, 2, false, false, false, []int64{45, 46}, service.AsyncImageTaskStatusSucceeded},
		{"passthrough switches unchanged URL request", service.AsyncImageReferenceTransportPassthrough, 2, false, false, false, []int64{45, 46}, service.AsyncImageTaskStatusSucceeded},
		{"hybrid only account reuses locally loaded files", service.AsyncImageReferenceTransportPassthroughFallbackLocal, 1, false, false, false, []int64{45, 45}, service.AsyncImageTaskStatusSucceeded},
		{"single account persisted upstream budget stops", service.AsyncImageReferenceTransportPassthrough, 1, true, false, false, []int64{45, 45, 45}, service.AsyncImageTaskStatusFailed},
		{"total retry budget stops", service.AsyncImageReferenceTransportPassthroughFallbackLocal, 1, true, true, false, []int64{45}, service.AsyncImageTaskStatusFailed},
		{"reference and upstream retry budgets stop", service.AsyncImageReferenceTransportPassthroughFallbackLocal, 1, true, false, true, []int64{45}, service.AsyncImageTaskStatusFailed},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			png, jpeg := generatedWorkerImageFixtures(t)
			var downloads atomic.Int32
			mockOpenAIReferenceURLTimeoutTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				downloads.Add(1)
				switch r.URL.Path {
				case "/reference.png":
					w.Header().Set("Content-Type", "image/png")
					_, _ = w.Write(png)
				case "/reference.jpg":
					w.Header().Set("Content-Type", "image/jpeg")
					_, _ = w.Write(jpeg)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			var mu sync.Mutex
			var received []openAIReferenceURLTimeoutRequest
			var protocolErrors []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				accountID, _ := strconv.ParseInt(r.Header.Get("X-Smoke-Account-ID"), 10, 64)
				mu.Lock()
				received = append(received, openAIReferenceURLTimeoutRequest{accountID: accountID, host: "api.openai.com", path: r.URL.Path, contentType: r.Header.Get("Content-Type"), body: body})
				sendNumber := len(received)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-ID", fmt.Sprintf("smoke-http-%d", sendNumber))
				if isMultipartContentType(r.Header.Get("Content-Type")) {
					if code, message := validateOfficialOpenAIReferenceMultipart(body, r.Header.Get("Content-Type")); code != "" {
						mu.Lock()
						protocolErrors = append(protocolErrors, code+": "+message)
						mu.Unlock()
						w.WriteHeader(400)
						_, _ = fmt.Fprintf(w, `{"error":{"code":%q,"message":%q}}`, code, message)
						return
					}
				}
				if sendNumber == 1 || scenario.alwaysReject {
					w.WriteHeader(454)
					_, _ = w.Write([]byte(referenceTimeout454Body))
					return
				}
				_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(png))
			}))
			t.Cleanup(server.Close)
			target, err := url.Parse(server.URL)
			require.NoError(t, err)
			transport := &http.Transport{Proxy: nil}
			t.Cleanup(transport.CloseIdleConnections)
			upstream := &referenceTimeout454HTTPUpstream{client: &http.Client{Transport: transport, Timeout: 5 * time.Second}, target: target}
			mode, size, ratio := scenario.mode, "4K", "16:9"
			groupID, price := int64(3), 0.04
			group := &service.Group{ID: groupID, Hydrated: true, Status: service.StatusActive, Platform: service.PlatformOpenAI, AllowImageGeneration: true, AllowAsyncImageGeneration: true, RateMultiplier: 1, ImagePrice1K: &price, ImagePrice2K: &price, ImagePrice4K: &price}
			key := &service.APIKey{ID: 7, UserID: 9, Status: service.StatusActive, GroupID: &groupID, Group: group, User: &service.User{ID: 9, Status: service.StatusActive, Concurrency: 1, Balance: 100}}
			body, err := json.Marshal(map[string]any{"model": model, "prompt": prompt, "resolution": size, "aspect_ratio": ratio, "quality": "high", "n": 1, "image_urls": []string{refOne, refTwo}})
			require.NoError(t, err)
			payload, err := json.Marshal(durableAsyncImagePayload{SourcePath: "/v1/images/generations_oa", ContentType: "application/json", Body: body})
			require.NoError(t, err)
			cipher := resultFetchTestCipher{}
			encrypted, err := cipher.Encrypt(string(payload))
			require.NoError(t, err)
			task := &service.AsyncImageTask{TaskID: "asyncimg_local_http_454", Version: 1, UserID: key.UserID, APIKeyID: key.ID, GroupID: groupID, Platform: service.PlatformOpenAI, Model: model, Protocol: service.AsyncImageProtocolBB, RequestType: service.AsyncImageRequestTypeImageToImage, Status: service.AsyncImageTaskStatusQueued, BillingStatus: service.AsyncImageBillingStatusPending, SubmittedAt: time.Now().UTC(), RequestedImageSize: &size, AspectRatio: &ratio, ReferenceTransport: &mode, RequestHash: "synthetic-http-input", RequestPayload: []byte(encrypted)}
			base := &generatedWorkerResultRepo{task: task}
			repo := &referenceTimeout454SmokeRepo{openAIReferenceURLTimeoutRepo: &openAIReferenceURLTimeoutRepo{&referenceLocalFallbackRepo{base}}}
			root := t.TempDir()
			const localURL = "https://images.example.test"
			local, err := service.NewLocalImageStorageWithURLOptions(root, localURL, "", "", nil)
			require.NoError(t, err)
			upstreamRetries := 2
			if scenario.referenceBlocked {
				upstreamRetries = 0
				task.ReferenceRetryCount = 2
			}
			if scenario.totalExhausted {
				task.RetryCount = 16
			}
			settings := service.NewImageStorageSettingService(nil, nil, nil,
				func(context.Context, *config.ImageStorageConfig) (service.ImageStorage, error) { return local, nil },
				config.ImageStorageConfig{Enabled: true, Backend: config.ImageStorageBackendLocal, Provider: config.ImageStorageProviderLocal, Prefix: "images/", Local: config.ImageStorageLocalConfig{DataDir: root, LocalURL: localURL}},
				config.AsyncImageConfig{StorageRetryAttempts: 3, BillingRetryAttempts: 3, RetryBackoffSeconds: 1, OpenAIReferenceTransportMode: mode, ReferenceFetchMaxRetries: 2, UpstreamTransientMaxRetries: upstreamRetries, TotalMaxRetries: 16, DownloadTimeoutSeconds: 3, MaxReferenceImages: 2})
			groupRepo := &generatedWorkerGroupRepo{group: group}
			billingRepo := &generatedWorkerBillingRepo{}
			// Rebuild every service that could retain selection or reference caches.
			newWorker := func() *DurableAsyncImageHandler {
				worker := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo), storage: settings, encryptor: cipher}
				worker.apiKeys = service.NewAPIKeyService(&openAIReferenceURLTimeoutKeyRepo{generatedWorkerKeyRepo{key: key}}, nil, groupRepo, nil, nil, nil, nil)
				var accounts []service.Account
				for i := 0; i < scenario.accounts; i++ {
					account := mockOpenAIReferenceURLTimeoutAccount(int64(45+i), groupID, i+1)
					account.Credentials["pool_mode_retry_status_codes"] = []any{454}
					accounts = append(accounts, account)
				}
				accountRepo := openAIImagesFailoverAccountRepo{accounts: accounts}
				worker.accounts = service.NewAccountService(accountRepo, groupRepo)
				cfg := &config.Config{RunMode: config.RunModeSimple}
				cache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(cache.Stop)
				gateway := service.NewOpenAIGatewayService(accountRepo, &generatedWorkerUsageRepo{}, billingRepo, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, cache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
				worker.openAI = NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(&fakeConcurrencyCache{}), cache, worker.apiKeys, nil, nil, nil, nil, cfg)
				worker.openAI.maxAccountSwitches = 3
				return worker
			}
			worker := newWorker()
			ctx := context.Background()
			first := worker.processAsyncImageTask(ctx, task.TaskID)
			require.Zero(t, downloads.Load(), "initial hybrid/passthrough request must not locally download references")
			if scenario.mode == service.AsyncImageReferenceTransportPassthroughFallbackLocal && scenario.wantStatus == service.AsyncImageTaskStatusSucceeded {
				require.True(t, first.requeue)
				require.Equal(t, service.AsyncImageTaskStatusQueued, repo.task.Status)
				require.Equal(t, service.AsyncImageReferenceTransportLocal, *repo.task.ReferenceTransport)
				require.Equal(t, 1, repo.task.ReferenceRetryCount)
				require.Equal(t, 1, repo.task.RetryCount)
				require.Equal(t, []byte(encrypted), repo.task.RequestPayload, "the next worker must use the original durable request, not a manually prepared multipart")
				require.Contains(t, string(repo.task.AccountAttempts), `"status_code":454`)
				require.Contains(t, string(repo.task.AccountAttempts), `"provider_error_code":"asset_download_timeout"`)
				worker = newWorker()
			}
			for round := 0; first.requeue && round < 6; round++ {
				first = worker.processAsyncImageTask(ctx, task.TaskID)
			}
			require.False(t, first.requeue, "task must reach a terminal state within persisted budgets")
			require.Equal(t, scenario.wantStatus, repo.task.Status, fmt.Sprint(repo.task.ErrorMessage))
			mu.Lock()
			requests := append([]openAIReferenceURLTimeoutRequest(nil), received...)
			errors := append([]string(nil), protocolErrors...)
			mu.Unlock()
			require.Empty(t, errors)
			var accounts []int64
			for index, request := range requests {
				accounts = append(accounts, request.accountID)
				if index > 0 && scenario.mode == service.AsyncImageReferenceTransportPassthroughFallbackLocal && scenario.wantStatus == service.AsyncImageTaskStatusSucceeded {
					assertOpenAIReferenceURLTimeoutMultipart(t, request, model, prompt, png, jpeg)
				} else {
					assertOpenAIReferenceURLTimeoutURLs(t, request, model, prompt, refOne, refTwo)
				}
			}
			require.Equal(t, scenario.wantAccounts, accounts)
			storedChecksum := "none"
			if scenario.wantStatus == service.AsyncImageTaskStatusSucceeded {
				require.Equal(t, service.AsyncImageBillingStatusNotBillable, repo.task.BillingStatus, "simple mode must not claim a production charge")
				require.Len(t, repo.results, 1)
				result := repo.results[0]
				stored, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(result.ObjectKey)))
				require.NoError(t, err)
				require.Equal(t, png, stored)
				checksum := sha256.Sum256(stored)
				require.Equal(t, hex.EncodeToString(checksum[:]), result.Checksum)
				storedChecksum = result.Checksum
				require.Equal(t, "image/png", result.ContentType)
				require.Equal(t, int64(len(png)), result.ByteSize)
				if scenario.mode == service.AsyncImageReferenceTransportPassthroughFallbackLocal {
					require.EqualValues(t, 2, downloads.Load())
					require.Equal(t, 1, repo.snapshotWrites)
					plain, err := cipher.Decrypt(string(repo.snapshot))
					require.NoError(t, err)
					var saved durableAsyncImagePayload
					require.NoError(t, json.Unmarshal([]byte(plain), &saved))
					require.NotNil(t, saved.Prepared)
					require.True(t, isMultipartContentType(saved.Prepared.ContentType))
					require.True(t, bytes.Contains(saved.Prepared.Body, png))
				} else {
					require.Zero(t, downloads.Load())
					require.Zero(t, repo.snapshotWrites)
				}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodGet, "/query", nil)
				worker.writeBBQuery(c, &service.AsyncImageTaskDetails{Task: repo.task, Results: repo.results}, service.AsyncImageRuntimeConfig{})
				require.Equal(t, 200, recorder.Code)
				require.Contains(t, recorder.Body.String(), localURL+"/"+result.ObjectKey)
			} else {
				require.Empty(t, repo.results)
				require.NotNil(t, repo.task.ErrorMessage)
				require.Contains(t, *repo.task.ErrorMessage, "HTTP 454")
				if scenario.alwaysReject && !scenario.totalExhausted && !scenario.referenceBlocked {
					require.Equal(t, 2, repo.task.UpstreamRetryCount)
					require.Equal(t, 2, repo.task.RetryCount)
				}
			}
			require.Zero(t, billingRepo.charges)
			require.Empty(t, repo.task.RequestPayload)
			beforeDownloads := downloads.Load()
			for i := 0; i < 2; i++ {
				require.False(t, worker.processAsyncImageTask(ctx, task.TaskID).requeue)
			}
			mu.Lock()
			count := len(received)
			mu.Unlock()
			require.Len(t, requests, count, "terminal delivery must not generate again")
			require.Equal(t, beforeDownloads, downloads.Load())
			t.Logf("real HTTP generation=%v reference GETs=%d local snapshots=%d final=%s retry=%d reference_retry=%d upstream_retry=%d sha256=%s", accounts, downloads.Load(), repo.snapshotWrites, repo.task.Status, repo.task.RetryCount, repo.task.ReferenceRetryCount, repo.task.UpstreamRetryCount, storedChecksum)
		})
	}
}
