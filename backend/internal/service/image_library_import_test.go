package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type imageLibraryFlowRepo struct {
	ImageLibraryRepository
	preflightFn          func(ImageLibraryImportPreflightParams) (*ImageLibraryItem, bool, error)
	createFn             func(CreateImageLibraryAssetParams) (*ImageLibraryItem, bool, error)
	prepareFn            func(int64, string, int) (*ImageLibraryItem, *ObjectRef, bool, error)
	createTaskFn         func(int64, string, int, *ObjectRef) (*ImageLibraryItem, bool, error)
	quarantineFn         func(int64, string, int, ObjectRef, string) error
	preparedIntent       ImageLibraryUploadIntent
	claimedIntents       []ImageLibraryUploadIntent
	prepareCalls         int
	completeCalls        int
	cleanupCompleteCalls int
	cleanupReleaseCalls  int
	releaseCalls         int
}

func (r *imageLibraryFlowRepo) PreflightImport(_ context.Context, in ImageLibraryImportPreflightParams) (*ImageLibraryItem, bool, error) {
	return r.preflightFn(in)
}

func (r *imageLibraryFlowRepo) ReleaseImportAttempt(context.Context, int64, *string, string) error {
	r.releaseCalls++
	return nil
}

func (r *imageLibraryFlowRepo) PrepareLibraryUploadIntent(_ context.Context, intent ImageLibraryUploadIntent) (int64, error) {
	r.prepareCalls++
	r.preparedIntent = intent
	return 101, nil
}

func (r *imageLibraryFlowRepo) CompleteLibraryUploadIntent(context.Context, int64) error {
	r.completeCalls++
	return nil
}

func (r *imageLibraryFlowRepo) ClaimExpiredLibraryUploadIntents(context.Context, time.Time, time.Time, int) ([]ImageLibraryUploadIntent, error) {
	return r.claimedIntents, nil
}

func (r *imageLibraryFlowRepo) CompleteLibraryUploadIntentDeletion(context.Context, int64, time.Time) error {
	r.cleanupCompleteCalls++
	return nil
}

func (r *imageLibraryFlowRepo) ReleaseLibraryUploadIntentDeletion(context.Context, int64, time.Time) error {
	r.cleanupReleaseCalls++
	return nil
}

func (r *imageLibraryFlowRepo) CreateAsset(_ context.Context, in CreateImageLibraryAssetParams) (*ImageLibraryItem, bool, error) {
	return r.createFn(in)
}

func (r *imageLibraryFlowRepo) PrepareAssetFromTask(_ context.Context, userID int64, taskID string, imageIndex int) (*ImageLibraryItem, *ObjectRef, bool, error) {
	return r.prepareFn(userID, taskID, imageIndex)
}

func (r *imageLibraryFlowRepo) CreateAssetFromTask(_ context.Context, userID int64, taskID string, imageIndex int, validated *ObjectRef, _ string, _ time.Time, _ int, _ int64) (*ImageLibraryItem, bool, error) {
	return r.createTaskFn(userID, taskID, imageIndex, validated)
}

func (r *imageLibraryFlowRepo) QuarantineAssetFromTask(_ context.Context, userID int64, taskID string, imageIndex int, ref ObjectRef, reason string) error {
	return r.quarantineFn(userID, taskID, imageIndex, ref, reason)
}

type imageLibraryFlowStorage struct {
	data        []byte
	saveErr     error
	deleteErr   error
	mismatch    bool
	saveCalls   int
	readCalls   int
	deleteCalls int
}

func (s *imageLibraryFlowStorage) Save(ctx context.Context, key, contentType string, data []byte) (string, error) {
	ref, err := s.SaveObject(ctx, key, contentType, data)
	if err != nil {
		return "", err
	}
	return "https://storage.example.test/" + ref.ObjectKey, nil
}

func (s *imageLibraryFlowStorage) SaveObject(_ context.Context, key, contentType string, data []byte) (ObjectRef, error) {
	s.saveCalls++
	if s.saveErr != nil {
		return ObjectRef{}, s.saveErr
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256(data))
	ref, err := s.ObjectIntent(key, contentType, int64(len(data)), checksum)
	if s.mismatch {
		ref.ObjectKey += ".unexpected"
	}
	return ref, err
}

func (s *imageLibraryFlowStorage) ObjectIntent(key, contentType string, sizeBytes int64, checksum string) (ObjectRef, error) {
	return ObjectRef{
		Provider: "custom_s3", Bucket: "images", ObjectKey: key,
		ContentType: contentType, SizeBytes: sizeBytes, ChecksumSHA256: checksum,
	}, nil
}

