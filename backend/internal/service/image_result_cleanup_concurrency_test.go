package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageResultCleanupConcurrencyPolicyCompatibility(t *testing.T) {
	require.Equal(t, 1, defaultImageResultCleanupPolicy(7).Concurrency)
	var policy ImageResultCleanupPolicy
	require.NoError(t, json.Unmarshal([]byte(`{"configured":true,"requests_per_second":2}`), &policy))
	require.Equal(t, 1, policy.Concurrency, "previously saved policies keep serial execution")
	var job ImageResultCleanupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"policy_snapshot":{"requests_per_second":2}}`), &job))
	require.Equal(t, 1, job.PolicySnapshot.Concurrency)
	for _, concurrency := range []int{-1, 0, 1, 4, 16, 17} {
		p := defaultImageResultCleanupPolicy(7)
		p.Concurrency = concurrency
		err := validateImageResultCleanupPolicy(&p)
		if concurrency >= 1 && concurrency <= 16 {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
}

// This repository records the durable boundary separately from remote I/O.
// Unlike the serial fake-clock fixture, it is safe when verification runs in
// storage workers and the coordinator is claiming/completing other tickets.
type concurrentCleanupRepo struct {
	ImageResultCleanupRepository
	mu                      sync.Mutex
	objects                 []*ImageResultCleanupObject
	claimed                 []int64
	completed               map[int64]string
	verified                map[int64]bool
	alive                   bool
	completeErr             error
	released                []string
	completionContextErrors []error
}

func (r *concurrentCleanupRepo) HeartbeatImageResultCleanupJob(context.Context, int64, int64, time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.alive, nil
}

func (r *concurrentCleanupRepo) ClaimImageResultCleanupObject(context.Context, int64, int64, time.Time) (*ImageResultCleanupObject, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.objects) == 0 {
		return nil, nil
	}
	item := r.objects[0]
	r.objects = r.objects[1:]
	r.claimed = append(r.claimed, item.ID)
	return item, nil
}

func (r *concurrentCleanupRepo) VerifyImageResultCleanupObject(_ context.Context, _, _, id int64, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.verified[id] = true
	return nil
}

func (r *concurrentCleanupRepo) CompleteImageResultCleanupObject(ctx context.Context, _, _, id int64, outcome string, _ *time.Time, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completed[id] = outcome
	r.completionContextErrors = append(r.completionContextErrors, ctx.Err())
	return r.completeErr
}

func (r *concurrentCleanupRepo) ReleaseImageResultCleanupJob(_ context.Context, _, _ int64, status string, _ *time.Time, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.released = append(r.released, status)
	return nil
}

type cleanupConcurrentRequest struct {
	method string
	key    string
	at     time.Time
}

type concurrentCleanupStorage struct {
	DurableImageStorage
	mu              sync.Mutex
	requests        []cleanupConcurrentRequest
	active, maximum int
	started         chan cleanupConcurrentRequest
	head            func(context.Context, ObjectRef) error
	del             func(context.Context, ObjectRef) error
}

func (s *concurrentCleanupStorage) request(ctx context.Context, ref ObjectRef, method string, run func(context.Context, ObjectRef) error) error {
	if gate, ok := ImageCleanupRequestGate(ctx); ok && gate != nil {
		if err := gate(ctx); err != nil {
			return err
		}
	}
	s.mu.Lock()
	event := cleanupConcurrentRequest{method: method, key: ref.ObjectKey, at: time.Now()}
	s.requests = append(s.requests, event)
	s.active++
	if s.active > s.maximum {
		s.maximum = s.active
	}
	s.mu.Unlock()
	if s.started != nil {
		s.started <- event
	}
	defer func() { s.mu.Lock(); s.active--; s.mu.Unlock() }()
	if run != nil {
		return run(ctx, ref)
	}
	return nil
}

func (s *concurrentCleanupStorage) Head(ctx context.Context, ref ObjectRef) (ObjectMetadata, error) {
	err := s.request(ctx, ref, "HEAD", s.head)
	return ObjectMetadata{ObjectRef: ref}, err
}

func (s *concurrentCleanupStorage) Delete(ctx context.Context, ref ObjectRef) error {
	return s.request(ctx, ref, "DELETE", s.del)
}

func concurrentCleanupFixture(count int) (*ImageResultCleanupService, *concurrentCleanupRepo, *concurrentCleanupStorage, *ImageResultCleanupJob) {
	r := &concurrentCleanupRepo{alive: true, completed: map[int64]string{}, verified: map[int64]bool{}}
	for i := 1; i <= count; i++ {
		r.objects = append(r.objects, &ImageResultCleanupObject{ID: int64(i), Action: "delete", Attempts: 1,
			Object: ObjectRef{Provider: "custom_s3", Bucket: "images", StorageIdentity: "identity", ObjectKey: fmt.Sprintf("images/results/%d", i), SizeBytes: 3, ChecksumSHA256: "abc"}})
	}
	load := &imageResultCleanupLoadGuard{sample: func(context.Context) (*opsCollectedSystemStats, error) {
		cpu, mem := 10.0, 20.0
		return &opsCollectedSystemStats{cpuUsagePercent: &cpu, memoryUsagePercent: &mem}, nil
	}}
	s := &ImageResultCleanupService{repo: r, load: load, now: time.Now, wait: waitImageResultCleanup,
		storage: cleanupRuntimeStorageStub{cfg: ImageResultReferenceConfig{Provider: "custom_s3", Bucket: "images", StorageIdentity: "identity"}}}
	storage := &concurrentCleanupStorage{started: make(chan cleanupConcurrentRequest, 1000)}
	job := &ImageResultCleanupJob{ID: 42, LeaseVersion: 1, Target: ImageResultCleanupTarget{Provider: "custom_s3", Bucket: "images", Identity: "identity"}}
	return s, r, storage, job
}

func waitCleanupStarts(t *testing.T, s *concurrentCleanupStorage, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		select {
		case event := <-s.started:
			require.Equal(t, "HEAD", event.method)
		case <-time.After(2 * time.Second):
			t.Fatal("parallel storage requests did not start")
		}
	}
}

func waitCleanupDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup did not drain its dispatched requests")
		return context.DeadlineExceeded
	}
}

func TestImageResultCleanupParallelInFlightBound(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		concurrency, batch, count, bound int
	}{
		{"four_workers", 4, 8, 8, 4},
		{"sixteen_workers", 16, 16, 16, 16},
		{"batch_limits_workers", 16, 2, 8, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, storage, job := concurrentCleanupFixture(tc.count)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			storage.head = func(ctx context.Context, _ ObjectRef) error {
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			p := defaultImageResultCleanupPolicy(7)
			p.Concurrency, p.BatchSize, p.RequestsPerSecond = tc.concurrency, tc.batch, 1000
			done := make(chan error, 1)
			go func() { done <- s.execute(ctx, job, p, storage) }()
			waitCleanupStarts(t, storage, tc.bound)
			select {
			case <-storage.started:
				t.Fatal("more objects than the configured in-flight bound started")
			case <-time.After(30 * time.Millisecond):
			}
			once.Do(func() { close(release) })
			require.NoError(t, waitCleanupDone(t, done))
			require.Equal(t, tc.bound, storage.maximum)
			require.Zero(t, storage.active)
			require.Len(t, repo.claimed, tc.batch)
			require.Len(t, repo.completed, tc.batch)
			require.Len(t, storage.requests, tc.batch*2)
		})
	}
}

func TestImageResultCleanupParallelSharesTotalRequestRate(t *testing.T) {
	s, repo, storage, job := concurrentCleanupFixture(4)
	storage.head = func(ctx context.Context, _ ObjectRef) error { return waitImageResultCleanup(ctx, 110*time.Millisecond) }
	p := defaultImageResultCleanupPolicy(7)
	p.Concurrency, p.BatchSize, p.RequestsPerSecond = 4, 4, 25
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, s.execute(ctx, job, p, storage))
	require.Len(t, repo.completed, 4)
	require.Len(t, storage.requests, 8)
	require.Greater(t, storage.maximum, 1, "test must exercise actual overlapping I/O")
	for i := 1; i < len(storage.requests); i++ {
		require.GreaterOrEqual(t, storage.requests[i].at.Sub(storage.requests[i-1].at), 37*time.Millisecond,
			"HEAD and DELETE across all workers share one 25 requests/s budget")
	}
}

func TestImageResultCleanupParallelLegacyVerification(t *testing.T) {
	s, repo, storage, job := concurrentCleanupFixture(4)
	for _, object := range repo.objects {
		object.Object.StorageIdentity = ""
	}
	storage.head = func(ctx context.Context, _ ObjectRef) error { return waitImageResultCleanup(ctx, 30*time.Millisecond) }
	p := defaultImageResultCleanupPolicy(7)
	p.Concurrency, p.BatchSize, p.RequestsPerSecond = 4, 4, 1000
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, s.execute(ctx, job, p, storage))
	require.Len(t, repo.verified, 4)
	require.Len(t, repo.completed, 4)
	require.Greater(t, storage.maximum, 1)
	for _, outcome := range repo.completed {
		require.Equal(t, "deleted", outcome)
	}
}

func TestImageResultCleanupParallelCancellationDrains(t *testing.T) {
	s, repo, storage, job := concurrentCleanupFixture(10)
	storage.head = func(ctx context.Context, _ ObjectRef) error { <-ctx.Done(); return ctx.Err() }
	p := defaultImageResultCleanupPolicy(7)
	p.Concurrency, p.BatchSize, p.RequestsPerSecond = 4, 10, 1000
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.execute(ctx, job, p, storage) }()
	waitCleanupStarts(t, storage, 4)
	cancel()
	_ = waitCleanupDone(t, done)
	require.Len(t, repo.claimed, 4)
	require.Len(t, repo.completed, 4, "cancelled in-flight calls still need durable outcome reconciliation")
	require.Len(t, storage.requests, 4, "no DELETE or new object may start after cancellation")
	require.Zero(t, storage.active)
	for _, err := range repo.completionContextErrors {
		require.NoError(t, err)
	}
}

func TestImageResultCleanupParallelDeadlineDrains(t *testing.T) {
	s, repo, storage, job := concurrentCleanupFixture(10)
	storage.head = func(ctx context.Context, _ ObjectRef) error { <-ctx.Done(); return ctx.Err() }
	p := defaultImageResultCleanupPolicy(7)
	p.Concurrency, p.BatchSize, p.RequestsPerSecond = 4, 10, 1000
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_ = s.execute(ctx, job, p, storage)
	require.Len(t, repo.claimed, 4)
	require.Len(t, repo.completed, 4)
	require.Len(t, storage.requests, 4)
	require.Zero(t, storage.active)
	for _, err := range repo.completionContextErrors {
		require.NoError(t, err)
	}
}

func TestImageResultCleanupParallelCompletionFailureKeepsLease(t *testing.T) {
	s, repo, storage, job := concurrentCleanupFixture(10)
	repo.completeErr = errors.New("database confirmation unavailable")
	release := make(chan struct{})
	deleteRelease := make(chan struct{})
	var once sync.Once
	var deleteOnce sync.Once
	defer once.Do(func() { close(release) })
	defer deleteOnce.Do(func() { close(deleteRelease) })
	storage.head = func(ctx context.Context, _ ObjectRef) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	storage.del = func(ctx context.Context, _ ObjectRef) error {
		select {
		case <-deleteRelease:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p := defaultImageResultCleanupPolicy(7)
	p.Concurrency, p.BatchSize, p.RequestsPerSecond = 4, 10, 1000
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.execute(ctx, job, p, storage) }()
	waitCleanupStarts(t, storage, 4)
	once.Do(func() { close(release) })
	for i := 0; i < 4; i++ {
		select {
		case event := <-storage.started:
			require.Equal(t, "DELETE", event.method)
		case <-time.After(2 * time.Second):
			t.Fatal("all four admitted DELETE requests must be in flight before confirmation fails")
		}
	}
	deleteOnce.Do(func() { close(deleteRelease) })
	require.ErrorContains(t, waitCleanupDone(t, done), "database confirmation unavailable")
	require.Len(t, repo.claimed, 4)
	require.Len(t, repo.completed, 4, "every already-admitted DELETE outcome must be reconciled even when confirmation fails")
	require.Empty(t, repo.released, "uncertain tickets remain under the existing owner until lease recovery")
	require.Zero(t, storage.active)
}

func TestImageResultCleanupParallelPermissionDenialStopsQueuedRequests(t *testing.T) {
	s, repo, storage, job := concurrentCleanupFixture(10)
	storage.head = func(context.Context, ObjectRef) error { return cleanupHTTPError(403) }
	p := defaultImageResultCleanupPolicy(7)
	p.Concurrency, p.BatchSize, p.RequestsPerSecond = 4, 10, 5
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, s.execute(ctx, job, p, storage))
	require.Len(t, storage.requests, 1, "permission denial must close the shared gate before queued workers send")
	require.Equal(t, "HEAD", storage.requests[0].method)
	require.LessOrEqual(t, len(repo.claimed), 4)
	require.Equal(t, []string{"paused"}, repo.released)
	require.Zero(t, storage.active)
}

func TestImageResultCleanupParallelLeaseLossStopsQueuedRequests(t *testing.T) {
	s, repo, storage, job := concurrentCleanupFixture(10)
	storage.head = func(context.Context, ObjectRef) error {
		repo.mu.Lock()
		repo.alive = false
		repo.mu.Unlock()
		return nil
	}
	p := defaultImageResultCleanupPolicy(7)
	p.Concurrency, p.BatchSize, p.RequestsPerSecond = 4, 10, 5
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = s.execute(ctx, job, p, storage)
	require.Len(t, storage.requests, 1, "losing the lease after HEAD must block DELETE and other queued HEADs")
	require.LessOrEqual(t, len(repo.claimed), 4)
	require.Zero(t, storage.active)
}

func TestImageResultCleanupParallelHighLoadStopsQueuedRequests(t *testing.T) {
	s, repo, storage, job := concurrentCleanupFixture(10)
	storage.head = func(context.Context, ObjectRef) error {
		s.load.mu.Lock()
		s.load.last = time.Time{}
		s.load.sample = func(context.Context) (*opsCollectedSystemStats, error) {
			cpu, mem := 85.0, 20.0
			return &opsCollectedSystemStats{cpuUsagePercent: &cpu, memoryUsagePercent: &mem}, nil
		}
		s.load.mu.Unlock()
		return nil
	}
	p := defaultImageResultCleanupPolicy(7)
	p.Concurrency, p.BatchSize, p.RequestsPerSecond = 4, 10, 5
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, s.execute(ctx, job, p, storage))
	require.Len(t, storage.requests, 1, "high load must stop queued HEAD and DELETE requests")
	require.LessOrEqual(t, len(repo.claimed), 4)
	require.Equal(t, []string{"pending"}, repo.released)
	require.Zero(t, storage.active)
}

func TestImageResultCleanupSingleWorkerUsesAdmissionProtection(t *testing.T) {
	for _, concurrency := range []int{0, 1} {
		t.Run(fmt.Sprintf("concurrency_%d", concurrency), func(t *testing.T) {
			s, repo, storage, job := concurrentCleanupFixture(3)
			storage.head = func(context.Context, ObjectRef) error {
				repo.mu.Lock()
				repo.alive = false
				repo.mu.Unlock()
				return nil
			}
			p := defaultImageResultCleanupPolicy(7)
			p.Concurrency, p.BatchSize, p.RequestsPerSecond = concurrency, 3, 1000
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = s.execute(ctx, job, p, storage)
			require.Len(t, storage.requests, 1, "the default single worker must recheck ownership between HEAD and DELETE")
			require.Equal(t, "HEAD", storage.requests[0].method)
			require.Len(t, repo.claimed, 1)
			require.Zero(t, storage.active)
		})
	}
}
