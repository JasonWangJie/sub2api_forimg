package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const legacyImagePlazaMigrationKey = "legacy_image_plaza_v1"

const (
	imageLibraryMaintenanceLease     = 2 * time.Minute
	imageLibraryMaintenanceHeartbeat = 30 * time.Second
)

var errLegacyImageQuarantined = errors.New("legacy image is unsafe or unreadable")

// ImageLibraryMaintenanceService runs recoverable database-backed maintenance.
// PostgreSQL owns the leases and progress; the goroutine is only an executor.
type ImageLibraryMaintenanceService struct {
	library *ImageLibraryService
	dataDir string

	startOnce sync.Once
	stopOnce  sync.Once
	mu        sync.Mutex
	stop      context.CancelFunc
	done      chan struct{}
	stopped   bool
}

func NewImageLibraryMaintenanceService(library *ImageLibraryService, cfg *config.Config) *ImageLibraryMaintenanceService {
	dataDir := "./data"
	if cfg != nil && strings.TrimSpace(cfg.Pricing.DataDir) != "" {
		dataDir = strings.TrimSpace(cfg.Pricing.DataDir)
	}
	return &ImageLibraryMaintenanceService{library: library, dataDir: dataDir}
}

func ProvideImageLibraryMaintenanceService(library *ImageLibraryService, cfg *config.Config) *ImageLibraryMaintenanceService {
	svc := NewImageLibraryMaintenanceService(library, cfg)
	svc.Start()
	return svc
}

func (s *ImageLibraryMaintenanceService) Start() {
	if s == nil || s.library == nil || s.library.repo == nil || s.library.storageSettings == nil {
		return
	}
	s.startOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.stopped {
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.stop = cancel
		s.done = make(chan struct{})
		go func() {
			defer close(s.done)
			s.loop(ctx)
		}()
	})
}

func (s *ImageLibraryMaintenanceService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		cancel, done := s.stop, s.done
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if done != nil {
			<-done
		}
	})
}

func (s *ImageLibraryMaintenanceService) MigrationState(ctx context.Context) (*ImageLibraryMigrationState, error) {
	if s == nil || s.library == nil || s.library.repo == nil {
		return nil, errors.New("image library maintenance is unavailable")
	}
	return s.library.repo.GetMigrationState(ctx, legacyImagePlazaMigrationKey)
}

func (s *ImageLibraryMaintenanceService) loop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		s.runOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *ImageLibraryMaintenanceService) runOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if err := s.processOutbox(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.L().Warn("image_library.outbox_failed", zap.Error(err))
	}
	if err := s.processUploadIntents(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.L().Warn("image_library.upload_intent_cleanup_failed", zap.Error(err))
	}
	if err := s.processStaleObjectDeletions(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.L().Warn("image_library.object_recovery_failed", zap.Error(err))
	}
	if err := s.processCleanup(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.L().Warn("image_library.cleanup_failed", zap.Error(err))
	}
	if err := s.processLegacyMigration(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.L().Warn("image_library.legacy_migration_failed", zap.Error(err))
	}
}

func (s *ImageLibraryMaintenanceService) processUploadIntents(ctx context.Context) error {
	repo := s.library.repo
	intents, err := repo.ClaimExpiredLibraryUploadIntents(
		ctx,
		time.Now().UTC(),
		time.Now().UTC().Add(-imageLibraryMaintenanceLease),
		100,
	)
	if err != nil {
		return err
	}
	var firstErr error
	for _, intent := range intents {
		if intent.CleanupClaimedAt == nil {
			if firstErr == nil {
				firstErr = errors.New("claimed image library upload intent has no lease timestamp")
			}
			continue
		}
		claimedAt := *intent.CleanupClaimedAt
		if !intent.Referenced {
			storage, storageErr := s.library.storageForObject(ctx, intent.ObjectRef)
			if storageErr == nil {
				storageErr = storage.Delete(ctx, intent.ObjectRef)
			}
			if storageErr != nil {
				_ = repo.ReleaseLibraryUploadIntentDeletion(ctx, intent.ID, claimedAt)
				if firstErr == nil {
					firstErr = fmt.Errorf("delete orphaned image upload %s: %w", intent.ObjectKey, storageErr)
				}
				continue
			}
		}
		if completeErr := repo.CompleteLibraryUploadIntentDeletion(ctx, intent.ID, claimedAt); completeErr != nil {
			_ = repo.ReleaseLibraryUploadIntentDeletion(ctx, intent.ID, claimedAt)
			if firstErr == nil {
				firstErr = completeErr
			}
		}
	}
	return firstErr
}

