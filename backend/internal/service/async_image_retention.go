package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAsyncImageCleanupBatch      = 100
	defaultAsyncImageCleanupClaimLease = 30 * time.Minute
)

// AsyncImageInputObject stores the durable identity of an SC upload. URLHash
// is only an ownership lookup key; expiring signed URLs are never persisted.
type AsyncImageInputObject struct {
	ID               int64
	UploadID         string
	UserID           int64
	APIKeyID         int64
	ObjectRef        ObjectRef
	URLHash          string
	Filename         string
	ExpiresAt        time.Time
	CleanupClaimedAt *time.Time
	CreatedAt        time.Time
}

type RegisterAsyncImageInputObjectParams struct {
	UploadID  string
	UserID    int64
	APIKeyID  int64
	ObjectRef ObjectRef
	URLHash   string
	Filename  string
	ExpiresAt time.Time
}

// AsyncImageRetentionTask is a terminal task claimed for final removal.
type AsyncImageRetentionTask struct {
	TaskID           string
	CleanupClaimedAt time.Time
}

// AsyncImageInputObjectRepository is kept separate from
// AsyncImageTaskRepository so existing task-service test doubles and legacy
// callers do not need to implement upload persistence.
type AsyncImageInputObjectRepository interface {
	RegisterAsyncImageInputObject(ctx context.Context, params RegisterAsyncImageInputObjectParams) (*AsyncImageInputObject, error)
	FindAsyncImageInputObjectsByURLHashes(ctx context.Context, hashes []string) ([]AsyncImageInputObject, error)
}

type AsyncImageTaskInputReference struct {
	URLHash   string
	ObjectRef ObjectRef
}

type AsyncImageTaskInputReferenceRepository interface {
	ResolveAsyncImageTaskInputReferences(ctx context.Context, taskID string, urlHashes []string) ([]AsyncImageTaskInputReference, error)
}

func (s *AsyncImageTaskService) ResolveTaskInputReferences(
	ctx context.Context,
	taskID string,
	urlHashes []string,
) (map[string]ObjectRef, error) {
	if s == nil || s.repo == nil || strings.TrimSpace(taskID) == "" || len(urlHashes) == 0 {
		return map[string]ObjectRef{}, nil
	}
	repo, ok := s.repo.(AsyncImageTaskInputReferenceRepository)
	if !ok {
		return nil, errors.New("async image task input reference repository is unavailable")
	}
	references, err := repo.ResolveAsyncImageTaskInputReferences(ctx, strings.TrimSpace(taskID), urlHashes)
	if err != nil {
		return nil, err
	}
	resolved := make(map[string]ObjectRef, len(references))
	for _, reference := range references {
		hash := strings.TrimSpace(reference.URLHash)
		if hash == "" {
			continue
		}
		if existing, exists := resolved[hash]; exists && (existing.Provider != reference.ObjectRef.Provider || existing.Bucket != reference.ObjectRef.Bucket || existing.ObjectKey != reference.ObjectRef.ObjectKey) {
			return nil, errors.New("ambiguous asynchronous image input reference")
		}
		resolved[hash] = reference.ObjectRef
	}
	return resolved, nil
}

// AsyncImageRetentionRepository owns the claim/delete protocol used by the
// background cleanup loop. A claimed row cannot be newly attached to a task.
type AsyncImageRetentionRepository interface {
	AsyncImageInputObjectRepository
	DeleteExpiredAsyncImageStagingObjects(ctx context.Context, before time.Time, limit int) (int64, error)
	ClaimExpiredAsyncImageInputObjects(ctx context.Context, before, staleBefore time.Time, limit int) ([]AsyncImageInputObject, error)
	CompleteAsyncImageInputObjectDeletion(ctx context.Context, id int64, claimedAt time.Time) error
	ReleaseAsyncImageInputObjectDeletion(ctx context.Context, id int64, claimedAt time.Time) error
	ClaimExpiredAsyncImageResults(ctx context.Context, createdBefore, staleBefore time.Time, limit int) ([]AsyncImageResult, error)
	CompleteAsyncImageResultDeletion(ctx context.Context, id int64, claimedAt time.Time) error
	ReleaseAsyncImageResultDeletion(ctx context.Context, id int64, claimedAt time.Time) error
	ClaimExpiredAsyncImageTasks(ctx context.Context, expiresBefore, createdBefore, staleBefore time.Time, limit int) ([]AsyncImageRetentionTask, error)
	CompleteAsyncImageTaskDeletion(ctx context.Context, taskID string, claimedAt time.Time) error
	ReleaseAsyncImageTaskDeletion(ctx context.Context, taskID string, claimedAt time.Time) error
	ListAsyncImageResults(ctx context.Context, taskID string) ([]AsyncImageResult, error)
}