func (s *imageLibraryFlowStorage) SignURL(_ context.Context, ref ObjectRef, _ time.Duration) (ObjectAccess, error) {
	return ObjectAccess{URL: "https://storage.example.test/" + ref.ObjectKey}, nil
}

func (s *imageLibraryFlowStorage) Read(context.Context, ObjectRef) (io.ReadCloser, error) {
	s.readCalls++
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func (s *imageLibraryFlowStorage) Head(_ context.Context, ref ObjectRef) (ObjectMetadata, error) {
	return ObjectMetadata{ObjectRef: ref}, nil
}

func (s *imageLibraryFlowStorage) Delete(context.Context, ObjectRef) error {
	s.deleteCalls++
	return s.deleteErr
}

func imageLibraryFlowService(repo ImageLibraryRepository, storage DurableImageStorage) *ImageLibraryService {
	settings, durable := imageLibraryFlowSettings(storage)
	return NewImageLibraryService(repo, settings, durable)
}

func imageLibraryFlowSettings(storage DurableImageStorage) (*ImageStorageSettingService, *ImageDurableStorageService) {
	settings := NewImageStorageSettingService(nil, nil, nil, func(context.Context, *config.ImageStorageConfig) (ImageStorage, error) {
		return storage, nil
	}, config.ImageStorageConfig{
		Enabled: true, Provider: ImageStorageProviderCustomS3,
		Endpoint: "https://s3.example.test", Region: "auto", Bucket: "images",
		AccessKeyID: "access", SecretAccessKey: "secret", Prefix: "images/",
	})
	durable := NewImageDurableStorageService(
		config.ImageDurableStorageConfig{
			Backend: config.ImageDurableBackendOSS,
			OSS: config.ImageStorageConfig{
				Enabled: true, Provider: ImageStorageProviderCustomS3,
				Endpoint: "https://s3.example.test", Region: "auto", Bucket: "library-durable",
				AccessKeyID: "access", SecretAccessKey: "secret", Prefix: "library/",
			},
		},
		"./data",
		func(context.Context, *config.ImageStorageConfig) (ImageStorage, error) {
			return storage, nil
		},
		settings,
	)
	return settings, durable
}

func TestImageLibraryImportBytesRejectsPreflightBeforeObjectWrite(t *testing.T) {
	storage := &imageLibraryFlowStorage{}
	var preflights []ImageLibraryImportPreflightParams
	repo := &imageLibraryFlowRepo{
		preflightFn: func(in ImageLibraryImportPreflightParams) (*ImageLibraryItem, bool, error) {
			preflights = append(preflights, in)
			return nil, false, infraerrors.Conflict("IMAGE_LIBRARY_BYTE_QUOTA", "quota exceeded")
		},
	}
	svc := imageLibraryFlowService(repo, storage)
	invalidImage := []byte(`<svg><script>alert(1)</script></svg>`)

	_, _, err := svc.ImportBytes(context.Background(), 42, ImageLibraryImportInput{
		ImageData: invalidImage, DeclaredMIME: "image/svg+xml", IdempotencyKey: "import-42",
	})
	require.Equal(t, "IMAGE_LIBRARY_BYTE_QUOTA", infraerrors.Reason(err))
	require.Len(t, preflights, 1)
	require.True(t, preflights[0].RecordAttempt)
	require.False(t, preflights[0].ContinueAttempt)
	require.Equal(t, int64(len(invalidImage)), preflights[0].IncomingBytes)
	require.Zero(t, storage.saveCalls)
}

func TestImageLibraryImportBytesRecordsOnlyOneAttempt(t *testing.T) {
	storage := &imageLibraryFlowStorage{}
	var preflights []ImageLibraryImportPreflightParams
	repo := &imageLibraryFlowRepo{
		preflightFn: func(in ImageLibraryImportPreflightParams) (*ImageLibraryItem, bool, error) {
			preflights = append(preflights, in)
			return nil, false, nil
		},
		createFn: func(in CreateImageLibraryAssetParams) (*ImageLibraryItem, bool, error) {
			require.Equal(t, int64(101), in.UploadIntentID)
			return &ImageLibraryItem{AssetID: "img_imported"}, false, nil
		},
	}
	svc := imageLibraryFlowService(repo, storage)
	imageData := testPNG(t)

	_, reused, err := svc.ImportBytes(context.Background(), 42, ImageLibraryImportInput{
		ImageData: imageData, DeclaredMIME: "image/png", IdempotencyKey: "bytes-import-42",
	})
	require.NoError(t, err)
	require.False(t, reused)
	require.Len(t, preflights, 2)
	require.True(t, preflights[0].RecordAttempt)
	require.False(t, preflights[0].ContinueAttempt)
	require.False(t, preflights[1].RecordAttempt)
	require.True(t, preflights[1].ContinueAttempt)
	require.Equal(t, preflights[0].RequestHash, preflights[1].RequestHash)
	require.Equal(t, int64(len(imageData)), preflights[0].IncomingBytes)
	require.Equal(t, int64(len(imageData)), preflights[1].IncomingBytes)
	require.Equal(t, 1, storage.saveCalls)
	require.Equal(t, 1, repo.prepareCalls)
	require.True(t, strings.HasPrefix(repo.preparedIntent.ObjectKey, "library/42/"))
	require.Equal(t, int64(len(imageData)), repo.preparedIntent.SizeBytes)
}

func TestImageLibraryImportKeepsUploadIntentWhenAssetCommitFails(t *testing.T) {
	storage := &imageLibraryFlowStorage{}
	repo := &imageLibraryFlowRepo{
		preflightFn: func(ImageLibraryImportPreflightParams) (*ImageLibraryItem, bool, error) {
			return nil, false, nil
		},
		createFn: func(CreateImageLibraryAssetParams) (*ImageLibraryItem, bool, error) {
			return nil, false, errors.New("database unavailable")
		},
	}
	svc := imageLibraryFlowService(repo, storage)

	_, _, err := svc.ImportBytes(context.Background(), 42, ImageLibraryImportInput{
		ImageData: testPNG(t), DeclaredMIME: "image/png", IdempotencyKey: "failed-commit",
	})
	require.ErrorContains(t, err, "database unavailable")
	require.Equal(t, 1, repo.prepareCalls)
	require.Equal(t, 1, storage.saveCalls)
	require.Zero(t, storage.deleteCalls)
	require.Zero(t, repo.completeCalls)
}

func TestImageLibraryImportDeletesFreshObjectBeforeCompletingReusedIntent(t *testing.T) {
	storage := &imageLibraryFlowStorage{}
	repo := &imageLibraryFlowRepo{
		preflightFn: func(ImageLibraryImportPreflightParams) (*ImageLibraryItem, bool, error) {
			return nil, false, nil
		},
		createFn: func(CreateImageLibraryAssetParams) (*ImageLibraryItem, bool, error) {
			return &ImageLibraryItem{AssetID: "img_existing"}, true, nil
		},
	}
	svc := imageLibraryFlowService(repo, storage)

	item, reused, err := svc.ImportBytes(context.Background(), 42, ImageLibraryImportInput{
		ImageData: testPNG(t), DeclaredMIME: "image/png", IdempotencyKey: "reused-after-upload",
	})
	require.NoError(t, err)
	require.True(t, reused)
	require.Equal(t, "img_existing", item.AssetID)
	require.Equal(t, 1, storage.deleteCalls)
	require.Equal(t, 1, repo.completeCalls)
}

func TestImageLibraryImportRejectsStorageIdentityMismatchAndKeepsIntent(t *testing.T) {
	storage := &imageLibraryFlowStorage{mismatch: true}
	repo := &imageLibraryFlowRepo{
		preflightFn: func(ImageLibraryImportPreflightParams) (*ImageLibraryItem, bool, error) {
			return nil, false, nil
		},
	}
	svc := imageLibraryFlowService(repo, storage)

	_, _, err := svc.ImportBytes(context.Background(), 42, ImageLibraryImportInput{
		ImageData: testPNG(t), DeclaredMIME: "image/png", IdempotencyKey: "mismatched-object",
	})
	require.Equal(t, "IMAGE_ARCHIVE_FAILED", infraerrors.Reason(err))
	require.Equal(t, 1, repo.prepareCalls)
	require.Zero(t, storage.deleteCalls)
	require.Zero(t, repo.completeCalls)
}

func TestImageLibraryImportBytesReleasesIdempotentAttemptAfterValidationFailure(t *testing.T) {
	storage := &imageLibraryFlowStorage{}
	var preflights []ImageLibraryImportPreflightParams
	repo := &imageLibraryFlowRepo{
		preflightFn: func(in ImageLibraryImportPreflightParams) (*ImageLibraryItem, bool, error) {
			preflights = append(preflights, in)
			return nil, false, nil
		},
	}
	svc := imageLibraryFlowService(repo, storage)

	_, _, err := svc.ImportBytes(context.Background(), 42, ImageLibraryImportInput{
		ImageData: []byte(`<script>alert(1)</script>`), DeclaredMIME: "image/png",
		IdempotencyKey: "invalid-import-42",
	})
	require.Equal(t, "UNSUPPORTED_IMAGE_FORMAT", infraerrors.Reason(err))
	require.Len(t, preflights, 1)
	require.Equal(t, 1, repo.releaseCalls)
	require.Zero(t, storage.saveCalls)
}

func TestImageLibraryImportURLRejectsPreflightBeforeParsingOrDownload(t *testing.T) {
	storage := &imageLibraryFlowStorage{}
	repo := &imageLibraryFlowRepo{
		preflightFn: func(ImageLibraryImportPreflightParams) (*ImageLibraryItem, bool, error) {
			return nil, false, infraerrors.TooManyRequests("IMAGE_LIBRARY_RATE_LIMIT", "limited")
		},
	}
	svc := imageLibraryFlowService(repo, storage)

	_, _, err := svc.ImportURL(context.Background(), 42, "not-a-valid-image-url", ImageLibraryImportInput{})
	require.Equal(t, "IMAGE_LIBRARY_RATE_LIMIT", infraerrors.Reason(err))
	require.Zero(t, storage.saveCalls)
}

func TestImageLibraryImportURLRecordsOnlyOneAttempt(t *testing.T) {
	storage := &imageLibraryFlowStorage{}
	var preflights []ImageLibraryImportPreflightParams
	repo := &imageLibraryFlowRepo{
		preflightFn: func(in ImageLibraryImportPreflightParams) (*ImageLibraryItem, bool, error) {
			preflights = append(preflights, in)
			return nil, false, nil
		},
		createFn: func(CreateImageLibraryAssetParams) (*ImageLibraryItem, bool, error) {
			return &ImageLibraryItem{AssetID: "img_imported"}, false, nil
		},
	}
	svc := imageLibraryFlowService(repo, storage)
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG(t))

	_, reused, err := svc.ImportURL(context.Background(), 42, dataURI, ImageLibraryImportInput{IdempotencyKey: "url-import-42"})
	require.NoError(t, err)
	require.False(t, reused)
	require.Len(t, preflights, 2)
	require.True(t, preflights[0].RecordAttempt)
	require.False(t, preflights[0].ContinueAttempt)
	require.False(t, preflights[1].RecordAttempt)
	require.True(t, preflights[1].ContinueAttempt)
	require.Equal(t, preflights[0].RequestHash, preflights[1].RequestHash)
	require.Equal(t, 1, storage.saveCalls)
}

