package service

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageResultCleanupPolicyTimeBoundaries(t *testing.T) {
	p := defaultImageResultCleanupPolicy(7)
	require.NoError(t, validateImageResultCleanupPolicy(&p))
	age, err := imageResultCleanupAge(2, "days")
	require.NoError(t, err)
	require.Equal(t, 48*time.Hour, age)
	for _, value := range []int{-1, 0, 87601} {
		_, err := imageResultCleanupAge(value, "hours")
		require.Error(t, err)
	}
	for _, unit := range []string{"", "weeks"} {
		_, err := imageResultCleanupAge(1, unit)
		require.Error(t, err)
	}
	now := time.Date(2026, 11, 12, 19, 0, 0, 0, time.UTC) // Beijing 03:00 next day.
	p.ScheduleMode = "daily"
	require.Equal(t, now.Add(24*time.Hour), nextImageResultCleanupRun(p, now))
	require.Equal(t, now, nextImageResultCleanupRun(p, now.Add(-time.Minute)))
	p.ScheduleMode = "interval"
	require.Equal(t, now.Add(15*time.Minute), nextImageResultCleanupRun(p, now))
	f, err := parseImageResultCleanupFilters([]byte(`{"older_than_value":24,"older_than_unit":"hours"}`), now)
	require.NoError(t, err)
	require.Equal(t, now.Add(-24*time.Hour), *f.Before)
	_, err = parseImageResultCleanupFilters([]byte(`{"before":"2026-11-11T00:00:00Z","older_than_value":1,"older_than_unit":"days"}`), now)
	require.Error(t, err)
	_, err = parseImageResultCleanupFilters([]byte(`{"before":"2026-11-13T00:00:00Z"}`), now)
	require.Error(t, err)
}

func TestImageResultCleanupLoadGuardHysteresis(t *testing.T) {
	cpu, mem := 81.0, 50.0
	g := &imageResultCleanupLoadGuard{sample: func(context.Context) (*opsCollectedSystemStats, error) {
		return &opsCollectedSystemStats{cpuUsagePercent: &cpu, memoryUsagePercent: &mem}, nil
	}}
	now := time.Now()
	ok, _ := g.allow(context.Background(), now)
	require.False(t, ok)
	cpu = 65
	ok, _ = g.allow(context.Background(), now.Add(time.Minute))
	require.False(t, ok)
	ok, _ = g.allow(context.Background(), now.Add(2*time.Minute))
	require.True(t, ok)
	mem = 86
	ok, _ = g.allow(context.Background(), now.Add(3*time.Minute))
	require.False(t, ok)
	g.sample = func(context.Context) (*opsCollectedSystemStats, error) { return nil, errors.New("unavailable") }
	ok, reason := g.allow(context.Background(), now.Add(4*time.Minute))
	require.False(t, ok)
	require.Contains(t, reason, "指标不可用")
}

type cleanupServiceRepoStub struct {
	ImageResultCleanupRepository
	policy             *ImageResultCleanupPolicy
	saves              int
	verified           bool
	claimed, completed int
	claimedJobs        int
	objects            []*ImageResultCleanupObject
	released           string
}

func (r *cleanupServiceRepoStub) ClaimImageResultCleanupJob(context.Context, time.Time, time.Time) (*ImageResultCleanupJob, error) {
	r.claimedJobs++
	return nil, nil
}

func TestImageResultCleanupReapsCancellationWhenStorageUnavailable(t *testing.T) {
	p := defaultImageResultCleanupPolicy(90)
	p.Configured = true
	p.Revision = 1
	repo := &cleanupServiceRepoStub{policy: &p}
	s := &ImageResultCleanupService{repo: repo, storage: cleanupRuntimeStorageStub{referenceErr: errors.New("storage disabled")}, now: time.Now}
	require.NoError(t, s.RunOnce(context.Background()))
	require.Equal(t, 1, repo.claimedJobs, "expired cancelled jobs must be reconciled without a working storage target")
}

