//go:build postgres

package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cleanupSmokeObject struct {
	bytes    []byte
	checksum string
}
type cleanupSmokeCall struct {
	method, key string
	at          time.Time
}
type cleanupSmokeS3 struct {
	mu                sync.Mutex
	objects           map[string]cleanupSmokeObject
	calls             []cleanupSmokeCall
	deniedKey         string
	blockKey          string
	entered           chan struct{}
	release           chan struct{}
	blockOnce         sync.Once
	active, maxActive int
	requestDelay      time.Duration
}

func (s *cleanupSmokeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/results/")
	if !strings.HasPrefix(r.URL.Path, "/results/") || r.Header.Get("Authorization") == "" {
		http.Error(w, "invalid signed object request", 400)
		return
	}
	if r.Method == http.MethodPut {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1024))
		if err != nil {
			http.Error(w, "read failure", 400)
			return
		}
		s.mu.Lock()
		s.objects[key] = cleanupSmokeObject{body, r.Header.Get("X-Amz-Meta-Sha256")}
		s.mu.Unlock()
		w.WriteHeader(200)
		return
	}
	s.mu.Lock()
	s.calls = append(s.calls, cleanupSmokeCall{r.Method, key, time.Now()})
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	obj, found := s.objects[key]
	denied := s.deniedKey == key
	block := s.blockKey == key && r.Method == http.MethodDelete
	delay := s.requestDelay
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.active--; s.mu.Unlock() }()
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
	}
	if block {
		s.blockOnce.Do(func() { close(s.entered); <-s.release })
	}
	if denied {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(403)
		_, _ = io.WriteString(w, "<Error><Code>AccessDenied</Code><Message>synthetic permission denial</Message></Error>")
		return
	}
	switch r.Method {
	case http.MethodHead:
		if !found {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.bytes)))
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("X-Amz-Meta-Sha256", obj.checksum)
		w.WriteHeader(200)
	case http.MethodDelete:
		s.mu.Lock()
		delete(s.objects, key)
		s.mu.Unlock()
		w.WriteHeader(204)
	default:
		http.Error(w, "cleanup must not list or download objects", 405)
	}
}

type cleanupSmokeFixture struct {
	db       *sql.DB
	repo     *imageLibraryRepository
	settings *service.ImageStorageSettingService
	svc      *service.ImageResultCleanupService
	api      *httptest.Server
	s3       *cleanupSmokeS3
	storage  service.DurableImageStorage
	refs     []service.ObjectRef
}

func newCleanupSmokeFixture(t *testing.T, count int) *cleanupSmokeFixture {
	t.Helper()
	return newCleanupSmokeFixtureWithDB(t, count, resultCleanupPostgresDB(t))
}