func (s *ImageLibraryMaintenanceService) processOutbox(ctx context.Context) error {
	repo := s.library.repo
	entries, err := repo.ClaimLibraryOutbox(ctx, 50, time.Now().UTC().Add(-imageLibraryMaintenanceLease))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		lease := startImageLibraryLease(ctx, func(heartbeatCtx context.Context) (bool, error) {
			return repo.HeartbeatLibraryOutbox(heartbeatCtx, entry.ID, entry.Attempts)
		})
		var processErr error
		switch entry.EventType {
		case "library.cleanup_requested":
			var objects []ObjectRef
			objects, processErr = repo.PrepareOutboxCleanup(lease.Context(), entry.AggregateID)
			if processErr == nil {
				processErr = s.deleteObjects(lease.Context(), 0, 0, objects)
			}
		case "library.created":
			// The event is retained for extensions such as indexing. There is no
			// external side effect in the first release.
		default:
			processErr = fmt.Errorf("unsupported image library outbox event %q", entry.EventType)
		}
		if leaseErr := lease.Stop(); leaseErr != nil {
			processErr = leaseErr
		}
		if processErr == nil {
			if err := repo.CompleteLibraryOutbox(ctx, entry.ID, entry.Attempts); err != nil {
				return err
			}
			continue
		}
		if errors.Is(processErr, ErrImageLibraryLeaseLost) {
			continue
		}
		backoff := time.Duration(minInt(entry.Attempts, 12)) * time.Minute
		if backoff < time.Minute {
			backoff = time.Minute
		}
		if err := repo.RetryLibraryOutbox(ctx, entry.ID, entry.Attempts, time.Now().UTC().Add(backoff), processErr.Error()); err != nil {
			if errors.Is(err, ErrImageLibraryLeaseLost) {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *ImageLibraryMaintenanceService) processStaleObjectDeletions(ctx context.Context) error {
	objects, err := s.library.repo.ClaimStaleCleanupObjects(ctx, time.Now().UTC().Add(-imageLibraryMaintenanceLease), 100)
	if err != nil {
		return err
	}
	return s.deleteObjects(ctx, 0, 0, objects)
}

func (s *ImageLibraryMaintenanceService) processCleanup(ctx context.Context) error {
	repo := s.library.repo
	if err := repo.EnsureExpiredCleanupJob(ctx); err != nil {
		return err
	}
	job, err := repo.ClaimCleanupJob(ctx, time.Now().UTC().Add(-imageLibraryMaintenanceLease))
	if err != nil || job == nil {
		return err
	}
	lease := startImageLibraryLease(ctx, func(heartbeatCtx context.Context) (bool, error) {
		return repo.HeartbeatCleanupJob(heartbeatCtx, job.ID, job.LeaseVersion)
	})
	var processErr error
	succeeded := false
	for {
		batch, batchErr := repo.PrepareCleanupBatch(lease.Context(), job.ID, job.LeaseVersion, job.Scope, job.Filters, 100)
		if batchErr != nil {
			processErr = batchErr
			break
		}
		if err := s.deleteObjects(lease.Context(), job.ID, job.LeaseVersion, batch.Objects); err != nil {
			processErr = err
			break
		}
		if batch.Done {
			succeeded = true
			break
		}
		if lease.Context().Err() != nil {
			processErr = lease.Context().Err()
			break
		}
	}
	if leaseErr := lease.Stop(); leaseErr != nil {
		processErr = leaseErr
		succeeded = false
	}
	if succeeded {
		return repo.FinishCleanupJob(ctx, job.ID, job.LeaseVersion, "succeeded", "")
	}
	if processErr == nil {
		processErr = errors.New("image library cleanup stopped before completion")
	}
	if !errors.Is(processErr, ErrImageLibraryLeaseLost) {
		_ = repo.FinishCleanupJob(ctx, job.ID, job.LeaseVersion, "failed", processErr.Error())
	}
	return processErr
}

func (s *ImageLibraryMaintenanceService) deleteObjects(ctx context.Context, jobID, leaseVersion int64, objects []ObjectRef) error {
	if len(objects) == 0 {
		return nil
	}
	for _, ref := range objects {
		storage, err := s.library.storageForObject(ctx, ref)
		if err != nil {
			return err
		}
		if err := storage.Delete(ctx, ref); err != nil {
			return fmt.Errorf("delete image object %s: %w", ref.ObjectKey, err)
		}
		if err := s.library.repo.CompleteCleanupObject(ctx, jobID, leaseVersion, ref); err != nil {
			return err
		}
	}
	return nil
}

func (s *ImageLibraryMaintenanceService) processLegacyMigration(ctx context.Context) error {
	repo := s.library.repo
	state, err := repo.ClaimMigration(ctx, legacyImagePlazaMigrationKey, time.Now().UTC().Add(-imageLibraryMaintenanceLease))
	if err != nil || state == nil {
		return err
	}
	lease := startImageLibraryLease(ctx, func(heartbeatCtx context.Context) (bool, error) {
		return repo.HeartbeatMigration(heartbeatCtx, legacyImagePlazaMigrationKey, state.LeaseVersion)
	})
	policy, err := s.library.Policy(lease.Context())
	if err != nil {
		_ = lease.Stop()
		_ = repo.FinishMigration(ctx, legacyImagePlazaMigrationKey, state.LeaseVersion, "failed", err.Error())
		return err
	}
	var processErr error
	succeeded := false
	for {
		items, err := repo.ListLegacyPlazaItems(lease.Context(), state.LastLegacyID, 50)
		if err != nil {
			processErr = err
			break
		}
		if len(items) == 0 {
			succeeded = true
			break
		}
		for _, item := range items {
			migrated, quarantined := int64(0), int64(0)
			if err := s.migrateLegacyItem(lease.Context(), item, policy); err != nil {
				if !errors.Is(err, errLegacyImageQuarantined) {
					processErr = err
					break
				}
				quarantined = 1
				logger.L().Warn("image_library.legacy_item_quarantined", zap.Int64("legacy_id", item.ID), zap.Error(err))
			} else {
				migrated = 1
			}
			if err := repo.AdvanceMigration(lease.Context(), legacyImagePlazaMigrationKey, state.LeaseVersion, item.ID, migrated, quarantined); err != nil {
				processErr = err
				break
			}
			state.LastLegacyID = item.ID
		}
		if processErr != nil {
			break
		}
		if lease.Context().Err() != nil {
			processErr = lease.Context().Err()
			break
		}
	}
	if leaseErr := lease.Stop(); leaseErr != nil {
		processErr = leaseErr
		succeeded = false
	}
	if succeeded {
		return repo.FinishMigration(ctx, legacyImagePlazaMigrationKey, state.LeaseVersion, "succeeded", "")
	}
	if processErr == nil {
		processErr = errors.New("legacy image migration stopped before completion")
	}
	if !errors.Is(processErr, ErrImageLibraryLeaseLost) {
		_ = repo.FinishMigration(ctx, legacyImagePlazaMigrationKey, state.LeaseVersion, "failed", processErr.Error())
	}
	return processErr
}

type imageLibraryLease struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan error
}

func startImageLibraryLease(parent context.Context, heartbeat func(context.Context) (bool, error)) *imageLibraryLease {
	return startImageLibraryLeaseWithInterval(parent, imageLibraryMaintenanceHeartbeat, heartbeat)
}

func startImageLibraryLeaseWithInterval(parent context.Context, interval time.Duration, heartbeat func(context.Context) (bool, error)) *imageLibraryLease {
	ctx, cancel := context.WithCancel(parent)
	lease := &imageLibraryLease{ctx: ctx, cancel: cancel, done: make(chan error, 1)}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				lease.done <- nil
				return
			case <-ticker.C:
				alive, err := heartbeat(ctx)
				if err != nil {
					if ctx.Err() != nil {
						lease.done <- nil
						return
					}
					cancel()
					lease.done <- err
					return
				}
				if !alive {
					if ctx.Err() != nil {
						lease.done <- nil
						return
					}
					cancel()
					lease.done <- ErrImageLibraryLeaseLost
					return
				}
			}
		}
	}()
	return lease
}

