package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

type imageResultCleanupStorage interface {
	RuntimeConfig(context.Context) (AsyncImageRuntimeConfig, error)
	ResultReferenceConfig(context.Context) (ImageResultReferenceConfig, error)
	DurableStorage(context.Context) (DurableImageStorage, bool, error)
}

type imageResultReferenceBackfiller interface {
	BackfillAsyncImageResultReferences(context.Context, ImageResultReferenceConfig, int) (bool, error)
}

// ImageResultCleanupService owns scheduling and bounded execution. All durable
// ownership and progress live in PostgreSQL, never in a request goroutine.
type ImageResultCleanupService struct {
	repo    ImageResultCleanupRepository
	storage imageResultCleanupStorage
	load    *imageResultCleanupLoadGuard
	now     func() time.Time
	wait    func(context.Context, time.Duration) error
}

func newImageResultCleanupService(repo ImageLibraryRepository, storage *ImageStorageSettingService) *ImageResultCleanupService {
	r, ok := repo.(ImageResultCleanupRepository)
	if !ok || storage == nil {
		return nil
	}
	return &ImageResultCleanupService{repo: r, storage: storage, load: newImageResultCleanupLoadGuard(), now: time.Now, wait: waitImageResultCleanup}
}

func (s *ImageLibraryService) ResultCleanup() *ImageResultCleanupService {
	if s == nil {
		return nil
	}
	return s.resultCleanup
}

func (s *ImageResultCleanupService) Policy(ctx context.Context) (*ImageResultCleanupPolicy, error) {
	p, err := s.repo.GetImageResultCleanupPolicy(ctx)
	if err != nil {
		return nil, err
	}
	if p != nil && p.Configured {
		return p, nil
	}
	runtime, err := s.storage.RuntimeConfig(ctx)
	if err != nil {
		return nil, err
	}
	compatible := defaultImageResultCleanupPolicy(runtime.ResultRetentionDays)
	if p != nil && p.RetentionValue == compatible.RetentionValue {
		return p, nil
	}
	revision := int64(0)
	if p != nil {
		revision = p.Revision
		compatible.NextRunAt = p.NextRunAt
	} else {
		now := s.now().UTC()
		compatible.NextRunAt = &now
	}
	saved, err := s.repo.SaveImageResultCleanupPolicy(ctx, compatible, revision)
	if errors.Is(err, ErrImageResultCleanupConflict) {
		return s.repo.GetImageResultCleanupPolicy(ctx)
	}
	return saved, err
}

func (s *ImageResultCleanupService) SavePolicy(ctx context.Context, adminID int64, p ImageResultCleanupPolicy) (*ImageResultCleanupPolicy, error) {
	if err := validateImageResultCleanupPolicy(&p); err != nil {
		return nil, apperrors.BadRequest("IMAGE_CLEANUP_INVALID_POLICY", err.Error())
	}
	current, err := s.Policy(ctx)
	if err != nil {
		return nil, err
	}
	if p.Revision <= 0 || p.Revision != current.Revision {
		return nil, ErrImageResultCleanupConflict
	}
	p.Configured = true
	p.UpdatedBy = &adminID
	if p.Enabled {
		next := nextImageResultCleanupRun(p, s.now())
		p.NextRunAt = &next
	} else {
		p.NextRunAt = nil
	}
	return s.repo.SaveImageResultCleanupPolicy(ctx, p, current.Revision)
}

func (s *ImageResultCleanupService) target(ctx context.Context) (ImageResultCleanupTarget, ImageResultReferenceConfig, error) {
	cfg, err := s.storage.ResultReferenceConfig(ctx)
	if err != nil {
		return ImageResultCleanupTarget{}, cfg, err
	}
	if cfg.Provider == "" || cfg.Bucket == "" || cfg.StorageIdentity == "" {
		return ImageResultCleanupTarget{}, cfg, apperrors.ServiceUnavailable("IMAGE_CLEANUP_STORAGE_UNAVAILABLE", "图片存储未就绪")
	}
	return ImageResultCleanupTarget{Provider: cfg.Provider, Bucket: cfg.Bucket, Identity: cfg.StorageIdentity}, cfg, nil
}

func imageCleanupTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *ImageResultCleanupService) Preview(ctx context.Context, adminID int64, raw json.RawMessage) (*ImageResultCleanupPreview, error) {
	now := s.now().UTC()
	f, err := parseImageResultCleanupFilters(raw, now)
	if err != nil {
		return nil, apperrors.BadRequest("IMAGE_CLEANUP_INVALID_FILTER", err.Error())
	}
	target, _, err := s.target(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.Policy(ctx)
	if err != nil {
		return nil, err
	}
	request := ImageResultCleanupRequest{Before: *f.Before, Target: target, Source: "manual", PolicyRevision: p.Revision, PolicySnapshot: *p}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	preview, err := s.repo.PreviewImageResultCleanup(queryCtx, request, 1000)
	if err != nil {
		return nil, err
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	preview.Before = *f.Before
	preview.PreviewToken = hex.EncodeToString(secret[:])
	if err := s.repo.SaveImageResultCleanupPreview(ctx, imageCleanupTokenHash(preview.PreviewToken), adminID, request, now.Add(10*time.Minute)); err != nil {
		return nil, err
	}
	return preview, nil
}

func (s *ImageResultCleanupService) Create(ctx context.Context, adminID int64, raw json.RawMessage) (*ImageResultCleanupJob, error) {
	var f imageResultCleanupFilters
	if err := json.Unmarshal(raw, &f); err != nil || len(f.PreviewToken) != 64 {
		return nil, apperrors.BadRequest("IMAGE_CLEANUP_PREVIEW_REQUIRED", "请先预览，再确认清理")
	}
	return s.repo.CreateImageResultCleanupJobFromPreview(ctx, imageCleanupTokenHash(f.PreviewToken), adminID, s.now().UTC())
}

func (s *ImageResultCleanupService) Control(ctx context.Context, adminID, jobID int64, action string) (*ImageResultCleanupJob, error) {
	if action != "cancel" && action != "resume" {
		return nil, apperrors.BadRequest("IMAGE_CLEANUP_INVALID_ACTION", "无效清理操作")
	}
	job, err := s.repo.ControlImageResultCleanupJob(ctx, jobID, action)
	if err == nil {
		logger.L().Info("image_result_cleanup.control", zap.Int64("admin_id", adminID), zap.Int64("job_id", jobID), zap.String("action", action))
	}
	return job, err
}

func (s *ImageResultCleanupService) List(ctx context.Context, limit int) ([]ImageResultCleanupJob, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	return s.repo.ListImageResultCleanupJobs(ctx, limit)
}

func waitImageResultCleanup(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *ImageResultCleanupService) loop(ctx context.Context) {
	for ctx.Err() == nil {
		delay := time.Second
		if err := s.RunOnce(ctx); err != nil && ctx.Err() == nil {
			logger.L().Warn("image_result_cleanup.run_failed", zap.Error(err))
			delay = 60 * time.Second
		}
		if s.wait(ctx, delay) != nil {
			return
		}
	}
}

func (s *ImageResultCleanupService) RunOnce(ctx context.Context) error {
	p, err := s.Policy(ctx)
	if err != nil {
		return err
	}
	target, cfg, targetErr := s.target(ctx)
	now := s.now().UTC()
	if targetErr == nil && p.Enabled && (p.NextRunAt == nil || !p.NextRunAt.After(now)) {
		// Compatible legacy values are deliberately not clamped during upgrade.
		age := imageResultCleanupCompatibleAge(*p)
		_, err = s.repo.EnqueueScheduledImageResultCleanup(ctx, ImageResultCleanupRequest{Before: now.Add(-age), Target: target, Source: "scheduled", PolicyRevision: p.Revision, PolicySnapshot: *p}, now, nextImageResultCleanupRun(*p, now))
		if err != nil && !errors.Is(err, ErrImageResultCleanupConflict) {
			return err
		}
	}
	job, err := s.repo.ClaimImageResultCleanupJob(ctx, now, now.Add(2*time.Minute))
	if err != nil || job == nil {
		return err
	}
	release := func(status string, delay time.Duration, message string) error {
		var next *time.Time
		if delay > 0 {
			v := s.now().UTC().Add(delay)
			next = &v
		}
		return s.repo.ReleaseImageResultCleanupJob(ctx, job.ID, job.LeaseVersion, status, next, message)
	}
	// Claim also finalizes cancelled jobs whose worker lease expired. It must
	// run even when storage was disabled after a process interruption.
	if targetErr != nil {
		return release("paused", 0, "图片存储暂不可用，请核对配置后恢复")
	}
	if job.Target != target {
		return release("paused", 0, "存储位置已变化，请核对原存储配置后恢复")
	}
	backfill, ok := s.repo.(imageResultReferenceBackfiller)
	if !ok {
		return release("paused", 0, "生成结果引用保护未就绪")
	}
	ready, err := backfill.BackfillAsyncImageResultReferences(ctx, cfg, 50)
	if err != nil || !ready {
		return release("pending", 60*time.Second, "等待历史任务参考图依赖确认")
	}
	allowed, reason := s.load.allow(ctx, now)
	if !allowed {
		return release("pending", 60*time.Second, reason)
	}
	storage, enabled, err := s.storage.DurableStorage(ctx)
	if err != nil || !enabled || storage == nil {
		return release("paused", 0, "图片存储暂不可用")
	}
	return s.execute(ctx, job, *p, storage)
}

func cleanupStorageError(err error) (missing, denied bool) {
	if errors.Is(err, os.ErrNotExist) {
		return true, false
	}
	if errors.Is(err, os.ErrPermission) {
		return false, true
	}
	var status interface{ HTTPStatusCode() int }
	if errors.As(err, &status) {
		return status.HTTPStatusCode() == 404, status.HTTPStatusCode() == 401 || status.HTTPStatusCode() == 403
	}
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		switch coded.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true, false
		case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch":
			return false, true
		}
	}
	return false, false
}

