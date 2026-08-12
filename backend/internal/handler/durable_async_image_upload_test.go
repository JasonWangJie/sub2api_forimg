//go:build unit

package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type asyncImageUploadHandlerRepositoryStub struct {
	service.AsyncImageTaskRepository
	admitCalls    int
	reserveCalls  int
	intentCalls   int
	completeCalls int
	failCalls     int
	aliasErr      error
	admit         func(context.Context, service.AdmitAsyncImageUploadParams) (*service.AsyncImageUploadAdmission, error)
	reserve       func(context.Context, service.ReserveAsyncImageUploadParams) (*service.AsyncImageUploadReservationResult, error)
}

func (s *asyncImageUploadHandlerRepositoryStub) AdmitAsyncImageUpload(ctx context.Context, params service.AdmitAsyncImageUploadParams) (*service.AsyncImageUploadAdmission, error) {
	s.admitCalls++
	if s.admit != nil {
		return s.admit(ctx, params)
	}
	return &service.AsyncImageUploadAdmission{AdmissionID: "asyncimg_admission"}, nil
}

func (s *asyncImageUploadHandlerRepositoryStub) ReserveAsyncImageUpload(ctx context.Context, params service.ReserveAsyncImageUploadParams) (*service.AsyncImageUploadReservationResult, error) {
	s.reserveCalls++
	if s.reserve == nil {
		return &service.AsyncImageUploadReservationResult{Reservation: &service.AsyncImageUploadReservation{
			ReservationID: "asyncimg_upload", UserID: params.UserID, APIKeyID: params.APIKeyID,
			RequestHash: params.RequestHash, ByteSize: params.ByteSize, Status: service.AsyncImageUploadStatusReserved,
		}}, nil
	}
	return s.reserve(ctx, params)
}

func (s *asyncImageUploadHandlerRepositoryStub) CompleteAsyncImageUpload(_ context.Context, params service.CompleteAsyncImageUploadParams) (*service.AsyncImageInputObject, error) {
	s.completeCalls++
	return &service.AsyncImageInputObject{
		ID: 1, UploadID: params.ReservationID, UserID: params.UserID, APIKeyID: params.APIKeyID,
		ObjectRef: params.ObjectRef, URLHash: params.URLHash, Filename: params.Filename,
		ExpiresAt: params.ExpiresAt, CreatedAt: time.Now().UTC(),
	}, nil
}

func (s *asyncImageUploadHandlerRepositoryStub) SetAsyncImageUploadObjectIntent(context.Context, service.SetAsyncImageUploadObjectIntentParams) error {
	s.intentCalls++
	return nil
}

func (s *asyncImageUploadHandlerRepositoryStub) FailAsyncImageUpload(context.Context, string, string, string) (bool, error) {
	s.failCalls++
	return true, nil
}

func (s *asyncImageUploadHandlerRepositoryStub) RegisterAsyncImageInputURLAlias(context.Context, service.RegisterAsyncImageInputURLAliasParams) error {
	return s.aliasErr
}

type scUploadTestStorage struct {
	data           []byte
	ref            service.ObjectRef
	uploadDeadline time.Time
}

func (*scUploadTestStorage) Save(context.Context, string, string, []byte) (string, error) {
	return "https://cdn.example.test/input.png", nil
}

func (s *scUploadTestStorage) ObjectIntent(key, contentType string, sizeBytes int64, checksum string) (service.ObjectRef, error) {
	return service.ObjectRef{
		Provider: service.ImageStorageProviderCustomS3, Bucket: "images", ObjectKey: key,
		ContentType: contentType, SizeBytes: sizeBytes, ChecksumSHA256: checksum,
	}, nil
}

func (s *scUploadTestStorage) SaveObject(ctx context.Context, key, contentType string, data []byte) (service.ObjectRef, error) {
	if deadline, ok := ctx.Deadline(); ok {
		s.uploadDeadline = deadline
	}
	s.data = append([]byte(nil), data...)
	checksum := fmt.Sprintf("%x", sha256.Sum256(data))
	ref, err := s.ObjectIntent(key, contentType, int64(len(data)), checksum)
	s.ref = ref
	return ref, err
}