func (r *cleanupServiceRepoStub) GetImageResultCleanupPolicy(context.Context) (*ImageResultCleanupPolicy, error) {
	return r.policy, nil
}
func (r *cleanupServiceRepoStub) SaveImageResultCleanupPolicy(_ context.Context, p ImageResultCleanupPolicy, expected int64) (*ImageResultCleanupPolicy, error) {
	if r.policy != nil && expected != r.policy.Revision {
		return nil, ErrImageResultCleanupConflict
	}
	r.saves++
	p.Revision = expected + 1
	r.policy = &p
	return &p, nil
}

func TestImageResultCleanupPolicyRequiresCurrentRevision(t *testing.T) {
	p := defaultImageResultCleanupPolicy(7)
	p.Configured = true
	p.Revision = 4
	repo := &cleanupServiceRepoStub{policy: &p}
	s := &ImageResultCleanupService{repo: repo, now: time.Now}
	for _, revision := range []int64{0, 3} {
		input := p
		input.Revision = revision
		_, err := s.SavePolicy(context.Background(), 12, input)
		require.ErrorIs(t, err, ErrImageResultCleanupConflict)
	}
	require.Zero(t, repo.saves)
	saved, err := s.SavePolicy(context.Background(), 12, p)
	require.NoError(t, err)
	require.Equal(t, int64(5), saved.Revision)
	_, err = s.SavePolicy(context.Background(), 12, p)
	require.ErrorIs(t, err, ErrImageResultCleanupConflict)
}

func TestImageResultCleanupPolicyRequestRateBounds(t *testing.T) {
	require.Equal(t, 2, defaultImageResultCleanupPolicy(7).RequestsPerSecond)
	for _, rate := range []int{-1, 0, 1, 5, 50, 100, 500, 1000, 1001} {
		t.Run(strconv.Itoa(rate), func(t *testing.T) {
			p := defaultImageResultCleanupPolicy(7)
			p.Configured, p.Revision = true, 4
			repo := &cleanupServiceRepoStub{policy: &p}
			s := &ImageResultCleanupService{repo: repo, now: time.Now}
			input := p
			input.RequestsPerSecond = rate
			saved, err := s.SavePolicy(context.Background(), 12, input)
			if rate < 1 || rate > 1000 {
				require.ErrorContains(t, err, "1～1000")
				require.Zero(t, repo.saves)
				return
			}
			require.NoError(t, err)
			require.Equal(t, rate, saved.RequestsPerSecond)
			require.Equal(t, 1, repo.saves)
		})
	}
}

func TestImageResultCleanupUpgradePreservesExistingRetention(t *testing.T) {
	repo := &cleanupServiceRepoStub{}
	s := &ImageResultCleanupService{repo: repo, storage: cleanupRuntimeStorageStub{}, now: time.Now}
	p, err := s.Policy(context.Background())
	require.NoError(t, err)
	require.Equal(t, 90, p.RetentionValue)
	require.Equal(t, "days", p.RetentionUnit)
	require.Equal(t, 15, p.IntervalMinutes)
	require.False(t, p.Configured)
	_, err = s.Policy(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, repo.saves)
	p.RetentionValue = 7
	p.Configured = true
	p, err = s.Policy(context.Background())
	require.NoError(t, err)
	require.Equal(t, 7, p.RetentionValue)
}

func (r *cleanupServiceRepoStub) VerifyImageResultCleanupObject(context.Context, int64, int64, int64, string) error {
	r.verified = true
	return nil
}
func (r *cleanupServiceRepoStub) HeartbeatImageResultCleanupJob(context.Context, int64, int64, time.Time) (bool, error) {
	return true, nil
}
func (r *cleanupServiceRepoStub) ClaimImageResultCleanupObject(context.Context, int64, int64, time.Time) (*ImageResultCleanupObject, error) {
	r.claimed++
	if len(r.objects) == 0 {
		return nil, nil
	}
	o := r.objects[0]
	r.objects = r.objects[1:]
	return o, nil
}
func (r *cleanupServiceRepoStub) CompleteImageResultCleanupObject(context.Context, int64, int64, int64, string, *time.Time, string) error {
	r.completed++
	return nil
}
func (r *cleanupServiceRepoStub) ReleaseImageResultCleanupJob(_ context.Context, _ int64, _ int64, status string, _ *time.Time, _ string) error {
	r.released = status
	return nil
}