func newCleanupSmokeFixtureWithDB(t *testing.T, count int, db *sql.DB) *cleanupSmokeFixture {
	t.Helper()
	ctx := context.Background()
	seedResultCleanup(t, db, count)
	s3 := &cleanupSmokeS3{objects: map[string]cleanupSmokeObject{}, entered: make(chan struct{}), release: make(chan struct{})}
	remote := httptest.NewServer(s3)
	t.Cleanup(remote.Close)
	cfg := config.ImageStorageConfig{Enabled: true, Backend: "oss", Provider: "custom_s3", Endpoint: remote.URL, Region: "auto", Bucket: "results", Prefix: "smoke/", PublicBaseURL: remote.URL + "/results", AccessKeyID: "synthetic-access", SecretAccessKey: "synthetic-secret", ForcePathStyle: true, PresignExpiry: 1}
	settings := service.NewImageStorageSettingService(nil, nil, nil, func(ctx context.Context, cfg *config.ImageStorageConfig) (service.ImageStorage, error) {
		return NewS3ImageStorage(ctx, cfg)
	}, cfg, config.AsyncImageConfig{ResultRetentionDays: 90})
	storage, enabled, err := settings.DurableStorage(ctx)
	require.NoError(t, err)
	require.True(t, enabled)
	repo := &imageLibraryRepository{db: db}
	library := service.NewImageLibraryService(repo, settings, nil)
	f := &cleanupSmokeFixture{db: db, repo: repo, settings: settings, svc: library.ResultCleanup(), s3: s3, storage: storage}
	service.SetImageResultCleanupPostgresSmokeSample(f.svc, 20, 20)
	for i := 1; i <= count; i++ {
		ref, err := storage.SaveObject(ctx, fmt.Sprintf("smoke/results/%d.png", i), "image/png", bytes.Repeat([]byte{byte(i)}, 100))
		require.NoError(t, err)
		f.refs = append(f.refs, ref)
		_, err = db.Exec(`UPDATE image_storage_objects SET object_key=$2,checksum_sha256=$3,storage_identity=$4 WHERE id=$1`, i, ref.ObjectKey, ref.ChecksumSHA256, ref.StorageIdentity)
		require.NoError(t, err)
		_, err = db.Exec(`UPDATE async_image_results SET object_key=$2,checksum=$3 WHERE id=$1`, i, ref.ObjectKey, ref.ChecksumSHA256)
		require.NoError(t, err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := handler.NewImageLibraryHandler(library, nil)
	// Only authentication is synthetic. All route handlers, JSON validation,
	// envelopes, services, repositories, SQL, and the AWS SDK are real.
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 1})
		c.Next()
	})
	router.GET("/cleanup-policy", h.AdminGetResultCleanupPolicy)
	router.PUT("/cleanup-policy", h.AdminUpdateResultCleanupPolicy)
	router.POST("/cleanup-jobs/preview", h.AdminPreviewCleanup)
	router.POST("/cleanup-jobs", h.AdminCreateCleanupJob)
	router.GET("/cleanup-jobs", h.AdminListCleanupJobs)
	router.POST("/cleanup-jobs/:id/cancel", h.AdminCancelResultCleanup)
	router.POST("/cleanup-jobs/:id/resume", h.AdminResumeResultCleanup)
	f.api = httptest.NewServer(router)
	t.Cleanup(f.api.Close)
	var policy service.ImageResultCleanupPolicy
	f.request(t, http.MethodGet, "/cleanup-policy", nil, 200, &policy)
	require.False(t, policy.Configured)
	require.Equal(t, 90, policy.RetentionValue)
	policy.Enabled = false
	policy.RetentionValue = 1
	policy.RetentionUnit = "hours"
	policy.RequestsPerSecond = 2
	f.request(t, http.MethodPut, "/cleanup-policy", policy, 200, &policy)
	require.True(t, policy.Configured)
	require.Equal(t, int64(2), policy.Revision)
	require.Nil(t, policy.NextRunAt)
	return f
}

func (f *cleanupSmokeFixture) request(t *testing.T, method, path string, body any, want int, out any) {
	t.Helper()
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request, err := http.NewRequest(method, f.api.URL+path, bytes.NewReader(encoded))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, want, response.StatusCode, string(payload))
	if out != nil {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(payload, &envelope))
		reflect.ValueOf(out).Elem().SetZero()
		require.NoError(t, json.Unmarshal(envelope.Data, out), string(payload))
	}
}

func (f *cleanupSmokeFixture) create(t *testing.T) *service.ImageResultCleanupJob {
	t.Helper()
	var preview service.ImageResultCleanupPreview
	f.request(t, http.MethodPost, "/cleanup-jobs/preview", gin.H{"scope": "async_results", "filters": gin.H{"older_than_value": 1, "older_than_unit": "hours"}}, 200, &preview)
	require.Len(t, preview.PreviewToken, 64)
	var job, replayed service.ImageResultCleanupJob
	body := gin.H{"scope": "async_results", "filters": gin.H{"preview_token": preview.PreviewToken}}
	f.request(t, http.MethodPost, "/cleanup-jobs", body, 201, &job)
	f.request(t, http.MethodPost, "/cleanup-jobs", body, 201, &replayed)
	require.Equal(t, job.ID, replayed.ID)
	return &job
}