func (*scUploadTestStorage) SignURL(context.Context, service.ObjectRef, time.Duration) (service.ObjectAccess, error) {
	return service.ObjectAccess{URL: "https://cdn.example.test/input.png"}, nil
}

func (s *scUploadTestStorage) Read(context.Context, service.ObjectRef) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func (s *scUploadTestStorage) Head(context.Context, service.ObjectRef) (service.ObjectMetadata, error) {
	return service.ObjectMetadata{ObjectRef: s.ref}, nil
}

func (s *scUploadTestStorage) Delete(context.Context, service.ObjectRef) error {
	s.data = nil
	return nil
}

func newSCUploadAdmissionTestRouter(t *testing.T, repo *asyncImageUploadHandlerRepositoryStub, maxBytes int64) (*gin.Engine, *scUploadTestStorage) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	storage := &scUploadTestStorage{}
	settings := service.NewImageStorageSettingService(
		nil, nil, nil,
		func(context.Context, *config.ImageStorageConfig) (service.ImageStorage, error) { return storage, nil },
		config.ImageStorageConfig{
			Enabled: true, Provider: service.ImageStorageProviderCustomS3,
			Endpoint: "https://storage.example.test", Region: "auto", Bucket: "images",
			AccessKeyID: "ak", SecretAccessKey: "sk", Prefix: "images/", MaxDownloadByte: maxBytes,
		},
		config.AsyncImageConfig{
			DownloadMaxBytes: maxBytes, UploadPerMinute: 20, MaxInputBytesPerKey: 1 << 30,
			UploadTimeoutSeconds: 2, InputRetentionHours: 24,
		},
	)
	h := &DurableAsyncImageHandler{tasks: service.NewAsyncImageTaskService(repo), storage: settings}
	groupID := int64(3)
	apiKey := &service.APIKey{
		ID: 9, UserID: 7, GroupID: &groupID,
		Group: &service.Group{
			ID: groupID, Platform: service.PlatformGemini,
			AllowImageGeneration: true, AllowAsyncImageGeneration: true,
		},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Next()
	})
	router.POST("/v1/uploads/images_sc", h.UploadSC)
	return router, storage
}

func multipartImageUploadRequest(t *testing.T, data []byte, contentType string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header["Content-Disposition"] = []string{`form-data; name="file"; filename="reference.png"`}
	header["Content-Type"] = []string{contentType}
	part, err := writer.CreatePart(header)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	req := httptest.NewRequest(http.MethodPost, "/v1/uploads/images_sc", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestUploadSCChecksPostgresAdmissionBeforeFullImageDecode(t *testing.T) {
	repo := &asyncImageUploadHandlerRepositoryStub{admit: func(context.Context, service.AdmitAsyncImageUploadParams) (*service.AsyncImageUploadAdmission, error) {
		return nil, service.ErrAsyncImageUploadRateLimited
	}}
	router, storage := newSCUploadAdmissionTestRouter(t, repo, 2<<20)
	req := multipartImageUploadRequest(t, []byte("definitely-not-an-image"), "image/png")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, repo.admitCalls)
	require.Zero(t, repo.reserveCalls)
	require.Empty(t, storage.data, "rate-limited bytes must never reach image decoding or OSS")
	require.Equal(t, "60", recorder.Header().Get("Retry-After"))
}

func TestUploadSCRejectsOversizedChunkedMultipartBeforeFormFileSpooling(t *testing.T) {
	repo := &asyncImageUploadHandlerRepositoryStub{reserve: func(context.Context, service.ReserveAsyncImageUploadParams) (*service.AsyncImageUploadReservationResult, error) {
		t.Fatal("oversized multipart body must be rejected before admission")
		return nil, nil
	}}
	router, storage := newSCUploadAdmissionTestRouter(t, repo, 64)
	req := multipartImageUploadRequest(t, bytes.Repeat([]byte("x"), int(asyncImageSCMultipartOverhead+1024)), "image/png")
	req.ContentLength = -1 // exercise MaxBytesReader instead of the Content-Length fast path
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, repo.admitCalls)
	require.Zero(t, repo.reserveCalls)
	require.Empty(t, storage.data)
}