type cleanupObjectStorageStub struct {
	DurableImageStorage
	ref            ObjectRef
	repo           *cleanupServiceRepoStub
	deleteErr      error
	headErr        error
	deletes, heads int
	requests       []time.Time
	now            func() time.Time
	onRequest      func()
}

func (s *cleanupObjectStorageStub) Head(ctx context.Context, _ ObjectRef) (ObjectMetadata, error) {
	gate, ok := ImageCleanupRequestGate(ctx)
	if ok {
		if err := gate(ctx); err != nil {
			return ObjectMetadata{}, err
		}
	}
	s.heads++
	if s.now != nil {
		s.requests = append(s.requests, s.now())
	}
	if s.onRequest != nil {
		s.onRequest()
	}
	return ObjectMetadata{ObjectRef: s.ref}, s.headErr
}
func (s *cleanupObjectStorageStub) Delete(ctx context.Context, _ ObjectRef) error {
	gate, ok := ImageCleanupRequestGate(ctx)
	if ok {
		if err := gate(ctx); err != nil {
			return err
		}
	}
	if s.repo != nil && !s.repo.verified {
		return errors.New("identity must be durable before delete")
	}
	s.deletes++
	if s.now != nil {
		s.requests = append(s.requests, s.now())
	}
	if s.onRequest != nil {
		s.onRequest()
	}
	return s.deleteErr
}

type cleanupHTTPError int

func (e cleanupHTTPError) Error() string       { return "storage error" }
func (e cleanupHTTPError) HTTPStatusCode() int { return int(e) }

func TestImageResultCleanupLegacyVerificationAndUnknownOutcome(t *testing.T) {
	now := time.Now()
	repo := &cleanupServiceRepoStub{}
	s := &ImageResultCleanupService{repo: repo, now: func() time.Time { return now }}
	ref := ObjectRef{Provider: "custom_s3", Bucket: "images", ObjectKey: "results/old.png", SizeBytes: 100, ChecksumSHA256: "abc"}
	job := &ImageResultCleanupJob{ID: 1, Target: ImageResultCleanupTarget{Provider: ref.Provider, Bucket: ref.Bucket, Identity: "identity"}}
	item := &ImageResultCleanupObject{Object: ref, Attempts: 1}
	storage := &cleanupObjectStorageStub{ref: ref, repo: repo, deleteErr: context.DeadlineExceeded}
	outcome, retry, _, stop := s.deleteObject(context.Background(), job, item, storage, func(context.Context) error { return nil })
	require.True(t, repo.verified)
	require.Equal(t, 1, storage.deletes)
	require.Equal(t, "retry", outcome)
	require.NotNil(t, retry)
	require.False(t, stop)
	item.Object.StorageIdentity = "identity"
	storage.deleteErr = cleanupHTTPError(404)
	outcome, _, _, _ = s.deleteObject(context.Background(), job, item, storage, func(context.Context) error { return nil })
	require.Equal(t, "missing", outcome)
	require.Equal(t, 2, storage.heads)
	item.Object.StorageIdentity = ""
	storage.ref.ChecksumSHA256 = "wrong"
	storage.deletes = 0
	outcome, _, _, _ = s.deleteObject(context.Background(), job, item, storage, func(context.Context) error { return nil })
	require.Equal(t, "skipped", outcome)
	require.Zero(t, storage.deletes)
	storage.headErr = cleanupHTTPError(404)
	outcome, _, _, _ = s.deleteObject(context.Background(), job, item, storage, func(context.Context) error { return nil })
	require.Equal(t, "skipped", outcome)
}

