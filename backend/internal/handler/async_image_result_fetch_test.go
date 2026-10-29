package handler

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type resultFetchTestCipher struct{}

func (resultFetchTestCipher) Encrypt(s string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}
func (resultFetchTestCipher) Decrypt(s string) (string, error) {
	b, e := base64.StdEncoding.DecodeString(s)
	return string(b), e
}

type resultFetchTestRepo struct {
	*generatedWorkerResultRepo
	pending *service.AsyncImageResultFetch
	events  []service.AsyncImageEvent
}

func (r *resultFetchTestRepo) GetAsyncImageResultFetch(context.Context, string) (*service.AsyncImageResultFetch, error) {
	return r.pending, nil
}
func (r *resultFetchTestRepo) SaveAsyncImageResultFetch(ctx context.Context, tr service.AsyncImageTaskTransition, b []byte, expires time.Time) (*service.AsyncImageTask, error) {
	r.pending = &service.AsyncImageResultFetch{EncryptedPayload: b, Attempts: map[int]int{}, ExpiresAt: expires}
	return r.TransitionAsyncImageTask(ctx, tr)
}
func (r *resultFetchTestRepo) BeginAsyncImageResultFetchAttempt(_ context.Context, _ string, index, limit int) (int, error) {
	if r.pending.Attempts[index] >= limit {
		return 0, service.ErrAsyncImageInvalidTransition
	}
	r.pending.Attempts[index]++
	return r.pending.Attempts[index], nil
}
func (r *resultFetchTestRepo) SaveAsyncImageFetchedObject(_ context.Context, o service.AsyncImageStagingObject) error {
	r.staging = append(r.staging, o)
	return nil
}
func (r *resultFetchTestRepo) AppendAsyncImageEvent(_ context.Context, e service.AsyncImageEvent) error {
	r.events = append(r.events, e)
	return nil
}

func TestAsyncImageResultFetchResumesPartialImagesAndNeverRegenerates(t *testing.T) {
	png, _ := generatedWorkerImageFixtures(t)
	calls := map[string]int{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		if r.URL.Path == "/first" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
		} else {
			w.WriteHeader(502)
		}
	}))
	defer server.Close()
	original := http.DefaultTransport
	http.DefaultTransport = &http.Transport{DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true}}).DialContext(ctx, "tcp", strings.TrimPrefix(server.URL, "https://"))
	}} // test-only local TLS endpoint
	defer func() { http.DefaultTransport = original }()
	repo := &resultFetchTestRepo{generatedWorkerResultRepo: &generatedWorkerResultRepo{task: &service.AsyncImageTask{TaskID: "asyncimg_fetch", Status: service.AsyncImageTaskStatusUpstreamSucceeded, Platform: service.PlatformOpenAI, Version: 1}}}
	h, _, _ := newGeneratedWorkerLocalHandler(t, repo.generatedWorkerResultRepo)
	h.tasks = service.NewAsyncImageTaskService(repo)
	h.encryptor = resultFetchTestCipher{}
	envelope := asyncImageResultEnvelope{Version: 1, Images: []asyncImageResultSource{{URL: "https://8.8.8.8/first?secret=hidden"}, {URL: "https://8.8.8.8/second?secret=hidden"}}}
	plain, err := json.Marshal(envelope)
	require.NoError(t, err)
	cipher, _ := h.encryptor.Encrypt(string(plain))
	repo.pending = &service.AsyncImageResultFetch{EncryptedPayload: []byte(cipher), Attempts: map[int]int{}, ExpiresAt: time.Now().Add(time.Hour)}
	cfg := service.AsyncImageRuntimeConfig{DownloadTimeoutSeconds: 3, DownloadMaxBytes: 1 << 20, DownloadMaxPixels: 100, DownloadMaxRedirects: 3}
	for i := 0; i < 3; i++ {
		d, handled := h.resumeAsyncImageResultFetch(context.Background(), repo.task, cfg)
		require.True(t, handled)
		require.Equal(t, i < 2, d.requeue)
	}
	require.Equal(t, 1, calls["/first"])
	require.Equal(t, 3, calls["/second"])
	require.Len(t, repo.staging, 1)
	require.Equal(t, png, repo.staging[0].Content)
	require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
	require.Equal(t, "result_fetch_failed", *repo.task.ErrorCode)
	require.NotContains(t, *repo.task.ErrorMessage, "hidden")
	require.Empty(t, repo.task.BillingPayload)
	for _, tr := range repo.transitions {
		require.NotEqual(t, service.AsyncImageTaskStatusQueued, tr.ToStatus)
		require.NotEqual(t, service.AsyncImageTaskStatusInvoking, tr.ToStatus)
	}
	require.Nil(t, h.openAI, "no generation gateway is needed for result recovery")
}

