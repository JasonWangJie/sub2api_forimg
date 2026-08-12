package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type asyncImageRetentionRepoStub struct {
	stagingDeleted int64
	inputs         []AsyncImageInputObject
	results        []AsyncImageResult
	tasks          []AsyncImageRetentionTask
	taskResults    map[string][]AsyncImageResult
	events         *[]string
	sharedObjects  map[string]bool
	referenceArgs  *[]string
}

func (s *asyncImageRetentionRepoStub) RegisterAsyncImageInputObject(context.Context, RegisterAsyncImageInputObjectParams) (*AsyncImageInputObject, error) {
	return nil, nil
}

func (s *asyncImageRetentionRepoStub) FindAsyncImageInputObjectsByURLHashes(context.Context, []string) ([]AsyncImageInputObject, error) {
	return nil, nil
}

func (s *asyncImageRetentionRepoStub) DeleteExpiredAsyncImageStagingObjects(context.Context, time.Time, int) (int64, error) {
	return s.stagingDeleted, nil
}

func (s *asyncImageRetentionRepoStub) ClaimExpiredAsyncImageInputObjects(context.Context, time.Time, time.Time, int) ([]AsyncImageInputObject, error) {
	return s.inputs, nil
}

func (s *asyncImageRetentionRepoStub) CompleteAsyncImageInputObjectDeletion(_ context.Context, id int64, _ time.Time) error {
	*s.events = append(*s.events, "complete-input")
	return nil
}

func (s *asyncImageRetentionRepoStub) ReleaseAsyncImageInputObjectDeletion(_ context.Context, id int64, _ time.Time) error {
	*s.events = append(*s.events, "release-input")
	return nil
}

func (s *asyncImageRetentionRepoStub) ClaimExpiredAsyncImageResults(context.Context, time.Time, time.Time, int) ([]AsyncImageResult, error) {
	return s.results, nil
}

func (s *asyncImageRetentionRepoStub) CompleteAsyncImageResultDeletion(_ context.Context, id int64, _ time.Time) error {
	*s.events = append(*s.events, "complete-result")
	return nil
}

func (s *asyncImageRetentionRepoStub) ReleaseAsyncImageResultDeletion(_ context.Context, id int64, _ time.Time) error {
	*s.events = append(*s.events, "release-result")
	return nil
}

func (s *asyncImageRetentionRepoStub) ClaimExpiredAsyncImageTasks(context.Context, time.Time, time.Time, time.Time, int) ([]AsyncImageRetentionTask, error) {
	return s.tasks, nil
}

func (s *asyncImageRetentionRepoStub) CompleteAsyncImageTaskDeletion(_ context.Context, taskID string, _ time.Time) error {
	*s.events = append(*s.events, "complete-task")
	return nil
}

func (s *asyncImageRetentionRepoStub) ReleaseAsyncImageTaskDeletion(_ context.Context, taskID string, _ time.Time) error {
	*s.events = append(*s.events, "release-task")
	return nil
}

func (s *asyncImageRetentionRepoStub) ListAsyncImageResults(_ context.Context, taskID string) ([]AsyncImageResult, error) {
	return s.taskResults[taskID], nil
}

func (s *asyncImageRetentionRepoStub) HasLiveImageObjectReference(_ context.Context, ref ObjectRef, excludedResultID int64, excludedTaskID string) (bool, error) {
	if s.referenceArgs != nil {
		*s.referenceArgs = append(*s.referenceArgs, ref.ObjectKey+":"+excludedTaskID)
	}
	return s.sharedObjects[ref.ObjectKey], nil
}

type asyncImageRetentionStorageStub struct {
	durable DurableImageStorage
	cfg     AsyncImageRuntimeConfig
}

func (s *asyncImageRetentionStorageStub) DurableStorage(context.Context) (DurableImageStorage, bool, error) {
	return s.durable, s.durable != nil, nil
}

func (s *asyncImageRetentionStorageStub) RuntimeConfig(context.Context) (AsyncImageRuntimeConfig, error) {
	return s.cfg, nil
}

type asyncImageRetentionDurableStub struct {
	events   *[]string
	failKeys map[string]error
}

func (s *asyncImageRetentionDurableStub) Save(context.Context, string, string, []byte) (string, error) {
	return "", nil
}

func (s *asyncImageRetentionDurableStub) SaveObject(context.Context, string, string, []byte) (ObjectRef, error) {
	return ObjectRef{}, nil
}