func (l *imageLibraryLease) Context() context.Context {
	return l.ctx
}

func (l *imageLibraryLease) Stop() error {
	l.cancel()
	return <-l.done
}

func (s *ImageLibraryMaintenanceService) migrateLegacyItem(ctx context.Context, item LegacyImagePlazaItem, policy ImageLibraryRuntimeConfig) error {
	absPath, err := resolveLegacyImagePath(s.dataDir, item.StoragePath)
	if err != nil {
		return err
	}
	file, err := os.Open(absPath)
	if err != nil {
		return fmt.Errorf("%w: open legacy image: %v", errLegacyImageQuarantined, err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, policy.MaxImageBytes+1))
	if err != nil {
		return fmt.Errorf("%w: read legacy image: %v", errLegacyImageQuarantined, err)
	}
	if int64(len(data)) > policy.MaxImageBytes {
		return fmt.Errorf("%w: image exceeds the configured byte limit", errLegacyImageQuarantined)
	}
	if _, err := ValidateImageBytes(data, item.ContentType, policy.MaxImageBytes, policy.MaxImagePixels); err != nil {
		return fmt.Errorf("%w: %v", errLegacyImageQuarantined, err)
	}
	idempotencyKey := fmt.Sprintf("legacy-image-plaza:%d", item.ID)
	asset, _, err := s.library.importLegacyBytes(ctx, item.UserID, ImageLibraryImportInput{
		GenerationMode: "import", SourceType: "legacy_plaza", Model: item.Model,
		RequestedSize: item.Size, ActualSize: item.Size, Quality: item.Quality,
		Title: item.Title, Prompt: item.Prompt, ImageData: data, DeclaredMIME: item.ContentType,
		IdempotencyKey: idempotencyKey,
	}, policy)
	if err != nil {
		return err
	}
	_, err = s.library.repo.CreatePublication(ctx, CreateImagePublicationParams{
		UserID: item.UserID, AssetID: asset.AssetID,
		PublicTitle: cleanLibraryText(item.Title, 200), SharePrompt: false,
		ExpiresAt: time.Now().UTC().AddDate(0, 0, policy.RetentionDays), RateLimit: 0,
	})
	return err
}

func resolveLegacyImagePath(dataDir, storagePath string) (string, error) {
	absRoot, err := filepath.Abs(dataDir)
	if err != nil {
		return "", fmt.Errorf("resolve data directory: %w", err)
	}
	cleanPath := filepath.Clean(filepath.FromSlash(strings.TrimSpace(storagePath)))
	if cleanPath == "." || cleanPath == "" || filepath.IsAbs(cleanPath) {
		return "", fmt.Errorf("%w: invalid storage path", errLegacyImageQuarantined)
	}
	absPath, err := filepath.Abs(filepath.Join(absRoot, cleanPath))
	if err != nil {
		return "", fmt.Errorf("%w: resolve storage path: %v", errLegacyImageQuarantined, err)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: storage path escapes the data directory", errLegacyImageQuarantined)
	}
	return absPath, nil
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