type AsyncImageResultUploadIntentRetentionRepository interface {
	ClaimExpiredAsyncImageResultUploadIntents(ctx context.Context, before, staleBefore time.Time, limit int) ([]AsyncImageResultUploadIntent, error)
	CompleteAsyncImageResultUploadIntentDeletion(ctx context.Context, id int64, claimedAt time.Time) error
	ReleaseAsyncImageResultUploadIntentDeletion(ctx context.Context, id int64, claimedAt time.Time) error
}

type AsyncImageRetentionStorage interface {
	DurableStorage(ctx context.Context) (DurableImageStorage, bool, error)
	RuntimeConfig(ctx context.Context) (AsyncImageRuntimeConfig, error)
}

type AsyncImageRetentionStats struct {
	StagingDeleted             int64
	UploadStateDeleted         int64
	UploadIntentsDeleted       int
	ResultUploadIntentsDeleted int
	InputsDeleted              int
	ResultsDeleted             int
	TasksDeleted               int
}

type AsyncImageRetentionService struct {
	repo    AsyncImageRetentionRepository
	storage AsyncImageRetentionStorage
	batch   int
}

func NewAsyncImageRetentionService(tasks *AsyncImageTaskService, storage AsyncImageRetentionStorage) *AsyncImageRetentionService {
	if tasks == nil || storage == nil {
		return nil
	}
	repo, ok := tasks.repo.(AsyncImageRetentionRepository)
	if !ok {
		return nil
	}
	return &AsyncImageRetentionService{repo: repo, storage: storage, batch: defaultAsyncImageCleanupBatch}
}

func AsyncImageInputURLHash(rawURL string) string {
	value := strings.TrimSpace(rawURL)
	if parsed, err := url.Parse(value); err == nil && parsed.IsAbs() && parsed.Host != "" {
		parsed.Fragment = ""
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		value = parsed.String()
	}
	return asyncImageURLValueHash(value)
}

func AsyncImageInputURLHashes(rawURL string) []string {
	raw := strings.TrimSpace(rawURL)
	values := make([]string, 0, 4)
	if parsed, err := url.Parse(raw); err == nil && parsed.IsAbs() && parsed.Host != "" {
		legacyWithoutFragment := *parsed
		legacyWithoutFragment.Fragment = ""

		normalized := legacyWithoutFragment
		normalized.Scheme = strings.ToLower(normalized.Scheme)
		normalized.Host = strings.ToLower(normalized.Host)
		values = append(values, normalized.String())

		resourceIdentity := normalized
		resourceIdentity.RawQuery = ""
		resourceIdentity.ForceQuery = false
		values = append(values, resourceIdentity.String(), legacyWithoutFragment.String())
	}
	values = append(values, raw)
	hashes := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		hash := asyncImageURLValueHash(value)
		if _, exists := seen[hash]; exists {
			continue
		}
		seen[hash] = struct{}{}
		hashes = append(hashes, hash)
	}
	return hashes
}

func asyncImageURLValueHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *AsyncImageTaskService) RegisterInputObject(ctx context.Context, params RegisterAsyncImageInputObjectParams) (*AsyncImageInputObject, error) {
	repo, ok := s.inputObjectRepository()
	if !ok || !validAsyncImageInputObject(params) {
		return nil, ErrAsyncImageInvalidInput
	}
	return repo.RegisterAsyncImageInputObject(ctx, params)
}