func TestImageLibraryIdempotentReplaySkipsObjectWrite(t *testing.T) {
	storage := &imageLibraryFlowStorage{}
	repo := &imageLibraryFlowRepo{
		preflightFn: func(ImageLibraryImportPreflightParams) (*ImageLibraryItem, bool, error) {
			return &ImageLibraryItem{AssetID: "img_existing"}, true, nil
		},
	}
	svc := imageLibraryFlowService(repo, storage)

	item, reused, err := svc.ImportBytes(context.Background(), 42, ImageLibraryImportInput{
		ImageData: []byte(`<svg><script>alert(1)</script></svg>`), DeclaredMIME: "image/svg+xml", IdempotencyKey: "same-request",
	})
	require.NoError(t, err)
	require.True(t, reused)
	require.Equal(t, "img_existing", item.AssetID)
	require.Zero(t, storage.saveCalls)
}

func TestImageLibraryLegacyImportBypassesInteractiveLimits(t *testing.T) {
	storage := &imageLibraryFlowStorage{}
	var preflight ImageLibraryImportPreflightParams
	var create CreateImageLibraryAssetParams
	repo := &imageLibraryFlowRepo{
		preflightFn: func(in ImageLibraryImportPreflightParams) (*ImageLibraryItem, bool, error) {
			preflight = in
			return nil, false, nil
		},
		createFn: func(in CreateImageLibraryAssetParams) (*ImageLibraryItem, bool, error) {
			create = in
			return &ImageLibraryItem{AssetID: "img_legacy"}, false, nil
		},
	}
	svc := imageLibraryFlowService(repo, storage)

	item, reused, err := svc.importLegacyBytes(context.Background(), 42, ImageLibraryImportInput{
		SourceType: "legacy_plaza", GenerationMode: "import",
		ImageData: testPNG(t), DeclaredMIME: "image/png",
		IdempotencyKey: "legacy-image-plaza:42",
	}, ImageLibraryRuntimeConfig{
		MaxImageBytes: 20 << 20, MaxImagePixels: 40_000_000,
		MaxItemsPerUser: 1, MaxBytesPerUser: 1, ImportPerMinute: 1,
		RetentionDays: 90,
	})
	require.NoError(t, err)
	require.False(t, reused)
	require.Equal(t, "img_legacy", item.AssetID)
	require.True(t, preflight.RecordAttempt)
	require.Zero(t, preflight.MaxItems)
	require.Zero(t, preflight.MaxBytes)
	require.Zero(t, preflight.RateLimit)
	require.Zero(t, create.MaxItems)
	require.Zero(t, create.MaxBytes)
	require.Zero(t, create.RateLimit)
	require.Equal(t, 1, storage.saveCalls)
}