func TestAsyncImageResultFetchCancellationExpiryAndBudget(t *testing.T) {
	for _, kind := range []string{"cancel", "expiry", "exhausted", "private"} {
		t.Run(kind, func(t *testing.T) {
			repo := &resultFetchTestRepo{generatedWorkerResultRepo: &generatedWorkerResultRepo{task: &service.AsyncImageTask{TaskID: "fetch", Status: service.AsyncImageTaskStatusUpstreamSucceeded, Version: 1}}}
			h := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo), encryptor: resultFetchTestCipher{}}
			plain, _ := json.Marshal(asyncImageResultEnvelope{Version: 1, Images: []asyncImageResultSource{{URL: "https://127.0.0.1/image?secret=hidden"}}})
			cipher, _ := h.encryptor.Encrypt(string(plain))
			repo.pending = &service.AsyncImageResultFetch{EncryptedPayload: []byte(cipher), Attempts: map[int]int{}, ExpiresAt: time.Now().Add(time.Hour)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "cancel":
				cancel()
			case "expiry":
				repo.pending.ExpiresAt = time.Now().Add(-time.Second)
			case "exhausted":
				repo.pending.Attempts[0] = 3
			}
			d, handled := h.resumeAsyncImageResultFetch(ctx, repo.task, service.AsyncImageRuntimeConfig{DownloadTimeoutSeconds: 1})
			require.True(t, handled)
			if kind == "cancel" {
				require.True(t, d.requeue)
				require.Zero(t, repo.pending.Attempts[0])
			} else {
				require.False(t, d.requeue)
				require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
				require.NotContains(t, *repo.task.ErrorMessage, "hidden")
			}
		})
	}
}

func TestAsyncImageResultEnvelopeOmitsCredentialsAndRetainsBillingIdentity(t *testing.T) {
	secret := "private-user-secret"
	multiplier := 1.7
	usage := &AsyncImageUsageCapture{}
	usage.setOpenAI(&service.OpenAIRecordUsageInput{APIKey: &service.APIKey{ID: 2, Key: secret, User: &service.User{ID: 3, PasswordHash: secret, TotpSecretEncrypted: &secret}}, User: &service.User{ID: 3, PasswordHash: secret}, Account: &service.Account{ID: 4, Credentials: map[string]any{"api_key": secret}, Extra: map[string]any{"secret": secret}, RateMultiplier: &multiplier}, Result: &service.OpenAIForwardResult{RequestID: "upstream-fixed", ResponseHeaders: http.Header{"Authorization": []string{secret}}}})
	envelope, err := buildAsyncImageResultEnvelope(&service.AsyncImageTask{Platform: service.PlatformOpenAI}, []byte(`{"data":[{"url":"https://example.com/result?sig=signed"}]}`), usage, nil)
	require.NoError(t, err)
	raw, err := json.Marshal(envelope)
	require.NoError(t, err)
	require.NotContains(t, string(raw), secret)
	require.Equal(t, "upstream-fixed", envelope.OpenAI.Result.RequestID)
	require.Equal(t, multiplier, *envelope.OpenAI.Account.RateMultiplier)
	require.NotZero(t, envelope.OpenAI.PricingAt)
	require.Equal(t, secret, usage.OpenAI().APIKey.Key, "do not mutate the live credentials")
}

func TestAsyncImageResultFetchRetryClassification(t *testing.T) {
	for _, code := range []int{408, 425, 429, 500, 502, 503, 504} {
		require.True(t, asyncImageResultFetchRetryable(&service.AsyncImageReferenceDownloadError{Phase: "response", StatusCode: code}))
	}
	for _, code := range []int{400, 401, 403, 404, 413, 422} {
		require.False(t, asyncImageResultFetchRetryable(&service.AsyncImageReferenceDownloadError{Phase: "response", StatusCode: code}))
	}
	require.False(t, asyncImageResultFetchRetryable(&service.AsyncImageReferenceDownloadError{Phase: "validate", Err: errors.New("invalid image")}))
	require.False(t, asyncImageResultFetchRetryable(context.Canceled))
	require.True(t, asyncImageResultFetchRetryable(&service.AsyncImageReferenceDownloadError{Phase: "read", Err: errors.New("connection reset")}))
	require.Equal(t, 5*time.Minute, asyncImageResultFetchDelay(&service.AsyncImageReferenceDownloadError{RetryAfter: time.Hour}))
}

