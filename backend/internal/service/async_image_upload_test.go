package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type asyncImageUploadRepositoryStub struct {
	AsyncImageTaskRepository
	admit    func(context.Context, AdmitAsyncImageUploadParams) (*AsyncImageUploadAdmission, error)
	reserve  func(context.Context, ReserveAsyncImageUploadParams) (*AsyncImageUploadReservationResult, error)
	aliasErr error
}

func (s *asyncImageUploadRepositoryStub) AdmitAsyncImageUpload(ctx context.Context, params AdmitAsyncImageUploadParams) (*AsyncImageUploadAdmission, error) {
	if s.admit != nil {
		return s.admit(ctx, params)
	}
	return &AsyncImageUploadAdmission{AdmissionID: params.AdmissionID, AttemptedAt: params.Now}, nil
}

func (s *asyncImageUploadRepositoryStub) ReserveAsyncImageUpload(ctx context.Context, params ReserveAsyncImageUploadParams) (*AsyncImageUploadReservationResult, error) {
	return s.reserve(ctx, params)
}

func (*asyncImageUploadRepositoryStub) CompleteAsyncImageUpload(context.Context, CompleteAsyncImageUploadParams) (*AsyncImageInputObject, error) {
	return nil, errors.New("not implemented")
}

func (*asyncImageUploadRepositoryStub) SetAsyncImageUploadObjectIntent(context.Context, SetAsyncImageUploadObjectIntentParams) error {
	return nil
}

func (*asyncImageUploadRepositoryStub) FailAsyncImageUpload(context.Context, string, string, string) (bool, error) {
	return false, nil
}

func (s *asyncImageUploadRepositoryStub) RegisterAsyncImageInputURLAlias(context.Context, RegisterAsyncImageInputURLAliasParams) error {
	return s.aliasErr
}

func TestAsyncImageUploadRuntimeDefaultsAreFailClosedAndFinite(t *testing.T) {
	cfg := defaultAsyncImageRuntimeConfig()
	require.Equal(t, 300, cfg.UploadTimeoutSeconds)
	require.Equal(t, 20, cfg.UploadPerMinute)
	require.Equal(t, int64(1<<30), cfg.MaxInputBytesPerKey)

	var empty AsyncImageRuntimeConfig
	normalizeAsyncImageRuntimeConfig(&empty)
	require.Equal(t, cfg.UploadTimeoutSeconds, empty.UploadTimeoutSeconds)
	require.Equal(t, cfg.UploadPerMinute, empty.UploadPerMinute)
	require.Equal(t, cfg.MaxInputBytesPerKey, empty.MaxInputBytesPerKey)

	extreme := AsyncImageRuntimeConfig{
		DownloadMaxBytes: 1 << 62, InputRetentionHours: 1 << 30,
		UploadTimeoutSeconds: 1 << 30, UploadPerMinute: 1 << 30, MaxInputBytesPerKey: 1 << 62,
	}
	normalizeAsyncImageRuntimeConfig(&extreme)
	require.Equal(t, maxAsyncImageDownloadBytes, extreme.DownloadMaxBytes)
	require.Equal(t, maxAsyncImageInputRetention, extreme.InputRetentionHours)
	require.Equal(t, maxAsyncImageUploadTimeout, extreme.UploadTimeoutSeconds)
	require.Equal(t, maxAsyncImageUploadsPerMinute, extreme.UploadPerMinute)
	require.Equal(t, maxAsyncImageInputBytesPerKey, extreme.MaxInputBytesPerKey)
}

func TestAsyncImageUploadRequestHashBindsRawBytesAndMetadata(t *testing.T) {
	first := AsyncImageUploadRequestHash([]byte("same"), "image/png", "one.png")
	require.Len(t, first, 64)
	require.Equal(t, first, AsyncImageUploadRequestHash([]byte("same"), " IMAGE/PNG ", "one.png"))
	require.NotEqual(t, first, AsyncImageUploadRequestHash([]byte("different"), "image/png", "one.png"))
	require.NotEqual(t, first, AsyncImageUploadRequestHash([]byte("same"), "image/jpeg", "one.png"))
	require.NotEqual(t, first, AsyncImageUploadRequestHash([]byte("same"), "image/png", "two.png"))
}