func TestImageResultCleanupMissingLocalFileDoesNotBlockQueue(t *testing.T) {
	storage, err := NewLocalImageStorage(t.TempDir())
	require.NoError(t, err)
	ref := ObjectRef{Provider: ImageStorageProviderLocal, Bucket: "local", ObjectKey: "images/results/missing.png", SizeBytes: 100, ChecksumSHA256: "old-checksum"}
	_, headErr := storage.Head(context.Background(), ref)
	require.Error(t, headErr)
	missing, denied := cleanupStorageError(headErr)
	require.True(t, missing)
	require.False(t, denied)
	s := &ImageResultCleanupService{repo: &cleanupServiceRepoStub{}, now: time.Now}
	job := &ImageResultCleanupJob{Target: ImageResultCleanupTarget{Provider: ref.Provider, Bucket: ref.Bucket, Identity: storage.storageIdentity}}
	outcome, retry, reason, stop := s.deleteObject(context.Background(), job, &ImageResultCleanupObject{Object: ref, Attempts: 1}, storage, func(context.Context) error { return nil })
	require.Equal(t, "skipped", outcome)
	require.Nil(t, retry)
	require.False(t, stop)
	require.Contains(t, reason, "无法确认")
	ref.StorageIdentity = job.Target.Identity
	outcome, retry, _, stop = s.deleteObject(context.Background(), job, &ImageResultCleanupObject{Object: ref, Attempts: 1}, storage, func(context.Context) error { return nil })
	require.Equal(t, "missing", outcome)
	require.Nil(t, retry)
	require.False(t, stop)
}

func TestImageResultCleanupKnownMissingObjectDoesNotCountDelete(t *testing.T) {
	s := &ImageResultCleanupService{repo: &cleanupServiceRepoStub{}, now: time.Now}
	ref := ObjectRef{Provider: "custom_s3", Bucket: "images", StorageIdentity: "identity", ObjectKey: "results/missing.png", SizeBytes: 100}
	job := &ImageResultCleanupJob{Target: ImageResultCleanupTarget{Provider: ref.Provider, Bucket: ref.Bucket, Identity: ref.StorageIdentity}}
	storage := &cleanupObjectStorageStub{headErr: cleanupHTTPError(404)}
	outcome, retry, _, stop := s.deleteObject(context.Background(), job, &ImageResultCleanupObject{Object: ref, Attempts: 1}, storage, func(context.Context) error { return nil })
	require.Equal(t, "missing", outcome)
	require.Nil(t, retry)
	require.False(t, stop)
	require.Equal(t, 1, storage.heads)
	require.Zero(t, storage.deletes)
}

func TestImageResultCleanupLocalPermissionDenialPauses(t *testing.T) {
	s := &ImageResultCleanupService{repo: &cleanupServiceRepoStub{}, now: time.Now}
	ref := ObjectRef{Provider: ImageStorageProviderLocal, Bucket: "local", StorageIdentity: "identity", ObjectKey: "results/private.png"}
	job := &ImageResultCleanupJob{Target: ImageResultCleanupTarget{Provider: ref.Provider, Bucket: ref.Bucket, Identity: ref.StorageIdentity}}
	storage := &cleanupObjectStorageStub{headErr: &os.PathError{Op: "stat", Path: ref.ObjectKey, Err: os.ErrPermission}}
	outcome, retry, reason, stop := s.deleteObject(context.Background(), job, &ImageResultCleanupObject{Object: ref, Attempts: 1}, storage, func(context.Context) error { return nil })
	require.Equal(t, "retry", outcome)
	require.NotNil(t, retry)
	require.True(t, stop)
	require.Contains(t, reason, "权限")
	require.Zero(t, storage.deletes)
}

type cleanupRuntimeStorageStub struct {
	cfg          ImageResultReferenceConfig
	referenceErr error
}

func (s cleanupRuntimeStorageStub) ResultReferenceConfig(context.Context) (ImageResultReferenceConfig, error) {
	return s.cfg, s.referenceErr
}
func (s cleanupRuntimeStorageStub) RuntimeConfig(context.Context) (AsyncImageRuntimeConfig, error) {
	return defaultAsyncImageRuntimeConfig(), nil
}
func (s cleanupRuntimeStorageStub) DurableStorage(context.Context) (DurableImageStorage, bool, error) {
	return nil, false, nil
}

