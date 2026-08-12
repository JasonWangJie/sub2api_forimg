package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type imagePlazaLibraryStub struct {
	deleteHandled bool
	deleteErr     error
	deletedUserID int64
	deletedID     string
	item          *service.ImageLibraryItem
	publication   *service.ImagePublication
	listResult    *service.PublicImagePlazaListResult
	listParams    service.ImagePublicationListParams
	importInput   service.ImageLibraryImportInput
	importErr     error
	importCalls   int
}

func (s *imagePlazaLibraryStub) ListPublished(_ context.Context, _ int64, in service.ImagePublicationListParams) (*service.PublicImagePlazaListResult, error) {
	s.listParams = in
	if s.listResult != nil {
		return s.listResult, nil
	}
	return &service.PublicImagePlazaListResult{Items: []service.PublicImagePlazaItem{}}, nil
}

func (s *imagePlazaLibraryStub) ImportBytes(_ context.Context, _ int64, in service.ImageLibraryImportInput) (*service.ImageLibraryItem, bool, error) {
	s.importCalls++
	s.importInput = in
	return s.item, false, s.importErr
}

func (s *imagePlazaLibraryStub) Publish(context.Context, service.CreateImagePublicationParams) (*service.ImagePublication, error) {
	return s.publication, nil
}

func (s *imagePlazaLibraryStub) ResolvePublishedObject(context.Context, string) (service.ObjectAccess, error) {
	return service.ObjectAccess{}, service.ErrImagePublicationNotFound
}

func (s *imagePlazaLibraryStub) OpenPublishedObject(context.Context, string) (service.ObjectAccess, io.ReadCloser, string, error) {
	return service.ObjectAccess{}, nil, "", service.ErrImagePublicationNotFound
}

func (s *imagePlazaLibraryStub) DeleteLegacyPlazaIdentifier(_ context.Context, userID int64, identifier string) (bool, error) {
	s.deletedUserID, s.deletedID = userID, identifier
	return s.deleteHandled, s.deleteErr
}

type legacyImagePlazaRepoStub struct {
	item    *service.ImagePlazaItem
	deleted bool
}

func (s *legacyImagePlazaRepoStub) Create(context.Context, *service.ImagePlazaItem) error {
	return nil
}

func (s *legacyImagePlazaRepoStub) GetByID(context.Context, int64) (*service.ImagePlazaItem, error) {
	if s.item == nil {
		return nil, service.ErrImagePlazaNotFound
	}
	copy := *s.item
	return &copy, nil
}

func (s *legacyImagePlazaRepoStub) ListPublic(context.Context, service.ImagePlazaListParams) (*service.ImagePlazaListResult, error) {
	return &service.ImagePlazaListResult{}, nil
}

func (s *legacyImagePlazaRepoStub) Delete(context.Context, int64, int64) (bool, error) {
	return s.deleted, nil
}

func TestLegacyImagePlazaPublishKeepsHTTP200DirectItemShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	library := &imagePlazaLibraryStub{
		item: &service.ImageLibraryItem{
			AssetID: "img_asset", Model: "gpt-image-1", ActualSize: "1024x1024",
			Quality: "high", Title: "title", PrivatePrompt: "prompt", Visibility: "private",
			ContentType: "image/png", ByteSize: 70, ImageURL: "/api/v1/user/image-library/img_asset/view", CreatedAt: now,
		},
		publication: &service.ImagePublication{PublicID: "imgpub_public", AssetID: "img_asset", Status: service.ImagePublicationPending},
	}
	h := &ImagePlazaHandler{library: library}
	payload := map[string]any{
		"prompt": "prompt", "title": "title", "model": "gpt-image-1", "size": "1024x1024",
		"quality": "high", "format": "png", "image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(handlerTestPNG(t)),
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/image-plaza", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})

	h.Publish(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "true", recorder.Header().Get("Deprecation"))
	require.NotEmpty(t, recorder.Header().Get("Sunset"))
	var envelope response.Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	data, ok := envelope.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "img_asset", data["id"])
	require.Equal(t, "imgpub_public", data["publication_id"])
	require.Contains(t, data, "item")
	require.Contains(t, data, "publication")
}