func TestImageLibraryFromTaskReplaySkipsHistoricalObjectRead(t *testing.T) {
	storage := &imageLibraryFlowStorage{data: []byte("not an image")}
	repo := &imageLibraryFlowRepo{
		prepareFn: func(int64, string, int) (*ImageLibraryItem, *ObjectRef, bool, error) {
			return &ImageLibraryItem{AssetID: "img_archived"}, nil, true, nil
		},
	}
	svc := imageLibraryFlowService(repo, storage)

	item, reused, err := svc.FromTask(context.Background(), 42, "asyncimg_task", 0, "")
	require.NoError(t, err)
	require.True(t, reused)
	require.Equal(t, "img_archived", item.AssetID)
	require.Zero(t, storage.readCalls)
}

func TestImageLibraryFromTaskQuarantinesInvalidHistoricalResult(t *testing.T) {
	storage := &imageLibraryFlowStorage{data: []byte("<script>alert(1)</script>")}
	ref := ObjectRef{Provider: "custom_s3", Bucket: "images", ObjectKey: "tasks/old.png", ContentType: "image/png", SizeBytes: 25, ChecksumSHA256: "legacy"}
	quarantined := false
	created := false
	repo := &imageLibraryFlowRepo{
		prepareFn: func(int64, string, int) (*ImageLibraryItem, *ObjectRef, bool, error) {
			return nil, &ref, false, nil
		},
		createTaskFn: func(int64, string, int, *ObjectRef) (*ImageLibraryItem, bool, error) {
			created = true
			return nil, false, nil
		},
		quarantineFn: func(_ int64, _ string, _ int, got ObjectRef, reason string) error {
			quarantined = true
			require.Equal(t, ref.ObjectKey, got.ObjectKey)
			require.NotEmpty(t, reason)
			return nil
		},
	}
	svc := imageLibraryFlowService(repo, storage)

	_, _, err := svc.FromTask(context.Background(), 42, "asyncimg_task", 0, "")
	require.Equal(t, "ASYNC_IMAGE_RESULT_QUARANTINED", infraerrors.Reason(err))
	require.Equal(t, 1, storage.readCalls)
	require.True(t, quarantined)
	require.False(t, created)
}