func TestReserveInputUploadPassesNormalizedLimitsAndAllocatesLease(t *testing.T) {
	hash := AsyncImageUploadRequestHash([]byte("payload"), "image/png", "image.png")
	repo := &asyncImageUploadRepositoryStub{}
	repo.reserve = func(_ context.Context, params ReserveAsyncImageUploadParams) (*AsyncImageUploadReservationResult, error) {
		require.Equal(t, int64(7), params.APIKeyID)
		require.Equal(t, 20, params.UploadPerMinute)
		require.Equal(t, int64(1<<30), params.MaxInputBytesPerKey)
		require.True(t, params.LeaseExpiresAt.After(params.Now))
		require.NotEmpty(t, params.ReservationID)
		return &AsyncImageUploadReservationResult{Reservation: &AsyncImageUploadReservation{
			ReservationID: params.ReservationID, RequestHash: params.RequestHash,
		}}, nil
	}
	svc := NewAsyncImageTaskService(repo)
	result, err := svc.ReserveInputUpload(context.Background(), ReserveAsyncImageUploadParams{
		AdmissionID: "asyncimg_admit", UserID: 3, APIKeyID: 7, RequestHash: hash, ByteSize: 7,
		UploadPerMinute: 20, MaxInputBytesPerKey: 1 << 30,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Reservation.ReservationID)
}

func TestReserveInputUploadMapsRepositoryFailureToServiceUnavailable(t *testing.T) {
	hash := AsyncImageUploadRequestHash([]byte("payload"), "image/png", "image.png")
	repo := &asyncImageUploadRepositoryStub{reserve: func(context.Context, ReserveAsyncImageUploadParams) (*AsyncImageUploadReservationResult, error) {
		return nil, errors.New("database unavailable")
	}}
	svc := NewAsyncImageTaskService(repo)
	_, err := svc.ReserveInputUpload(context.Background(), ReserveAsyncImageUploadParams{
		AdmissionID: "asyncimg_admit", UserID: 3, APIKeyID: 7, RequestHash: hash, ByteSize: 7,
		UploadPerMinute: 20, MaxInputBytesPerKey: 1 << 30,
	})
	require.Error(t, err)
	require.Equal(t, 503, infraerrors.Code(err))
	require.Equal(t, "ASYNC_IMAGE_UPLOAD_ADMISSION_UNAVAILABLE", infraerrors.Reason(err))
}

func TestAdmitInputUploadFailsClosedWhenPostgresIsUnavailable(t *testing.T) {
	repo := &asyncImageUploadRepositoryStub{admit: func(context.Context, AdmitAsyncImageUploadParams) (*AsyncImageUploadAdmission, error) {
		return nil, errors.New("database unavailable")
	}}
	svc := NewAsyncImageTaskService(repo)
	_, err := svc.AdmitInputUpload(context.Background(), AdmitAsyncImageUploadParams{
		UserID: 3, APIKeyID: 7, UploadPerMinute: 20,
	})
	require.Error(t, err)
	require.Equal(t, 503, infraerrors.Code(err))
	require.Equal(t, "ASYNC_IMAGE_UPLOAD_ADMISSION_UNAVAILABLE", infraerrors.Reason(err))
}

func TestRegisterInputURLAliasPreservesStructuredLimitError(t *testing.T) {
	repo := &asyncImageUploadRepositoryStub{aliasErr: ErrAsyncImageUploadAliasLimit}
	svc := NewAsyncImageTaskService(repo)
	err := svc.RegisterInputURLAlias(context.Background(), RegisterAsyncImageInputURLAliasParams{
		InputObjectID: 1, UserID: 2, APIKeyID: 3,
		URLHash:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	require.ErrorIs(t, err, ErrAsyncImageUploadAliasLimit)
	require.Equal(t, http.StatusTooManyRequests, infraerrors.Code(err))
	require.Equal(t, "ASYNC_IMAGE_UPLOAD_ALIAS_LIMIT", infraerrors.Reason(err))
}