func TestUploadSCSuccessRunsAdmissionIntentStorageAndCompletion(t *testing.T) {
	repo := &asyncImageUploadHandlerRepositoryStub{}
	router, storage := newSCUploadAdmissionTestRouter(t, repo, 2<<20)
	pngData, err := base64.StdEncoding.DecodeString(durableAsyncImageOnePixelPNG)
	require.NoError(t, err)
	req := multipartImageUploadRequest(t, pngData, "image/png")
	req.Header.Set("Idempotency-Key", "upload-success")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "https://cdn.example.test/input.png", response["url"])
	require.Equal(t, "reference.png", response["filename"])
	require.Equal(t, "image/png", response["content_type"])
	require.Equal(t, float64(len(pngData)), response["bytes"])
	require.Equal(t, 1, repo.admitCalls)
	require.Equal(t, 1, repo.reserveCalls)
	require.Equal(t, 1, repo.intentCalls)
	require.Equal(t, 1, repo.completeCalls)
	require.Zero(t, repo.failCalls)
	require.Equal(t, pngData, storage.data)
	require.False(t, storage.uploadDeadline.IsZero())
	require.WithinDuration(t, time.Now().Add(2*time.Second), storage.uploadDeadline, time.Second)
}

func TestUploadSCReplayReturnsStructuredAliasLimit(t *testing.T) {
	pngData, err := base64.StdEncoding.DecodeString(durableAsyncImageOnePixelPNG)
	require.NoError(t, err)
	now := time.Now().UTC()
	repo := &asyncImageUploadHandlerRepositoryStub{aliasErr: service.ErrAsyncImageUploadAliasLimit}
	repo.reserve = func(_ context.Context, params service.ReserveAsyncImageUploadParams) (*service.AsyncImageUploadReservationResult, error) {
		return &service.AsyncImageUploadReservationResult{
			Reused: true,
			Reservation: &service.AsyncImageUploadReservation{
				ReservationID: "asyncimg_existing", UserID: params.UserID, APIKeyID: params.APIKeyID,
			},
			InputObject: &service.AsyncImageInputObject{
				ID: 11, UploadID: "asyncimg_existing", UserID: params.UserID, APIKeyID: params.APIKeyID,
				ObjectRef: service.ObjectRef{
					Provider: service.ImageStorageProviderCustomS3, Bucket: "images", ObjectKey: "inputs/existing.png",
					ContentType: "image/png", SizeBytes: int64(len(pngData)), ChecksumSHA256: fmt.Sprintf("%x", sha256.Sum256(pngData)),
				},
				Filename: "reference.png", ExpiresAt: now.Add(time.Hour), CreatedAt: now,
			},
		}, nil
	}
	router, _ := newSCUploadAdmissionTestRouter(t, repo, 2<<20)
	req := multipartImageUploadRequest(t, pngData, "image/png")
	req.Header.Set("Idempotency-Key", "alias-limit")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusTooManyRequests, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "async_image_upload_alias_limit")
}

func TestAsyncImageUploadFilenameIsStorageSafeAndBounded(t *testing.T) {
	require.Equal(t, "evil.png", asyncImageUploadFilename(`..\folder\evil.png`))
	require.Equal(t, "cleanname.png", asyncImageUploadFilename("clean\x00name\r\n.png"))
	require.Empty(t, asyncImageUploadFilename(".."))
	long := asyncImageUploadFilename(string(bytes.Repeat([]byte("a"), 400)) + ".png")
	require.LessOrEqual(t, len(long), 255)
}