func (s *ImageResultCleanupService) execute(ctx context.Context, job *ImageResultCleanupJob, p ImageResultCleanupPolicy, storage DurableImageStorage) error {
	end := s.now().Add(30 * time.Second)
	sliceCtx, sliceCancel := context.WithDeadline(ctx, end)
	defer sliceCancel()
	ctx = sliceCtx
	requestAt := s.now()
	gate := func(callCtx context.Context) error {
		if err := s.wait(callCtx, requestAt.Sub(s.now())); err != nil {
			return err
		}
		requestAt = s.now().Add(time.Second / time.Duration(p.RequestsPerSecond))
		return nil
	}
	status, message, delay := "pending", "", time.Second
	for count := 0; count < p.BatchSize && s.now().Before(end) && ctx.Err() == nil; count++ {
		alive, err := s.repo.HeartbeatImageResultCleanupJob(ctx, job.ID, job.LeaseVersion, s.now().Add(2*time.Minute))
		if err != nil {
			return err
		}
		if !alive {
			status = "cancelled"
			break
		}
		current, _, err := s.target(ctx)
		if err != nil || current != job.Target {
			status, message, delay = "paused", "存储位置已变化，请核对后恢复", 0
			break
		}
		allowed, reason := s.load.allow(ctx, s.now())
		if !allowed {
			message, delay = reason, 60*time.Second
			break
		}
		queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		object, err := s.repo.ClaimImageResultCleanupObject(queryCtx, job.ID, job.LeaseVersion, s.now().UTC())
		cancel()
		if err != nil {
			return err
		}
		if object == nil {
			status, delay = "succeeded", 0
			break
		}
		if object.Action != "delete" {
			continue
		}
		outcome, retry, reason, stop := s.deleteObject(ctx, job, object, storage, gate)
		// Complete in a bounded independent context even after shutdown/cancel:
		// a request already sent to storage may have committed its deletion.
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err = s.repo.CompleteImageResultCleanupObject(finishCtx, job.ID, job.LeaseVersion, object.ID, outcome, retry, reason)
		finishCancel()
		if err != nil {
			return fmt.Errorf("confirm result cleanup: %w", err)
		}
		if stop {
			status, message, delay = "paused", reason, 0
			break
		}
	}
	var next *time.Time
	if delay > 0 {
		v := s.now().UTC().Add(delay)
		next = &v
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.repo.ReleaseImageResultCleanupJob(finishCtx, job.ID, job.LeaseVersion, status, next, message)
}

func (s *ImageResultCleanupService) deleteObject(ctx context.Context, job *ImageResultCleanupJob, item *ImageResultCleanupObject, storage DurableImageStorage, gate func(context.Context) error) (string, *time.Time, string, bool) {
	ref := item.Object
	if ref.Provider == ImageStorageProviderSuperbed {
		return "skipped", nil, "当前图床不提供可验证的物理删除接口", false
	}
	if ref.Provider != job.Target.Provider || ref.Bucket != job.Target.Bucket || (ref.StorageIdentity != "" && ref.StorageIdentity != job.Target.Identity) {
		return "skipped", nil, "对象存储身份不匹配", false
	}
	call := func(fn func(context.Context) error) error {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return fn(WithImageCleanupRequest(c, gate))
	}
	// DeleteObject can succeed for an already absent key. Check existence first
	// so missing objects are reconciled without inflating deleted bytes/counts.
	var meta ObjectMetadata
	operationErr := call(func(c context.Context) error { var e error; meta, e = storage.Head(c, ref); return e })
	if ref.StorageIdentity == "" {
		if operationErr == nil {
			if ref.ChecksumSHA256 == "" || meta.ChecksumSHA256 != ref.ChecksumSHA256 || meta.SizeBytes != ref.SizeBytes {
				return "skipped", nil, "历史对象校验值或大小不一致", false
			}
			verifier, ok := s.repo.(interface {
				VerifyImageResultCleanupObject(context.Context, int64, int64, int64, string) error
			})
			if !ok {
				return "retry", cleanupRetryAt(s.now(), item.Attempts), "对象身份确认功能不可用", true
			}
			operationErr = verifier.VerifyImageResultCleanupObject(ctx, job.ID, job.LeaseVersion, item.ID, job.Target.Identity)
		} else if missing, _ := cleanupStorageError(operationErr); missing {
			return "skipped", nil, "历史对象不存在于当前存储，无法确认原存储归属", false
		}
	}
	if operationErr == nil {
		operationErr = call(func(c context.Context) error { return storage.Delete(c, ref) })
	}
	if operationErr == nil {
		return "deleted", nil, "", false
	}
	missing, denied := cleanupStorageError(operationErr)
	if missing {
		return "missing", nil, "", false
	}
	if denied {
		return "retry", cleanupRetryAt(s.now(), item.Attempts), "存储权限验证失败，请检查凭据后恢复", true
	}
	return "retry", cleanupRetryAt(s.now(), item.Attempts), "存储请求失败或结果未知，已保留恢复记录", item.Attempts >= 5
}

func cleanupRetryAt(now time.Time, attempt int) *time.Time {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	v := now.UTC().Add(time.Duration(1<<uint(attempt-1)) * time.Minute)
	return &v
}

func (s *ImageResultCleanupService) prepareLegacy(ctx context.Context, job *ImageLibraryCleanupJob) (bool, error) {
	target, _, err := s.target(ctx)
	if err != nil {
		return false, err
	}
	p, err := s.Policy(ctx)
	if err != nil {
		return false, err
	}
	var filter imageResultCleanupFilters
	if len(job.Filters) > 0 {
		if err := json.Unmarshal(job.Filters, &filter); err != nil {
			return false, err
		}
	}
	before := job.CreatedAt.UTC()
	if filter.Before != nil {
		before = filter.Before.UTC()
	}
	ready, err := s.repo.PrepareLegacyImageResultCleanup(ctx, job.ID, job.LeaseVersion, ImageResultCleanupRequest{Before: before, Target: target, Source: "legacy", PolicyRevision: p.Revision, PolicySnapshot: *p})
	if errors.Is(err, ErrImageResultCleanupConflict) {
		return false, nil
	}
	return ready, err
}

func imageResultCleanupCompatibleAge(p ImageResultCleanupPolicy) time.Duration {
	unit := 24 * time.Hour
	if p.RetentionUnit == "hours" {
		unit = time.Hour
	}
	if int64(p.RetentionValue) > math.MaxInt64/int64(unit) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(p.RetentionValue) * unit
}