// ResolveOwnedInputObjectIDs recognizes URLs issued by uploads/images_sc.
// Unknown URLs remain valid remote references, while a known URL owned by a
// different key, already expired, or claimed for deletion is rejected.
func (s *AsyncImageTaskService) ResolveOwnedInputObjectIDs(ctx context.Context, apiKeyID int64, rawURLs []string, now time.Time) ([]int64, error) {
	if apiKeyID <= 0 || len(rawURLs) == 0 {
		return nil, nil
	}
	repo, ok := s.inputObjectRepository()
	if !ok {
		return nil, errors.New("async image input repository is unavailable")
	}
	hashes := make([]string, 0, len(rawURLs))
	seenHashes := make(map[string]struct{}, len(rawURLs))
	for _, rawURL := range rawURLs {
		if strings.TrimSpace(rawURL) == "" {
			continue
		}
		for _, hash := range AsyncImageInputURLHashes(rawURL) {
			if _, exists := seenHashes[hash]; exists {
				continue
			}
			seenHashes[hash] = struct{}{}
			hashes = append(hashes, hash)
		}
	}
	objects, err := repo.FindAsyncImageInputObjectsByURLHashes(ctx, hashes)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(objects))
	seenIDs := make(map[int64]struct{}, len(objects))
	for i := range objects {
		object := &objects[i]
		if object.APIKeyID != apiKeyID || !object.ExpiresAt.After(now) || object.CleanupClaimedAt != nil {
			return nil, ErrAsyncImageInvalidInput
		}
		if _, exists := seenIDs[object.ID]; exists {
			continue
		}
		seenIDs[object.ID] = struct{}{}
		ids = append(ids, object.ID)
	}
	return ids, nil
}

func (s *AsyncImageTaskService) inputObjectRepository() (AsyncImageInputObjectRepository, bool) {
	if s == nil || s.repo == nil {
		return nil, false
	}
	repo, ok := s.repo.(AsyncImageInputObjectRepository)
	return repo, ok
}

func validAsyncImageInputObject(params RegisterAsyncImageInputObjectParams) bool {
	ref := params.ObjectRef
	return strings.TrimSpace(params.UploadID) != "" && params.UserID > 0 && params.APIKeyID > 0 &&
		strings.TrimSpace(ref.Provider) != "" && strings.TrimSpace(ref.Bucket) != "" &&
		strings.TrimSpace(ref.ObjectKey) != "" && strings.TrimSpace(ref.ContentType) != "" &&
		ref.SizeBytes >= 0 && strings.TrimSpace(ref.ChecksumSHA256) != "" &&
		len(strings.TrimSpace(params.URLHash)) == 64 && !params.ExpiresAt.IsZero()
}