func (s *asyncImageRetentionDurableStub) SignURL(context.Context, ObjectRef, time.Duration) (ObjectAccess, error) {
	return ObjectAccess{}, nil
}

func (s *asyncImageRetentionDurableStub) Read(context.Context, ObjectRef) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func (s *asyncImageRetentionDurableStub) Head(context.Context, ObjectRef) (ObjectMetadata, error) {
	return ObjectMetadata{}, nil
}

func (s *asyncImageRetentionDurableStub) Delete(_ context.Context, ref ObjectRef) error {
	*s.events = append(*s.events, "delete:"+ref.ObjectKey)
	return s.failKeys[ref.ObjectKey]
}

type asyncImageUploadIntentRetentionRepoStub struct {
	*asyncImageRetentionRepoStub
	stateDeleted int64
	intents      []AsyncImageUploadCleanupIntent
	removed      bool
}

type asyncImageResultIntentRetentionRepoStub struct {
	*asyncImageRetentionRepoStub
	intents []AsyncImageResultUploadIntent
}

func (s *asyncImageResultIntentRetentionRepoStub) ClaimExpiredAsyncImageResultUploadIntents(context.Context, time.Time, time.Time, int) ([]AsyncImageResultUploadIntent, error) {
	return s.intents, nil
}

func (s *asyncImageResultIntentRetentionRepoStub) CompleteAsyncImageResultUploadIntentDeletion(_ context.Context, _ int64, _ time.Time) error {
	*s.events = append(*s.events, "complete-result-intent")
	return nil
}

func (s *asyncImageResultIntentRetentionRepoStub) ReleaseAsyncImageResultUploadIntentDeletion(_ context.Context, _ int64, _ time.Time) error {
	*s.events = append(*s.events, "release-result-intent")
	return nil
}

func (s *asyncImageUploadIntentRetentionRepoStub) DeleteExpiredAsyncImageUploadAdmissionState(context.Context, time.Time, int) (int64, error) {
	return s.stateDeleted, nil
}

func (s *asyncImageUploadIntentRetentionRepoStub) ClaimAsyncImageUploadCleanupIntents(context.Context, time.Time, time.Time, int) ([]AsyncImageUploadCleanupIntent, error) {
	return s.intents, nil
}

func (s *asyncImageUploadIntentRetentionRepoStub) CompleteAsyncImageUploadIntentDeletion(context.Context, string, time.Time) (bool, error) {
	*s.events = append(*s.events, "complete-upload-intent")
	return s.removed, nil
}

func (s *asyncImageUploadIntentRetentionRepoStub) ReleaseAsyncImageUploadIntentDeletion(context.Context, string, time.Time) error {
	*s.events = append(*s.events, "release-upload-intent")
	return nil
}

func TestAsyncImageRetentionKeepsIntentAfterFirstSuccessfulDelete(t *testing.T) {
	now := time.Now().UTC()
	events := make([]string, 0)
	claimedAt := now.Add(-time.Minute)
	base := &asyncImageRetentionRepoStub{
		events: &events, taskResults: map[string][]AsyncImageResult{}, sharedObjects: map[string]bool{},
	}
	repo := &asyncImageUploadIntentRetentionRepoStub{
		asyncImageRetentionRepoStub: base,
		stateDeleted:                3,
		intents: []AsyncImageUploadCleanupIntent{{
			ReservationID: "asyncimg_orphan", ObjectRef: ObjectRef{ObjectKey: "orphan"}, CleanupClaimedAt: claimedAt,
		}},
	}
	durable := &asyncImageRetentionDurableStub{events: &events, failKeys: map[string]error{}}
	svc := &AsyncImageRetentionService{
		repo: repo,
		storage: &asyncImageRetentionStorageStub{
			durable: durable,
			cfg:     AsyncImageRuntimeConfig{ResultRetentionDays: 90, TaskRetentionDays: 90},
		},
		batch: 25,
	}
	stats, err := svc.RunOnce(context.Background(), now)
	require.NoError(t, err)
	require.Equal(t, int64(3), stats.UploadStateDeleted)
	require.Zero(t, stats.UploadIntentsDeleted)
	require.Equal(t, []string{"delete:orphan", "complete-upload-intent"}, events)
}