func TestLegacyImagePlazaPublishDefersStrictDecodeUntilLibraryPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	library := &imagePlazaLibraryStub{
		importErr: apperrors.TooManyRequests("IMAGE_LIBRARY_RATE_LIMIT", "limited"),
	}
	h := &ImagePlazaHandler{library: library}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	payload := map[string]any{
		"prompt": "prompt", "title": "title", "model": "gpt-image-1",
		"image": "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(svg),
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/image-plaza", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})

	h.Publish(c)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, 1, library.importCalls)
	require.Equal(t, svg, library.importInput.ImageData)
	require.Equal(t, "image/svg+xml", library.importInput.DeclaredMIME)
}

func TestLegacyImagePlazaListKeepsPageContractAndAppliesNewFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	library := &imagePlazaLibraryStub{listResult: &service.PublicImagePlazaListResult{
		Items: []service.PublicImagePlazaItem{
			{PublicationPK: 9, PublicationID: "imgpub_nine", PublishedAt: now},
			{PublicationPK: 8, PublicationID: "imgpub_eight", PublishedAt: now.Add(-time.Minute)},
		},
		Total: 17,
	}}
	h := &ImagePlazaHandler{library: library}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/image-plaza?page=2&page_size=2&platform=gemini&model=gemini-image&aspect_ratio=16%3A9&sort=oldest&q=city", nil)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})

	h.List(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 2, library.listParams.Offset)
	require.Equal(t, 2, library.listParams.Limit)
	require.Nil(t, library.listParams.Cursor)
	require.Equal(t, "gemini", library.listParams.Platform)
	require.Equal(t, "gemini-image", library.listParams.Model)
	require.Equal(t, "16:9", library.listParams.AspectRatio)
	require.Equal(t, "oldest", library.listParams.Sort)
	require.Equal(t, "city", library.listParams.Query)

	var payload struct {
		Data struct {
			Items      []map[string]any `json:"items"`
			Total      int64            `json:"total"`
			Page       int              `json:"page"`
			PageSize   int              `json:"page_size"`
			NextCursor string           `json:"next_cursor"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 2)
	require.Equal(t, int64(17), payload.Data.Total)
	require.Equal(t, 2, payload.Data.Page)
	require.Equal(t, 2, payload.Data.PageSize)
	require.NotEmpty(t, payload.Data.NextCursor)
	for _, item := range payload.Data.Items {
		require.NotContains(t, item, "moderation_status")
		require.NotContains(t, item, "review_reason")
	}
}

func TestLegacyImagePlazaDeleteAcceptsPublicationIdentifier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	library := &imagePlazaLibraryStub{deleteHandled: true}
	h := &ImagePlazaHandler{library: library}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/image-plaza/imgpub_public", nil)
	c.Params = gin.Params{{Key: "id", Value: "imgpub_public"}}
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})

	h.Delete(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(42), library.deletedUserID)
	require.Equal(t, "imgpub_public", library.deletedID)
	require.Equal(t, "true", recorder.Header().Get("Deprecation"))
}

func TestLegacyImagePlazaDeleteFallsBackForUnmigratedNumericIDWithoutLeakingOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, testCase := range []struct {
		name       string
		ownerID    int64
		wantStatus int
	}{
		{name: "owner", ownerID: 42, wantStatus: http.StatusOK},
		{name: "other user", ownerID: 99, wantStatus: http.StatusNotFound},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			library := &imagePlazaLibraryStub{deleteHandled: false}
			legacy := service.NewImagePlazaService(&legacyImagePlazaRepoStub{
				item: &service.ImagePlazaItem{ID: 7, UserID: testCase.ownerID, StoragePath: "pending"}, deleted: true,
			}, &config.Config{})
			h := &ImagePlazaHandler{svc: legacy, library: library}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/image-plaza/7", nil)
			c.Params = gin.Params{{Key: "id", Value: "7"}}
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})

			h.Delete(c)

			require.Equal(t, testCase.wantStatus, recorder.Code)
			require.NotEqual(t, http.StatusForbidden, recorder.Code)
			require.Equal(t, "true", recorder.Header().Get("Deprecation"))
		})
	}
}

func handlerTestPNG(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	return buffer.Bytes()
}