// RunOnce executes one bounded retention pass. Generated bytes are removed
// from PostgreSQL even when OSS is temporarily unavailable. Durable objects
// are always deleted before their database row or owning task is removed.
func (s *AsyncImageRetentionService) RunOnce(ctx context.Context, now time.Time) (AsyncImageRetentionStats, error) {
	var stats AsyncImageRetentionStats
	if s == nil || s.repo == nil || s.storage == nil {
		return stats, errors.New("async image retention service is unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	batch := s.batch
	if batch <= 0 {
		batch = defaultAsyncImageCleanupBatch
	}
	var firstErr error
	deleted, err := s.repo.DeleteExpiredAsyncImageStagingObjects(ctx, now, batch)
	if err != nil {
		firstErr = fmt.Errorf("delete expired async image staging objects: %w", err)
	} else {
		stats.StagingDeleted = deleted
	}
	intentRepo, hasIntentRepo := s.repo.(AsyncImageUploadIntentRetentionRepository)
	if hasIntentRepo {
		deleted, err = intentRepo.DeleteExpiredAsyncImageUploadAdmissionState(ctx, now, batch)
		if err != nil {
			firstErr = joinAsyncImageRetentionError(firstErr, fmt.Errorf("delete expired async image upload admission state: %w", err))
		} else {
			stats.UploadStateDeleted = deleted
		}
	}

	cfg, err := s.storage.RuntimeConfig(ctx)
	if err != nil {
		return stats, joinAsyncImageRetentionError(firstErr, fmt.Errorf("load async image retention config: %w", err))
	}
	durable, enabled, err := s.storage.DurableStorage(ctx)
	if err != nil {
		return stats, joinAsyncImageRetentionError(firstErr, fmt.Errorf("load durable image storage: %w", err))
	}
	if !enabled || durable == nil {
		return stats, joinAsyncImageRetentionError(firstErr, errors.New("durable image storage is unavailable"))
	}

	staleBefore := now.Add(-defaultAsyncImageCleanupClaimLease)
	if hasIntentRepo {
		if err := s.cleanupUploadIntents(ctx, durable, intentRepo, now, staleBefore, batch, &stats); err != nil {
			firstErr = joinAsyncImageRetentionError(firstErr, err)
		}
	}
	resultIntentRepo, hasResultIntentRepo := s.repo.(AsyncImageResultUploadIntentRetentionRepository)
	if hasResultIntentRepo {
		if err := s.cleanupResultUploadIntents(ctx, durable, resultIntentRepo, now, staleBefore, batch, &stats); err != nil {
			firstErr = joinAsyncImageRetentionError(firstErr, err)
		}
	}
	if err := s.cleanupInputs(ctx, durable, now, staleBefore, batch, &stats); err != nil {
		firstErr = joinAsyncImageRetentionError(firstErr, err)
	}
	resultCutoff := now.Add(-time.Duration(cfg.ResultRetentionDays) * 24 * time.Hour)
	if err := s.cleanupResults(ctx, durable, resultCutoff, staleBefore, batch, &stats); err != nil {
		firstErr = joinAsyncImageRetentionError(firstErr, err)
	}
	taskCutoff := now.Add(-time.Duration(cfg.TaskRetentionDays) * 24 * time.Hour)
	if err := s.cleanupTasks(ctx, durable, now, taskCutoff, staleBefore, batch, &stats); err != nil {
		firstErr = joinAsyncImageRetentionError(firstErr, err)
	}
	return stats, firstErr
}

func (s *AsyncImageRetentionService) cleanupResultUploadIntents(
	ctx context.Context,
	storage DurableImageStorage,
	repo AsyncImageResultUploadIntentRetentionRepository,
	before, staleBefore time.Time,
	batch int,
	stats *AsyncImageRetentionStats,
) error {
	intents, err := repo.ClaimExpiredAsyncImageResultUploadIntents(ctx, before, staleBefore, batch)
	if err != nil {
		return fmt.Errorf("claim expired async image result upload intents: %w", err)
	}
	var firstErr error
	for _, intent := range intents {
		if intent.CleanupClaimedAt == nil {
			continue
		}
		claimedAt := *intent.CleanupClaimedAt
		shared, checkErr := asyncImageObjectHasLiveReference(ctx, s.repo, intent.ObjectRef, 0, "")
		if checkErr != nil {
			_ = repo.ReleaseAsyncImageResultUploadIntentDeletion(ctx, intent.ID, claimedAt)
			firstErr = joinAsyncImageRetentionError(firstErr, checkErr)
			continue
		}
		if !shared {
			if deleteErr := storage.Delete(ctx, intent.ObjectRef); deleteErr != nil {
				_ = repo.ReleaseAsyncImageResultUploadIntentDeletion(ctx, intent.ID, claimedAt)
				firstErr = joinAsyncImageRetentionError(firstErr, fmt.Errorf("delete async image result upload intent %d: %w", intent.ID, deleteErr))
				continue
			}
		}
		if completeErr := repo.CompleteAsyncImageResultUploadIntentDeletion(ctx, intent.ID, claimedAt); completeErr != nil {
			_ = repo.ReleaseAsyncImageResultUploadIntentDeletion(ctx, intent.ID, claimedAt)
			firstErr = joinAsyncImageRetentionError(firstErr, completeErr)
			continue
		}
		stats.ResultUploadIntentsDeleted++
	}
	return firstErr
}

func (s *AsyncImageRetentionService) cleanupUploadIntents(ctx context.Context, storage DurableImageStorage, repo AsyncImageUploadIntentRetentionRepository, before, staleBefore time.Time, batch int, stats *AsyncImageRetentionStats) error {
	intents, err := repo.ClaimAsyncImageUploadCleanupIntents(ctx, before, staleBefore, batch)
	if err != nil {
		return fmt.Errorf("claim stale async image upload intents: %w", err)
	}
	var firstErr error
	for i := range intents {
		intent := &intents[i]
		if err := storage.Delete(ctx, intent.ObjectRef); err != nil {
			_ = repo.ReleaseAsyncImageUploadIntentDeletion(ctx, intent.ReservationID, intent.CleanupClaimedAt)
			firstErr = joinAsyncImageRetentionError(firstErr, fmt.Errorf("delete async image upload intent %s: %w", intent.ReservationID, err))
			continue
		}
		removed, err := repo.CompleteAsyncImageUploadIntentDeletion(ctx, intent.ReservationID, intent.CleanupClaimedAt)
		if err != nil {
			_ = repo.ReleaseAsyncImageUploadIntentDeletion(ctx, intent.ReservationID, intent.CleanupClaimedAt)
			firstErr = joinAsyncImageRetentionError(firstErr, fmt.Errorf("complete async image upload intent %s deletion: %w", intent.ReservationID, err))
			continue
		}
		if removed {
			stats.UploadIntentsDeleted++
		}
	}
	return firstErr
}

func (s *AsyncImageRetentionService) cleanupInputs(ctx context.Context, storage DurableImageStorage, before, staleBefore time.Time, batch int, stats *AsyncImageRetentionStats) error {
	objects, err := s.repo.ClaimExpiredAsyncImageInputObjects(ctx, before, staleBefore, batch)
	if err != nil {
		return fmt.Errorf("claim expired async image inputs: %w", err)
	}
	var firstErr error
	for i := range objects {
		object := &objects[i]
		claimedAt := dereferenceCleanupClaim(object.CleanupClaimedAt)
		if err := storage.Delete(ctx, object.ObjectRef); err != nil {
			_ = s.repo.ReleaseAsyncImageInputObjectDeletion(ctx, object.ID, claimedAt)
			firstErr = joinAsyncImageRetentionError(firstErr, fmt.Errorf("delete async image input %d: %w", object.ID, err))
			continue
		}
		if err := s.repo.CompleteAsyncImageInputObjectDeletion(ctx, object.ID, claimedAt); err != nil {
			_ = s.repo.ReleaseAsyncImageInputObjectDeletion(ctx, object.ID, claimedAt)
			firstErr = joinAsyncImageRetentionError(firstErr, fmt.Errorf("complete async image input %d deletion: %w", object.ID, err))
			continue
		}
		stats.InputsDeleted++
	}
	return firstErr
}

func (s *AsyncImageRetentionService) cleanupResults(ctx context.Context, storage DurableImageStorage, createdBefore, staleBefore time.Time, batch int, stats *AsyncImageRetentionStats) error {
	results, err := s.repo.ClaimExpiredAsyncImageResults(ctx, createdBefore, staleBefore, batch)
	if err != nil {
		return fmt.Errorf("claim expired async image results: %w", err)
	}
	var firstErr error
	for i := range results {
		result := &results[i]
		ref := asyncImageResultObjectRef(*result)
		shared, sharedErr := asyncImageObjectHasLiveReference(ctx, s.repo, ref, result.ID, "")
		if sharedErr != nil {
			_ = s.repo.ReleaseAsyncImageResultDeletion(ctx, result.ID, result.CleanupClaimedAt)
			firstErr = joinAsyncImageRetentionError(firstErr, fmt.Errorf("check async image result %d references: %w", result.ID, sharedErr))
			continue
		}
		if !shared {
			if err := storage.Delete(ctx, ref); err != nil {
				_ = s.repo.ReleaseAsyncImageResultDeletion(ctx, result.ID, result.CleanupClaimedAt)
				firstErr = joinAsyncImageRetentionError(firstErr, fmt.Errorf("delete async image result %d: %w", result.ID, err))
				continue
			}
		}
		if err := s.repo.CompleteAsyncImageResultDeletion(ctx, result.ID, result.CleanupClaimedAt); err != nil {
			_ = s.repo.ReleaseAsyncImageResultDeletion(ctx, result.ID, result.CleanupClaimedAt)
			firstErr = joinAsyncImageRetentionError(firstErr, fmt.Errorf("complete async image result %d deletion: %w", result.ID, err))
			continue
		}
		stats.ResultsDeleted++
	}
	return firstErr
}

func (s *AsyncImageRetentionService) cleanupTasks(ctx context.Context, storage DurableImageStorage, expiresBefore, createdBefore, staleBefore time.Time, batch int, stats *AsyncImageRetentionStats) error {
	tasks, err := s.repo.ClaimExpiredAsyncImageTasks(ctx, expiresBefore, createdBefore, staleBefore, batch)
	if err != nil {
		return fmt.Errorf("claim expired async image tasks: %w", err)
	}
	var firstErr error
	for i := range tasks {
		task := &tasks[i]
		results, listErr := s.repo.ListAsyncImageResults(ctx, task.TaskID)
		if listErr != nil {
			_ = s.repo.ReleaseAsyncImageTaskDeletion(ctx, task.TaskID, task.CleanupClaimedAt)
			firstErr = joinAsyncImageRetentionError(firstErr, fmt.Errorf("list results for expired async image task %s: %w", task.TaskID, listErr))
			continue
		}
		deleteFailed := false
		for j := range results {
			ref := asyncImageResultObjectRef(results[j])
			shared, sharedErr := asyncImageObjectHasLiveReference(ctx, s.repo, ref, 0, task.TaskID)
			if sharedErr != nil {
				deleteFailed = true
				firstErr = joinAsyncImageRetentionError(firstErr, fmt.Errorf("check references for expired async image task %s: %w", task.TaskID, sharedErr))
				continue
			}
			if shared {
				continue
			}
			if deleteErr := storage.Delete(ctx, ref); deleteErr != nil {
				deleteFailed = true
				firstErr = joinAsyncImageRetentionError(firstErr, fmt.Errorf("delete result for expired async image task %s: %w", task.TaskID, deleteErr))
			}
		}
		if deleteFailed {
			_ = s.repo.ReleaseAsyncImageTaskDeletion(ctx, task.TaskID, task.CleanupClaimedAt)
			continue
		}
		if err := s.repo.CompleteAsyncImageTaskDeletion(ctx, task.TaskID, task.CleanupClaimedAt); err != nil {
			_ = s.repo.ReleaseAsyncImageTaskDeletion(ctx, task.TaskID, task.CleanupClaimedAt)
			firstErr = joinAsyncImageRetentionError(firstErr, fmt.Errorf("complete async image task %s deletion: %w", task.TaskID, err))
			continue
		}
		stats.TasksDeleted++
	}
	return firstErr
}

type asyncImageLibraryReferenceChecker interface {
	HasLiveImageLibraryObjectReference(ctx context.Context, ref ObjectRef) (bool, error)
}

type asyncImageObjectReferenceChecker interface {
	HasLiveImageObjectReference(ctx context.Context, ref ObjectRef, excludedResultID int64, excludedTaskID string) (bool, error)
}

func asyncImageObjectHasLiveReference(ctx context.Context, repo AsyncImageRetentionRepository, ref ObjectRef, excludedResultID int64, excludedTaskID string) (bool, error) {
	if checker, ok := repo.(asyncImageObjectReferenceChecker); ok {
		return checker.HasLiveImageObjectReference(ctx, ref, excludedResultID, excludedTaskID)
	}
	checker, ok := repo.(asyncImageLibraryReferenceChecker)
	if !ok {
		return false, nil
	}
	return checker.HasLiveImageLibraryObjectReference(ctx, ref)
}

func asyncImageResultObjectRef(result AsyncImageResult) ObjectRef {
	ref := ObjectRef{
		Provider: result.Provider, Bucket: result.Bucket, ObjectKey: result.ObjectKey,
		ContentType: result.ContentType, SizeBytes: result.ByteSize, ChecksumSHA256: result.Checksum,
	}
	if result.Width != nil {
		ref.Width = *result.Width
	}
	if result.Height != nil {
		ref.Height = *result.Height
	}
	return ref
}

func dereferenceCleanupClaim(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func joinAsyncImageRetentionError(existing, next error) error {
	if existing == nil {
		return next
	}
	if next == nil {
		return existing
	}
	return errors.Join(existing, next)
}
