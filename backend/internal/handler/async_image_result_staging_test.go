package handler

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type resultStagingFaultRepo struct {
	*resultFetchTestRepo
	saveCalls       int
	failures        int
	commitOnFailure bool
	commitAt        int
	beforeSave      func(context.Context, service.AsyncImageStagingObject)
}

func (r *resultStagingFaultRepo) SaveAsyncImageFetchedObject(ctx context.Context, o service.AsyncImageStagingObject) error {
	r.saveCalls++
	if r.beforeSave != nil {
		r.beforeSave(ctx, o)
	}
	if r.failures > 0 {
		r.failures--
		if r.commitOnFailure && (r.commitAt == 0 || r.saveCalls == r.commitAt) {
			r.staging = append(r.staging, o)
		}
		return errors.New("database temporarily unavailable; token=private-storage-token")
	}
	return r.resultFetchTestRepo.SaveAsyncImageFetchedObject(ctx, o)
}

func TestAsyncImageResultStagingFailuresNeverRedownload(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failures  int
		ambiguous bool
		commitAt  int
		wantSaves int
		wantFail  bool
	}{
		{name: "transient", failures: 2, wantSaves: 3},
		{name: "ambiguous_commit", failures: 1, ambiguous: true, wantSaves: 1},
		{name: "ambiguous_final_commit", failures: 3, ambiguous: true, commitAt: 3, wantSaves: 3},
		{name: "persistent", failures: 3, wantSaves: 3, wantFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			png, _ := generatedWorkerImageFixtures(t)
			var gets atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gets.Add(1)
				w.Header().Set("Content-Type", "image/png")
				_, _ = w.Write(png)
			}))
			defer server.Close()
			original := http.DefaultTransport
			transport := &http.Transport{DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true}}).DialContext(ctx, "tcp", strings.TrimPrefix(server.URL, "https://"))
			}}
			http.DefaultTransport = transport // test-only local TLS endpoint
			defer func() { http.DefaultTransport = original; transport.CloseIdleConnections() }()
			repo := &resultStagingFaultRepo{
				resultFetchTestRepo: &resultFetchTestRepo{generatedWorkerResultRepo: &generatedWorkerResultRepo{task: &service.AsyncImageTask{TaskID: "staging", Status: service.AsyncImageTaskStatusUpstreamSucceeded, Platform: service.PlatformOpenAI, ImageCount: 1, Version: 1}}},
				failures:            tc.failures, commitOnFailure: tc.ambiguous, commitAt: tc.commitAt,
			}
			h := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo), encryptor: resultFetchTestCipher{}}
			plain, err := json.Marshal(asyncImageResultEnvelope{Version: 1, Images: []asyncImageResultSource{{URL: "https://8.8.8.8/result?secret=private-url-token"}}})
			require.NoError(t, err)
			cipher, err := h.encryptor.Encrypt(string(plain))
			require.NoError(t, err)
			repo.pending = &service.AsyncImageResultFetch{EncryptedPayload: []byte(cipher), Attempts: map[int]int{}, ExpiresAt: time.Now().Add(time.Hour)}
			cfg := service.AsyncImageRuntimeConfig{DownloadTimeoutSeconds: 3, DownloadMaxBytes: 1 << 20, DownloadMaxPixels: 100}
			d, handled := h.resumeAsyncImageResultFetch(context.Background(), repo.task, cfg)
			require.True(t, handled)
			require.Equal(t, tc.wantSaves, repo.saveCalls)
			require.EqualValues(t, 1, gets.Load(), "database retries must reuse downloaded bytes")
			require.Equal(t, 1, repo.pending.Attempts[0])
			require.NotNil(t, repo.pending, "retain the original result source")
			if tc.wantFail {
				require.False(t, d.requeue, "do not silently discard the bytes and issue another GET")
				require.Equal(t, service.AsyncImageTaskStatusFailed, repo.task.Status)
				require.Equal(t, "result_staging_failed", *repo.task.ErrorCode)
				require.NotContains(t, *repo.task.ErrorMessage, "private-storage-token")
				require.Empty(t, repo.staging)
				require.Zero(t, repo.pending.Attempts[-1], "no billing preparation after staging failed")
			} else {
				require.Len(t, repo.staging, 1)
				require.Equal(t, png, repo.staging[0].Content)
				// Missing usage postpones finalization; simulate another worker.
				require.True(t, d.requeue)
				require.Equal(t, service.AsyncImageTaskStatusUpstreamSucceeded, repo.task.Status)
				_, handled = h.resumeAsyncImageResultFetch(context.Background(), repo.task, cfg)
				require.True(t, handled)
				require.EqualValues(t, 1, gets.Load(), "a later worker uses persisted image bytes")
				require.Equal(t, tc.wantSaves, repo.saveCalls)
			}
			var saves []int
			for _, event := range repo.events {
				require.NotContains(t, string(event.Payload), "private-storage-token")
				require.NotContains(t, string(event.Payload), "private-url-token")
				if event.EventType == "result_staging_save_failed" {
					var payload struct {
						SaveAttempt int `json:"save_attempt"`
					}
					require.NoError(t, json.Unmarshal(event.Payload, &payload))
					saves = append(saves, payload.SaveAttempt)
				}
			}
			for i, save := range saves {
				require.Equal(t, i+1, save)
			}
			require.Len(t, saves, min(tc.failures, tc.wantSaves))
			for _, tr := range repo.transitions {
				require.NotEqual(t, service.AsyncImageTaskStatusQueued, tr.ToStatus)
				require.NotEqual(t, service.AsyncImageTaskStatusInvoking, tr.ToStatus)
			}
			require.Nil(t, h.openAI, "staging recovery must not need a generation gateway")
		})
	}
}