func TestImageResultCleanupWorkerBoundsAndRequestRate(t *testing.T) {
	now := time.Now()
	clock := now
	repo := &cleanupServiceRepoStub{}
	ref := ObjectRef{Provider: "custom_s3", Bucket: "b", StorageIdentity: "identity", ObjectKey: "images/results/1"}
	for i := 0; i < 100; i++ {
		repo.objects = append(repo.objects, &ImageResultCleanupObject{ID: int64(i + 1), Object: ref, Action: "delete"})
	}
	load := &imageResultCleanupLoadGuard{sample: func(context.Context) (*opsCollectedSystemStats, error) {
		cpu, mem := 10.0, 20.0
		return &opsCollectedSystemStats{cpuUsagePercent: &cpu, memoryUsagePercent: &mem}, nil
	}}
	s := &ImageResultCleanupService{repo: repo, storage: cleanupRuntimeStorageStub{cfg: ImageResultReferenceConfig{Provider: ref.Provider, Bucket: ref.Bucket, StorageIdentity: "identity"}}, load: load, now: func() time.Time { return clock }, wait: func(_ context.Context, d time.Duration) error {
		if d > 0 {
			clock = clock.Add(d)
		}
		return nil
	}}
	storage := &cleanupObjectStorageStub{now: s.now}
	p := defaultImageResultCleanupPolicy(90)
	p.BatchSize = 50
	p.RequestsPerSecond = 1
	err := s.execute(context.Background(), &ImageResultCleanupJob{Target: ImageResultCleanupTarget{Provider: ref.Provider, Bucket: ref.Bucket, Identity: "identity"}}, p, storage)
	require.NoError(t, err)
	require.LessOrEqual(t, repo.completed, 31)
	require.Equal(t, "pending", repo.released)
	for i := 1; i < len(storage.requests); i++ {
		require.GreaterOrEqual(t, storage.requests[i].Sub(storage.requests[i-1]), time.Second)
	}
	require.LessOrEqual(t, repo.claimed, 50)
}

func TestImageResultCleanupWorkerHighRateBounds(t *testing.T) {
	for _, tc := range []struct {
		name           string
		batchSize      int
		requestLatency time.Duration
		wantCompleted  int
	}{
		{"default_batch", 50, 0, 50},
		{"maximum_batch", 100, 0, 100},
		{"time_slice", 100, 5 * time.Second, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			clock := start
			repo := &cleanupServiceRepoStub{}
			ref := ObjectRef{Provider: "custom_s3", Bucket: "b", StorageIdentity: "identity", ObjectKey: "images/results/1"}
			for i := 0; i < 150; i++ {
				repo.objects = append(repo.objects, &ImageResultCleanupObject{ID: int64(i + 1), Object: ref, Action: "delete"})
			}
			load := &imageResultCleanupLoadGuard{sample: func(context.Context) (*opsCollectedSystemStats, error) {
				cpu, mem := 10.0, 20.0
				return &opsCollectedSystemStats{cpuUsagePercent: &cpu, memoryUsagePercent: &mem}, nil
			}}
			s := &ImageResultCleanupService{repo: repo, storage: cleanupRuntimeStorageStub{cfg: ImageResultReferenceConfig{Provider: ref.Provider, Bucket: ref.Bucket, StorageIdentity: "identity"}}, load: load, now: func() time.Time { return clock }, wait: func(ctx context.Context, d time.Duration) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if d > 0 {
					clock = clock.Add(d)
				}
				return nil
			}}
			storage := &cleanupObjectStorageStub{now: s.now, onRequest: func() { clock = clock.Add(tc.requestLatency) }}
			p := defaultImageResultCleanupPolicy(90)
			p.BatchSize, p.RequestsPerSecond = tc.batchSize, 1000
			err := s.execute(context.Background(), &ImageResultCleanupJob{Target: ImageResultCleanupTarget{Provider: ref.Provider, Bucket: ref.Bucket, Identity: "identity"}}, p, storage)
			require.NoError(t, err)
			require.Equal(t, tc.wantCompleted, repo.claimed)
			require.Equal(t, tc.wantCompleted, repo.completed)
			require.Equal(t, tc.wantCompleted, storage.heads)
			require.Equal(t, tc.wantCompleted, storage.deletes)
			require.Equal(t, "pending", repo.released)
			require.LessOrEqual(t, clock.Sub(start), 30*time.Second)
			for i := 1; i < len(storage.requests); i++ {
				gap := storage.requests[i].Sub(storage.requests[i-1])
				require.GreaterOrEqual(t, gap, time.Millisecond, "HEAD and DELETE share the nonzero request interval")
				if tc.requestLatency == 0 {
					require.Equal(t, time.Millisecond, gap, "1000/s must not remain capped at the former 5/s limit")
				}
			}
		})
	}
}
