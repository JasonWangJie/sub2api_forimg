//go:build unit

package handler

import (
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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type newRetryWorkerAccountRepo struct {
	openAIImagesFailoverAccountRepo
}

func (r newRetryWorkerAccountRepo) ListSchedulableByGroupIDAndPlatforms(_ context.Context, _ int64, platforms []string) ([]service.Account, error) {
	var accounts []service.Account
	for _, platform := range platforms {
		accounts = append(accounts, r.accountsForPlatform(platform)...)
	}
	return accounts, nil
}

func TestAsyncImageNewRetryFullWorkerHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, scenario := range []struct {
		name, platform, mode string
		status, accounts     int
		rejectAll            bool
		readFault            string
	}{
		{"Gemini missing file hybrid rebuild", service.PlatformGemini, service.AsyncImageReferenceTransportPassthroughFallbackLocal, 404, 2, false, ""},
		{"Gemini missing file passthrough", service.PlatformGemini, service.AsyncImageReferenceTransportPassthrough, 404, 2, false, ""},
		{"OpenAI gateway timed-out switches", service.PlatformOpenAI, service.AsyncImageReferenceTransportPassthrough, 504, 2, false, ""},
		{"Gemini processing timeout switches", service.PlatformGemini, service.AsyncImageReferenceTransportPassthrough, 408, 2, false, ""},
		{"Gemini single channel budget stops", service.PlatformGemini, service.AsyncImageReferenceTransportPassthrough, 504, 1, true, ""},
		{"OpenAI single channel budget stops", service.PlatformOpenAI, service.AsyncImageReferenceTransportPassthrough, 408, 1, true, ""},
		{"OpenAI interrupted 503 becomes unknown without failover", service.PlatformOpenAI, service.AsyncImageReferenceTransportPassthrough, 503, 2, false, "read error"},
		{"Gemini truncated 503 becomes unknown without failover", service.PlatformGemini, service.AsyncImageReferenceTransportPassthrough, 503, 2, false, "limit"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			const referenceURL = "https://8.8.8.8/reference.png?sig=synthetic"
			const prompt = "Render an observatory using the reference colors."
			png, _ := generatedWorkerImageFixtures(t)
			var downloads atomic.Int32
			mockOpenAIReferenceURLTimeoutTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				downloads.Add(1)
				w.Header().Set("Content-Type", "image/png")
				_, _ = w.Write(png)
			}))
			var mu sync.Mutex
			var sentAccounts []int64
			var sentBodies [][]byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				id, _ := strconv.ParseInt(r.Header.Get("X-Smoke-Account-ID"), 10, 64)
				mu.Lock()
				sentAccounts = append(sentAccounts, id)
				sentBodies = append(sentBodies, body)
				number := len(sentAccounts)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-ID", fmt.Sprintf("received-http-%d", number))
				if number == 1 || scenario.rejectAll {
					if scenario.readFault != "" {
						body := `{"error":{"code":"upstream_error","message":"Gateway timed-out waiting for processing"}}`
						if scenario.readFault == "read error" {
							w.Header().Set("Content-Length", strconv.Itoa(len(body)+100))
						} else {
							body += strings.Repeat(" ", 8<<20)
						}
						w.WriteHeader(scenario.status)
						_, _ = w.Write([]byte(body))
						return
					}
					w.WriteHeader(scenario.status)
					if scenario.status == 404 {
						_, _ = fmt.Fprintf(w, `{"error":{"code":404,"message":%q}}`, "文件引用不存在: "+referenceURL)
					} else {
						_, _ = w.Write([]byte(`{"error":{"code":"upstream_error","message":"Gateway timed-out waiting for processing"}}`))
					}
					return
				}
				if scenario.platform == service.PlatformOpenAI {
					_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(png))
				} else {
					_, _ = fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":%q}}]}}]}`, base64.StdEncoding.EncodeToString(png))
				}
			}))
			t.Cleanup(server.Close)
			target, err := url.Parse(server.URL)
			require.NoError(t, err)
			transport := &http.Transport{Proxy: nil}
			t.Cleanup(transport.CloseIdleConnections)
			upstream := &referenceTimeout454HTTPUpstream{client: &http.Client{Transport: transport, Timeout: 5 * time.Second}, target: target}
			model := "gpt-image-2.5-flare"
			if scenario.platform == service.PlatformGemini {
				model = "gemini-3-pro-image-preview"
			}
			mode, size, ratio, groupID, price := scenario.mode, "2K", "1:1", int64(3), 0.04
			group := &service.Group{ID: groupID, Hydrated: true, Status: service.StatusActive, Platform: scenario.platform, AllowImageGeneration: true, AllowAsyncImageGeneration: true, RateMultiplier: 1, ImagePrice1K: &price, ImagePrice2K: &price, ImagePrice4K: &price}
			key := &service.APIKey{ID: 7, UserID: 9, Status: service.StatusActive, GroupID: &groupID, Group: group, User: &service.User{ID: 9, Status: service.StatusActive, Concurrency: 1, Balance: 100}}
			body, err := json.Marshal(map[string]any{"model": model, "prompt": prompt, "resolution": size, "aspect_ratio": ratio, "image_urls": []string{referenceURL}, "n": 1})
			require.NoError(t, err)
			payload := durableAsyncImagePayload{SourcePath: "/v1/images/generations_oa", ContentType: "application/json", Body: body}
			if scenario.platform == service.PlatformGemini {
				payload.SourcePath = "/v1/chat/completions_async"
				payload.Body = []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":%q}},{"type":"text","text":%q}]}],"extra_body":{"google":{"image_config":{"image_size":"2K","aspect_ratio":"1:1"}}}}`, model, referenceURL, prompt))
				payload.Normalized, err = service.ParseBBGeminiImageRequest(payload.Body, payload.SourcePath)
				require.NoError(t, err)
			}
			plain, err := json.Marshal(payload)
			require.NoError(t, err)
			cipher := resultFetchTestCipher{}
			encrypted, err := cipher.Encrypt(string(plain))
			require.NoError(t, err)
			task := &service.AsyncImageTask{TaskID: "asyncimg_new_retry_" + scenario.platform, Version: 1, UserID: 9, APIKeyID: 7, GroupID: groupID, Platform: scenario.platform, Model: model, Protocol: service.AsyncImageProtocolBB, RequestType: service.AsyncImageRequestTypeImageToImage, Status: service.AsyncImageTaskStatusQueued, BillingStatus: service.AsyncImageBillingStatusPending, SubmittedAt: time.Now().UTC(), RequestedImageSize: &size, AspectRatio: &ratio, ReferenceTransport: &mode, RequestHash: "synthetic-new-retry", RequestPayload: []byte(encrypted)}
			base := &generatedWorkerResultRepo{task: task}
			repo := &referenceTimeout454SmokeRepo{openAIReferenceURLTimeoutRepo: &openAIReferenceURLTimeoutRepo{&referenceLocalFallbackRepo{base}}}
			root := t.TempDir()
			local, err := service.NewLocalImageStorageWithURLOptions(root, "https://images.example.test", "", "", nil)
			require.NoError(t, err)
			settings := service.NewImageStorageSettingService(nil, nil, nil, func(context.Context, *config.ImageStorageConfig) (service.ImageStorage, error) { return local, nil }, config.ImageStorageConfig{Enabled: true, Backend: config.ImageStorageBackendLocal, Provider: config.ImageStorageProviderLocal, Prefix: "images/", Local: config.ImageStorageLocalConfig{DataDir: root, LocalURL: "https://images.example.test"}}, config.AsyncImageConfig{StorageRetryAttempts: 3, BillingRetryAttempts: 3, RetryBackoffSeconds: 1, OpenAIReferenceTransportMode: mode, GeminiReferenceTransportMode: mode, ReferenceFetchMaxRetries: 2, UpstreamTransientMaxRetries: 2, TotalMaxRetries: 16, DownloadTimeoutSeconds: 3, MaxReferenceImages: 2})
			groupRepo := &generatedWorkerGroupRepo{group: group}
			billing := &generatedWorkerBillingRepo{}
			newWorker := func() *DurableAsyncImageHandler {
				worker := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo), storage: settings, encryptor: cipher}
				worker.apiKeys = service.NewAPIKeyService(&openAIReferenceURLTimeoutKeyRepo{generatedWorkerKeyRepo{key: key}}, nil, groupRepo, nil, nil, nil, nil)
				var accounts []service.Account
				for index := 0; index < scenario.accounts; index++ {
					account := mockOpenAIReferenceURLTimeoutAccount(int64(17+index), groupID, index+1)
					account.Platform = scenario.platform
					account.Credentials["pool_mode_retry_status_codes"] = []any{scenario.status}
					accounts = append(accounts, account)
				}
				accountRepo := newRetryWorkerAccountRepo{openAIImagesFailoverAccountRepo{accounts: accounts}}
				worker.accounts = service.NewAccountService(accountRepo, groupRepo)
				cfg := &config.Config{RunMode: config.RunModeSimple}
				cache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(cache.Stop)
				concurrency := service.NewConcurrencyService(&fakeConcurrencyCache{})
				if scenario.platform == service.PlatformOpenAI {
					gateway := service.NewOpenAIGatewayService(accountRepo, &generatedWorkerUsageRepo{}, billing, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, cache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
					worker.openAI = NewOpenAIGatewayHandler(gateway, concurrency, cache, worker.apiKeys, nil, nil, nil, nil, cfg)
					worker.openAI.maxAccountSwitches = 3
				} else {
					gateway := service.NewGatewayService(accountRepo, groupRepo, &generatedWorkerUsageRepo{}, billing, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, cache, nil, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
					compat := service.NewGeminiMessagesCompatService(nil, nil, nil, nil, nil, nil, upstream, nil, cfg)
					worker.gateway = NewGatewayHandler(gateway, nil, compat, nil, nil, concurrency, cache, nil, worker.apiKeys, nil, nil, nil, nil, cfg, nil)
					worker.gateway.maxAccountSwitchesGemini = 3
				}
				return worker
			}
			worker := newWorker()
			disposition := worker.processAsyncImageTask(context.Background(), task.TaskID)
			if scenario.mode == service.AsyncImageReferenceTransportPassthroughFallbackLocal {
				require.True(t, disposition.requeue)
				require.Equal(t, service.AsyncImageReferenceTransportLocal, *repo.task.ReferenceTransport)
				require.Equal(t, []byte(encrypted), repo.task.RequestPayload)
				require.Zero(t, downloads.Load())
				worker = newWorker()
			}
			for round := 0; disposition.requeue && round < 6; round++ {
				disposition = worker.processAsyncImageTask(context.Background(), task.TaskID)
			}
			require.False(t, disposition.requeue)
			mu.Lock()
			accounts := append([]int64(nil), sentAccounts...)
			bodies := append([][]byte(nil), sentBodies...)
			mu.Unlock()
			wantAccounts := []int64{17, 18}
			if scenario.rejectAll {
				wantAccounts = []int64{17, 17, 17}
			}
			if scenario.readFault != "" {
				wantAccounts = []int64{17}
			}
			require.Equal(t, wantAccounts, accounts)
			require.Contains(t, string(repo.task.AccountAttempts), fmt.Sprintf(`"status_code":%d`, scenario.status))
			if scenario.readFault != "" {
				require.Equal(t, service.AsyncImageTaskStatusExecutionUnknown, repo.task.Status)
				require.Zero(t, repo.task.RetryCount)
				require.Empty(t, repo.results)
			} else if scenario.rejectAll {
				require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
				require.Equal(t, 2, repo.task.UpstreamRetryCount)
				require.Contains(t, *repo.task.ErrorMessage, fmt.Sprintf("HTTP %d", scenario.status))
				require.Empty(t, repo.results)
			} else {
				require.Equal(t, service.AsyncImageTaskStatusSucceeded, repo.task.Status)
				require.Equal(t, service.AsyncImageBillingStatusNotBillable, repo.task.BillingStatus)
				require.Len(t, repo.results, 1)
				stored, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(repo.results[0].ObjectKey)))
				require.NoError(t, err)
				require.Equal(t, png, stored)
				checksum := sha256.Sum256(stored)
				require.Equal(t, hex.EncodeToString(checksum[:]), repo.results[0].Checksum)
				if scenario.mode == service.AsyncImageReferenceTransportPassthroughFallbackLocal {
					require.EqualValues(t, 1, downloads.Load())
					require.Equal(t, 1, repo.snapshotWrites)
					require.NotContains(t, string(bodies[1]), referenceURL)
					require.Contains(t, string(bodies[1]), base64.StdEncoding.EncodeToString(png))
				} else {
					require.Zero(t, downloads.Load())
					require.Equal(t, string(bodies[0]), string(bodies[1]))
				}
			}
			require.Zero(t, billing.charges)
			require.Empty(t, repo.task.RequestPayload)
			for repeat := 0; repeat < 2; repeat++ {
				require.False(t, worker.processAsyncImageTask(context.Background(), task.TaskID).requeue)
			}
			mu.Lock()
			count := len(sentAccounts)
			mu.Unlock()
			require.Equal(t, len(accounts), count)
			t.Logf("HTTP status=%d accounts=%v reference_GETs=%d snapshots=%d final=%s retries=%d", scenario.status, accounts, downloads.Load(), repo.snapshotWrites, repo.task.Status, repo.task.RetryCount)
		})
	}
}