func TestAsyncImageOutputStructureContainsNoResponseContent(t *testing.T) {
	raw := asyncImageOutputStructure([]byte(`{"candidates":[{"finishReason":"SAFETY","content":{"parts":[{"text":"private prompt"},{"fileData":{"fileUri":"https://example.com/private?token=secret"}}]}}]}`), nil)
	require.Contains(t, string(raw), `"SAFETY":1`)
	require.Contains(t, string(raw), `"text":1`)
	require.NotContains(t, string(raw), "private")
	require.NotContains(t, string(raw), "secret")
	require.NotContains(t, string(raw), "example.com")
}

func TestAsyncImageResultSnapshotStripsNestedSecretsButKeepsQuotaSettings(t *testing.T) {
	secret := "nested-private-secret"
	a := &service.Account{ID: 8, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": secret}, Extra: map[string]any{"secret": secret, "quota_limit": 7.0, "quota_daily_limit": 2.0, "quota_weekly_limit": 4.0, "openai_long_context_billing_enabled": true}}
	u := &service.User{ID: 9, PasswordHash: secret}
	g := &service.Group{ID: 3, AccountGroups: []service.AccountGroup{{Account: a}}, Description: secret}
	s := asyncImageBillingSubscription(&service.UserSubscription{ID: 11, User: u, AssignedByUser: u, Group: g, Notes: secret})
	raw, err := json.Marshal([]any{asyncImageBillingAccount(a), asyncImageBillingKey(&service.APIKey{ID: 2, Key: secret, Group: g, User: u}), s})
	require.NoError(t, err)
	require.NotContains(t, string(raw), secret)
	c := asyncImageBillingAccount(a)
	require.True(t, c.HasAnyQuotaLimit())
	require.Equal(t, 7.0, c.GetQuotaLimit())
	require.True(t, c.IsOpenAILongContextBillingEnabled())
	require.Equal(t, int64(11), s.ID)
	require.Equal(t, int64(9), s.User.ID)
}

func TestAsyncImageResultEnvelopeRejectsOversizedOutputBeforeDecode(t *testing.T) {
	usage := &AsyncImageUsageCapture{}
	_, err := buildAsyncImageResultEnvelope(&service.AsyncImageTask{Platform: service.PlatformOpenAI, ImageCount: 1}, []byte(`{"data":[{"url":"https://example.com/1"},{"url":"https://example.com/2"}]}`), usage, nil)
	require.ErrorIs(t, err, errAsyncImageResultLimit)
	body := []byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(make([]byte, 1024)) + `"}]}`)
	_, err = buildAsyncImageResultEnvelope(&service.AsyncImageTask{Platform: service.PlatformOpenAI, ImageCount: 1}, body, usage, nil, service.AsyncImageRuntimeConfig{DownloadMaxBytes: 16})
	require.ErrorIs(t, err, errAsyncImageResultLimit)
	envelope := &asyncImageResultEnvelope{Images: make([]asyncImageResultSource, asyncImageResultMaxImages+1)}
	require.ErrorIs(t, validateAsyncImageResultEnvelope(envelope, &service.AsyncImageTask{Platform: service.PlatformGemini}, service.AsyncImageRuntimeConfig{}), errAsyncImageResultLimit)
}

func TestAsyncImageResultFinalizationRetriesKeepStagedBytesWithoutFetchingAgain(t *testing.T) {
	png, _ := generatedWorkerImageFixtures(t)
	repo := &resultFetchTestRepo{generatedWorkerResultRepo: &generatedWorkerResultRepo{task: &service.AsyncImageTask{TaskID: "finalize", Version: 1, Platform: service.PlatformGemini, Status: service.AsyncImageTaskStatusUpstreamSucceeded}}}
	h := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo), encryptor: resultFetchTestCipher{}}
	// Missing usage stands in for a failed preparation. Completed bytes stay
	// staged while the separate durable finalization budget is consumed.
	plain, _ := json.Marshal(asyncImageResultEnvelope{Version: 1, Images: []asyncImageResultSource{{Data: png, MIME: "image/png"}}})
	cipher, _ := h.encryptor.Encrypt(string(plain))
	repo.pending = &service.AsyncImageResultFetch{EncryptedPayload: []byte(cipher), Attempts: map[int]int{}, ExpiresAt: time.Now().Add(time.Hour)}
	for i := 1; i <= 3; i++ {
		d, handled := h.resumeAsyncImageResultFetch(context.Background(), repo.task, service.AsyncImageRuntimeConfig{})
		require.True(t, handled)
		require.Equal(t, i < 3, d.requeue)
		require.Equal(t, i, repo.pending.Attempts[-1])
		require.Zero(t, repo.pending.Attempts[0])
		require.Len(t, repo.staging, 1)
	}
	require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
	require.Equal(t, "result_billing_prepare_failed", *repo.task.ErrorCode)
}