func TestAsyncImageRetentionDoesNotDeleteResultIntentWithLiveReference(t *testing.T) {
	now := time.Now().UTC()
	claimedAt := now.Add(-time.Minute)
	events := make([]string, 0)
	checks := make([]string, 0)
	base := &asyncImageRetentionRepoStub{
		events: &events, taskResults: map[string][]AsyncImageResult{},
		sharedObjects: map[string]bool{"shared-intent": true}, referenceArgs: &checks,
	}
	repo := &asyncImageResultIntentRetentionRepoStub{
		asyncImageRetentionRepoStub: base,
		intents: []AsyncImageResultUploadIntent{{
			ID: 9, TaskID: "asyncimg_partial", ImageIndex: 0,
			ObjectRef: serviceObjectRefForRetentionTest("shared-intent"),
			ExpiresAt: now.Add(-time.Hour), CleanupClaimedAt: &claimedAt,
		}},
	}
	durable := &asyncImageRetentionDurableStub{events: &events, failKeys: map[string]error{}}
	svc := &AsyncImageRetentionService{
		repo: repo,
		storage: &asyncImageRetentionStorageStub{
			durable: durable,
			cfg:     AsyncImageRuntimeConfig{ResultRetentionDays: 90, TaskRetentionDays: 90},
		},
	}

	stats, err := svc.RunOnce(context.Background(), now)
	require.NoError(t, err)
	require.Equal(t, 1, stats.ResultUploadIntentsDeleted)
	require.Equal(t, []string{"complete-result-intent"}, events)
	require.Equal(t, []string{"shared-intent:"}, checks)
}

func serviceObjectRefForRetentionTest(key string) ObjectRef {
	return ObjectRef{Provider: "aliyun", Bucket: "images", ObjectKey: key}
}

func TestAsyncImageRetentionDeletesObjectsBeforeRowsAndTasks(t *testing.T) {
	now := time.Now().UTC()
	events := make([]string, 0)
	claim := now.Add(-time.Minute)
	repo := &asyncImageRetentionRepoStub{
		stagingDeleted: 2,
		inputs: []AsyncImageInputObject{{
			ID: 1, ObjectRef: ObjectRef{ObjectKey: "input"}, CleanupClaimedAt: &claim,
		}},
		results: []AsyncImageResult{{
			ID: 2, ObjectKey: "result", CleanupClaimedAt: claim,
		}},
		tasks: []AsyncImageRetentionTask{{TaskID: "asyncimg_old", CleanupClaimedAt: claim}},
		taskResults: map[string][]AsyncImageResult{
			"asyncimg_old": {{ID: 3, ObjectKey: "task-result"}},
		},
		events: &events,
	}
	durable := &asyncImageRetentionDurableStub{events: &events, failKeys: map[string]error{}}
	svc := &AsyncImageRetentionService{
		repo: repo,
		storage: &asyncImageRetentionStorageStub{
			durable: durable,
			cfg:     AsyncImageRuntimeConfig{ResultRetentionDays: 90, TaskRetentionDays: 90},
		},
		batch: 25,
	}

	stats, err := svc.RunOnce(context.Background(), now)
	require.NoError(t, err)
	require.Equal(t, AsyncImageRetentionStats{StagingDeleted: 2, InputsDeleted: 1, ResultsDeleted: 1, TasksDeleted: 1}, stats)
	require.Equal(t, []string{
		"delete:input", "complete-input",
		"delete:result", "complete-result",
		"delete:task-result", "complete-task",
	}, events)
}

func TestAsyncImageRetentionReleasesClaimWhenObjectDeleteFails(t *testing.T) {
	now := time.Now().UTC()
	events := make([]string, 0)
	claim := now.Add(-time.Minute)
	repo := &asyncImageRetentionRepoStub{
		inputs: []AsyncImageInputObject{{
			ID: 1, ObjectRef: ObjectRef{ObjectKey: "input"}, CleanupClaimedAt: &claim,
		}},
		taskResults: map[string][]AsyncImageResult{}, events: &events,
	}
	durable := &asyncImageRetentionDurableStub{
		events: &events, failKeys: map[string]error{"input": errors.New("storage unavailable")},
	}
	svc := &AsyncImageRetentionService{
		repo: repo,
		storage: &asyncImageRetentionStorageStub{
			durable: durable,
			cfg:     AsyncImageRuntimeConfig{ResultRetentionDays: 90, TaskRetentionDays: 90},
		},
	}

	stats, err := svc.RunOnce(context.Background(), now)
	require.Error(t, err)
	require.Zero(t, stats.InputsDeleted)
	require.Equal(t, []string{"delete:input", "release-input"}, events)
}

