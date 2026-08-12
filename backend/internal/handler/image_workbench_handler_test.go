package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type handlerImageWorkbenchKeyReader struct {
	key *service.APIKey
}

func (s handlerImageWorkbenchKeyReader) GetByID(context.Context, int64) (*service.APIKey, error) {
	return s.key, nil
}

func TestImageWorkbenchHandlerGetCapabilities(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(20)
	key := &service.APIKey{
		ID: 10, UserID: 7, GroupID: &groupID, Status: service.StatusActive,
		Group: &service.Group{
			ID: 20, Platform: service.PlatformGemini, Status: service.StatusActive,
			AllowImageGeneration: true,
		},
	}
	workbench := service.NewImageWorkbenchService(handlerImageWorkbenchKeyReader{key: key}, nil)
	h := NewImageWorkbenchHandler(workbench)
	router := gin.New()
	router.GET("/api/v1/user/image-workbench/capabilities/:api_key_id", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		h.GetCapabilities(c)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/user/image-workbench/capabilities/10", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Code int                                `json:"code"`
		Data service.ImageWorkbenchCapabilities `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Zero(t, payload.Code)
	require.True(t, payload.Data.Available)
	require.Equal(t, service.ImageWorkbenchProtocolGeminiNative, payload.Data.Protocol)
}

func TestGatewayHandlersShareImageConcurrencyLimiter(t *testing.T) {
	sharedConcurrency := service.NewConcurrencyService(nil)
	openAI := NewOpenAIGatewayHandler(nil, sharedConcurrency, nil, nil, nil, nil, nil, nil, nil)
	gemini := NewGatewayHandler(nil, nil, nil, nil, nil, sharedConcurrency, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	require.Same(t, openAI.imageLimiter, gemini.imageLimiter)
}

func TestGeminiNativeImageIntentRequiresGroupPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini-3-pro-image:generateContent",
		strings.NewReader(`{"generationConfig":{"responseModalities":["TEXT","IMAGE"]}}`),
	)
	c.Params = gin.Params{{Key: "modelAction", Value: "gemini-3-pro-image:generateContent"}}
	groupID := int64(20)
	group := &service.Group{ID: groupID, Platform: service.PlatformGemini, Status: service.StatusActive, AllowImageGeneration: false}
	key := &service.APIKey{ID: 10, UserID: 7, GroupID: &groupID, Group: group, Status: service.StatusActive, User: &service.User{ID: 7}}
	c.Set(string(middleware.ContextKeyAPIKey), key)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})

	(&GatewayHandler{}).GeminiV1BetaModels(c)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), service.ImageGenerationPermissionMessage())
}
