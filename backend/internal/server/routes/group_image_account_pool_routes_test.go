package routes

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGroupImageAccountPoolAdminRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{
		Group: adminhandler.NewGroupHandler(nil, nil, nil),
	}}
	registerGroupRoutes(router.Group("/api/v1/admin"), handlers)

	want := map[string]bool{
		"GET /api/v1/admin/groups/:id/image-account-pools": false,
		"PUT /api/v1/admin/groups/:id/image-account-pools": false,
		"GET /api/v1/admin/groups/:id/image-size-accounts": false,
		"PUT /api/v1/admin/groups/:id/image-size-accounts": false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, registered := range want {
		require.Truef(t, registered, "missing admin route %s", route)
	}
}