func TestAsyncImageRetentionKeepsSharedObjectAndOnlyRemovesResultRow(t *testing.T) {
	now := time.Now().UTC()
	events := make([]string, 0)
	checks := make([]string, 0)
	claim := now.Add(-time.Minute)
	repo := &asyncImageRetentionRepoStub{
		results: []AsyncImageResult{{
			ID: 2, ObjectKey: "shared-result", CleanupClaimedAt: claim,
		}},
		taskResults:   map[string][]AsyncImageResult{},
		events:        &events,
		sharedObjects: map[string]bool{"shared-result": true},
		referenceArgs: &checks,
	}
	durable := &asyncImageRetentionDurableStub{events: &events, failKeys: map[string]error{}}
	svc := &AsyncImageRetentionService{
		repo: repo,
		storage: &asyncImageRetentionStorageStub{
			durable: durable,
			cfg:     AsyncImageRuntimeConfig{ResultRetentionDays: 90, TaskRetentionDays: 90},
		},
	}

	stats, err := svc.RunOnce(context.Background(), now)
	require.NoError(t, err)
	require.Equal(t, 1, stats.ResultsDeleted)
	require.Equal(t, []string{"complete-result"}, events)
	require.Equal(t, []string{"shared-result:"}, checks)
}

type asyncImageInputResolverRepoStub struct {
	AsyncImageTaskRepository
	objects []AsyncImageInputObject
}

func (s *asyncImageInputResolverRepoStub) RegisterAsyncImageInputObject(context.Context, RegisterAsyncImageInputObjectParams) (*AsyncImageInputObject, error) {
	return nil, nil
}

func (s *asyncImageInputResolverRepoStub) FindAsyncImageInputObjectsByURLHashes(context.Context, []string) ([]AsyncImageInputObject, error) {
	return s.objects, nil
}

func TestResolveOwnedInputObjectIDsRejectsAnotherAPIKey(t *testing.T) {
	now := time.Now().UTC()
	repo := &asyncImageInputResolverRepoStub{objects: []AsyncImageInputObject{{
		ID: 7, APIKeyID: 99, ExpiresAt: now.Add(time.Hour),
	}}}
	svc := NewAsyncImageTaskService(repo)

	_, err := svc.ResolveOwnedInputObjectIDs(context.Background(), 42, []string{"https://storage.example/input"}, now)
	require.ErrorIs(t, err, ErrAsyncImageInvalidInput)
}

func TestResolveOwnedInputObjectIDsAllowsUnknownRemoteAndBindsOwnedUpload(t *testing.T) {
	now := time.Now().UTC()
	repo := &asyncImageInputResolverRepoStub{objects: []AsyncImageInputObject{{
		ID: 7, APIKeyID: 42, ExpiresAt: now.Add(time.Hour),
	}}}
	svc := NewAsyncImageTaskService(repo)

	ids, err := svc.ResolveOwnedInputObjectIDs(context.Background(), 42, []string{
		"https://storage.example/input", "https://remote.example/reference.png",
	}, now)
	require.NoError(t, err)
	require.Equal(t, []int64{7}, ids)
}

func TestAsyncImageInputURLHashCanonicalizesFragmentAndHostCase(t *testing.T) {
	base := "https://storage.example/input.png?token=abc"
	withFragment := "HTTPS://STORAGE.EXAMPLE/input.png?token=abc#preview"
	require.Equal(t, AsyncImageInputURLHash(base), AsyncImageInputURLHash(withFragment))

	hashes := AsyncImageInputURLHashes(withFragment)
	require.GreaterOrEqual(t, len(hashes), 3, "resource and legacy hashes remain available during migration")
	require.Equal(t, AsyncImageInputURLHash(base), hashes[0])
	require.NotEqual(t, hashes[0], hashes[1])
}

func TestAsyncImageInputURLHashesTreatsSignedQueryVariantsAsOneResource(t *testing.T) {
	a := AsyncImageInputURLHashes("https://storage.example/input.png?x=1&y=2")
	b := AsyncImageInputURLHashes("https://storage.example/input.png?y=2&x=1#preview")
	require.Contains(t, b, a[1], "the query-independent object resource hash must be stable")
}