func (f *cleanupSmokeFixture) run(t *testing.T, jobID int64) *service.ImageResultCleanupJob {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	for attempt := 0; attempt < 4; attempt++ {
		require.NoError(t, f.svc.RunOnce(ctx))
		job, err := f.repo.GetImageResultCleanupJob(ctx, jobID)
		require.NoError(t, err)
		if job.Status != "pending" || job.LastError == nil || !(strings.Contains(*job.LastError, "负载") || strings.Contains(*job.LastError, "资源指标")) {
			return job
		}
		t.Logf("Real host load guard deferred smoke: %s", *job.LastError)
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(6 * time.Second):
		}
		// Advance only the test job's due time; retain the real load guard and its
		// two low samples requirement. Never bypass CPU/memory safety for a pass.
		_, err = f.db.ExecContext(ctx, `UPDATE image_library_cleanup_jobs SET next_attempt_at=NOW()-INTERVAL '1 second' WHERE id=$1`, jobID)
		require.NoError(t, err)
	}
	t.Fatal("load guard did not permit the isolated smoke run")
	return nil
}

func TestImageResultCleanupSmokePostgresHTTPToS3Lifecycle(t *testing.T) {
	f := newCleanupSmokeFixture(t, 9)
	ctx := context.Background()
	_, err := f.db.Exec(`UPDATE image_storage_objects SET storage_identity='' WHERE id IN(2,6);
 UPDATE image_storage_objects SET checksum_sha256=repeat('b',64) WHERE id=6;
 UPDATE async_image_results SET checksum=repeat('b',64) WHERE id=6;
 UPDATE async_image_results SET created_at=NOW() WHERE id=5;
 INSERT INTO image_library_items(id,storage_object_id)VALUES(3,3);
 INSERT INTO image_library_items(id,storage_object_id,deleted_at)VALUES(7,7,NOW());
 INSERT INTO image_plaza_publications(library_item_id,status,expires_at)VALUES(7,'published',NOW()+INTERVAL '1 day');`)
	require.NoError(t, err)
	refs, err := f.settings.ResultReferenceConfig(ctx)
	require.NoError(t, err)
	urls, _ := json.Marshal([]string{refs.URLBases[0] + "/" + f.refs[3].ObjectKey})
	_, err = f.db.Exec(`INSERT INTO async_image_tasks(task_id,status,result_references_checked,reference_image_urls)VALUES('live-dependent','invoking',FALSE,$1)`, urls)
	require.NoError(t, err)
	f.s3.mu.Lock()
	delete(f.s3.objects, f.refs[8].ObjectKey)
	f.s3.mu.Unlock()
	var protectedPreview service.ImageResultCleanupPreview
	f.request(t, http.MethodPost, "/cleanup-jobs/preview", gin.H{"scope": "async_results", "filters": gin.H{"older_than_value": 1, "older_than_unit": "hours"}}, 200, &protectedPreview)
	require.Zero(t, protectedPreview.MatchedItems, "old active task references have not been backfilled yet")
	require.Greater(t, protectedPreview.ScannedCount, int64(0), "the UI can enqueue a guarded verification job")
	job := f.create(t)
	// Cancel/resume use the real HTTP handlers before any irreversible call.
	f.request(t, http.MethodPost, fmt.Sprintf("/cleanup-jobs/%d/cancel", job.ID), nil, 200, job)
	require.Equal(t, "cancelled", job.Status)
	require.NoError(t, f.svc.RunOnce(ctx))
	f.s3.mu.Lock()
	beforeCalls := len(f.s3.calls)
	f.s3.mu.Unlock()
	require.Zero(t, beforeCalls)
	f.request(t, http.MethodPost, fmt.Sprintf("/cleanup-jobs/%d/resume", job.ID), nil, 200, job)
	service.SetImageResultCleanupPostgresSmokeSample(f.svc, 99, 99)
	require.NoError(t, f.svc.RunOnce(ctx))
	deferred, err := f.repo.GetImageResultCleanupJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", deferred.Status)
	require.NotNil(t, deferred.LastError)
	require.Contains(t, *deferred.LastError, "负载偏高")
	f.s3.mu.Lock()
	highLoadCalls := len(f.s3.calls)
	f.s3.mu.Unlock()
	require.Zero(t, highLoadCalls)
	service.SetImageResultCleanupPostgresSmokeSample(f.svc, 20, 20)
	_, err = f.db.Exec(`UPDATE image_library_cleanup_jobs SET next_attempt_at=NOW()-INTERVAL '1 second' WHERE id=$1`, job.ID)
	require.NoError(t, err)
	require.NoError(t, f.svc.RunOnce(ctx))
	deferred, err = f.repo.GetImageResultCleanupJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", deferred.Status)
	require.Contains(t, *deferred.LastError, "连续两次")
	f.s3.mu.Lock()
	firstLowCalls := len(f.s3.calls)
	f.s3.mu.Unlock()
	require.Zero(t, firstLowCalls)
	service.SetImageResultCleanupPostgresSmokeSample(f.svc, 20, 20)
	_, err = f.db.Exec(`UPDATE image_library_cleanup_jobs SET next_attempt_at=NOW()-INTERVAL '1 second' WHERE id=$1`, job.ID)
	require.NoError(t, err)
	job = f.run(t, job.ID)
	require.Equal(t, "succeeded", job.Status)
	require.Equal(t, int64(3), job.DeletedCount)
	require.Equal(t, int64(300), job.DeletedBytes)
	require.Equal(t, int64(6), job.DetachedCount)
	require.Equal(t, int64(3), job.ProtectedCount)
	require.Equal(t, int64(1), job.SkippedCount)
	require.NotNil(t, job.LastError)
	require.Contains(t, *job.LastError, "校验值")
	var ids string
	require.NoError(t, f.db.QueryRow(`SELECT string_agg(id::text,',' ORDER BY id) FROM async_image_results`).Scan(&ids))
	require.Equal(t, "4,5,6", ids)
	var dependencyCount int
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM async_image_task_result_references WHERE task_id='live-dependent' AND storage_object_id=4`).Scan(&dependencyCount))
	require.Equal(t, 1, dependencyCount)
	var listed struct {
		Items []service.ImageLibraryCleanupJob `json:"items"`
	}
	f.request(t, http.MethodGet, "/cleanup-jobs", nil, 200, &listed)
	require.Len(t, listed.Items, 1)
	require.Equal(t, job.DeletedBytes, listed.Items[0].DeletedBytes)
	f.s3.mu.Lock()
	calls := append([]cleanupSmokeCall(nil), f.s3.calls...)
	maxActive := f.s3.maxActive
	var missingProtected []int
	for _, i := range []int{3, 4, 5, 6, 7} {
		_, exists := f.s3.objects[f.refs[i-1].ObjectKey]
		if !exists {
			missingProtected = append(missingProtected, i)
		}
	}
	f.s3.mu.Unlock()
	require.Empty(t, missingProtected, "protected or fresh objects must survive")
	require.Equal(t, 1, maxActive)
	var methods []string
	for i, call := range calls {
		require.Contains(t, []string{http.MethodHead, http.MethodDelete}, call.method)
		methods = append(methods, call.method+" "+call.key)
		if i > 0 {
			require.GreaterOrEqual(t, call.at.Sub(calls[i-1].at), 450*time.Millisecond, "HEAD and DELETE share rate tokens")
		}
	}
	require.Len(t, calls, 8)
	for _, call := range calls {
		require.False(t, call.key == f.refs[8].ObjectKey && call.method == http.MethodDelete, "known missing object must not count a DELETE")
	}
	t.Logf("HTTP policy/preview/create/cancel/resume/list -> real service/PG/AWS SDK: %v; deleted=3 protected=3 skipped=1", methods)
}

func TestImageResultCleanupSmokePostgresPermissionRetryAndLease(t *testing.T) {
	f := newCleanupSmokeFixture(t, 2)
	ctx := context.Background()
	f.s3.deniedKey = f.refs[0].ObjectKey
	job := f.create(t)
	job = f.run(t, job.ID)
	require.Equal(t, "paused", job.Status)
	require.Equal(t, int64(1), job.FailedCount)
	require.Zero(t, job.DeletedCount)
	var retained int
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM async_image_results`).Scan(&retained))
	require.Equal(t, 2, retained)
	var state string
	require.NoError(t, f.db.QueryRow(`SELECT state FROM image_storage_objects WHERE id=1`).Scan(&state))
	require.Equal(t, "deleting", state)
	f.s3.mu.Lock()
	deniedCalls := len(f.s3.calls)
	f.s3.deniedKey = ""
	f.s3.blockKey = f.refs[0].ObjectKey
	f.s3.mu.Unlock()
	require.Equal(t, 1, deniedCalls, "AWS SDK must not silently retry denied storage operations")
	f.request(t, http.MethodPost, fmt.Sprintf("/cleanup-jobs/%d/resume", job.ID), nil, 200, job)
	// Retry wall time belongs to persisted scheduling; advance it only in this
	// isolated fixture so the smoke need not wait the production minute backoff.
	_, err := f.db.Exec(`UPDATE image_result_cleanup_objects SET next_attempt_at=NOW()-INTERVAL '1 second' WHERE job_id=$1`, job.ID)
	require.NoError(t, err)
	done := make(chan error, 1)
	runCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	go func() { done <- f.svc.RunOnce(runCtx) }()
	released := false
	defer func() {
		if !released {
			close(f.s3.release)
		}
	}()
	select {
	case <-f.s3.entered:
	case err := <-done:
		current, _ := f.repo.GetImageResultCleanupJob(ctx, job.ID)
		t.Fatalf("worker ended before delete: %v; state=%+v", err, current)
	case <-time.After(15 * time.Second):
		t.Fatal("no retry DELETE received")
	}
	// A second real service sharing the DB cannot claim work while the first
	// awaits HTTP, and unrelated uploads/writes still complete in that interval.
	second := service.NewImageLibraryService(f.repo, f.settings, nil).ResultCleanup()
	require.NoError(t, second.RunOnce(ctx))
	newRef, err := f.storage.SaveObject(ctx, "smoke/results/new.png", "image/png", []byte("new-result"))
	require.NoError(t, err)
	_, err = f.db.Exec(`WITH task AS(INSERT INTO async_image_tasks(task_id,status,result_references_checked)VALUES('new-task','succeeded',TRUE) RETURNING task_id),
 object AS(INSERT INTO image_storage_objects(provider,bucket,object_key,content_type,byte_size,checksum_sha256,storage_identity)
 VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id)
 INSERT INTO async_image_results(task_id,provider,bucket,object_key,content_type,byte_size,checksum,storage_object_id)
 SELECT task.task_id,$1,$2,$3,$4,$5,$6,object.id FROM task,object`, newRef.Provider, newRef.Bucket, newRef.ObjectKey, newRef.ContentType, newRef.SizeBytes, newRef.ChecksumSHA256, newRef.StorageIdentity)
	require.NoError(t, err)
	close(f.s3.release)
	released = true
	require.NoError(t, <-done)
	job, err = f.repo.GetImageResultCleanupJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", job.Status)
	require.Equal(t, int64(2), job.DeletedCount)
	require.Equal(t, int64(1), job.FailedCount)
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM async_image_results WHERE task_id='new-task'`).Scan(&retained))
	require.Equal(t, 1, retained)
	f.s3.mu.Lock()
	calls := append([]cleanupSmokeCall(nil), f.s3.calls...)
	maxActive := f.s3.maxActive
	f.s3.mu.Unlock()
	require.Equal(t, 1, maxActive)
	require.Len(t, calls, 5)
	require.GreaterOrEqual(t, calls[2].at.Sub(calls[1].at), 450*time.Millisecond)
	t.Log("403 pauses without deleting DB rows; HTTP resume recovers the durable ticket; second worker makes no DELETE; concurrent new PUT and result commit succeed")
	// The legacy all parent may clean its other scopes only after the safe child.
	// Active SC inputs and recovery facts belong to their own retention protocol.
	_, err = f.db.Exec(`ALTER TABLE image_library_items ADD COLUMN purged_at TIMESTAMPTZ,ADD COLUMN created_at TIMESTAMPTZ DEFAULT NOW();
 CREATE TEMP TABLE async_image_input_objects(id BIGINT PRIMARY KEY,expires_at TIMESTAMPTZ);
 CREATE TEMP TABLE async_image_task_inputs(task_id TEXT,input_object_id BIGINT);
 CREATE TEMP TABLE async_image_result_upload_intents(id BIGINT PRIMARY KEY);
 CREATE TEMP TABLE image_library_upload_intents(id BIGINT PRIMARY KEY);
 CREATE TEMP TABLE async_image_upload_reservations(id BIGINT PRIMARY KEY,status TEXT,intent_object_key TEXT);
 INSERT INTO async_image_input_objects VALUES(1,NOW()+INTERVAL '1 hour');
 INSERT INTO async_image_tasks(task_id,status,result_references_checked)VALUES('live-sc-task','invoking',TRUE);
 INSERT INTO async_image_task_inputs VALUES('live-sc-task',1);
 INSERT INTO async_image_result_upload_intents VALUES(1);
 INSERT INTO image_library_upload_intents VALUES(1);
 INSERT INTO async_image_upload_reservations VALUES(1,'reserved','sc-input');`)
	require.NoError(t, err)
	filters, err := json.Marshal(gin.H{"before": job.Before})
	require.NoError(t, err)
	var parentID int64
	err = f.db.QueryRow(`INSERT INTO image_library_cleanup_jobs(scope,status,lease_version,result_child_job_id,filters)
 VALUES('all','running',1,$1,$2) RETURNING id`, job.ID, filters).Scan(&parentID)
	require.NoError(t, err)
	batch, err := f.repo.PrepareCleanupBatch(ctx, parentID, 1, "all", filters, 10)
	require.NoError(t, err)
	require.True(t, batch.Done)
	require.Empty(t, batch.Objects)
	var recoveryRows int
	require.NoError(t, f.db.QueryRow(`SELECT(SELECT COUNT(*) FROM async_image_input_objects)+(SELECT COUNT(*) FROM async_image_task_inputs)+
 (SELECT COUNT(*) FROM async_image_result_upload_intents)+(SELECT COUNT(*) FROM image_library_upload_intents)+(SELECT COUNT(*) FROM async_image_upload_reservations)`).Scan(&recoveryRows))
	require.Equal(t, 5, recoveryRows)
	t.Log("legacy all child completed; active SC input and all recovery facts remain intact")
}

func TestImageResultCleanupSmokePostgresScheduledDailyRun(t *testing.T) {
	f := newCleanupSmokeFixture(t, 1)
	ctx := context.Background()
	var policy service.ImageResultCleanupPolicy
	f.request(t, http.MethodGet, "/cleanup-policy", nil, 200, &policy)
	policy.Enabled = true
	policy.ScheduleMode = "daily"
	policy.DailyTime = "03:15"
	f.request(t, http.MethodPut, "/cleanup-policy", policy, 200, &policy)
	require.NotNil(t, policy.NextRunAt)
	next := policy.NextRunAt.In(time.FixedZone("Asia/Shanghai", 8*60*60))
	require.Equal(t, 3, next.Hour())
	require.Equal(t, 15, next.Minute())
	_, err := f.db.Exec(`UPDATE image_result_cleanup_policy SET next_run_at=NOW()-INTERVAL '1 second' WHERE id=1`)
	require.NoError(t, err)
	require.NoError(t, f.svc.RunOnce(ctx))
	jobs, err := f.svc.List(ctx, 10)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "scheduled", jobs[0].Source)
	require.Equal(t, "succeeded", jobs[0].Status)
	require.Equal(t, int64(1), jobs[0].DeletedCount)
	require.Equal(t, "daily", jobs[0].PolicySnapshot.ScheduleMode)
	f.request(t, http.MethodGet, "/cleanup-policy", nil, 200, &policy)
	require.True(t, policy.NextRunAt.After(time.Now()))
	require.NoError(t, f.svc.RunOnce(ctx))
	jobs, err = f.svc.List(ctx, 10)
	require.NoError(t, err)
	require.Len(t, jobs, 1, "same daily slot must not enqueue twice")
	f.s3.mu.Lock()
	calls := len(f.s3.calls)
	f.s3.mu.Unlock()
	require.Equal(t, 2, calls, "one HEAD and one DELETE for the one scheduled object")
	t.Log("HTTP daily policy -> due scheduler -> persisted scheduled job -> real S3 HEAD/DELETE; next Beijing slot retained and duplicate tick does nothing")
}

func TestImageResultCleanupSmokePostgresConcurrentObjects(t *testing.T) {
	f := newCleanupSmokeFixtureWithDB(t, 10, resultCleanupConcurrentPostgresDB(t))
	_, err := f.db.Exec(`INSERT INTO image_library_items(storage_object_id)VALUES(1);
 UPDATE async_image_tasks SET status='invoking' WHERE task_id='task-2';
 UPDATE image_storage_objects SET storage_identity='' WHERE id=3;`)
	require.NoError(t, err)
	f.s3.mu.Lock()
	f.s3.requestDelay = 150 * time.Millisecond
	delete(f.s3.objects, f.refs[3].ObjectKey)
	f.s3.mu.Unlock()
	var policy service.ImageResultCleanupPolicy
	f.request(t, http.MethodGet, "/cleanup-policy", nil, 200, &policy)
	policy.Concurrency, policy.RequestsPerSecond, policy.BatchSize = 4, 20, 6
	f.request(t, http.MethodPut, "/cleanup-policy", policy, 200, &policy)
	require.Equal(t, 4, policy.Concurrency)
	job := f.create(t)
	job = f.run(t, job.ID)
	require.Equal(t, "pending", job.Status)
	require.Equal(t, int64(6), job.ScannedCount, "concurrency must share one per-slice batch budget")
	require.Equal(t, 4, job.PolicySnapshot.Concurrency)
	f.s3.mu.Lock()
	firstSliceCalls := len(f.s3.calls)
	f.s3.mu.Unlock()
	_, err = f.db.Exec(`UPDATE image_library_cleanup_jobs SET next_attempt_at=NOW()-INTERVAL '1 second' WHERE id=$1`, job.ID)
	require.NoError(t, err)
	job = f.run(t, job.ID)
	require.Equal(t, "succeeded", job.Status)
	require.Equal(t, int64(10), job.ScannedCount)
	require.Equal(t, int64(7), job.DeletedCount)
	require.Equal(t, int64(700), job.DeletedBytes)
	require.Equal(t, int64(9), job.DetachedCount)
	require.Equal(t, int64(2), job.ProtectedCount)
	require.Zero(t, job.FailedCount)
	var remaining string
	require.NoError(t, f.db.QueryRow(`SELECT string_agg(id::text,',' ORDER BY id) FROM async_image_results`).Scan(&remaining))
	require.Equal(t, "2", remaining, "the active task keeps its result while the library keeps its object")
	f.s3.mu.Lock()
	calls := append([]cleanupSmokeCall(nil), f.s3.calls...)
	maxActive := f.s3.maxActive
	_, libraryExists := f.s3.objects[f.refs[0].ObjectKey]
	_, activeExists := f.s3.objects[f.refs[1].ObjectKey]
	f.s3.mu.Unlock()
	require.True(t, libraryExists)
	require.True(t, activeExists)
	require.Greater(t, maxActive, 1, "real SDK HTTP calls must overlap at configured object concurrency")
	require.LessOrEqual(t, maxActive, 4)
	require.Len(t, calls, 15, "7 HEAD+DELETE pairs and one missing HEAD")
	seen := map[string]int{}
	for i, call := range calls {
		seen[call.method+" "+call.key]++
		require.Equal(t, 1, seen[call.method+" "+call.key], "no object may be claimed twice in the same job lease")
		if i > 0 && i != firstSliceCalls {
			require.GreaterOrEqual(t, call.at.Sub(calls[i-1].at), 40*time.Millisecond, "all concurrent HEAD/DELETE calls share the 20/s gate")
		}
	}
	t.Logf("real HTTP/PG/S3 concurrency=%d (configured 4), aggregate 20 requests/s, 6-object batch honored; protected library+active task survived, 7 deleted and 1 missing", maxActive)
}