func TestAsyncImageResultStagingSurvivesDownloadCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	width, height := 2, 3
	o := service.AsyncImageStagingObject{TaskID: "cancelled", ImageIndex: 0, Content: []byte("completed-image"), ContentType: "image/png", ByteSize: 15, Checksum: "checksum", Width: &width, Height: &height}
	repo := &resultStagingFaultRepo{
		resultFetchTestRepo: &resultFetchTestRepo{generatedWorkerResultRepo: &generatedWorkerResultRepo{}}, failures: 1,
		beforeSave: func(saveCtx context.Context, saved service.AsyncImageStagingObject) {
			require.NoError(t, saveCtx.Err(), "a completed image gets an independent persistence deadline")
			deadline, ok := saveCtx.Deadline()
			require.True(t, ok)
			require.WithinDuration(t, time.Now().Add(15*time.Second), deadline, time.Second)
			require.Equal(t, o, saved)
		},
	}
	h := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo)}
	require.NoError(t, h.saveAsyncImageFetchedObjectWithRetry(ctx, repo, o, 1))
	require.Equal(t, 2, repo.saveCalls)
	require.Len(t, repo.staging, 1)
}

func TestAsyncImageResultStagingReadbackRejectsDifferentImage(t *testing.T) {
	width, height := 2, 3
	o := service.AsyncImageStagingObject{TaskID: "different", ImageIndex: 0, Content: []byte("image-one"), ContentType: "image/png", ByteSize: 9, Checksum: "checksum", Width: &width, Height: &height}
	for _, different := range []string{"bytes", "dimensions"} {
		t.Run(different, func(t *testing.T) {
			stored := o
			if different == "bytes" {
				stored.Content = []byte("image-two")
			} else {
				otherWidth := 4
				stored.Width = &otherWidth
			}
			repo := &resultStagingFaultRepo{resultFetchTestRepo: &resultFetchTestRepo{generatedWorkerResultRepo: &generatedWorkerResultRepo{staging: []service.AsyncImageStagingObject{stored}}}, failures: 3}
			h := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo)}
			require.Error(t, h.saveAsyncImageFetchedObjectWithRetry(context.Background(), repo, o, 1))
			require.Equal(t, 3, repo.saveCalls)
		})
	}
}