func TestImageLibraryFromTaskPersistsStrictlyValidatedMetadata(t *testing.T) {
	imageData := testPNG(t)
	storage := &imageLibraryFlowStorage{data: imageData}
	ref := ObjectRef{Provider: "custom_s3", Bucket: "images", ObjectKey: "tasks/old.png", ContentType: "image/png", SizeBytes: 1, ChecksumSHA256: "legacy"}
	repo := &imageLibraryFlowRepo{
		prepareFn: func(int64, string, int) (*ImageLibraryItem, *ObjectRef, bool, error) {
			return nil, &ref, false, nil
		},
		createTaskFn: func(_ int64, _ string, _ int, validated *ObjectRef) (*ImageLibraryItem, bool, error) {
			require.NotNil(t, validated)
			require.Equal(t, int64(len(imageData)), validated.SizeBytes)
			require.NotEqual(t, "legacy", validated.ChecksumSHA256)
			require.Equal(t, 2, validated.Width)
			require.Equal(t, 2, validated.Height)
			return &ImageLibraryItem{AssetID: "img_validated"}, false, nil
		},
	}
	svc := imageLibraryFlowService(repo, storage)

	item, reused, err := svc.FromTask(context.Background(), 42, "asyncimg_task", 0, "")
	require.NoError(t, err)
	require.False(t, reused)
	require.Equal(t, "img_validated", item.AssetID)
	require.Equal(t, 1, storage.readCalls)
}
