package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupImageSizeAccountsRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &GroupHandler{adminService: newStubAdminService()}
	r.GET("/admin/groups/:id/image-size-accounts", h.ListImageSizeAccounts)
	r.PUT("/admin/groups/:id/image-size-accounts", h.ReplaceImageSizeAccounts)
	return r
}

func TestGroupHandlerImageSizeAccountsSmoke(t *testing.T) {
	r := setupImageSizeAccountsRouter(t)

	getReq := httptest.NewRequest(http.MethodGet, "/admin/groups/7/image-size-accounts", nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	payload := service.GroupImageSizeAccountBindings{
		service.ImageBillingSize4K: {{AccountID: 9, Priority: 1}},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	putReq := httptest.NewRequest(http.MethodPut, "/admin/groups/7/image-size-accounts", bytes.NewReader(body))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	r.ServeHTTP(putRec, putReq)
	require.Equal(t, http.StatusOK, putRec.Code)
}
